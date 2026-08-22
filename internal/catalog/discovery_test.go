package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// fakeHealth records what discovery told the breaker, without a real one.
type fakeHealth struct {
	mu       sync.Mutex
	signals  []health.Signal
	keys     []health.Key
	cooling  map[health.CredKey]bool
	lastUsed map[health.CredKey]time.Time
}

func (f *fakeHealth) Record(k health.Key, s health.Signal) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, k)
	f.signals = append(f.signals, s)
}

func (f *fakeHealth) Available(k health.Key) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.cooling[health.CredKey{ProviderID: k.ProviderID, KeyID: k.KeyID}]
}

func (f *fakeHealth) LastUsedSnapshot() map[health.CredKey]time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[health.CredKey]time.Time, len(f.lastUsed))
	for k, v := range f.lastUsed {
		out[k] = v
	}
	return out
}

func (f *fakeHealth) MarkUsed(ck health.CredKey, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lastUsed == nil {
		f.lastUsed = map[health.CredKey]time.Time{}
	}
	f.lastUsed[ck] = at
}

// staticSource serves a fixed provider set.
type staticSource struct{ ps []provider.Provider }

func (s *staticSource) Providers(context.Context) ([]provider.Provider, error) { return s.ps, nil }
func (s *staticSource) Revision() uint64                                       { return 1 }

func discoveryDB(t *testing.T, ids ...string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(t.TempDir() + "/d.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO providers (id, kind, base_url, created_at) VALUES (?, 'openaicompat', 'http://x', 0)`,
			id); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestSweepRecordsWhatItListed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("probed %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-1" {
			t.Errorf("auth = %q", got)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"},{"id":"m2"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k1", Secret: "sk-1", Enabled: true}},
	}}}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{})
	d.SweepOnce(context.Background())

	rows, err := db.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d models, want 2", len(rows))
	}
	if rows[0].State != "live" {
		t.Errorf("state = %q", rows[0].State)
	}
}

func TestSweepCoolsTheCredentialOnA401(t *testing.T) {
	// A 401 on a probe is the same evidence as a 401 on a request, so it must
	// cool the credential rather than being logged and forgotten.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k1", Secret: "sk", Enabled: true}},
	}}}
	h := &fakeHealth{}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.signals) != 1 {
		t.Fatalf("recorded %d signals, want 1", len(h.signals))
	}
	if h.signals[0].Outcome != adapter.OutcomeRetryableCredential || h.signals[0].StatusCode != 401 {
		t.Errorf("signal = %+v", h.signals[0])
	}
	if h.keys[0].ProviderID != "p" || h.keys[0].KeyID != "k1" {
		t.Errorf("key = %+v", h.keys[0])
	}
	// A failed probe must still be recorded as a failure, not silently
	// dropped, or the three-strike ladder never advances.
	states, _ := db.DiscoveryStates(context.Background())
	if states["p"].ConsecutiveFailures != 1 {
		t.Errorf("failures = %d, want 1", states["p"].ConsecutiveFailures)
	}
}

func TestSweepSkipsCoolingCredentialsAndPicksLRU(t *testing.T) {
	var seen atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{
			{ID: "hot", Secret: "sk-hot", Enabled: true},
			{ID: "cold", Secret: "sk-cold", Enabled: true},
			{ID: "cooling", Secret: "sk-cooling", Enabled: true},
		},
	}}}
	h := &fakeHealth{
		cooling:  map[health.CredKey]bool{{ProviderID: "p", KeyID: "cooling"}: true},
		lastUsed: map[health.CredKey]time.Time{{ProviderID: "p", KeyID: "hot"}: time.Now()},
	}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	// "cold" has never been used, so it sorts before "hot"; "cooling" is
	// excluded outright.
	if got := seen.Load(); got != "Bearer sk-cold" {
		t.Errorf("probed with %v, want the least-recently-used credential", got)
	}
}

