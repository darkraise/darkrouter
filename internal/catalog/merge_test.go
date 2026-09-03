package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

func mergeInput() MergeInput {
	return MergeInput{
		Providers: []provider.Provider{{ID: "p", Kind: "openaicompat", Preset: "acme"}},
		Presets: Presets{"acme": {
			Name: "Acme", Kind: "openaicompat", ModelsDevID: "acme",
			Surfaces: []string{"llm", "embedding"},
			ModelTraits: []TraitRule{
				{Match: "big", Adaptive: true},
				{Match: "big-v2", Adaptive: true, ManualBudget: true, FreeSampling: true},
			},
		}},
		Doc: Doc{"acme": {
			"big": {
				ContextWindow: 200_000, MaxOutputTokens: 64_000,
				InputMicrosPerMTok: 5_000_000, PriceKnown: true,
				Capabilities: Capabilities{Tools: true, Vision: true, Known: true},
			},
		}},
		Rows: []store.ModelRow{
			{ProviderID: "p", ModelID: "big", State: "live", CapabilitiesSource: "inferred"},
		},
	}
}

func find(t *testing.T, ms []Model, id string) Model {
	t.Helper()
	for _, m := range ms {
		if m.ModelID == id {
			return m
		}
	}
	t.Fatalf("model %q not in %d results", id, len(ms))
	return Model{}
}

