package router

import (
	"errors"
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
	q := Query{Model: "llama", Surface: ir.SurfaceEmbedding}

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

// snapWithModels builds a snapshot over a fixed catalog. snapOf cannot: it
// derives its catalog from the provider set, where every model is live and
// every capability inferred.
func snapWithModels(t *testing.T, ms []catalog.Model) Snapshot {
	t.Helper()
	ps := []provider.Provider{{
		ID: "p", Kind: "openaicompat", BaseURL: "https://p.example/v1",
		Credentials: []provider.Credential{{ID: "k1", Secret: "a", Enabled: true}},
	}}
	return Snapshot{
		At:        time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		Providers: ps,
		Catalog:   catalog.NewSnapshot(ms, []string{"p"}),
		Health:    health.Availability{},
	}
}

func TestRetiredModelsAreNotCandidates(t *testing.T) {
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "gone", State: catalog.StateRemovedUpstream,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}})

	// Addressed as provider/model, which is the path that reaches filterTarget.
	// A bare name never gets there — Offering already excludes retired models,
	// so resolveModel returns no targets at all.
	cands, skips, err := Resolve(Query{Model: "p/gone", Surface: ir.SurfaceLLM}, snap)
	if len(cands) != 0 {
		t.Fatalf("got %d candidates for a retired model", len(cands))
	}
	if err == nil {
		t.Error("Resolve returned no error with no candidates")
	}
	var found bool
	for _, sk := range skips {
		if sk.Reason == SkipRemoved {
			found = true
		}
	}
	if !found {
		t.Errorf("no SkipRemoved recorded; skips = %+v", skips)
	}
}

func TestRetiredModelsAreNotOfferedByBareName(t *testing.T) {
	// The other half: Offering excludes them, so a bare name is simply not
	// found rather than being found and then rejected.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "gone", State: catalog.StateRemovedUpstream,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}})
	if _, _, err := Resolve(Query{Model: "gone", Surface: ir.SurfaceLLM}, snap); !errors.Is(err, ErrModelNotFound) {
		t.Errorf("err = %v, want ErrModelNotFound", err)
	}
}

func TestStaleModelsStillRoute(t *testing.T) {
	// The breaker, not the catalog, is what avoids a provider that is actually
	// broken. Emptying the catalog on a flaky probe would break every alias.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateStale,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}})
	cands, _, err := Resolve(Query{Model: "m", Surface: ir.SurfaceLLM}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Errorf("got %d candidates for a stale model, want 1", len(cands))
	}
}

func TestKnownCapabilitiesNowFilter(t *testing.T) {
	// The carried-forward item: this used to admit everything because nothing
	// was ever Known.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces:     []ir.Surface{ir.SurfaceLLM},
		Capabilities: catalog.Capabilities{Tools: false, Known: true},
		Source:       catalog.SourceModelsDev,
	}})
	cands, skips, _ := Resolve(Query{Model: "m", Surface: ir.SurfaceLLM, NeedsTools: true}, snap)
	if len(cands) != 0 {
		t.Error("a model known to have no tools was a candidate for a tool request")
	}
	var found bool
	for _, s := range skips {
		if s.Reason == SkipCapability {
			found = true
		}
	}
	if !found {
		t.Errorf("no SkipCapability recorded; skips = %+v", skips)
	}
}

func TestInferredCapabilitiesStillRouteAndAreFlagged(t *testing.T) {
	// Master design §6.4. Claude Code always sends tools; hard-filtering on a
	// guess would mean a local model appears in the catalog and never serves.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "llama3.3:70b", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
		Source:   catalog.SourceInferred,
	}})
	cands, _, err := Resolve(Query{Model: "llama3.3:70b", Surface: ir.SurfaceLLM, NeedsTools: true}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Fatalf("got %d candidates, want 1 — an inferred model must still route", len(cands))
	}
	if !cands[0].Inferred {
		t.Error("the candidate is not flagged inferred; nothing downstream can warn")
	}
}

