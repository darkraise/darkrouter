package catalog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// embeddedPriced is a model the shipped snapshot carries a price for. The
// point of these tests is the path where the join against the embedded
// document succeeds, which is where a freshly synced row used to be ignored.
const (
	embeddedPreset = "groq"
	embeddedModel  = "llama-3.1-8b-instant"
)

func liveDocFixture(t *testing.T) (*store.DB, *Store) {
	t.Helper()
	ctx := context.Background()
	db := discoveryDB(t, "p")
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE providers SET preset = ? WHERE id = 'p'`, embeddedPreset); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]store.DiscoveredModel{{ModelID: embeddedModel}}, nil, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	src := &staticSource{ps: []provider.Provider{
		{ID: "p", Kind: "openaicompat", Preset: embeddedPreset},
	}}
	return db, NewStore(db, src)
}

func TestRebuildServesTheEmbeddedPriceWithNoLiveDocument(t *testing.T) {
	_, cat := liveDocFixture(t)
	if err := cat.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, ok := cat.Snapshot().Lookup("p", embeddedModel)
	if !ok {
		t.Fatalf("%s is not in the snapshot", embeddedModel)
	}
	want, _ := FallbackDoc().Metadata(embeddedPreset, embeddedModel)
	if m.Pricing.InputMicrosPerMTok != want.InputMicrosPerMTok {
		t.Errorf("input price = %d, want the embedded %d",
			m.Pricing.InputMicrosPerMTok, want.InputMicrosPerMTok)
	}
}

func TestRebuildPrefersTheLiveDocumentOverTheEmbeddedOne(t *testing.T) {
	_, cat := liveDocFixture(t)
	cat.SetDoc(func() Doc {
		return Doc{embeddedPreset: {embeddedModel: {
			ContextWindow:      4096,
			InputMicrosPerMTok: 111, OutputMicrosPerMTok: 222,
			CacheWriteMicrosPerMTok: 333,
			PriceKnown:              true,
		}}}
	})
	if err := cat.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, ok := cat.Snapshot().Lookup("p", embeddedModel)
	if !ok {
		t.Fatalf("%s is not in the snapshot", embeddedModel)
	}
	if m.Pricing.InputMicrosPerMTok != 111 || m.Pricing.OutputMicrosPerMTok != 222 {
		t.Errorf("pricing = %d/%d, want 111/222: a rebuild must price against "+
			"the newest document, not the one frozen into the binary",
			m.Pricing.InputMicrosPerMTok, m.Pricing.OutputMicrosPerMTok)
	}
	if m.ContextWindow != 4096 {
		t.Errorf("context window = %d, want 4096", m.ContextWindow)
	}
}

// A nil document source, and one that returns nothing, both have to fall back
// rather than blanking every price the binary shipped with.
func TestRebuildFallsBackWhenTheLiveDocumentIsEmpty(t *testing.T) {
	_, cat := liveDocFixture(t)
	cat.SetDoc(func() Doc { return nil })
	if err := cat.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ := cat.Snapshot().Lookup("p", embeddedModel)
	want, _ := FallbackDoc().Metadata(embeddedPreset, embeddedModel)
	if m.Pricing.InputMicrosPerMTok != want.InputMicrosPerMTok {
		t.Errorf("input price = %d, want the embedded %d",
			m.Pricing.InputMicrosPerMTok, want.InputMicrosPerMTok)
	}
}

// The syncer registers itself as the store's document source. Doing it here
// rather than at the call site is what stops a rebuild pricing against the
// embedded snapshot because a caller forgot to wire the two together.
func TestNewSyncerRegistersItselfAsTheStoresDocument(t *testing.T) {
	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{Presets: testPresets()})
	live := Doc{"acme": {"big": {InputMicrosPerMTok: 7, PriceKnown: true}}}
	s.doc.Store(&live)

	m, ok := cat.liveDoc().Metadata("acme", "big")
	if !ok || m.InputMicrosPerMTok != 7 {
		t.Errorf("acme/big priced at %d (found=%t); the syncer's own document "+
			"must be the one a rebuild prices against", m.InputMicrosPerMTok, ok)
	}
}

// A model no document prices falls back to its row. The row now carries a
// cache-write rate, so the merge has to read it rather than leaving it zero.
func TestMergeReadsCacheWritePricingFromAnUnjoinedRow(t *testing.T) {
	got := mergeOne(store.ModelRow{
		ProviderID: "p", ModelID: "private", State: "live",
		InputMicrosPerMTok: 300_000, OutputMicrosPerMTok: 1_500_000,
		CacheReadMicrosPerMTok: 30_000, CacheWriteMicrosPerMTok: 375_000,
		PriceKnown: true,
	}, "", Preset{}, Doc{}, LiteLLMDoc{}, FreeCatalog{}, store.ModelOverride{})

	if got.Source != SourceInferred {
		t.Fatalf("source = %v, want the row to be the source", got.Source)
	}
	if got.Pricing.CacheWriteMicrosPerMTok != 375_000 {
		t.Errorf("cache write price = %d, want 375000",
			got.Pricing.CacheWriteMicrosPerMTok)
	}
}

// The sync parses a cache-write rate out of models.dev; it has to reach SQLite
// or the row it writes prices cached writes at zero.
func TestSyncPersistsCacheWritePricing(t *testing.T) {
	const doc = `{"acme":{"id":"acme","models":{
	  "big":{"id":"big","limit":{"context":100,"output":10},
	         "cost":{"input":0.3,"output":1.5,"cache_read":0.03,"cache_write":0.375}}
	}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(doc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	s := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()})
	if err := s.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ModelID != "big" {
			continue
		}
		if r.CacheWriteMicrosPerMTok != 375_000 {
			t.Errorf("cache write price = %d, want 375000", r.CacheWriteMicrosPerMTok)
		}
		return
	}
	t.Fatal("no row for big")
}

