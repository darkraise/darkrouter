package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/provider/providertest"
	"github.com/darkraise/darkrouter/internal/store"
)

// freePort returns a port that was listenable a moment ago. Racy in principle,
// fine for a test that binds it immediately.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func serverOn(t *testing.T, proxyAddr, adminAddr string) *Server {
	t.Helper()
	cfgStore := config.NewStoreOf(testConfigOf(t, func(c *config.Config) {
		c.Server.ProxyListen, c.Server.AdminListen = proxyAddr, adminAddr
		c.Server.ShutdownGrace = time.Second
	}))
	return serverBackedBy(t, cfgStore, fakeProvider)
}

func TestRunReturnsOnContextCancelAndReleasesPorts(t *testing.T) {
	proxyAddr, adminAddr := freePort(t), freePort(t)
	s := serverOn(t, proxyAddr, adminAddr)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	waitListening(t, proxyAddr)
	waitListening(t, adminAddr)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	// Both ports must be rebindable, or Run leaked a listener.
	for _, addr := range []string{proxyAddr, adminAddr} {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("port %s not released: %v", addr, err)
		}
		_ = l.Close()
	}
}

// A listener that cannot bind must not leave the other server running: that
// leaks a port and a goroutine for the process lifetime.
func TestRunClosesSurvivingServerWhenOneListenerFails(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()

	adminAddr := freePort(t)
	s := serverOn(t, taken.Addr().String(), adminAddr)

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected Run to return the bind error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after a listener failed")
	}

	l, err := net.Listen("tcp", adminAddr)
	if err != nil {
		t.Fatalf("admin port leaked after proxy bind failure: %v", err)
	}
	_ = l.Close()
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return
		}
		timer := time.NewTimer(20 * time.Millisecond)
		<-timer.C
	}
	t.Fatalf("%s never started listening", addr)
}

