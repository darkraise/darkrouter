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

func TestSweepReadsCapabilitiesFromTheRuntime(t *testing.T) {
	// Spec §6 and its done criterion: a local model's tool support is read
	// from the runtime rather than guessed, so it becomes a fact the router
	// can filter on.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"llama3.3:70b"}]}`))
		case "/api/show":
			_, _ = w.Write([]byte(`{"capabilities":["completion","tools"]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	db := discoveryDB(t, "local")
	src := &staticSource{ps: []provider.Provider{{
		ID: "local", Kind: "openaicompat", BaseURL: srv.URL + "/v1", Preset: "ollama",
		Credentials: []provider.Credential{{ID: "k", Secret: "", Enabled: true}},
	}}}
	NewDiscoverer(db, src, NewStore(db, src), &fakeHealth{}, DiscoveryOptions{}).SweepOnce(context.Background())

	rows, err := db.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].CapabilitiesSource != "discovered" || !rows[0].Capabilities.Tools {
		t.Errorf("row = (%q, %+v)", rows[0].CapabilitiesSource, rows[0].Capabilities)
	}
}

func TestPriceLookupPrefersTheSyncedDocument(t *testing.T) {
	// A price refreshed this morning is the one an import filter should decide
	// on. The snapshot is the offline fallback, not the source of record.
	live := Doc{"acme": {"m": {InputMicrosPerMTok: 0, OutputMicrosPerMTok: 0, PriceKnown: true}}}
	d := &Discoverer{opts: DiscoveryOptions{Metadata: func() Doc { return live }}}
	price := d.priceLookup(Preset{ModelsDevID: "acme"})
	if price == nil {
		t.Fatal("a preset with a join key must yield a lookup")
	}
	meta, ok := price("m")
	if !ok || !meta.PriceKnown {
		t.Fatalf("lookup returned (%+v, %v); want the synced entry", meta, ok)
	}
}

func TestPriceLookupFallsBackToTheSnapshot(t *testing.T) {
	// No syncer, or one that has never held a document: the embedded snapshot
	// still prices the filter, which is what an offline gateway runs on.
	for _, meta := range []func() Doc{nil, func() Doc { return Doc{} }} {
		d := &Discoverer{opts: DiscoveryOptions{Metadata: meta}}
		price := d.priceLookup(Preset{ModelsDevID: "groq"})
		if price == nil {
			t.Fatal("a preset with a join key must yield a lookup")
		}
		if _, ok := price("allam-2-7b"); !ok {
			t.Error("the embedded snapshot did not answer")
		}
	}
}

func TestPriceLookupIsNilForAnUncataloguedPreset(t *testing.T) {
	d := &Discoverer{opts: DiscoveryOptions{}}
	if d.priceLookup(Preset{}) != nil {
		t.Error("a preset with no join key has nothing to look prices up in")
	}
}

func TestFreeRulesCarryTheProvidersCuratedTier(t *testing.T) {
	// Keyed on the preset, because the curated catalogue is a fact about the
	// upstream vendor rather than about the row an operator named.
	d := &Discoverer{opts: DiscoveryOptions{}}
	rules := d.freeRules(provider.Provider{ID: "my-groq", Preset: "groq"}, Preset{ModelsDevID: "groq"})
	if rules.Curated == nil {
		t.Fatal("a provider the catalogue covers must carry its curated rule")
	}
	if !rules.Curated("openai/gpt-oss-120b") {
		t.Error("gpt-oss-120b is on Groq's documented free tier")
	}
	if rules.Curated("not-a-model") {
		t.Error("the curated rule admitted a model the catalogue does not list")
	}
}

func TestFreeRulesFallBackToTheProviderIDForAnUnpresetedRow(t *testing.T) {
	d := &Discoverer{opts: DiscoveryOptions{}}
	rules := d.freeRules(provider.Provider{ID: "groq"}, Preset{})
	if rules.Curated == nil || !rules.Curated("openai/gpt-oss-20b") {
		t.Error("a row with no preset must fall back to its own id")
	}
}

func TestFreeRulesLeaveAnUncoveredProviderToItsPrices(t *testing.T) {
	d := &Discoverer{opts: DiscoveryOptions{}}
	rules := d.freeRules(provider.Provider{ID: "nobody", Preset: "nobody"}, Preset{})
	if rules.Curated != nil {
		t.Error("a provider no catalogue covers must carry no curated rule")
	}
}

func TestFreeRulesPreferTheSyncedCatalogue(t *testing.T) {
	// A provider that opened a free tier after this binary was built is
	// invisible to the embedded catalogue, which is the whole reason the daily
	// refresh exists.
	live := FreeCatalog{Providers: map[string]map[string]FreeTier{
		"newcomer": {"m": {FreeType: "recurring-daily"}},
	}}
	d := &Discoverer{opts: DiscoveryOptions{FreeTiers: func() FreeCatalog { return live }}}
	rules := d.freeRules(provider.Provider{ID: "newcomer", Preset: "newcomer"}, Preset{})
	if rules.Curated == nil || !rules.Curated("m") {
		t.Error("the synced catalogue did not reach the import filter")
	}
}

func TestFreeRulesFallBackToTheEmbeddedCatalogue(t *testing.T) {
	for _, tiers := range []func() FreeCatalog{nil, func() FreeCatalog { return FreeCatalog{} }} {
		d := &Discoverer{opts: DiscoveryOptions{FreeTiers: tiers}}
		rules := d.freeRules(provider.Provider{ID: "groq", Preset: "groq"}, Preset{})
		if rules.Curated == nil || !rules.Curated("openai/gpt-oss-120b") {
			t.Error("an unsynced gateway lost the catalogue its release shipped with")
		}
	}
}

func TestAKeylessProviderIsSweptWithoutACredential(t *testing.T) {
	// Its catalogue would never fill otherwise: a local runtime an operator
	// can reach from a browser would sit in the console offering nothing.
	d := &Discoverer{opts: DiscoveryOptions{}, health: &fakeHealth{}}
	_, ok := d.pickCredential(provider.Provider{ID: "ollama", AuthStyle: "none"})
	if !ok {
		t.Error("a keyless provider must be swept with no credential")
	}
}

func TestAKeyedProviderWithNoCredentialIsNotSwept(t *testing.T) {
	// Unchanged: a sweep needs one of the provider's own keys to ask what it
	// serves, and there is nothing to ask with.
	d := &Discoverer{opts: DiscoveryOptions{}, health: &fakeHealth{}}
	if _, ok := d.pickCredential(provider.Provider{ID: "groq", AuthStyle: "bearer"}); ok {
		t.Error("a keyed provider with no credential must not be swept")
	}
}

func TestSeedingReadsTheSyncedDocument(t *testing.T) {
	// A seed-only kind has no listing to refresh from, so the synced document
	// is the only way a model models.dev added after the build reaches it.
	preset := Embedded()["vertex"]
	live := Doc{preset.ModelsDevID: {"fresh-model": {ContextWindow: 1, PriceKnown: true}}}
	d := &Discoverer{opts: DiscoveryOptions{Metadata: func() Doc { return live }}}
	seeded := SeedFromPreset(preset, d.doc())
	if len(seeded) != 1 || seeded[0].ModelID != "fresh-model" {
		t.Fatalf("seeded = %+v, want the synced document's model", seeded)
	}
	for _, meta := range []func() Doc{nil, func() Doc { return Doc{} }} {
		d := &Discoverer{opts: DiscoveryOptions{Metadata: meta}}
		if len(SeedFromPreset(preset, d.doc())) == 0 {
			t.Error("the embedded snapshot did not seed")
		}
	}
}