// unpricedModel is a model no metadata document has heard of, which is the
// state the LiteLLM index exists to answer.
const unpricedModel = "zzz-only-the-index-knows-me"

func liteLLMFixture(t *testing.T) (*store.DB, *Store) {
	t.Helper()
	db, cat := liveDocFixture(t)
	if err := db.RecordDiscoverySuccess(context.Background(), "p",
		[]store.DiscoveredModel{{ModelID: embeddedModel}, {ModelID: unpricedModel}},
		nil, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	return db, cat
}

// The store is only wired to the index if a rebuild reads it. Proving mergeOne
// resolves a LiteLLM candidate says nothing about whether one ever reaches it.
func TestRebuildPricesFromTheLiveLiteLLMIndex(t *testing.T) {
	_, cat := liteLLMFixture(t)
	cat.SetLiteLLM(func() LiteLLMDoc {
		return LiteLLMDoc{embeddedPreset: {unpricedModel: {
			InputMicrosPerMTok: 590, OutputMicrosPerMTok: 790,
			Known: true, Source: SourceLiteLLM,
		}}}
	})
	if err := cat.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, ok := cat.Snapshot().Lookup("p", unpricedModel)
	if !ok {
		t.Fatalf("%s is not in the snapshot", unpricedModel)
	}
	if m.Pricing.Source != SourceLiteLLM || m.Pricing.InputMicrosPerMTok != 590 {
		t.Errorf("pricing = %+v, want the live index's 590 at grade indexed: a "+
			"rebuild that does not read the index leaves the model unpriced",
			m.Pricing)
	}
}

// No index source at all is the cold start, and the state a disabled refresh
// leaves permanently. It must read as unpriced rather than panicking.
func TestRebuildWithNoLiteLLMIndexLeavesTheModelUnpriced(t *testing.T) {
	_, cat := liteLLMFixture(t)
	cat.SetLiteLLM(func() LiteLLMDoc { return nil })
	if err := cat.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ := cat.Snapshot().Lookup("p", unpricedModel)
	if m.Pricing.Known {
		t.Errorf("pricing = %+v, want no price", m.Pricing)
	}
}

// The whole path: a fetch parses an index, the callback rebuilds, and the model
// is priced. Without the callback a successful sync is invisible until
// something else happens to rebuild.
func TestASyncedLiteLLMIndexReachesTheSnapshot(t *testing.T) {
	_, cat := liteLLMFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"groq/` + unpricedModel +
			`": {"litellm_provider": "groq", "input_cost_per_token": 5.9e-07,"output_cost_per_token":5.9e-07}}`))
	}))
	defer srv.Close()

	syncer := NewLiteLLMSyncer(LiteLLMSyncOptions{URL: srv.URL, OnUpdate: func(c context.Context) {
		if err := cat.Rebuild(c); err != nil {
			t.Error(err)
		}
	}})
	cat.SetLiteLLM(syncer.Doc)
	if err := syncer.SyncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	m, ok := cat.Snapshot().Lookup("p", unpricedModel)
	if !ok {
		t.Fatalf("%s is not in the snapshot", unpricedModel)
	}
	if m.Pricing.Source != SourceLiteLLM || m.Pricing.InputMicrosPerMTok != 590_000 {
		t.Errorf("pricing = %+v, want the synced index's 590000", m.Pricing)
	}
}

