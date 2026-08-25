package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

func catalogOf(m catalog.Model) *catalog.Store {
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{m}, []string{m.ProviderID}))
	return cat
}

func TestLogPricesTheServedModel(t *testing.T) {
	cap := &captureLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(catalog.Model{
		ProviderID: "groq", ModelID: "openai/gpt-oss-120b",
		Pricing: catalog.Pricing{
			InputMicrosPerMTok: 3_000_000, OutputMicrosPerMTok: 15_000_000, Known: true,
		},
	})}}

	e.log(&store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "openai/gpt-oss-120b",
		TokensIn: 2_000_000, TokensOut: 1_000_000,
	})

	rec := cap.only(t)
	if rec.CostMicros == nil {
		t.Fatal("priced model: CostMicros is nil")
	}
	if *rec.CostMicros != 21_000_000 {
		t.Fatalf("want 21000000, got %d", *rec.CostMicros)
	}
}

func TestLogLeavesCostNilForAnUnpricedModel(t *testing.T) {
	cap := &captureLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(catalog.Model{
		ProviderID: "together", ModelID: "black-forest-labs/FLUX.1-schnell",
		Pricing: catalog.Pricing{Known: false},
	})}}

	e.log(&store.RequestRecord{
		FinalProviderID: "together", FinalModel: "black-forest-labs/FLUX.1-schnell",
		TokensIn: 10, TokensOut: 10,
	})

	rec := cap.only(t)
	if rec.CostMicros != nil {
		t.Fatalf("unpriced model: want nil, got %d", *rec.CostMicros)
	}
}

func TestLogLeavesCostNilWhenNothingServed(t *testing.T) {
	// Every attempt failed, so there is no served model to price. A zero here
	// would put a free request in the log for one that never completed.
	cap := &captureLogger{}
	e := &Executor{deps: Deps{Log: cap}}
	e.log(&store.RequestRecord{FinalProviderID: "", FinalModel: ""})
	rec := cap.only(t)
	if rec.CostMicros != nil {
		t.Fatalf("no served model: want nil, got %d", *rec.CostMicros)
	}
}

func TestLogDoesNotOverwriteACostAlreadySet(t *testing.T) {
	// A surface that priced itself (per-call rather than per-token) keeps its
	// own number; the catalog rate would be wrong for it.
	cap := &captureLogger{}
	pre := int64(999)
	e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(catalog.Model{
		ProviderID: "groq", ModelID: "m",
		Pricing: catalog.Pricing{InputMicrosPerMTok: 1_000_000, Known: true},
	})}}
	e.log(&store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 1_000_000, CostMicros: &pre,
	})
	rec := cap.only(t)
	if rec.CostMicros == nil || *rec.CostMicros != 999 {
		t.Fatalf("want the pre-set 999, got %v", rec.CostMicros)
	}
}
