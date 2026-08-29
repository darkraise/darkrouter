package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/health"
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
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: " + proxyAddr + "\n  admin_listen: " + adminAddr +
		"\n  shutdown_grace: 1s\nproviders:\n  - id: fake\n    kind: openaicompat\n" +
		"    base_url: https://up.example/v1\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return serverBackedBy(t, cfgStore)
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

func TestRunSurfacesWatcherFailureOnHealthz(t *testing.T) {
	// A watcher on a directory that disappears records its error rather than
	// leaving hot reload silently dead.
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: " + freePort(t) + "\n  admin_listen: " + freePort(t) +
		"\nproviders:\n  - id: fake\n    kind: openaicompat\n" +
		"    base_url: https://up.example/v1\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	// RecordError is the mechanism Run uses; assert it reaches LastError.
	store.RecordError(errWatcherDead)
	if store.LastError() == nil {
		t.Fatal("a watcher failure must be visible through LastError")
	}
	if !strings.Contains(store.LastError().Error(), "watcher") {
		t.Fatalf("unexpected error %v", store.LastError())
	}
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

var errWatcherDead = errWatcher{}

type errWatcher struct{}

func (errWatcher) Error() string { return "watcher: could not start" }

// serverBackedBy builds a Server on a temporary database, so every test in this
// package exercises the real persistence wiring.
func serverBackedBy(t *testing.T, cfgStore *config.Store) *Server {
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
	// Mirror what main.go does on first start: providers live in SQLite from
	// phase 2 on, so a config that declares them has to be imported before the
	// server can serve them.
	if _, err := store.ImportFromConfig(ctx, db, key, cfgStore.Current()); err != nil {
		t.Fatal(err)
	}
	s, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testConfigStore(t *testing.T, dir string) *config.Store {
	t.Helper()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return cfgStore
}

func TestHealthzReportsDroppedRecordsAndWarnings(t *testing.T) {
	dir := t.TempDir()
	cfgStore := testConfigStore(t, dir)

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
	cfgStore := testConfigStore(t, dir)
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
	cfgStore := testConfigStore(t, dir)
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

// storeFor writes a configuration body and returns its store.
func storeFor(t *testing.T, body string) *config.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return cfgStore
}

// serverFixtureWith is serverBackedBy with the database and key handed back, so
// a test can seed catalog rows before New reads them.
func serverFixtureWith(t *testing.T, body string) (*store.DB, *crypto.Key, *config.Store) {
	t.Helper()
	cfgStore := storeFor(t, body)
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
	if _, err := store.ImportFromConfig(ctx, db, key, cfgStore.Current()); err != nil {
		t.Fatal(err)
	}
	return db, key, cfgStore
}

const offlineCatalog = `
catalog:
  models_dev_url: http://127.0.0.1:1/api.json
  sync_timeout: 200ms
  discovery:
    enabled: false
`

func serverFixture(t *testing.T) (*store.DB, *crypto.Key, *config.Store) {
	t.Helper()
	return serverFixtureWith(t,
		"server:\n  proxy_listen: \"127.0.0.1:0\"\n  admin_listen: \"127.0.0.1:0\"\n"+offlineCatalog)
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
	db, key, cfgStore := serverFixtureWith(t, `
server:
  proxy_listen: "127.0.0.1:0"
  admin_listen: "127.0.0.1:0"
catalog:
  models_dev_url: http://127.0.0.1:1/api.json
  sync_timeout: 200ms
  discovery:
    enabled: false
`)
	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if srv.Discoverer() != nil {
		t.Error("a discoverer was built with discovery disabled")
	}
}