// The router's veto reads the tier carried on the model, so a free-tier
// grading the daily sync changed must reach the snapshot, not only the import
// filter.
func TestASyncedFreeTierReachesTheSnapshot(t *testing.T) {
	const model = "freshly-graded-model"
	ctx := context.Background()
	db := discoveryDB(t, "p")
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE providers SET preset = 'groq' WHERE id = 'p'`); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]store.DiscoveredModel{{ModelID: model}}, nil, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	src := &staticSource{ps: []provider.Provider{{ID: "p", Kind: "openaicompat", Preset: "groq"}}}
	cat := NewStore(db, src)
	if _, ok := FreeModels().Tier("groq", model); ok {
		t.Fatal("the embedded catalogue already grades the model; the test proves nothing")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`
export const FREE_CATALOG_CURATED_AT = "2026-09-01";
export const FREE_MODEL_BUDGETS: FreeModelBudget[] = [
  { provider: "groq", modelId: "` + model + `", displayName: "Fresh", monthlyTokens: 0, creditTokens: 0, freeType: "recurring-daily", poolKey: "groq", tos: "avoid" },
];
`))
	}))
	defer srv.Close()

	syncer := NewFreeSyncer(FreeSyncOptions{URL: srv.URL, OnUpdate: func(c context.Context) {
		if err := cat.Rebuild(c); err != nil {
			t.Error(err)
		}
	}})
	cat.SetFreeTiers(syncer.Catalog)
	if err := syncer.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}

	m, ok := cat.Snapshot().Lookup("p", model)
	if !ok {
		t.Fatalf("%s is not in the snapshot", model)
	}
	if !m.FreeTier.Vetoed() {
		t.Errorf("free tier = %+v, want the synced avoid grading", m.FreeTier)
	}
}

// A listing that quotes input and output but no cache rates outranks
// models.dev for the rates it quoted, and only those. Taking the stored record
// whole costed every cached token at zero.
func TestAListedPriceTakesCacheRatesItDidNotQuote(t *testing.T) {
	ctx := context.Background()
	db := discoveryDB(t, "p")
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE providers SET preset = ? WHERE id = 'p'`, embeddedPreset); err != nil {
		t.Fatal(err)
	}
	zero := int64(0)
	if err := db.RecordDiscoverySuccess(ctx, "p", []store.DiscoveredModel{
		{ModelID: embeddedModel, Pricing: &store.ModelPricing{
			InputMicrosPerMTok: 50_000, OutputMicrosPerMTok: 80_000,
		}},
		{ModelID: "quoted-zero-cache", Pricing: &store.ModelPricing{
			InputMicrosPerMTok: 50_000, OutputMicrosPerMTok: 80_000,
			CacheReadMicrosPerMTok: &zero,
		}},
	}, nil, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	src := &staticSource{ps: []provider.Provider{{ID: "p", Kind: "openaicompat", Preset: embeddedPreset}}}
	cat := NewStore(db, src)
	directory := Metadata{
		InputMicrosPerMTok: 999_000, OutputMicrosPerMTok: 999_000,
		CacheReadMicrosPerMTok: 25_000, CacheWriteMicrosPerMTok: 60_000,
		PriceKnown: true,
	}
	cat.SetDoc(func() Doc {
		return Doc{embeddedPreset: {embeddedModel: directory, "quoted-zero-cache": directory}}
	})
	if err := cat.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}

	m, _ := cat.Snapshot().Lookup("p", embeddedModel)
	if m.Pricing.Source != SourceDiscovered || m.Pricing.InputMicrosPerMTok != 50_000 ||
		m.Pricing.OutputMicrosPerMTok != 80_000 {
		t.Errorf("pricing = %+v, want the listing's input and output", m.Pricing)
	}
	if m.Pricing.CacheReadMicrosPerMTok != 25_000 || m.Pricing.CacheWriteMicrosPerMTok != 60_000 {
		t.Errorf("cache = %d/%d, want models.dev's 25000/60000 for rates the listing did not quote",
			m.Pricing.CacheReadMicrosPerMTok, m.Pricing.CacheWriteMicrosPerMTok)
	}

	// The filled rates are models.dev's, and a cost that used one rests on it.
	if got := m.Pricing.GradeFor(Tokens{Input: 1000}); got != GradeMeasured {
		t.Errorf("grade without cached tokens = %q, want measured", got)
	}
	if got := m.Pricing.GradeFor(Tokens{Input: 1000, CacheRead: 1000}); got != GradeIndexed {
		t.Errorf("grade with cache reads = %q, want indexed: the read rate is models.dev's", got)
	}
	if got := m.Pricing.GradeFor(Tokens{Input: 1000, CacheWrite: 1000}); got != GradeIndexed {
		t.Errorf("grade with cache writes = %q, want indexed: the write rate is models.dev's", got)
	}

	// A rate the listing did quote, zero included, is still the listing's.
	q, _ := cat.Snapshot().Lookup("p", "quoted-zero-cache")
	if q.Pricing.CacheReadMicrosPerMTok != 0 {
		t.Errorf("cache read = %d, want the quoted 0", q.Pricing.CacheReadMicrosPerMTok)
	}
	if q.Pricing.CacheWriteMicrosPerMTok != 60_000 {
		t.Errorf("cache write = %d, want models.dev's 60000", q.Pricing.CacheWriteMicrosPerMTok)
	}
	if got := q.Pricing.GradeFor(Tokens{Input: 1000, CacheRead: 1000}); got != GradeMeasured {
		t.Errorf("grade with cache reads = %q, want measured: the listing quoted that rate", got)
	}
	if got := q.Pricing.GradeFor(Tokens{Input: 1000, CacheWrite: 1000}); got != GradeIndexed {
		t.Errorf("grade with cache writes = %q, want indexed", got)
	}
	// A write whose TTL the response broke out is priced from the input rate,
	// which the listing quoted.
	if got := q.Pricing.GradeFor(Tokens{Input: 1000, CacheWrite: 1000, CacheWrite5m: 1000}); got != GradeMeasured {
		t.Errorf("grade with TTL-priced cache writes = %q, want measured", got)
	}
}

