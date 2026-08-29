package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

func TestFreeCatalogIsPopulated(t *testing.T) {
	c := FreeModels()
	if c.CuratedAt == "" {
		t.Error("the catalogue does not say when it was curated")
	}
	if len(c.Providers) < 50 {
		t.Fatalf("free catalogue has %d providers; regenerate with tools/presetgen", len(c.Providers))
	}
}

func TestFreeCatalogCoversAProvidersDocumentedModels(t *testing.T) {
	// The case the whole catalogue exists for. Groq prices every model it
	// serves, and its free tier covers them anyway -- a price index cannot
	// answer that, and before the catalogue the filter imported nothing.
	c := FreeModels()
	if !c.Covers("groq", "openai/gpt-oss-120b") {
		t.Error("gpt-oss-120b is documented on Groq's free tier")
	}
	if c.Covers("groq", "no-such-model") {
		t.Error("a model the catalogue does not list must not read as free")
	}
	if c.Covers("no-such-provider", "anything") {
		t.Error("an uncovered provider must not read as free")
	}
}

func TestFreeCatalogExcludesAWithdrawnTier(t *testing.T) {
	// A discontinued free tier is history the upstream catalogue keeps. It is
	// not something an import filter may count on.
	c := FreeCatalog{Providers: map[string]map[string]string{
		"p": {"gone": "discontinued", "live": "recurring-daily"},
	}}
	if c.Covers("p", "gone") {
		t.Error("a withdrawn free tier still read as free")
	}
	if !c.Covers("p", "live") {
		t.Error("a live free tier stopped reading as free")
	}
	if got := c.ModelsFor("p"); len(got) != 1 || got[0] != "live" {
		t.Errorf("ModelsFor = %v, want [live]", got)
	}
	if c.ModelsFor("absent") != nil {
		t.Error("a provider the catalogue never covered must list nothing")
	}
}

func TestCuratedFreeBeatsAPaidPrice(t *testing.T) {
	// The inversion this fixes. Groq's free tier covers gpt-oss-120b while
	// models.dev prices it at $0.15/$0.60 -- the paid tier's number, for an
	// account that is not on the paid tier.
	free := FreeModels()
	curated := func(id string) bool { return free.Covers("groq", id) }
	paid := Metadata{InputMicrosPerMTok: 150_000, OutputMicrosPerMTok: 600_000, PriceKnown: true}
	if !IsFreeModel("openai/gpt-oss-120b", paid, true, curated) {
		t.Error("a curated free model must outrank its paid-tier price")
	}
	// And the catalogue does not make everything free: a model it does not
	// list still answers to its price.
	if IsFreeModel("some-other-model", paid, true, curated) {
		t.Error("a priced model the catalogue does not list must stay paid")
	}
}

func TestSelectModelsForImportKeepsCuratedFreeModels(t *testing.T) {
	in := []store.DiscoveredModel{{ModelID: "covered"}, {ModelID: "priced"}}
	kept, dropped := SelectModelsForImport(in, true, FreeRules{
		Price: func(string) (Metadata, bool) {
			return Metadata{InputMicrosPerMTok: 880_000, OutputMicrosPerMTok: 880_000, PriceKnown: true}, true
		},
		Curated: func(id string) bool { return id == "covered" },
	})
	if len(kept) != 1 || kept[0].ModelID != "covered" {
		t.Errorf("kept %v, want [covered]", kept)
	}
	if len(dropped) != 1 || dropped[0] != "priced" {
		t.Errorf("dropped %v, want [priced]", dropped)
	}
}