func TestMergeTakesLimitsAndPricingFromModelsDev(t *testing.T) {
	m := find(t, Merge(mergeInput()), "big")
	if m.ContextWindow != 200_000 || m.MaxOutputTokens != 64_000 {
		t.Errorf("limits = (%d, %d)", m.ContextWindow, m.MaxOutputTokens)
	}
	if m.Pricing.InputMicrosPerMTok != 5_000_000 || !m.Pricing.Known {
		t.Errorf("pricing = %+v", m.Pricing)
	}
	if m.Source != SourceModelsDev {
		t.Errorf("source = %q, want models_dev", m.Source)
	}
	if !m.Capabilities.Known || !m.Capabilities.Tools || !m.Capabilities.Vision {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
}

func TestMergePrefersDiscoveredOverModelsDevForCapabilities(t *testing.T) {
	// A runtime that reports its own capabilities outranks a directory's guess
	// about a model of the same name.
	in := mergeInput()
	in.Rows[0].CapabilitiesSource = "discovered"
	in.Rows[0].Capabilities = store.ModelCapabilities{Tools: false}
	m := find(t, Merge(in), "big")
	if m.Capabilities.Tools {
		t.Error("models.dev outranked a discovered capability")
	}
	if m.Source != SourceDiscovered {
		t.Errorf("source = %q, want discovered", m.Source)
	}
	// Metadata still comes from models.dev: precedence is per field.
	if m.ContextWindow != 200_000 {
		t.Errorf("context window = %d; capability precedence leaked into limits", m.ContextWindow)
	}
}

func TestMergeOverrideBeatsEverything(t *testing.T) {
	in := mergeInput()
	in.Rows[0].CapabilitiesSource = "discovered"
	in.Rows[0].Capabilities = store.ModelCapabilities{Tools: false}
	ctxWin := 999
	in.Overrides = []store.ModelOverride{{
		ProviderID: "p", ModelID: "big",
		Capabilities:  &store.ModelCapabilities{Tools: true, Reasoning: true},
		ContextWindow: &ctxWin,
		Surfaces:      []string{"llm"},
	}}
	m := find(t, Merge(in), "big")
	if !m.Capabilities.Tools || !m.Capabilities.Reasoning || m.Capabilities.Vision {
		t.Errorf("capabilities = %+v", m.Capabilities)
	}
	if m.Source != SourceOverride {
		t.Errorf("source = %q, want override", m.Source)
	}
	if m.ContextWindow != 999 {
		t.Errorf("context window = %d, want 999", m.ContextWindow)
	}
	if len(m.Surfaces) != 1 || m.Surfaces[0] != ir.SurfaceLLM {
		t.Errorf("surfaces = %v", m.Surfaces)
	}
}

func TestMergeFallsBackToInferred(t *testing.T) {
	// A model nothing knows about still exists and still routes. Spec §6: the
	// local-model story depends on it.
	in := mergeInput()
	in.Rows = append(in.Rows, store.ModelRow{
		ProviderID: "p", ModelID: "private-finetune", State: "live", CapabilitiesSource: "inferred",
	})
	m := find(t, Merge(in), "private-finetune")
	if m.Source != SourceInferred || m.Capabilities.Known {
		t.Errorf("source = %q, known = %v", m.Source, m.Capabilities.Known)
	}
	if m.ContextWindow != 0 || m.Pricing.Known {
		t.Errorf("invented metadata: %+v", m)
	}
	if !m.Routable() {
		t.Error("an inferred model is not routable")
	}
}

func TestMergeTakesSurfacesFromThePreset(t *testing.T) {
	m := find(t, Merge(mergeInput()), "big")
	if len(m.Surfaces) != 2 || m.Surfaces[0] != ir.SurfaceLLM || m.Surfaces[1] != ir.SurfaceEmbedding {
		t.Errorf("surfaces = %v", m.Surfaces)
	}
}

func TestMergeTakesTraitsFromTheLongestPresetMatch(t *testing.T) {
	// "big-v2" contains "big". Shortest-first would give it the wrong wire
	// shape, which is a 400 on every reasoning request.
	in := mergeInput()
	in.Rows = append(in.Rows, store.ModelRow{
		ProviderID: "p", ModelID: "big-v2", State: "live", CapabilitiesSource: "inferred",
	})
	v2 := find(t, Merge(in), "big-v2")
	if !v2.Traits.Known || !v2.Traits.ManualBudget || !v2.Traits.FreeSampling {
		t.Errorf("big-v2 traits = %+v", v2.Traits)
	}
	big := find(t, Merge(in), "big")
	if !big.Traits.Known || big.Traits.ManualBudget {
		t.Errorf("big traits = %+v", big.Traits)
	}
}

func TestMergeLeavesTraitsUnknownWithoutAPreset(t *testing.T) {
	// An uncatalogued provider declares nothing. The adapter must then honor
	// what the client asked for rather than acting on a guess.
	in := mergeInput()
	in.Providers[0].Preset = ""
	m := find(t, Merge(in), "big")
	if m.Traits.Known {
		t.Errorf("traits invented for a presetless provider: %+v", m.Traits)
	}
}

func TestMergeCarriesStateThrough(t *testing.T) {
	in := mergeInput()
	in.Rows[0].State = "removed_upstream"
	m := find(t, Merge(in), "big")
	if m.State != StateRemovedUpstream || m.Routable() {
		t.Errorf("state = %q, routable = %v", m.State, m.Routable())
	}
	in.Rows[0].State = "stale"
	stale := find(t, Merge(in), "big")
	if !stale.Routable() {
		t.Error("a stale model is not routable; the breaker is what avoids a broken provider")
	}
}

func TestMergeIgnoresRowsOfUnknownProviders(t *testing.T) {
	// A provider deleted between the snapshot read and the merge leaves orphan
	// rows. Emitting them would offer models nothing can route to.
	in := mergeInput()
	in.Rows = append(in.Rows, store.ModelRow{ProviderID: "gone", ModelID: "x", State: "live"})
	for _, m := range Merge(in) {
		if m.ProviderID == "gone" {
			t.Error("an orphan row survived the merge")
		}
	}
}

func TestMergeIsDeterministic(t *testing.T) {
	// Two runs over the same input must produce the same order, or a snapshot
	// rebuild silently reorders the candidate list a request sees.
	a, b := Merge(mergeInput()), Merge(mergeInput())
	if len(a) != len(b) {
		t.Fatalf("lengths differ: %d and %d", len(a), len(b))
	}
	for i := range a {
		if a[i].ProviderID != b[i].ProviderID || a[i].ModelID != b[i].ModelID {
			t.Fatalf("order differs at %d: %v and %v", i, a[i], b[i])
		}
	}
}

func TestPresetSurfacesAreNotShadowedByADiscoveredRow(t *testing.T) {
	// Discovery hardcodes '["llm"]' into every row it inserts
	// (store/catalog_lifecycle.go) and never updates it, and the models.dev
	// sync echoes that value straight back. With the row outranking the preset,
	// widening a preset had no effect on any discovered model: an embedding
	// request was skipped as SkipSurface and returned "no provider offers
	// this" — on exactly the providers whose discovery works.
	in := mergeInput()
	in.Presets["acme"] = Preset{
		Name: "Acme", Kind: "openaicompat", ModelsDevID: "acme",
		Surfaces: []string{"llm", "embedding"},
	}
	in.Rows = []store.ModelRow{{
		ProviderID: "p", ModelID: "text-embedding-3-small",
		State: "live", CapabilitiesSource: "inferred",
		Surfaces: []string{"llm"}, // exactly what RecordDiscoverySuccess writes
	}}

	m := find(t, Merge(in), "text-embedding-3-small")
	if !m.DeclaresSurface(ir.SurfaceEmbedding) {
		t.Errorf("surfaces = %v; the discovered row's constant shadowed the preset", m.Surfaces)
	}
}

func TestAnOverrideStillBeatsThePreset(t *testing.T) {
	// The operator's intent is the one source that outranks the preset. It is
	// also the only writer of per-model surfaces that will ever carry
	// information — discovery writes a constant.
	in := mergeInput()
	in.Presets["acme"] = Preset{
		Name: "Acme", Kind: "openaicompat", ModelsDevID: "acme",
		Surfaces: []string{"llm", "embedding"},
	}
	in.Overrides = []store.ModelOverride{{
		ProviderID: "p", ModelID: "big", Surfaces: []string{"llm"},
	}}
	m := find(t, Merge(in), "big")
	if len(m.Surfaces) != 1 || m.Surfaces[0] != ir.SurfaceLLM {
		t.Errorf("surfaces = %v, want the override's [llm]", m.Surfaces)
	}
}

func TestAPresetlessProviderFallsBackToTheRow(t *testing.T) {
	// An uncatalogued provider declares nothing, so the row is all there is.
	in := mergeInput()
	in.Providers[0].Preset = ""
	in.Rows = []store.ModelRow{{
		ProviderID: "p", ModelID: "m", State: "live",
		Surfaces: []string{"llm", "rerank"},
	}}
	m := find(t, Merge(in), "m")
	if len(m.Surfaces) != 2 || !m.DeclaresSurface(ir.SurfaceRerank) {
		t.Errorf("surfaces = %v, want the row's [llm rerank]", m.Surfaces)
	}
}

// A models.dev join is a directory's figure, not the seller's. Stamping it
// keeps "a directory priced this" distinct from "nobody did", which the
// Known bool alone cannot express.
func TestMergeStampsModelsDevPriceSource(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "big"}
	doc := Doc{"p": {"big": Metadata{
		InputMicrosPerMTok: 500, OutputMicrosPerMTok: 1500, PriceKnown: true,
	}}}
	m := mergeOne(row, Preset{ModelsDevID: "p"}, doc, LiteLLMDoc{}, store.ModelOverride{})
	if m.Pricing.Source != SourceModelsDev {
		t.Errorf("Pricing.Source = %q, want %q", m.Pricing.Source, SourceModelsDev)
	}
	if m.Pricing.Source.Grade() != GradeIndexed {
		t.Errorf("grade = %q, want %q", m.Pricing.Source.Grade(), GradeIndexed)
	}
}

