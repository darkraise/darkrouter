package catalog

import (
	_ "embed"
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
	c := FreeCatalog{Providers: map[string]map[string]FreeTier{
		"p": {
			"gone": {FreeType: "discontinued"},
			"live": {FreeType: "recurring-daily"},
		},
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

//go:embed testdata/free-catalog-sample.ts
var freeCatalogSampleTS []byte

// The record is read from a verbatim upstream excerpt through the production
// parser, so every field this phase adds is exercised against the real shape
// rather than one assumed for it.
func TestParseFreeCatalogReadsTheWholeRecord(t *testing.T) {
	c, err := ParseFreeCatalog(freeCatalogSampleTS)
	if err != nil {
		t.Fatal(err)
	}
	var withBudget, unsanctioned, pooled int
	for _, models := range c.Providers {
		for _, tier := range models {
			if tier.MonthlyTokens > 0 || tier.CreditTokens > 0 {
				withBudget++
			}
			if tier.Unsanctioned() {
				unsanctioned++
			}
			if tier.PoolKey != "" {
				pooled++
			}
		}
	}
	if withBudget == 0 {
		t.Error("no entry carried a budget; monthlyTokens/creditTokens are being dropped")
	}
	if unsanctioned == 0 {
		t.Error("no entry graded avoid; tos is being dropped")
	}
	if pooled == 0 {
		t.Error("no entry carried a pool; poolKey is being dropped")
	}
}

// A null poolKey is a real value upstream — seven rows carry it — and must
// read as absent rather than as the literal string "null".
func TestParseFreeCatalogReadsANullPool(t *testing.T) {
	c, err := ParseFreeCatalog(freeCatalogSampleTS)
	if err != nil {
		t.Fatal(err)
	}
	for _, models := range c.Providers {
		for id, tier := range models {
			if tier.PoolKey == "null" {
				t.Fatalf("%s: poolKey read as the literal string null", id)
			}
		}
	}
}

// The embedded snapshot is generated. If a regeneration is skipped after the
// record widens, FreeModels() degrades to an empty catalogue and the free
// filter silently stops working — this fails loudly instead.
func TestEmbeddedFreeCatalogCarriesTheFullRecord(t *testing.T) {
	c := FreeModels()
	if len(c.Providers) == 0 {
		t.Fatal("embedded free catalogue is empty")
	}
	for _, models := range c.Providers {
		for _, tier := range models {
			if tier.ToS != "" {
				return
			}
		}
	}
	t.Error("no embedded entry carries a tos; free_models.json needs regenerating")
}
