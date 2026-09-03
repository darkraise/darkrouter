package catalog

import (
	"bytes"
	_ "embed"
	"strings"
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

// A null poolKey is a real value upstream — some rows carry it — and must
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

// A shape change that costs the entry pattern only part of the file must be an
// error, not a shorter catalogue. The caller stores whatever it is handed, so a
// partial read would replace a good catalogue with one missing the rows it
// could not parse — and nothing downstream can tell the difference.
func TestParseFreeCatalogRefusesAPartialRead(t *testing.T) {
	// Two of the fixture's rows are given a shape the pattern cannot read,
	// which is what an upstream field change looks like before it reaches
	// every row.
	const broke = 2
	target, replacement := []byte("monthlyTokens: 0,"), []byte("monthlyTokens: null,")
	if n := bytes.Count(freeCatalogSampleTS, target); n < broke {
		t.Fatalf("the fixture holds %d rows this test can break, want at least %d", n, broke)
	}
	broken := bytes.Replace(freeCatalogSampleTS, target, replacement, broke)

	_, err := ParseFreeCatalog(broken)
	if err == nil {
		t.Fatal("a catalogue missing rows the pattern could not read parsed cleanly")
	}
	// The numbers are left to the implementation; what matters is that the
	// message says a read fell short, so an operator reading a log knows the
	// catalogue is incomplete rather than merely stale.
	if !strings.Contains(err.Error(), "entries") || !strings.Contains(err.Error(), " of ") {
		t.Errorf("error does not name the shortfall: %v", err)
	}
	// And that it blames the shape rather than a duplicate. The two failures
	// send an operator to different places, so one message for both is worth
	// no more than none.
	if !strings.Contains(err.Error(), "row shape") {
		t.Errorf("error does not blame the row shape: %v", err)
	}
	if strings.Contains(err.Error(), "twice") {
		t.Errorf("a shape break was reported as a duplicated row: %v", err)
	}
}

// Two rows naming the same provider and model collapse into one map entry, so
// a duplicate shrinks the catalogue exactly as a failed match does. Counting
// what was stored, rather than what was matched, is what catches it.
func TestParseFreeCatalogRefusesADuplicatedRow(t *testing.T) {
	rows := bytes.SplitAfter(freeCatalogSampleTS, []byte("\n"))
	var dup []byte
	var provider, model string
	for _, row := range rows {
		if m := freeEntry.FindSubmatch(row); m != nil {
			dup, provider, model = row, string(m[1]), string(m[2])
			break
		}
	}
	if dup == nil {
		t.Fatal("the fixture holds no entry row to duplicate")
	}
	_, err := ParseFreeCatalog(append(append([]byte{}, freeCatalogSampleTS...), dup...))
	if err == nil {
		t.Fatal("a catalogue carrying the same row twice parsed cleanly")
	}
	// Every row here parsed, so blaming the pattern would send an operator
	// hunting a regression that is not there. The message must say upstream
	// repeated a row, and name the row it repeated.
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("error does not say a row was listed twice: %v", err)
	}
	if strings.Contains(err.Error(), "row shape") {
		t.Errorf("a duplicated row was reported as a shape break: %v", err)
	}
	if !strings.Contains(err.Error(), provider) || !strings.Contains(err.Error(), model) {
		t.Errorf("error does not name the duplicated %s/%s: %v", provider, model, err)
	}
}

// The embedded snapshot is generated. If a regeneration is skipped after the
// record widens, FreeModels() degrades to an empty catalogue and the free
// filter silently stops working — this fails loudly instead.
//
// Each widened field is asserted present on at least one row rather than on
// every row: a real snapshot legitimately holds rows with no budget and no
// pool, so a per-row assertion would fail on good data.
func TestEmbeddedFreeCatalogCarriesEveryWidenedField(t *testing.T) {
	c := FreeModels()
	if len(c.Providers) == 0 {
		t.Fatal("embedded free catalogue is empty")
	}
	var withToS, withMonthly, withCredit, withPool int
	for _, models := range c.Providers {
		for _, tier := range models {
			if tier.ToS != "" {
				withToS++
			}
			if tier.MonthlyTokens > 0 {
				withMonthly++
			}
			if tier.CreditTokens > 0 {
				withCredit++
			}
			if tier.PoolKey != "" {
				withPool++
			}
		}
	}
	for _, f := range []struct {
		field string
		count int
	}{
		{"tos", withToS},
		{"monthly_tokens", withMonthly},
		{"credit_tokens", withCredit},
		{"pool_key", withPool},
	} {
		if f.count == 0 {
			t.Errorf("no embedded entry carries a %s; free_models.json needs regenerating", f.field)
		}
	}
}

// The snapshot's size is its own guard. ParseFreeCatalog stores what it reads,
// so a shape change that costs the entry pattern a slice of the file yields a
// smaller but still plausible-looking catalogue, and every other test here
// still passes. The floor is well under the 437 rows upstream carries today so
// ordinary curation does not redden the suite, and far above what a structural
// break would leave.
func TestEmbeddedFreeCatalogHoldsEveryUpstreamRow(t *testing.T) {
	const floor = 400
	var entries int
	for _, models := range FreeModels().Providers {
		entries += len(models)
	}
	if entries < floor {
		t.Errorf("embedded free catalogue holds %d entries, want at least %d; "+
			"either the parse is dropping rows or free_models.json needs regenerating",
			entries, floor)
	}
}