func TestMergeStampsRowPriceSourceWhenModelsDevMisses(t *testing.T) {
	row := store.ModelRow{
		ProviderID: "p", ModelID: "unknown",
		InputMicrosPerMTok: 100, PriceKnown: true,
		PriceSource: string(SourceDiscovered),
	}
	m := mergeOne(row, Preset{}, Doc{}, LiteLLMDoc{}, store.ModelOverride{})
	if m.Pricing.Source != SourceDiscovered {
		t.Errorf("Pricing.Source = %q, want %q", m.Pricing.Source, SourceDiscovered)
	}
}

// An empty stored source is a guess, not a measurement.
func TestMergeDefaultsAbsentPriceSourceToInferred(t *testing.T) {
	m := mergeOne(store.ModelRow{ProviderID: "p", ModelID: "x"}, Preset{}, Doc{}, LiteLLMDoc{}, store.ModelOverride{})
	if m.Pricing.Source != SourceInferred {
		t.Errorf("Pricing.Source = %q, want %q", m.Pricing.Source, SourceInferred)
	}
}

func TestResolvePriceTakesTheFirstKnownCandidate(t *testing.T) {
	md := Pricing{InputMicrosPerMTok: 500, Known: true, Source: SourceModelsDev}
	ll := Pricing{InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}
	if got := resolvePrice(md, ll); got.Source != SourceModelsDev || got.InputMicrosPerMTok != 500 {
		t.Errorf("got %+v, want models.dev's 500", got)
	}
}

