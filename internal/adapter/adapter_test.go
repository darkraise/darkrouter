package adapter

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// silentAdapter implements Adapter without SurfaceProvider. Only Kind is
// exercised; the rest satisfy the interface.
type silentAdapter struct{ Adapter }

func (silentAdapter) Kind() string { return "silent" }

type talkativeAdapter struct{ Adapter }

func (talkativeAdapter) Kind() string { return "talkative" }
func (talkativeAdapter) Surfaces() SurfaceSet {
	return SurfaceSet{ir.SurfaceLLM: true, ir.SurfaceEmbedding: true}
}

func TestSurfacesOfDefaultsToChatOnly(t *testing.T) {
	// An adapter that declares nothing serves llm. Defaulting to everything
	// would make an unimplemented surface a runtime 404 from the provider
	// instead of a routing decision Darkrouter can explain.
	got := SurfacesOf(silentAdapter{})
	if !got.Has(ir.SurfaceLLM) {
		t.Error("the default does not include llm")
	}
	for _, s := range ir.AllSurfaces() {
		if s == ir.SurfaceLLM {
			continue
		}
		if got.Has(s) {
			t.Errorf("the default claims %q", s)
		}
	}
}

func TestSurfacesOfReadsTheDeclaration(t *testing.T) {
	got := SurfacesOf(talkativeAdapter{})
	if !got.Has(ir.SurfaceLLM) || !got.Has(ir.SurfaceEmbedding) {
		t.Errorf("surfaces = %v", got)
	}
	if got.Has(ir.SurfaceTTS) {
		t.Error("an undeclared surface reported present")
	}
}

func TestSurfaceSetHasIsNilSafe(t *testing.T) {
	// A nil set is "nothing declared", not a panic: the zero value has to be
	// usable because a map field is easy to leave unset.
	var s SurfaceSet
	if s.Has(ir.SurfaceLLM) {
		t.Error("a nil set claimed a surface")
	}
}
