package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/ir"
)

type catalogBody struct {
	Models []struct {
		Model     string   `json:"model"`
		Providers []string `json:"providers"`
		Surfaces  []string `json:"surfaces"`
		Inferred  bool     `json:"inferred"`
	} `json:"models"`
	Aliases []struct {
		Name    string   `json:"name"`
		Targets []string `json:"targets"`
	} `json:"aliases"`
}

func getCatalog(t *testing.T, s *Server, query string) catalogBody {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/models"+query, "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body catalogBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestTheCatalogListsModelsAcrossProviders(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "")

	var shared bool
	for _, m := range body.Models {
		if m.Model == "shared-model" {
			shared = true
			if len(m.Providers) != 2 {
				t.Errorf("shared-model providers = %v, want two", m.Providers)
			}
		}
	}
	if !shared {
		t.Fatalf("shared-model is missing: %+v", body.Models)
	}
	if len(body.Models) != 3 {
		t.Errorf("got %d rows for 4 catalog entries; the fold by model name is wrong",
			len(body.Models))
	}
}

func TestTheCatalogMarksInferredMetadata(t *testing.T) {
	// Master design §6.4 routes a guessed model with a warning. An operator
	// reading the catalog needs to see which rows are guesses, or a guessed
	// row that refuses tool calls looks like a Darkrouter bug.
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "")

	var found bool
	for _, m := range body.Models {
		if m.Model == "guessed-model" {
			found = true
			if !m.Inferred {
				t.Error("guessed-model is not marked inferred")
			}
		}
		if m.Model == "known-model" && m.Inferred {
			t.Error("known-model is marked inferred")
		}
	}
	if !found {
		t.Error("guessed-model is missing")
	}
}

func TestTheCatalogFiltersBySurface(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "?surface=embedding")

	if len(body.Models) != 1 || body.Models[0].Model != "guessed-model" {
		t.Fatalf("the embedding filter returned %+v", body.Models)
	}
}

func TestTheCatalogSearchesBySubstring(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "?q=guess")

	if len(body.Models) != 1 || body.Models[0].Model != "guessed-model" {
		t.Errorf("search = %+v", body.Models)
	}
}

func TestTheCatalogFiltersByContextWindow(t *testing.T) {
	s, _ := testServerWithCatalog(t, "")
	body := getCatalog(t, s, "?min_context=100000")

	if len(body.Models) != 1 || body.Models[0].Model != "shared-model" {
		t.Errorf("min_context = %+v", body.Models)
	}
}

func TestTheCatalogReportsWhatEachAliasResolvesTo(t *testing.T) {
	// The chain lives in the configuration and the catalog lives in the
	// database. Joining them in the browser would duplicate resolution rules
	// the router already owns.
	s, _ := testServerWithCatalog(t, "  fast:\n    - a/shared-model\n    - b/shared-model\n")
	body := getCatalog(t, s, "")

	if len(body.Aliases) != 1 || body.Aliases[0].Name != "fast" {
		t.Fatalf("aliases = %+v", body.Aliases)
	}
	if len(body.Aliases[0].Targets) != 2 || body.Aliases[0].Targets[0] != "a/shared-model" {
		t.Errorf("targets = %v", body.Aliases[0].Targets)
	}
}

func TestAnEmptyCatalogReturnsArraysNotNull(t *testing.T) {
	// Both lists are ranged over by the screen.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/models", "")
	got := w.Body.String()
	if !strings.Contains(got, `"models":[]`) || !strings.Contains(got, `"aliases":[]`) {
		t.Errorf("body = %s", got)
	}
}

func pricedCatalog() *catalog.Store {
	c := &catalog.Store{}
	c.Set(catalog.NewSnapshot([]catalog.Model{
		{ProviderID: "groq", ModelID: "priced-model", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, Publisher: "meta",
			Source:       catalog.SourceModelsDev,
			Capabilities: catalog.Capabilities{Known: true},
			Pricing: catalog.Pricing{
				InputMicrosPerMTok: 150000, OutputMicrosPerMTok: 600000, Known: true,
				Source: catalog.SourceModelsDev,
			}},
		{ProviderID: "groq", ModelID: "overridden-model", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, Publisher: "meta",
			Source:       catalog.SourceOverride,
			Capabilities: catalog.Capabilities{Known: true},
			Pricing: catalog.Pricing{
				// The model's own metadata was overridden by the operator, but its
				// price still comes from live discovery — the two provenances are
				// independent, and a row where they differ is what makes the price
				// source distinguishable from the model's merge source.
				InputMicrosPerMTok: 200000, OutputMicrosPerMTok: 800000, Known: true,
				Source: catalog.SourceDiscovered,
			}},
		{ProviderID: "groq", ModelID: "free-model", State: catalog.StateLive,
			Surfaces:     []ir.Surface{ir.SurfaceLLM},
			Source:       catalog.SourceModelsDev,
			Capabilities: catalog.Capabilities{Known: true},
			FreeTier: catalog.FreeTier{
				FreeType: "recurring-daily", DisplayName: "Free model",
				MonthlyTokens: 24000000, CreditTokens: 5000,
				PoolKey: "groq-shared", ToS: "caution",
			}},
		{ProviderID: "groq", ModelID: "unpriced-model", State: catalog.StateLive,
			Surfaces:     []ir.Surface{ir.SurfaceLLM},
			Source:       catalog.SourceInferred,
			Capabilities: catalog.Capabilities{Known: false}},
	}, []string{"groq"}))
	return c
}