// An unknown candidate is skipped, not returned. A source that had nothing to
// say must not shadow one that did.
func TestResolvePriceSkipsUnknownCandidates(t *testing.T) {
	empty := Pricing{Source: SourceDiscovered}
	ll := Pricing{InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}
	if got := resolvePrice(empty, ll); got.Source != SourceLiteLLM {
		t.Errorf("got %+v, want the litellm price", got)
	}
}

// A known price of zero is a price. This is the free-model case.
func TestResolvePriceTakesAKnownZero(t *testing.T) {
	free := Pricing{Known: true, Source: SourceDiscovered}
	ll := Pricing{InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}
	if got := resolvePrice(free, ll); got.Source != SourceDiscovered {
		t.Errorf("got %+v, want the discovered free price", got)
	}
}

func TestResolvePriceWithNoCandidatesIsInferred(t *testing.T) {
	got := resolvePrice()
	if got.Known || got.Source != SourceInferred {
		t.Errorf("got %+v, want an unknown inferred price", got)
	}
}

// The row holds one price slot whose stamp may outrank a directory or not, so
// the stored candidate moves rather than sitting at a fixed position. A
// runtime that quoted its own rates keeps them even where models.dev has an
// entry for the same name.
func TestAStoredDiscoveredPriceBeatsModelsDev(t *testing.T) {
	row := store.ModelRow{
		ProviderID: "p", ModelID: "big",
		InputMicrosPerMTok: 100, PriceKnown: true,
		PriceSource: string(SourceDiscovered),
	}
	doc := Doc{"p": {"big": Metadata{InputMicrosPerMTok: 500, PriceKnown: true}}}
	got := mergeOne(row, Preset{ModelsDevID: "p"}, doc, LiteLLMDoc{}, store.ModelOverride{})
	if got.Pricing.Source != SourceDiscovered || got.Pricing.InputMicrosPerMTok != 100 {
		t.Errorf("got %+v, want the stored discovered price of 100", got.Pricing)
	}
}

// The mirror of the above: a guess in the row's slot loses to a directory.
func TestAStoredInferredPriceLosesToModelsDev(t *testing.T) {
	row := store.ModelRow{
		ProviderID: "p", ModelID: "big",
		InputMicrosPerMTok: 1, PriceKnown: true,
		PriceSource: string(SourceInferred),
	}
	doc := Doc{"p": {"big": Metadata{InputMicrosPerMTok: 500, PriceKnown: true}}}
	got := mergeOne(row, Preset{ModelsDevID: "p"}, doc, LiteLLMDoc{}, store.ModelOverride{})
	if got.Pricing.Source != SourceModelsDev || got.Pricing.InputMicrosPerMTok != 500 {
		t.Errorf("got %+v, want models.dev's 500", got.Pricing)
	}
}

// A join that carries no cost must not blank a price the row already holds.
func TestAPricelessJoinKeepsTheStoredPrice(t *testing.T) {
	row := store.ModelRow{
		ProviderID: "p", ModelID: "big",
		InputMicrosPerMTok: 100, PriceKnown: true,
		PriceSource: string(SourceInferred),
	}
	doc := Doc{"p": {"big": Metadata{ContextWindow: 200_000}}}
	got := mergeOne(row, Preset{ModelsDevID: "p"}, doc, LiteLLMDoc{}, store.ModelOverride{})
	if got.Pricing.Source != SourceInferred || got.Pricing.InputMicrosPerMTok != 100 {
		t.Errorf("got %+v, want the stored 100 kept", got.Pricing)
	}
}

// A model models.dev has never heard of takes the LiteLLM price rather than
// reading as unpriced, which is the whole point of the phase.
func TestLiteLLMPricesAModelModelsDevMisses(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "llama-3.3-70b"}
	ll := LiteLLMDoc{"groq": {"llama-3.3-70b": {
		InputMicrosPerMTok: 590, OutputMicrosPerMTok: 790,
		Known: true, Source: SourceLiteLLM,
	}}}
	got := mergeOne(row, Preset{LiteLLMID: "groq"}, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.Source != SourceLiteLLM || got.Pricing.InputMicrosPerMTok != 590 {
		t.Errorf("got %+v, want the litellm price", got.Pricing)
	}
	if got.Pricing.Source.Grade() != GradeIndexed {
		t.Errorf("grade = %q, want indexed", got.Pricing.Source.Grade())
	}
}

