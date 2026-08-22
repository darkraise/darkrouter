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
			Surfaces: []string{"llm", "embeddings"},
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
	if len(m.Surfaces) != 2 || m.Surfaces[0] != ir.SurfaceLLM || m.Surfaces[1] != ir.SurfaceEmbeddings {
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
		Surfaces: []string{"llm", "embeddings"},
	}
	in.Rows = []store.ModelRow{{
		ProviderID: "p", ModelID: "text-embedding-3-small",
		State: "live", CapabilitiesSource: "inferred",
		Surfaces: []string{"llm"}, // exactly what RecordDiscoverySuccess writes
	}}

	m := find(t, Merge(in), "text-embedding-3-small")
	if !m.DeclaresSurface(ir.SurfaceEmbeddings) {
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
		Surfaces: []string{"llm", "embeddings"},
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
