package router

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

// publisherReader is a catalog whose one model carries a publisher. The
// provider-derived reader cannot express one, and the publisher is precisely
// what this asserts on.
type publisherReader struct {
	model catalog.Model
}

func (r publisherReader) Lookup(providerID, modelID string) (catalog.Model, bool) {
	if providerID == r.model.ProviderID && modelID == r.model.ModelID {
		return r.model, true
	}
	return catalog.Model{}, false
}

func (r publisherReader) Offering(modelID string) []string {
	if modelID == r.model.ModelID {
		return []string{r.model.ProviderID}
	}
	return nil
}

func vertexSnapshot(t *testing.T) Snapshot {
	t.Helper()
	ps := []provider.Provider{{
		ID: "vx", Kind: "vertex", BaseURL: "", Preset: "vertex-anthropic",
		Project: "proj", Location: "us-central1", Priority: 10,
		Models:      []string{"claude-sonnet-4-5"},
		Credentials: []provider.Credential{{ID: "k1", Secret: "{}", Enabled: true, Kind: "gcp_sa"}},
	}}
	return Snapshot{
		At:        time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		Providers: ps,
		Catalog: publisherReader{model: catalog.Model{
			ProviderID: "vx", ModelID: "claude-sonnet-4-5",
			Publisher: "publishers/anthropic",
			Surfaces:  []ir.Surface{ir.SurfaceLLM},
			State:     catalog.StateLive,
			Source:    catalog.SourceModelsDev,
			Capabilities: catalog.Capabilities{
				Tools: true, Vision: true, Reasoning: true, Known: true,
			},
		}},
		Config: &config.Config{},
		Health: health.Availability{},
	}
}

func TestCandidateCarriesThePublisher(t *testing.T) {
	// Without this every Vertex request takes the Google route regardless of
	// the model, and every Claude call 400s.
	cands, skips, err := Resolve(
		Query{Model: "claude-sonnet-4-5", Surface: ir.SurfaceLLM},
		vertexSnapshot(t))
	if err != nil {
		t.Fatalf("resolve: %v (skips %+v)", err, skips)
	}
	if len(cands) == 0 {
		t.Fatalf("no candidates; skips = %+v", skips)
	}
	if cands[0].Publisher != "publishers/anthropic" {
		t.Errorf("publisher = %q, want publishers/anthropic", cands[0].Publisher)
	}
}

func TestANonVertexCandidateHasNoPublisher(t *testing.T) {
	cands, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM},
		fullSnap(twoProviders(), nil, health.Availability{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) == 0 {
		t.Fatal("no candidates")
	}
	if cands[0].Publisher != "" {
		t.Errorf("publisher = %q, want empty for openaicompat", cands[0].Publisher)
	}
}