// models.dev outranks the index where both know the model.
func TestModelsDevBeatsLiteLLM(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "big"}
	doc := Doc{"p": {"big": Metadata{InputMicrosPerMTok: 500, PriceKnown: true}}}
	ll := LiteLLMDoc{"groq": {"big": {InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}}}
	got := mergeOne(row, Preset{ModelsDevID: "p", LiteLLMID: "groq"}, doc, ll, store.ModelOverride{})
	if got.Pricing.Source != SourceModelsDev {
		t.Errorf("got %+v, want models.dev", got.Pricing)
	}
}

// A preset with no LiteLLM key joins nothing rather than matching by accident.
func TestNoLiteLLMKeyJoinsNothing(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "m"}
	ll := LiteLLMDoc{"groq": {"m": {InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}}}
	got := mergeOne(row, Preset{NoLiteLLM: true}, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.Known {
		t.Errorf("got %+v, want no price", got.Pricing)
	}
}

// The price join walks the same alias-exact-normalized ladder the metadata join
// does. An id models.dev can be reached with and the index cannot is a model
// that reads as fully described and unpriced for no discoverable reason.
func TestLiteLLMJoinsANormalizedModelID(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "openai/gpt-oss-120b"}
	ll := LiteLLMDoc{"groq": {"gpt-oss-120b": {
		InputMicrosPerMTok: 150_000, Known: true, Source: SourceLiteLLM,
	}}}
	got := mergeOne(row, Preset{LiteLLMID: "groq"}, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.Source != SourceLiteLLM || got.Pricing.InputMicrosPerMTok != 150_000 {
		t.Errorf("got %+v, want the index's 150000 through the normalized id", got.Pricing)
	}
}

// An Ollama tag is a colon where every other source writes a dash, which is the
// case NormalizeModelID was written for.
func TestLiteLLMJoinsAnOllamaTag(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "llama3.3:70b"}
	ll := LiteLLMDoc{"ollama": {"llama3.3-70b": {
		InputMicrosPerMTok: 400, Known: true, Source: SourceLiteLLM,
	}}}
	got := mergeOne(row, Preset{LiteLLMID: "ollama"}, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.InputMicrosPerMTok != 400 {
		t.Errorf("got %+v, want 400", got.Pricing)
	}
}

// The alias exists for the forms no rule reaches, so it leads the ladder here
// for the same reason it leads it in Join.
func TestLiteLLMPrefersAnAliasOverTheNormalizedID(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "vendor/big"}
	ll := LiteLLMDoc{"groq": {
		"big":       {InputMicrosPerMTok: 100, Known: true, Source: SourceLiteLLM},
		"the-truth": {InputMicrosPerMTok: 900, Known: true, Source: SourceLiteLLM},
	}}
	preset := Preset{LiteLLMID: "groq", ModelAliases: map[string]string{"vendor/big": "the-truth"}}
	got := mergeOne(row, preset, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.InputMicrosPerMTok != 900 {
		t.Errorf("got %+v, want the alias's 900 rather than the rule's 100", got.Pricing)
	}
}

// The exemption is the operator-facing promise, so it has to be what stops the
// join rather than an empty key happening to miss.
func TestNoLiteLLMBeatsAKeyThatWouldMatch(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "m"}
	ll := LiteLLMDoc{"groq": {"m": {InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}}}
	got := mergeOne(row, Preset{LiteLLMID: "groq", NoLiteLLM: true}, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.Known {
		t.Errorf("got %+v, want no price: an exempt preset must not join", got.Pricing)
	}
}

// A row stamped models_dev with no price is a real state — the sync stamps the
// column whether or not it found a rate — so the authoritative branch still has
// to rank the document above the index behind it.
func TestModelsDevBeatsLiteLLMOnAnAuthoritativeRow(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "big", PriceSource: string(SourceModelsDev)}
	doc := Doc{"p": {"big": Metadata{InputMicrosPerMTok: 500, PriceKnown: true}}}
	ll := LiteLLMDoc{"groq": {"big": {InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}}}
	got := mergeOne(row, Preset{ModelsDevID: "p", LiteLLMID: "groq"}, doc, ll, store.ModelOverride{})
	if got.Pricing.Source != SourceModelsDev || got.Pricing.InputMicrosPerMTok != 500 {
		t.Errorf("got %+v, want models.dev's 500", got.Pricing)
	}
}
