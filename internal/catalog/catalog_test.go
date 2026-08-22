package catalog

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

func fleet() []provider.Provider {
	return []provider.Provider{
		{ID: "groq", Models: []string{"llama", "shared"}},
		{ID: "cerebras", Models: []string{"shared"}},
	}
}

func TestLookupFindsAProvidersModel(t *testing.T) {
	r := FromProviders(fleet())
	m, ok := r.Lookup("groq", "llama")
	if !ok {
		t.Fatal("groq/llama not found")
	}
	if m.ProviderID != "groq" || m.ModelID != "llama" {
		t.Errorf("model = %+v", m)
	}
	if m.Source != SourceInferred {
		t.Errorf("Source = %q, want inferred — phase 3 has no catalog data", m.Source)
	}
	if len(m.Surfaces) != 1 || m.Surfaces[0] != ir.SurfaceLLM {
		t.Errorf("Surfaces = %v, want [llm]", m.Surfaces)
	}
}

func TestLookupMissesAreReported(t *testing.T) {
	r := FromProviders(fleet())
	if _, ok := r.Lookup("groq", "nope"); ok {
		t.Error("a model the provider does not offer must not be found")
	}
	if _, ok := r.Lookup("nosuch", "llama"); ok {
		t.Error("a provider that does not exist must not be found")
	}
}

func TestOfferingListsEveryProviderInOrder(t *testing.T) {
	r := FromProviders(fleet())
	got := r.Offering("shared")
	if len(got) != 2 || got[0] != "groq" || got[1] != "cerebras" {
		t.Errorf("Offering = %v, want [groq cerebras] in provider order", got)
	}
	if len(r.Offering("nope")) != 0 {
		t.Error("an unoffered model must yield no providers")
	}
}

func TestModelDeclaresSurface(t *testing.T) {
	r := FromProviders(fleet())
	m, _ := r.Lookup("groq", "llama")
	if !m.DeclaresSurface(ir.SurfaceLLM) {
		t.Error("llm must be declared")
	}
	if m.DeclaresSurface(ir.SurfaceEmbeddings) {
		t.Error("embeddings must not be declared in phase 3")
	}
}

// Inferred capabilities admit everything: hard-filtering on guessed metadata
// would make every local model unroutable for agentic traffic.
func TestInferredCapabilitiesSatisfyEveryRequirement(t *testing.T) {
	r := FromProviders(fleet())
	m, _ := r.Lookup("groq", "llama")
	if !m.Capabilities.Satisfies(Capabilities{Tools: true, Vision: true, Reasoning: true}) {
		t.Error("inferred capabilities must admit the candidate")
	}
	if !m.Inferred() {
		t.Error("phase 3 models are inferred and the router must warn about them")
	}
}

func TestKnownCapabilitiesAreSelective(t *testing.T) {
	// Phase 6 supplies real data; the comparison must already be correct.
	known := Capabilities{Tools: false, Vision: true, Reasoning: false, Known: true}
	if known.Satisfies(Capabilities{Tools: true}) {
		t.Error("a known model without tools must not satisfy a tools requirement")
	}
	if !known.Satisfies(Capabilities{Vision: true}) {
		t.Error("a known model with vision must satisfy a vision requirement")
	}
	if !known.Satisfies(Capabilities{}) {
		t.Error("no requirement is always satisfied")
	}
}
