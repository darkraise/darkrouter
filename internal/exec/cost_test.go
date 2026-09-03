package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

// newPricedExecutorWithCacheWrite extends newPricedExecutor's rates with a
// cache-write price, for tests that need the premium billed.
func newPricedExecutorWithCacheWrite(t *testing.T) *Executor {
	t.Helper()
	return &Executor{deps: Deps{Catalog: catalogOf(catalog.Model{
		ProviderID: "groq", ModelID: "m",
		Pricing: catalog.Pricing{
			InputMicrosPerMTok: 1_000_000, OutputMicrosPerMTok: 1_000_000,
			CacheReadMicrosPerMTok: 100_000, CacheWriteMicrosPerMTok: 1_250_000,
			Known: true,
		},
	})}}
}

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

func TestTheRequestsCacheWritesArePriced(t *testing.T) {
	e := newPricedExecutorWithCacheWrite(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		TokensIn: 1000, TokensOut: 0, CacheWriteTokens: 4000,
	}
	e.priceRecord(rec)
	if rec.CostMicros == nil {
		t.Fatal("not priced")
	}
	// The cache-write component must be present, not silently dropped.
	if *rec.CostMicros <= 1000 {
		t.Fatalf("cost %d does not include the cache write", *rec.CostMicros)
	}
}

func TestLogRecordsTheGradeBehindTheServedPrice(t *testing.T) {
	// Both directions, so a constant cannot pass: the grade has to come from
	// the price the model actually carries.
	for _, tc := range []struct {
		source catalog.Source
		want   string
	}{
		{catalog.SourceDiscovered, "measured"},
		{catalog.SourceModelsDev, "indexed"},
	} {
		t.Run(string(tc.source), func(t *testing.T) {
			cap := &captureLogger{}
			e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(catalog.Model{
				ProviderID: "groq", ModelID: "m",
				Pricing: catalog.Pricing{
					InputMicrosPerMTok: 1_000_000, Known: true, Source: tc.source,
				},
			})}}

			e.log(&store.RequestRecord{
				FinalProviderID: "groq", FinalModel: "m", TokensIn: 1_000_000,
			})

			rec := cap.only(t)
			if rec.CostMicros == nil {
				t.Fatal("priced model: CostMicros is nil")
			}
			if rec.PriceGrade != tc.want {
				t.Fatalf("PriceGrade = %q, want %q: the spend total cannot say what "+
					"it rests on if the grade is not recorded with the cost",
					rec.PriceGrade, tc.want)
			}
		})
	}
}

func TestLogLeavesTheGradeEmptyForAnUnpricedModel(t *testing.T) {
	// A grade with no cost behind it would mark a total that this request
	// contributed nothing to.
	cap := &captureLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(catalog.Model{
		ProviderID: "groq", ModelID: "m",
		Pricing: catalog.Pricing{Known: false, Source: catalog.SourceModelsDev},
	})}}

	e.log(&store.RequestRecord{FinalProviderID: "groq", FinalModel: "m", TokensIn: 10})

	if rec := cap.only(t); rec.PriceGrade != "" {
		t.Fatalf("PriceGrade = %q, want empty for a model with no price", rec.PriceGrade)
	}
}

func TestLogGradesADiscardedAttemptAgainstTheModelItTried(t *testing.T) {
	// The failed attempt's tokens were burned at the failed provider's rate,
	// so they carry that provider's authority -- not the one that served.
	// Both directions, and the two providers are graded oppositely, so a grade
	// copied from the record fails as loudly as one never set.
	for _, tc := range []struct {
		tried, served catalog.Source
		wantAttempt   string
		wantRecord    string
	}{
		{catalog.SourceModelsDev, catalog.SourceDiscovered, "indexed", "measured"},
		{catalog.SourceDiscovered, catalog.SourceModelsDev, "measured", "indexed"},
	} {
		t.Run(string(tc.tried), func(t *testing.T) {
			cap := &captureLogger{}
			e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(
				catalog.Model{ProviderID: "cerebras", ModelID: "m",
					Pricing: catalog.Pricing{
						InputMicrosPerMTok: 1_000_000, Known: true, Source: tc.tried,
					}},
				catalog.Model{ProviderID: "groq", ModelID: "m",
					Pricing: catalog.Pricing{
						InputMicrosPerMTok: 1_000_000, Known: true, Source: tc.served,
					}},
			)}}

			e.log(&store.RequestRecord{
				FinalProviderID: "groq", FinalModel: "m", TokensIn: 1_000_000,
				Attempts: []store.AttemptRecord{
					{Seq: 1, ProviderID: "cerebras", Model: "m",
						Outcome: "retryable_provider", TokensIn: 500_000},
					{Seq: 2, ProviderID: "groq", Model: "m",
						Outcome: "success", TokensIn: 1_000_000},
				},
			})

			rec := cap.only(t)
			if rec.PriceGrade != tc.wantRecord {
				t.Fatalf("record PriceGrade = %q, want %q", rec.PriceGrade, tc.wantRecord)
			}
			if got := rec.Attempts[0].PriceGrade; got != tc.wantAttempt {
				t.Fatalf("discarded attempt PriceGrade = %q, want %q: it was priced "+
					"against %s, so it carries that provider's authority",
					got, tc.wantAttempt, tc.tried)
			}
		})
	}
}

func TestLogCarriesTheGradeOntoTheServedAttempt(t *testing.T) {
	// The attempt row that served arrives with no usage of its own -- the
	// record holds it -- so this row takes the record's cost, and must take
	// the authority behind that cost with it. A cost with no grade beside it
	// is spend the total cannot mark, and every request with attempt rows
	// reaches SpendSince through this row.
	//
	// Both directions, so a constant cannot pass.
	for _, tc := range []struct {
		source catalog.Source
		want   string
	}{
		{catalog.SourceDiscovered, "measured"},
		{catalog.SourceModelsDev, "indexed"},
	} {
		t.Run(string(tc.source), func(t *testing.T) {
			cap := &captureLogger{}
			e := &Executor{deps: Deps{Log: cap, Catalog: catalogOf(catalog.Model{
				ProviderID: "groq", ModelID: "m",
				Pricing: catalog.Pricing{
					InputMicrosPerMTok: 1_000_000, Known: true, Source: tc.source,
				},
			})}}

			e.log(&store.RequestRecord{
				FinalProviderID: "groq", FinalModel: "m", TokensIn: 1_000_000,
				// Zero tokens on the attempt is what routes it through the
				// record's own price rather than a second lookup.
				Attempts: []store.AttemptRecord{
					{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success"},
				},
			})

			a := cap.only(t).Attempts[0]
			if a.CostMicros == nil {
				t.Fatal("served attempt: CostMicros is nil")
			}
			if a.PriceGrade != tc.want {
				t.Fatalf("served attempt PriceGrade = %q, want %q: its cost reaches "+
					"the spend total, so an unmarked row hides the estimate",
					a.PriceGrade, tc.want)
			}
		})
	}
}
