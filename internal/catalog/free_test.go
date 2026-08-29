package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

func priced(in, out int64) Metadata {
	return Metadata{InputMicrosPerMTok: in, OutputMicrosPerMTok: out, PriceKnown: true}
}

func TestIsFreeModelReadsTheSuffixFirst(t *testing.T) {
	// A gateway publishing both tiers of one model distinguishes them exactly
	// here, and it does so without any price being known.
	if !IsFreeModel("deepseek-r1:free", Metadata{}, false, nil) {
		t.Error("a :free suffix is free whatever the catalogue says")
	}
}

func TestIsFreeModelReadsAZeroPrice(t *testing.T) {
	if !IsFreeModel("gemma-3-27b", priced(0, 0), true, nil) {
		t.Error("zero on both sides is free")
	}
	if IsFreeModel("llama-3.3-70b", priced(880_000, 880_000), true, nil) {
		t.Error("a priced model is not free")
	}
	if IsFreeModel("half-free", priced(0, 600_000), true, nil) {
		t.Error("free input and paid output is not free")
	}
}

func TestAnUnpricedModelIsNotFree(t *testing.T) {
	// The load-bearing case. Known is what separates a model priced at zero
	// from one nobody has priced, and both are zero — importing the second
	// under a free-only filter is how an operator on a free tier gets a bill.
	if IsFreeModel("mystery", Metadata{}, true, nil) {
		t.Error("an unpriced model must not pass the free filter")
	}
	if IsFreeModel("absent", Metadata{}, false, nil) {
		t.Error("a model the catalogue has never heard of must not pass")
	}
}

func TestAKeylessProviderThatMatchedNothingKeepsEverything(t *testing.T) {
	// UncloseAI serves one model whose id rotates and which no price list
	// covers. Filtering it out leaves a provider that routes nothing, and
	// protects the operator from a charge that cannot reach them: there is no
	// account behind a keyless provider to bill.
	models := []store.DiscoveredModel{{ModelID: "Lorbus/Qwen3.6-27B-int4-AutoRound"}}
	kept, dropped := SelectModelsForImport(models, true, FreeRules{Keyless: true})
	if len(kept) != 1 || len(dropped) != 0 {
		t.Fatalf("kept %d, dropped %v", len(kept), dropped)
	}
}

func TestAKeylessProviderWithAFreeTierIsStillFiltered(t *testing.T) {
	// OpenCode's premium models answer 401 without a key, and its curated
	// entry names the ones that do not. The fallback must not undo that.
	models := []store.DiscoveredModel{{ModelID: "mimo-v2.5-free"}, {ModelID: "claude-fable-5"}}
	rules := FreeRules{Keyless: true, Curated: func(id string) bool { return id == "mimo-v2.5-free" }}
	kept, dropped := SelectModelsForImport(models, true, rules)
	if len(kept) != 1 || kept[0].ModelID != "mimo-v2.5-free" {
		t.Fatalf("kept = %v", kept)
	}
	if len(dropped) != 1 || dropped[0] != "claude-fable-5" {
		t.Errorf("dropped = %v", dropped)
	}
}

func TestAKeyedProviderThatMatchedNothingKeepsNothing(t *testing.T) {
	// The fallback is about a provider nobody can be billed for. With a key in
	// play, importing an unpriced model is exactly what the filter is for.
	models := []store.DiscoveredModel{{ModelID: "some-paid-model"}}
	kept, dropped := SelectModelsForImport(models, true, FreeRules{})
	if len(kept) != 0 || len(dropped) != 1 {
		t.Fatalf("kept %v, dropped %v", kept, dropped)
	}
}

func TestSelectModelsForImportPassesEverythingThroughWhenOff(t *testing.T) {
	in := []store.DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}
	got, dropped := SelectModelsForImport(in, false, FreeRules{Price: func(string) (Metadata, bool) {
		t.Error("the price lookup must not run when the filter is off")
		return Metadata{}, false
	}})
	if len(got) != 2 || len(dropped) != 0 {
		t.Errorf("got %d models and %v dropped, want 2 and none", len(got), dropped)
	}
}

func TestSelectModelsForImportKeepsOnlyTheFreeOnes(t *testing.T) {
	in := []store.DiscoveredModel{
		{ModelID: "paid"},
		{ModelID: "zero"},
		{ModelID: "suffixed:free"},
		{ModelID: "unpriced"},
	}
	prices := map[string]Metadata{"paid": priced(880_000, 880_000), "zero": priced(0, 0)}
	got, dropped := SelectModelsForImport(in, true, FreeRules{Price: func(id string) (Metadata, bool) {
		m, ok := prices[id]
		return m, ok
	}})
	if len(got) != 2 || got[0].ModelID != "zero" || got[1].ModelID != "suffixed:free" {
		t.Errorf("kept %v, want [zero suffixed:free]", got)
	}
	// Named rather than counted: the catalogue has to delete exactly these
	// rows, because the provider still lists them.
	if len(dropped) != 2 || dropped[0] != "paid" || dropped[1] != "unpriced" {
		t.Errorf("dropped %v, want [paid unpriced]", dropped)
	}
}

func TestSelectModelsForImportSurvivesAnUncataloguedProvider(t *testing.T) {
	// No join key means no price lookup, so every model rests on the suffix.
	in := []store.DiscoveredModel{{ModelID: "x"}, {ModelID: "y:free"}}
	got, dropped := SelectModelsForImport(in, true, FreeRules{})
	if len(got) != 1 || got[0].ModelID != "y:free" {
		t.Errorf("kept %v, want [y:free]", got)
	}
	if len(dropped) != 1 || dropped[0] != "x" {
		t.Errorf("dropped %v, want [x]", dropped)
	}
}

func TestSelectModelsForImportReportsAWhollyPaidProvider(t *testing.T) {
	// The case the count exists for: the sweep worked, the provider listed
	// models, and none of them are free. Recording that as an empty import
	// with nothing else said is indistinguishable from a provider discovery
	// has never visited.
	in := []store.DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}
	kept, dropped := SelectModelsForImport(in, true, FreeRules{Price: func(string) (Metadata, bool) {
		return priced(880_000, 880_000), true
	}})
	if len(kept) != 0 || len(dropped) != 2 {
		t.Errorf("kept %d and dropped %v, want 0 and two", len(kept), dropped)
	}
}

func TestSelectModelsForImportKeepsAModelPricedAtZero(t *testing.T) {
	// Free and unpriced are both zero, and only Metadata.PriceKnown tells
	// them apart. Losing that flag anywhere between models.dev and here drops
	// every free model a provider has.
	in := []store.DiscoveredModel{{ModelID: "free"}, {ModelID: "unpriced"}}
	kept, dropped := SelectModelsForImport(in, true, FreeRules{Price: func(id string) (Metadata, bool) {
		if id == "free" {
			return priced(0, 0), true
		}
		return Metadata{}, true
	}})
	if len(kept) != 1 || kept[0].ModelID != "free" {
		t.Errorf("kept %v, want [free]", kept)
	}
	if len(dropped) != 1 || dropped[0] != "unpriced" {
		t.Errorf("dropped %v, want [unpriced]", dropped)
	}
}
