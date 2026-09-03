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
	}, Preset{}, Doc{}, LiteLLMDoc{}, store.ModelOverride{})

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
			`": {"litellm_provider": "groq", "input_cost_per_token": 5.9e-07}}`))
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