// modelSummary is the pricing/publisher/provenance slice of modelView that
// modelViews reads back. Named rather than repeated inline as a map value
// type, a var out struct field, and a composite literal, which is the same
// struct written three times.
type modelSummary struct {
	Publisher   string `json:"publisher"`
	MergeSource string `json:"merge_source"`
	Pricing     *struct {
		InputMicros  int64  `json:"input_micros"`
		OutputMicros int64  `json:"output_micros"`
		Source       string `json:"price_source"`
		Grade        string `json:"price_grade"`
	} `json:"pricing"`
	FreeTier *struct {
		FreeType      string `json:"free_type"`
		MonthlyTokens int64  `json:"monthly_tokens"`
		CreditTokens  int64  `json:"credit_tokens"`
		PoolKey       string `json:"pool_key"`
		ToS           string `json:"tos"`
	} `json:"free_tier"`
}

func modelViews(t *testing.T, s *Server) map[string]modelSummary {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/models", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/models = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Models []struct {
			Model string `json:"model"`
			modelSummary
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	byModel := map[string]modelSummary{}
	for _, m := range out.Models {
		byModel[m.Model] = m.modelSummary
	}
	return byModel
}

func TestAModelViewCarriesPricingAndPublisher(t *testing.T) {
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	got := modelViews(t, s)["priced-model"]
	if got.Pricing == nil {
		t.Fatal("a priced model must carry a price")
	}
	if got.Pricing.InputMicros != 150000 || got.Pricing.OutputMicros != 600000 {
		t.Fatalf("pricing = %+v", got.Pricing)
	}
	if got.Publisher != "meta" {
		t.Fatalf("publisher = %q", got.Publisher)
	}
}

func TestAnUnpricedModelRendersNullNotZero(t *testing.T) {
	// Zero is a claim that the model is free. Null is the claim that the
	// catalog has no price for it, which is the true one.
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	if got := modelViews(t, s)["unpriced-model"]; got.Pricing != nil {
		t.Fatalf("unpriced model priced as %+v", got.Pricing)
	}
}

func TestAModelViewNamesItsSource(t *testing.T) {
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	got := modelViews(t, s)
	if got["priced-model"].MergeSource != "models_dev" {
		t.Errorf("merge_source = %q", got["priced-model"].MergeSource)
	}
	if got["overridden-model"].MergeSource != "override" {
		t.Errorf("merge_source = %q", got["overridden-model"].MergeSource)
	}
	if got["unpriced-model"].MergeSource != "inferred" {
		t.Errorf("merge_source = %q", got["unpriced-model"].MergeSource)
	}
}

func TestModelViewCarriesPriceProvenance(t *testing.T) {
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	got := modelViews(t, s)

	priced := got["priced-model"]
	if priced.Pricing == nil {
		t.Fatal("pricing view is nil")
	}
	if priced.Pricing.Source != "models_dev" {
		t.Errorf("price source = %q, want %q", priced.Pricing.Source, "models_dev")
	}
	if priced.Pricing.Grade != "indexed" {
		t.Errorf("price grade = %q, want %q", priced.Pricing.Grade, "indexed")
	}

	// overridden-model's own metadata came from an operator override, but its
	// price came from live discovery. A view that read the model's merge
	// source instead of the price's own source would report "override" and
	// "declared" here, not "discovered" and "measured".
	overridden := got["overridden-model"]
	if overridden.Pricing == nil {
		t.Fatal("overridden-model pricing view is nil")
	}
	if overridden.Pricing.Source != "discovered" {
		t.Errorf("price source = %q, want %q", overridden.Pricing.Source, "discovered")
	}
	if overridden.Pricing.Grade != "measured" {
		t.Errorf("price grade = %q, want %q", overridden.Pricing.Grade, "measured")
	}
}

func TestModelAPICarriesTheFreeTierRecord(t *testing.T) {
	// A budget and a terms verdict cannot be shown by a client that was never
	// sent them, so the whole upstream record travels on the row.
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	got := modelViews(t, s)["free-model"].FreeTier
	if got == nil {
		t.Fatal("a model with a free tier must carry one")
	}
	if got.FreeType != "recurring-daily" || got.ToS != "caution" {
		t.Errorf("free tier = %+v", got)
	}
	if got.MonthlyTokens != 24000000 || got.CreditTokens != 5000 {
		t.Errorf("free tier allowance = %+v", got)
	}
	if got.PoolKey != "groq-shared" {
		t.Errorf("pool key = %q", got.PoolKey)
	}
}

func TestAModelWithNoFreeTierRendersNullNotZero(t *testing.T) {
	// A zeroed record reads as "free, uncapped, terms unknown". Null is the
	// true claim: this model has no free tier at all.
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	if got := modelViews(t, s)["priced-model"].FreeTier; got != nil {
		t.Fatalf("paid model carries a free tier: %+v", got)
	}
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/models", "")
	if !strings.Contains(w.Body.String(), `"free_tier":null`) {
		t.Errorf("body = %s", w.Body.String())
	}
}