func TestSweepSkipsProvidersWithNoUsableCredential(t *testing.T) {
	var probed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probed.Store(true)
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	h := &fakeHealth{cooling: map[health.CredKey]bool{{ProviderID: "p", KeyID: "k"}: true}}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	if probed.Load() {
		t.Error("probed with a cooling credential")
	}
	// Every credential cooling is not a discovery failure. Counting it would
	// walk the provider to stale for a reason that has nothing to do with its
	// listing endpoint.
	states, _ := db.DiscoveryStates(context.Background())
	if states["p"].ConsecutiveFailures != 0 {
		t.Errorf("failures = %d, want 0", states["p"].ConsecutiveFailures)
	}
}

func TestGlobalConcurrencyCapHoldsOnAColdStart(t *testing.T) {
	// Spec §5: the cap is global across the fleet. A per-provider cap would
	// not stop forty providers opening forty connections at once, which is the
	// case this asserts.
	var live, peak atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := live.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		live.Add(-1)
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	defer srv.Close()

	ids := make([]string, 40)
	ps := make([]provider.Provider, 40)
	for i := range ids {
		ids[i] = "p" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		ps[i] = provider.Provider{
			ID: ids[i], Kind: "openaicompat", BaseURL: srv.URL + "/v1",
			Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
		}
	}
	db := discoveryDB(t, ids...)
	src := &staticSource{ps: ps}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{Concurrency: 4})
	d.SweepOnce(context.Background())

	if got := peak.Load(); got > 4 {
		t.Errorf("peak concurrency = %d, want at most 4", got)
	}
	rows, _ := db.Models(context.Background())
	if len(rows) != 40 {
		t.Errorf("catalogued %d models, want 40 — the cap dropped work", len(rows))
	}
}

func TestSweepSkipsUndiscoverableKindsWithoutFailing(t *testing.T) {
	// Vertex has no listing API. Probing it every tick would walk it to stale
	// and, with a real breaker, cool a credential for a call that was never
	// going to work.
	db := discoveryDB(t)
	if _, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('v', 'vertex', 'https://example.invalid', 0)`); err != nil {
		t.Fatal(err)
	}
	src := &staticSource{ps: []provider.Provider{{
		ID: "v", Kind: "vertex", BaseURL: "https://example.invalid",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	h := &fakeHealth{}
	NewDiscoverer(db, src, NewStore(db, src), h, DiscoveryOptions{}).SweepOnce(context.Background())

	states, _ := db.DiscoveryStates(context.Background())
	if states["v"].ConsecutiveFailures != 0 {
		t.Errorf("failures = %d for an undiscoverable kind, want 0", states["v"].ConsecutiveFailures)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.signals) != 0 {
		t.Errorf("recorded %d health signals for a kind it never probed", len(h.signals))
	}
}

func TestSweepRebuildsTheSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	cat := NewStore(db, src)
	NewDiscoverer(db, src, cat, &fakeHealth{}, DiscoveryOptions{}).SweepOnce(context.Background())

	// Without the rebuild the router keeps serving the previous snapshot and
	// the newly discovered model is invisible until something else swaps it.
	if _, ok := cat.Snapshot().Lookup("p", "m1"); !ok {
		t.Error("the snapshot was not rebuilt after discovery")
	}
}

func TestTriggerProbesOneProviderPromptly(t *testing.T) {
	// Spec §5: on-demand discovery so the UI shows models immediately rather
	// than after the next fifteen-minute tick.
	done := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
		select {
		case done <- struct{}{}:
		default:
		}
	}))
	defer srv.Close()

	db := discoveryDB(t, "p")
	src := &staticSource{ps: []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: srv.URL + "/v1",
		Credentials: []provider.Credential{{ID: "k", Secret: "sk", Enabled: true}},
	}}}
	// A long interval, so anything that arrives came from the trigger.
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{Interval: time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	d.Trigger("p")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the trigger did not produce a probe")
	}
	cancel()
	if err := <-errCh; err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	db := discoveryDB(t)
	src := &staticSource{}
	d := NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop on cancel")
	}
}
