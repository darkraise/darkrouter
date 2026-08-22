package router

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

func fleetWith(cs ...provider.Credential) []provider.Provider {
	return []provider.Provider{{
		ID: "groq", Kind: "openaicompat", BaseURL: "https://groq.example/v1",
		Credentials: cs, Models: []string{"llama"},
	}}
}

func snapOf(t *testing.T, ps []provider.Provider, avail health.Availability) Snapshot {
	t.Helper()
	return Snapshot{
		At:        time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		Providers: ps,
		Catalog:   catalog.FromProviders(ps),
		Health:    avail,
	}
}

func byIDOf(ps []provider.Provider) map[string]provider.Provider {
	m := make(map[string]provider.Provider, len(ps))
	for _, p := range ps {
		m[p.ID] = p
	}
	return m
}

func llmQuery() Query { return Query{Model: "llama", Surface: ir.SurfaceLLM} }

func TestFilterProducesOneCandidatePerCredential(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true},
		provider.Credential{ID: "k2", Secret: "b", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, found := filterTarget(target{"groq", "llama"}, llmQuery(), snap, byIDOf(ps))
	if !found {
		t.Fatal("provider should have been found")
	}
	if len(skips) != 0 {
		t.Errorf("skips = %+v, want none", skips)
	}
	if len(cands) != 2 || cands[0].KeyID != "k1" || cands[1].KeyID != "k2" {
		t.Fatalf("candidates = %+v", cands)
	}
	if cands[0].ProviderID != "groq" || cands[0].Model != "llama" || cands[0].Kind != "openaicompat" {
		t.Errorf("candidate = %+v", cands[0])
	}
}

func TestFilterSkipsAProviderWithNoCredentials(t *testing.T) {
	ps := fleetWith()
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, _ := filterTarget(target{"groq", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v, want none", cands)
	}
	if len(skips) != 1 || skips[0].Reason != SkipNoCredential {
		t.Fatalf("skips = %+v, want one no_credential", skips)
	}
}

func TestFilterSkipsACoolingCredentialAndKeepsTheOther(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true},
		provider.Credential{ID: "k2", Secret: "b", Enabled: true})

	b := health.New(3, time.Minute)
	cooled := health.Key{ProviderID: "groq", KeyID: "k1", Model: "llama"}
	for i := 0; i < 3; i++ {
		b.Record(cooled, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	snap := snapOf(t, ps, b.SnapshotAvailability(time.Now()))

	cands, skips, _ := filterTarget(target{"groq", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 1 || cands[0].KeyID != "k2" {
		t.Fatalf("candidates = %+v, want only k2", cands)
	}
	if len(skips) != 1 || skips[0].Reason != SkipCooling || skips[0].KeyID != "k1" {
		t.Fatalf("skips = %+v, want one cooling on k1", skips)
	}
}

func TestFilterSkipsAModelTheProviderDoesNotOffer(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, found := filterTarget(target{"groq", "nope"},
		Query{Model: "nope", Surface: ir.SurfaceLLM}, snap, byIDOf(ps))
	if !found {
		t.Fatal("the provider exists even though the model does not")
	}
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v", cands)
	}
	if len(skips) != 1 || skips[0].Reason != SkipSurface {
		t.Fatalf("skips = %+v, want one surface", skips)
	}
}

func TestFilterSkipsTheWrongSurface(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})
	q := Query{Model: "llama", Surface: ir.SurfaceEmbeddings}

	cands, skips, _ := filterTarget(target{"groq", "llama"}, q, snap, byIDOf(ps))
	if len(cands) != 0 || len(skips) != 1 || skips[0].Reason != SkipSurface {
		t.Fatalf("candidates=%+v skips=%+v", cands, skips)
	}
}

func TestFilterReportsAnUnknownProvider(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, found := filterTarget(target{"nosuch", "llama"}, llmQuery(), snap, byIDOf(ps))
	if found {
		t.Fatal("an unknown provider must be reported as not found")
	}
	if len(cands) != 0 || len(skips) != 1 || skips[0].Reason != SkipDisabled {
		t.Fatalf("candidates=%+v skips=%+v", cands, skips)
	}
}

// Phase 3 capabilities are inferred, so a tools requirement admits the
// candidate and the filter is exercised without being selective.
func TestFilterAdmitsInferredCapabilities(t *testing.T) {
	ps := fleetWith(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})
	q := Query{Model: "llama", Surface: ir.SurfaceLLM, NeedsTools: true, NeedsVision: true}

	cands, skips, _ := filterTarget(target{"groq", "llama"}, q, snap, byIDOf(ps))
	if len(cands) != 1 {
		t.Fatalf("inferred capabilities must admit the candidate, got %+v %+v", cands, skips)
	}
}