func TestKnownCapableModelsAreNotFlagged(t *testing.T) {
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces:     []ir.Surface{ir.SurfaceLLM},
		Capabilities: catalog.Capabilities{Tools: true, Known: true},
		Source:       catalog.SourceModelsDev,
	}})
	cands, _, _ := Resolve(Query{Model: "m", Surface: ir.SurfaceLLM, NeedsTools: true}, snap)
	if len(cands) != 1 {
		t.Fatalf("got %d candidates", len(cands))
	}
	if cands[0].Inferred {
		t.Error("a models.dev-sourced candidate was flagged inferred")
	}
}

func TestAnAdapterThatCannotServeTheSurfaceIsFiltered(t *testing.T) {
	// The catalog says the upstream offers embeddings; the adapter cannot
	// render them. Routing anyway would turn a knowable gap into a runtime
	// failure from the provider.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}})
	snap.AdapterSurfaces = map[string]adapter.SurfaceSet{
		"openaicompat": {ir.SurfaceLLM: true},
	}

	cands, skips, err := Resolve(Query{Model: "m", Surface: ir.SurfaceEmbedding}, snap)
	if len(cands) != 0 {
		t.Fatalf("got %d candidates for a surface the adapter cannot render", len(cands))
	}
	if err == nil {
		t.Error("Resolve returned no error with no candidates")
	}
	var found bool
	for _, s := range skips {
		if s.Reason == SkipAdapterSurface {
			found = true
		}
	}
	if !found {
		t.Errorf("no SkipAdapterSurface recorded; skips = %+v", skips)
	}
}

func TestAnAdapterThatServesTheSurfaceIsKept(t *testing.T) {
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}})
	snap.AdapterSurfaces = map[string]adapter.SurfaceSet{
		"openaicompat": {ir.SurfaceLLM: true, ir.SurfaceEmbedding: true},
	}
	cands, _, err := Resolve(Query{Model: "m", Surface: ir.SurfaceEmbedding}, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 {
		t.Errorf("got %d candidates, want 1", len(cands))
	}
}

func TestANilAdapterSurfacesMapImposesNoConstraint(t *testing.T) {
	// A missing map is a wiring bug. Routing nothing would be a far worse
	// symptom than routing as before, and every phase 3 test builds a snapshot
	// without one.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
	}})
	if snap.AdapterSurfaces != nil {
		t.Fatal("the fixture set a map; this test needs it nil")
	}
	cands, _, err := Resolve(Query{Model: "m", Surface: ir.SurfaceEmbedding}, snap)
	if err != nil || len(cands) != 1 {
		t.Errorf("got %d candidates, err = %v; a nil map must not filter", len(cands), err)
	}
}

func TestAKindAbsentFromTheMapIsNotASurfaceSkip(t *testing.T) {
	// An unregistered kind is a no_adapter fact the executor reports from the
	// loop. Answering it here would relabel it as a surface gap, which points
	// the operator at a catalog they cannot fix.
	snap := snapWithModels(t, []catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}})
	snap.AdapterSurfaces = map[string]adapter.SurfaceSet{
		"some-other-kind": {ir.SurfaceLLM: true},
	}
	cands, skips, err := Resolve(Query{Model: "m", Surface: ir.SurfaceLLM}, snap)
	if err != nil || len(cands) != 1 {
		t.Fatalf("got %d candidates, err = %v; an absent kind must not filter", len(cands), err)
	}
	for _, s := range skips {
		if s.Reason == SkipAdapterSurface {
			t.Errorf("an absent kind was recorded as a surface skip: %+v", s)
		}
	}
}

// keylessFleet is a provider reached with no credential at all — a local
// runtime, or a public gateway that asks for nothing.
func keylessFleet(cs ...provider.Credential) []provider.Provider {
	return []provider.Provider{{
		ID: "ollama", Kind: "openaicompat", BaseURL: "http://localhost:11434/v1",
		AuthStyle: "none", Credentials: cs, Models: []string{"llama"},
	}}
}

