package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

func catalogOf(models ...catalog.Model) *catalog.Store {
	providers := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, m := range models {
		if !seen[m.ProviderID] {
			seen[m.ProviderID] = true
			providers = append(providers, m.ProviderID)
		}
	}
	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot(models, providers))
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

func TestLogPricesEachAttempt(t *testing.T) {
	cap := &captureLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(
		catalog.Model{ProviderID: "groq", ModelID: "m",
			Pricing: catalog.Pricing{InputMicrosPerMTok: 1_000_000, Known: true}},
		catalog.Model{ProviderID: "cerebras", ModelID: "m",
			Pricing: catalog.Pricing{Known: false}},
	)}}

	e.log(&store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 10,
		Attempts: []store.AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", TokensIn: 1_000_000},
			{Seq: 2, ProviderID: "cerebras", Model: "m", TokensIn: 500},
		},
	})

	a := cap.only(t).Attempts
	if a[0].CostMicros == nil || *a[0].CostMicros != 1_000_000 {
		t.Fatalf("attempt 1: want 1000000, got %v", a[0].CostMicros)
	}
	if a[1].CostMicros != nil {
		t.Fatalf("attempt 2 is unpriced: want nil, got %d", *a[1].CostMicros)
	}
}

func TestLogPricesAttemptsWhenNothingServed(t *testing.T) {
	// Every attempt failed, so FinalProviderID/FinalModel are empty and the
	// request itself stays unpriced -- but each attempt still burned tokens
	// at its own provider's rate and must be priced individually.
	cap := &captureLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(
		catalog.Model{ProviderID: "groq", ModelID: "m",
			Pricing: catalog.Pricing{InputMicrosPerMTok: 1_000_000, Known: true}},
	)}}

	e.log(&store.RequestRecord{
		FinalProviderID: "", FinalModel: "",
		Attempts: []store.AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", TokensIn: 1_000_000},
		},
	})

	rec := cap.only(t)
	if rec.CostMicros != nil {
		t.Fatalf("no served model: want request CostMicros nil, got %d", *rec.CostMicros)
	}
	if rec.Attempts[0].CostMicros == nil || *rec.Attempts[0].CostMicros != 1_000_000 {
		t.Fatalf("attempt 1: want 1000000, got %v", rec.Attempts[0].CostMicros)
	}
}
