package catalog

import (
	"sync"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func snapModels() []Model {
	return []Model{
		{ProviderID: "a", ModelID: "shared", State: StateLive, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "shared", State: StateStale, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "gone", State: StateRemovedUpstream, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "embed", State: StateLive, Surfaces: []ir.Surface{ir.SurfaceEmbeddings}},
	}
}

func TestSnapshotLookup(t *testing.T) {
	s := NewSnapshot(snapModels(), []string{"b", "a"})
	m, ok := s.Lookup("a", "shared")
	if !ok || m.State != StateLive {
		t.Fatalf("lookup = %+v %v", m, ok)
	}
	if _, ok := s.Lookup("a", "nope"); ok {
		t.Error("an unknown model reported a hit")
	}
	// A retired model is still looked up: the trace view and the UI show it
	// with provenance. Only routing excludes it.
	gone, ok := s.Lookup("b", "gone")
	if !ok {
		t.Fatal("a removed_upstream model vanished from Lookup")
	}
	if gone.Routable() {
		t.Error("a removed_upstream model reports routable")
	}
}

func TestOfferingIsInProviderOrderAndExcludesRemoved(t *testing.T) {
	// The provider order is priority order, and it is what decides which
	// provider a bare model name is attempted against first.
	s := NewSnapshot(snapModels(), []string{"b", "a"})
	got := s.Offering("shared")
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Errorf("Offering(shared) = %v, want [b a]", got)
	}
	// Stale stays offered; the breaker is what avoids a broken provider.
	if len(s.Offering("gone")) != 0 {
		t.Errorf("Offering(gone) = %v, want empty", s.Offering("gone"))
	}
}

func TestSearchFilters(t *testing.T) {
	s := NewSnapshot(snapModels(), []string{"a", "b"})
	if got := s.Search(Filter{Surface: ir.SurfaceEmbeddings}); len(got) != 1 || got[0].ModelID != "embed" {
		t.Errorf("surface filter = %v", got)
	}
	if got := s.Search(Filter{ProviderID: "a"}); len(got) != 1 || got[0].ProviderID != "a" {
		t.Errorf("provider filter = %v", got)
	}
	if got := s.Search(Filter{Query: "SHAR"}); len(got) != 2 {
		t.Errorf("query filter matched %d, want 2 (case-insensitive substring)", len(got))
	}
	// Removed models are excluded by default and reachable on request, because
	// the UI has to show them to offer a purge.
	if got := s.Search(Filter{}); len(got) != 3 {
		t.Errorf("default search returned %d, want 3", len(got))
	}
	if got := s.Search(Filter{IncludeRemoved: true}); len(got) != 4 {
		t.Errorf("IncludeRemoved returned %d, want 4", len(got))
	}
}

func TestSnapshotSatisfiesReader(t *testing.T) {
	var _ Reader = NewSnapshot(nil, nil)
}

func TestStoreServesAnEmptySnapshotBeforeFirstRebuild(t *testing.T) {
	// A request arriving before the first rebuild must get an answer, not a
	// nil dereference.
	var st Store
	s := st.Snapshot()
	if s == nil {
		t.Fatal("zero Store returned a nil snapshot")
	}
	if _, ok := s.Lookup("a", "b"); ok {
		t.Error("the empty snapshot reported a hit")
	}
	if len(s.Offering("b")) != 0 {
		t.Error("the empty snapshot offered something")
	}
}

func TestStoreSwapIsRaceFree(t *testing.T) {
	// The whole design: readers hold an immutable snapshot while a worker
	// replaces it. Run under -race; without the atomic swap this fails.
	var st Store
	st.Set(NewSnapshot(snapModels(), []string{"a", "b"}))

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := st.Snapshot()
				_, _ = s.Lookup("a", "shared")
				_ = s.Offering("shared")
				_ = s.Search(Filter{Surface: ir.SurfaceLLM})
			}
		}()
	}
	for i := 0; i < 200; i++ {
		st.Set(NewSnapshot(snapModels(), []string{"a", "b"}))
	}
	close(stop)
	wg.Wait()
}
