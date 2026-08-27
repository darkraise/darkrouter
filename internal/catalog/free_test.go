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
	if !IsFreeModel("deepseek-r1:free", Metadata{}, false) {
		t.Error("a :free suffix is free whatever the catalogue says")
	}
}

func TestIsFreeModelReadsAZeroPrice(t *testing.T) {
	if !IsFreeModel("gemma-3-27b", priced(0, 0), true) {
		t.Error("zero on both sides is free")
	}
	if IsFreeModel("llama-3.3-70b", priced(880_000, 880_000), true) {
		t.Error("a priced model is not free")
	}
	if IsFreeModel("half-free", priced(0, 600_000), true) {
		t.Error("free input and paid output is not free")
	}
}

func TestAnUnpricedModelIsNotFree(t *testing.T) {
	// The load-bearing case. Known is what separates a model priced at zero
	// from one nobody has priced, and both are zero — importing the second
	// under a free-only filter is how an operator on a free tier gets a bill.
	if IsFreeModel("mystery", Metadata{}, true) {
		t.Error("an unpriced model must not pass the free filter")
	}
	if IsFreeModel("absent", Metadata{}, false) {
		t.Error("a model the catalogue has never heard of must not pass")
	}
}

func TestSelectModelsForImportPassesEverythingThroughWhenOff(t *testing.T) {
	in := []store.DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}
	got, dropped := SelectModelsForImport(in, false, func(string) (Metadata, bool) {
		t.Error("the price lookup must not run when the filter is off")
		return Metadata{}, false
	})
	if len(got) != 2 || dropped != 0 {
		t.Errorf("got %d models and %d dropped, want 2 and 0", len(got), dropped)
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
	got, dropped := SelectModelsForImport(in, true, func(id string) (Metadata, bool) {
		m, ok := prices[id]
		return m, ok
	})
	if len(got) != 2 || got[0].ModelID != "zero" || got[1].ModelID != "suffixed:free" {
		t.Errorf("kept %v, want [zero suffixed:free]", got)
	}
	if dropped != 2 {
		t.Errorf("dropped %d, want 2", dropped)
	}
}

func TestSelectModelsForImportSurvivesAnUncataloguedProvider(t *testing.T) {
	// No join key means no price lookup, so every model rests on the suffix.
	in := []store.DiscoveredModel{{ModelID: "x"}, {ModelID: "y:free"}}
	got, dropped := SelectModelsForImport(in, true, nil)
	if len(got) != 1 || got[0].ModelID != "y:free" {
		t.Errorf("kept %v, want [y:free]", got)
	}
	if dropped != 1 {
		t.Errorf("dropped %d, want 1", dropped)
	}
}

func TestSelectModelsForImportReportsAWhollyPaidProvider(t *testing.T) {
	// The case the count exists for: the sweep worked, the provider listed
	// models, and none of them are free. Recording that as an empty import
	// with nothing else said is indistinguishable from a provider discovery
	// has never visited.
	in := []store.DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}
	kept, dropped := SelectModelsForImport(in, true, func(string) (Metadata, bool) {
		return priced(880_000, 880_000), true
	})
	if len(kept) != 0 || dropped != 2 {
		t.Errorf("kept %d and dropped %d, want 0 and 2", len(kept), dropped)
	}
}