// Restoring breaker health is best effort: the gateway serves without it, so
// a failed restore is reported but must not take the process out of rotation.
func TestAFailedRestoreWarnsWithoutFailingReadiness(t *testing.T) {
	proxyAddr, adminAddr := freePort(t), freePort(t)
	s := serverOn(t, proxyAddr, adminAddr)
	if _, err := s.db.Write.Exec(`DROP TABLE health`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	defer func() {
		cancel()
		<-done
	}()
	waitListening(t, adminAddr)

	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("readyz = %d %s, want 200", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	var got struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(got.Warnings, func(w string) bool {
		return strings.Contains(w, "health rehydration")
	}) {
		t.Fatalf("warnings = %q, want the failed restore named", got.Warnings)
	}
}

// fakeProvider is the one upstream most fixtures in this package declare. It
// is never called: the tests exercise the wiring around it.
var fakeProvider = providertest.Keyed("fake", "openaicompat", "https://up.example/v1", "sk", "m")

// testConfigOf builds a defaulted configuration on ephemeral listeners, then
// lets the caller change what its own case is about.
func testConfigOf(t *testing.T, tune func(*config.Config)) *config.Config {
	t.Helper()
	c := &config.Config{}
	config.ApplyDefaults(c)
	c.Server.ProxyListen, c.Server.AdminListen = ":0", ":0"
	if tune != nil {
		tune(c)
	}
	// The rules a stored configuration is held to, so a fixture cannot
	// exercise a combination the running gateway would refuse.
	if err := config.Validate(c); err != nil {
		t.Fatal(err)
	}
	return c
}

// seedProviders materialises a fixed provider set into the database.
//
// Providers have lived in SQLite since phase 2, so a provider set describes
// nothing the server can serve until the rows exist. The first-run YAML
// importer used to do this; it is gone, and this is the test-side
// replacement rather than a reason to keep production code alive for
// fixtures.
func seedProviders(t *testing.T, ctx context.Context, db *store.DB, key *crypto.Key, ps []provider.Provider) {
	t.Helper()
	for _, p := range ps {
		if err := db.CreateProvider(ctx, store.ProviderRow{
			ID: p.ID, Name: p.ID, Preset: p.Preset, Kind: p.Kind,
			BaseURL: p.BaseURL, Priority: p.Priority, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		for _, m := range p.Models {
			if _, err := db.Write.ExecContext(ctx,
				`INSERT INTO models (provider_id, model_id, capabilities_source)
				 VALUES (?, ?, 'inferred')`, p.ID, m); err != nil {
				t.Fatal(err)
			}
		}
		secret := ""
		if len(p.Credentials) > 0 {
			secret = p.Credentials[0].Secret
		}
		if _, err := db.AddCredential(ctx, key, store.Credential{
			ProviderID: p.ID, Label: "imported", Kind: "static",
			Secret: secret, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// serverBackedBy builds a Server on a temporary database, so every test in this
// package exercises the real persistence wiring.
func serverBackedBy(t *testing.T, cfgStore *config.Store, ps ...provider.Provider) *Server {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "test-master")
	if err != nil {
		t.Fatal(err)
	}
	seedProviders(t, ctx, db, key, ps)
	s, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testConfigStore(t *testing.T) *config.Store {
	t.Helper()
	return config.NewStoreOf(testConfigOf(t, nil))
}

// testServerWithConfig builds a server over a fixed, already-built Config,
// for cases exercising something about the config itself (such as
// cfg.Warnings) rather than the store's reload machinery that testConfigOf's
// callers usually want.
func testServerWithConfig(t *testing.T, c *config.Config) *Server {
	t.Helper()
	return serverBackedBy(t, config.NewStoreOf(c))
}

func TestHealthzReportsDroppedRecordsAndWarnings(t *testing.T) {
	dir := t.TempDir()
	cfgStore := testConfigStore(t)

	db, err := store.Open(filepath.Join(dir, "darkrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}

	s, err := New(cfgStore, db, key, []string{"a startup warning"})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["log_records_dropped"]; !ok {
		t.Error("healthz must report the log drop counter; without it a shortfall in spend is invisible")
	}
	warnings, _ := body["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if s, _ := w.(string); s == "a startup warning" {
			found = true
		}
	}
	if !found {
		t.Errorf("startup warnings must reach healthz, got %v", warnings)
	}
}

func TestMetricsReportsCounters(t *testing.T) {
	dir := t.TempDir()
	cfgStore := testConfigStore(t)
	db, err := store.Open(filepath.Join(dir, "darkrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, _ := store.OpenKeyring(ctx, db, "master")
	s, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "darkrouter_log_records_dropped_total") {
		t.Errorf("metrics = %s", rr.Body.String())
	}
}

// A done criterion: a cooldown survives a restart.
func TestCooldownSurvivesAGracefulRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.db")
	ctx := context.Background()
	k := health.Key{ProviderID: "groq", KeyID: "k1", Model: "m"}

	// First process: cool the triple, then flush on shutdown.
	db1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db1.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	b1 := health.New(3, 15*time.Minute)
	p1 := health.NewPersister(b1, db1, time.Hour)
	for i := 0; i < 3; i++ {
		b1.Record(k, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	if b1.Available(k) {
		t.Fatal("the triple should be cooling before the restart")
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p1.Run(runCtx) }()
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	// Second process: rehydrate and confirm the cooldown and counter survived.
	db2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	b2 := health.New(3, 15*time.Minute)
	p2 := health.NewPersister(b2, db2, time.Hour)
	if err := p2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if b2.Available(k) {
		t.Fatal("the cooldown did not survive the restart")
	}
	snap := b2.Snapshot()
	if len(snap) != 1 || snap[0].ConsecutiveFailures != 3 {
		t.Errorf("failure counter did not survive: %+v", snap)
	}
}

func TestRequestRowRecordsTheCandidateChain(t *testing.T) {
	dir := t.TempDir()
	cfgStore := testConfigStore(t)
	db, err := store.Open(filepath.Join(dir, "darkrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, _ := store.OpenKeyring(ctx, db, "master")
	s, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	// No providers are configured, so the router refuses before any attempt and
	// the request id must still reach the client.
	rr := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rr, httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"nope","messages":[]}`)))
	if rr.Code != 404 {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
	if rr.Header().Get("X-Darkrouter-Request") == "" {
		t.Error("the request id must be returned even when no attempt was made")
	}
	if rr.Header().Get("X-Darkrouter-Provider") != "" {
		t.Error("no attempt was made, so no provider may be named")
	}
}

// offlineCatalog is the fixture configuration for a server whose catalog
// workers must not reach the network: an unroutable source, a short timeout,
// and the discovery sweep off.
func offlineCatalog(c *config.Config) {
	c.Server.ProxyListen, c.Server.AdminListen = "127.0.0.1:0", "127.0.0.1:0"
	c.Catalog.ModelsDevURL = "http://127.0.0.1:1/api.json"
	c.Catalog.SyncTimeout = 200 * time.Millisecond
	off := false
	c.Catalog.Discovery.Enabled = &off
}

// serverFixtureWith is serverBackedBy with the database and key handed back, so
// a test can seed catalog rows before New reads them.
func serverFixtureWith(t *testing.T, tune func(*config.Config), ps ...provider.Provider) (*store.DB, *crypto.Key, *config.Store) {
	t.Helper()
	cfgStore := config.NewStoreOf(testConfigOf(t, tune))
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "test-master")
	if err != nil {
		t.Fatal(err)
	}
	seedProviders(t, ctx, db, key, ps)
	return db, key, cfgStore
}

func serverFixture(t *testing.T) (*store.DB, *crypto.Key, *config.Store) {
	t.Helper()
	return serverFixtureWith(t, offlineCatalog)
}

func TestNewRebuildsTheCatalogBeforeServing(t *testing.T) {
	// A request arriving in the first second must route against what the
	// database already knows, not against an empty snapshot.
	ctx := context.Background()
	db, key, cfgStore := serverFixture(t)

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: "p", Kind: "static", Secret: "sk", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]store.DiscoveredModel{{ModelID: "already-known"}}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := srv.Catalog().Snapshot().Lookup("p", "already-known"); !ok {
		t.Error("New did not rebuild the catalog; the first request would 404")
	}
}

func TestRunStartsAndStopsTheCatalogWorkers(t *testing.T) {
	// The real assertion is the absence of a leak: Run must return, and the
	// race detector must see no worker still touching the database after it.
	db, key, cfgStore := serverFixture(t)
	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return; a catalog worker is not honouring its context")
	}
}

func TestDiscoveryCanBeDisabled(t *testing.T) {
	// Discovery is outbound traffic the gateway initiates on the operator's
	// behalf. An operator on a locked-down network needs an off switch.
	db, key, cfgStore := serverFixtureWith(t, offlineCatalog)
	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Discoverer() != nil {
		t.Error("a discoverer was built with discovery disabled")
	}
}

// A restart-only edit stays on /healthz until the process is restarted.
// restartOnlyWarnings, the consecutive-reload diff, is cleared by the next
// unrelated save while the old value is still the one in force -- which is an
// operator told to restart and then quietly told they need not.
func TestHealthzReportsPendingRestartAcrossAnUnrelatedReload(t *testing.T) {
	var mu sync.Mutex
	live := testConfigOf(t, nil)
	cfgStore, err := config.NewStoreFrom(func() (*config.Config, error) {
		mu.Lock()
		defer mu.Unlock()
		return live, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfgStore.MarkBoot()
	s := serverBackedBy(t, cfgStore)

	swap := func(tune func(*config.Config)) {
		mu.Lock()
		live = testConfigOf(t, tune)
		mu.Unlock()
		if err := cfgStore.Reload(); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() []string {
		rr := httptest.NewRecorder()
		s.AdminHandler().ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
		if rr.Code != 200 {
			t.Fatalf("healthz = %d", rr.Code)
		}
		var body struct {
			PendingRestart []string `json:"pending_restart"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.PendingRestart
	}

	if got := pending(); len(got) != 0 {
		t.Fatalf("pending_restart = %v at boot, want none", got)
	}
	swap(func(c *config.Config) { c.Catalog.SyncInterval = 9 * time.Hour })
	if got := pending(); !slices.Contains(got, "catalog.sync_interval") {
		t.Fatalf("pending_restart = %v after a restart-only change, want catalog.sync_interval", got)
	}
	swap(func(c *config.Config) {
		c.Catalog.SyncInterval = 9 * time.Hour
		c.Log.Retention = 720 * time.Hour
	})
	if got := pending(); !slices.Contains(got, "catalog.sync_interval") {
		t.Fatalf("pending_restart = %v after an unrelated save, want the notice still standing", got)
	}
}

// A stream still running when the drain expires is cut by the gateway, and
// the handler has to be able to tell that apart from a client hanging up, or
// its request row blames the client. The cancellation also has to leave the
// handler time to send its final event before the socket is closed.
func TestADrainThatExpiresCancelsRequestsAsAShutdown(t *testing.T) {
	lc, cancelLC := context.WithCancelCause(context.Background())
	defer cancelLC(nil)
	started := make(chan struct{})
	causes := make(chan error, 1)
	proxy := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("data: first\n\n"))
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			causes <- context.Cause(r.Context())
			_, _ = w.Write([]byte("data: last\n\n"))
		}),
		BaseContext: func(net.Listener) context.Context { return lc },
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = proxy.Serve(ln)
	}()
	defer func() { <-served }()

	body := make(chan string, 1)
	go func() {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get("http://" + ln.Addr().String())
		if err != nil {
			body <- err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream never started")
	}

	drain, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := shutdownProxy(proxy, drain, cancelLC); err == nil {
		t.Fatal("the drain completed with a stream still in flight")
	}
	select {
	case cause := <-causes:
		if !errors.Is(cause, exec.ErrShutdown) {
			t.Errorf("request context cancelled with %v, want %v", cause, exec.ErrShutdown)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the in-flight request was never cancelled")
	}
	if got := <-body; !strings.Contains(got, "data: last") {
		t.Errorf("client saw %q, want the handler's final event", got)
	}
}
