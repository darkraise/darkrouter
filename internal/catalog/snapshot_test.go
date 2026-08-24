package catalog

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

func snapModels() []Model {
	return []Model{
		{ProviderID: "a", ModelID: "shared", State: StateLive, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "shared", State: StateStale, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "gone", State: StateRemovedUpstream, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "b", ModelID: "embed", State: StateLive, Surfaces: []ir.Surface{ir.SurfaceEmbedding}},
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
	if got := s.Search(Filter{Surface: ir.SurfaceEmbedding}); len(got) != 1 || got[0].ModelID != "embed" {
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

// gatedSource makes the window between Rebuild's reads and its publish
// observable: every call sleeps, so two overlapping rebuilds are guaranteed to
// be seen overlapping rather than merely being able to.
type gatedSource struct {
	mu     sync.Mutex
	active int
	peak   int
	ps     []provider.Provider
}

func (g *gatedSource) Providers(context.Context) ([]provider.Provider, error) {
	g.mu.Lock()
	g.active++
	if g.active > g.peak {
		g.peak = g.active
	}
	g.mu.Unlock()

	time.Sleep(20 * time.Millisecond)

	g.mu.Lock()
	g.active--
	g.mu.Unlock()
	return g.ps, nil
}

func (g *gatedSource) Revision() uint64 { return 1 }

func (g *gatedSource) peakConcurrency() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.peak
}

// Rebuild reads every source and then publishes. Two of them interleaving lets
// the one that read older data publish last, and its stale snapshot then serves
// routing until something rebuilds again — up to a full discovery interval. The
// discovery worker and the models.dev sync worker are independent goroutines
// that both write rows and then rebuild, so the overlap is reachable in
// production, not only here.
func TestRebuildDoesNotOverlapItself(t *testing.T) {
	db := discoveryDB(t, "a")
	src := &gatedSource{ps: []provider.Provider{{ID: "a", Kind: "openaicompat", BaseURL: "http://x"}}}
	s := NewStore(db, src)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Rebuild(context.Background()); err != nil {
				t.Errorf("Rebuild: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := src.peakConcurrency(); got != 1 {
		t.Errorf("peak concurrent Rebuild reads = %d, want 1: a rebuild that "+
			"read stale data can publish over one that read fresh data", got)
	}
}