func TestFilterRoutesToAKeylessProviderWithNoCredentials(t *testing.T) {
	// The whole point: requiring an invented secret to reach a server on the
	// loopback interface is a configuration step that protects nothing, and
	// until now it was the difference between a usable local runtime and one
	// the router would never choose.
	ps := keylessFleet()
	snap := snapOf(t, ps, health.Availability{})

	cands, skips, found := filterTarget(target{"ollama", "llama"}, llmQuery(), snap, byIDOf(ps))
	if !found {
		t.Fatal("provider should have been found")
	}
	if len(skips) != 0 {
		t.Errorf("skips = %+v, want none", skips)
	}
	if len(cands) != 1 {
		t.Fatalf("candidates = %+v, want one", cands)
	}
	// The empty string is the honest key id: there is no credential to name.
	if cands[0].KeyID != "" || cands[0].ProviderID != "ollama" {
		t.Errorf("candidate = %+v", cands[0])
	}
}

func TestAKeylessProviderStillCools(t *testing.T) {
	// Its one attempt is keyed on the empty credential id, so a provider that
	// is refusing everything backs off exactly as a keyed one does.
	ps := keylessFleet()
	b := health.New(1, time.Minute)
	b.Record(health.Key{ProviderID: "ollama", KeyID: "", Model: "llama"},
		health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	snap := snapOf(t, ps, b.SnapshotAvailability(time.Now()))

	cands, skips, _ := filterTarget(target{"ollama", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 0 {
		t.Fatalf("candidates = %+v, want none while cooling", cands)
	}
	if len(skips) != 1 || skips[0].Reason != SkipCooling {
		t.Fatalf("skips = %+v, want one cooling", skips)
	}
}

func TestAKeylessProviderPrefersItsCredentialsWhenItHasSome(t *testing.T) {
	// Keyless says a credential is not required, not that one is ignored: an
	// operator who added a token to a local runtime behind a reverse proxy
	// means it to be sent.
	ps := keylessFleet(provider.Credential{ID: "k1", Secret: "a", Enabled: true})
	snap := snapOf(t, ps, health.Availability{})

	cands, _, _ := filterTarget(target{"ollama", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 1 || cands[0].KeyID != "k1" {
		t.Fatalf("candidates = %+v, want the real credential", cands)
	}
}

func TestAKeyedProviderWithNoCredentialsStillSkips(t *testing.T) {
	// The guard that keyless must not weaken: a bearer provider with no key
	// cannot serve, and routing to it would 401 every request.
	ps := fleetWith()
	ps[0].AuthStyle = "bearer"
	snap := snapOf(t, ps, health.Availability{})

	_, skips, _ := filterTarget(target{"groq", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(skips) != 1 || skips[0].Reason != SkipNoCredential {
		t.Fatalf("skips = %+v, want one no_credential", skips)
	}
}

func TestAnOptionalProviderRoutesBothWays(t *testing.T) {
	// The style exists for gateways that answer an unauthenticated request and
	// answer a credentialled one more generously, so both shapes must route.
	ps := keylessFleet()
	ps[0].AuthStyle = "optional"
	snap := snapOf(t, ps, health.Availability{})
	cands, _, _ := filterTarget(target{"ollama", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 1 || cands[0].KeyID != "" {
		t.Fatalf("with no credential: %+v", cands)
	}

	ps[0].Credentials = []provider.Credential{{ID: "k1", Secret: "a", Enabled: true}}
	snap = snapOf(t, ps, health.Availability{})
	cands, _, _ = filterTarget(target{"ollama", "llama"}, llmQuery(), snap, byIDOf(ps))
	if len(cands) != 1 || cands[0].KeyID != "k1" {
		t.Fatalf("with a credential: %+v", cands)
	}
}