// A model the listing quotes free is free for cached tokens too. Syncs before
// cache columns were tracked stored a quoted zero cache rate as unset, so a
// fill here would start charging for a model the provider gives away.
func TestAFreeListedPriceTakesNoCacheRates(t *testing.T) {
	ctx := context.Background()
	db := discoveryDB(t, "p")
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE providers SET preset = ? WHERE id = 'p'`, embeddedPreset); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []store.DiscoveredModel{
		{ModelID: embeddedModel, Pricing: &store.ModelPricing{}},
	}, nil, time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	src := &staticSource{ps: []provider.Provider{{ID: "p", Kind: "openaicompat", Preset: embeddedPreset}}}
	cat := NewStore(db, src)
	cat.SetDoc(func() Doc {
		return Doc{embeddedPreset: {embeddedModel: Metadata{
			CacheReadMicrosPerMTok: 25_000, CacheWriteMicrosPerMTok: 60_000,
			PriceKnown: true,
		}}}
	})
	if err := cat.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}

	m, _ := cat.Snapshot().Lookup("p", embeddedModel)
	if m.Pricing.Source != SourceDiscovered || !m.Pricing.Known {
		t.Fatalf("pricing = %+v, want the listing's known free price", m.Pricing)
	}
	if m.Pricing.CacheReadMicrosPerMTok != 0 || m.Pricing.CacheWriteMicrosPerMTok != 0 {
		t.Errorf("cache = %d/%d, want 0/0 for a model listed free",
			m.Pricing.CacheReadMicrosPerMTok, m.Pricing.CacheWriteMicrosPerMTok)
	}
}
