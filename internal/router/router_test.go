package router

import (
	"errors"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

func twoProviders() []provider.Provider {
	return []provider.Provider{
		{ID: "groq", Kind: "openaicompat", BaseURL: "https://groq.example/v1",
			Priority: 10, Models: []string{"shared"},
			Credentials: []provider.Credential{
				{ID: "g1", Secret: "a", Enabled: true},
				{ID: "g2", Secret: "b", Enabled: true},
			}},
		{ID: "cerebras", Kind: "openaicompat", BaseURL: "https://cerebras.example/v1",
			Priority: 5, Models: []string{"shared"},
			Credentials: []provider.Credential{{ID: "c1", Secret: "c", Enabled: true}}},
	}
}

func fullSnap(ps []provider.Provider, cfg *config.Config, avail health.Availability) Snapshot {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return Snapshot{
		At:        time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC),
		Providers: ps,
		Catalog:   catalog.FromProviders(ps),
		Config:    cfg,
		Health:    avail,
	}
}

func seq(cands []Candidate) [][2]string {
	out := make([][2]string, 0, len(cands))
	for _, c := range cands {
		out = append(out, [2]string{c.ProviderID, c.KeyID})
	}
	return out
}

// Credentials rotate before providers: that is the whole point of holding
// several free-tier keys.
func TestResolveDrainsCredentialsBeforeAdvancingProviders(t *testing.T) {
	ps := twoProviders()
	cands, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM},
		fullSnap(ps, nil, health.Availability{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Errorf("skips = %+v", skips)
	}
	got := seq(cands)
	want := [][2]string{{"groq", "g1"}, {"groq", "g2"}, {"cerebras", "c1"}}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("sequence = %v, want %v", got, want)
		}
	}
	if len(got) != 3 {
		t.Fatalf("sequence = %v, want exactly 3", got)
	}
}

func TestResolveExpandsAnAliasInChainOrder(t *testing.T) {
	ps := twoProviders()
	cfg := &config.Config{Aliases: map[string][]string{
		"fast": {"cerebras/shared", "groq/shared"},
	}}
	cands, _, err := Resolve(Query{Model: "fast", Surface: ir.SurfaceLLM},
		fullSnap(ps, cfg, health.Availability{}))
	if err != nil {
		t.Fatal(err)
	}
	got := seq(cands)
	if len(got) != 3 || got[0] != [2]string{"cerebras", "c1"} || got[1] != [2]string{"groq", "g1"} {
		t.Errorf("sequence = %v, want cerebras first", got)
	}
}

func TestResolveUnknownModel(t *testing.T) {
	_, _, err := Resolve(Query{Model: "nope", Surface: ir.SurfaceLLM},
		fullSnap(twoProviders(), nil, health.Availability{}))
	if !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("err = %v, want ErrModelNotFound", err)
	}
}

func TestResolveWrongSurfaceIsDistinguishable(t *testing.T) {
	_, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceEmbedding},
		fullSnap(twoProviders(), nil, health.Availability{}))
	if !errors.Is(err, ErrSurfaceUnsupported) {
		t.Fatalf("err = %v, want ErrSurfaceUnsupported", err)
	}
	if len(skips) != 2 {
		t.Errorf("skips = %+v, want one per provider", skips)
	}
}

func TestResolveEverythingCoolingIsDistinguishable(t *testing.T) {
	ps := twoProviders()
	b := health.New(3, time.Minute)
	for _, k := range []health.Key{
		{ProviderID: "groq", KeyID: "g1", Model: "shared"},
		{ProviderID: "groq", KeyID: "g2", Model: "shared"},
		{ProviderID: "cerebras", KeyID: "c1", Model: "shared"},
	} {
		for i := 0; i < 3; i++ {
			b.Record(k, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
		}
	}
	snap := fullSnap(ps, nil, b.SnapshotAvailability(time.Now()))

	cands, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
	if !errors.Is(err, ErrAllCooling) {
		t.Fatalf("err = %v, want ErrAllCooling", err)
	}
	if len(cands) != 0 {
		t.Errorf("candidates = %+v", cands)
	}
	// The skips must still explain the ordering without live health.
	if len(skips) != 3 {
		t.Fatalf("skips = %+v, want three", skips)
	}
	for _, s := range skips {
		if s.Reason != SkipCooling {
			t.Errorf("skip = %+v, want cooling", s)
		}
	}
}

// One cooling credential does not make the request fail; it makes the sequence
// shorter and the trace explain why.
func TestResolveSkipsOneCoolingCredentialAndContinues(t *testing.T) {
	ps := twoProviders()
	b := health.New(3, time.Minute)
	k := health.Key{ProviderID: "groq", KeyID: "g1", Model: "shared"}
	for i := 0; i < 3; i++ {
		b.Record(k, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	snap := fullSnap(ps, nil, b.SnapshotAvailability(time.Now()))

	cands, skips, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
	if err != nil {
		t.Fatal(err)
	}
	got := seq(cands)
	if len(got) != 2 || got[0] != [2]string{"groq", "g2"} {
		t.Errorf("sequence = %v, want g2 then cerebras", got)
	}
	if len(skips) != 1 || skips[0].KeyID != "g1" || skips[0].Reason != SkipCooling {
		t.Errorf("skips = %+v", skips)
	}
}

// Purity: the same snapshot must give the same answer every time.
func TestResolveIsDeterministic(t *testing.T) {
	ps := twoProviders()
	snap := fullSnap(ps, nil, health.Availability{})
	first, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM}, snap)
		if err != nil {
			t.Fatal(err)
		}
		a, b := seq(first), seq(again)
		for j := range a {
			if a[j] != b[j] {
				t.Fatalf("run %d diverged: %v vs %v", i, a, b)
			}
		}
	}
}

func TestResolveDoesNotTruncateToMaxAttempts(t *testing.T) {
	ps := twoProviders()
	cfg := &config.Config{Policy: config.PolicyConfig{Retry: config.RetryConfig{MaxAttempts: 1}}}
	cands, _, err := Resolve(Query{Model: "shared", Surface: ir.SurfaceLLM},
		fullSnap(ps, cfg, health.Availability{}))
	if err != nil {
		t.Fatal(err)
	}
	// The trace must record everything that was eligible; the loop truncates.
	if len(cands) != 3 {
		t.Errorf("got %d candidates, want the full list of 3", len(cands))
	}
}

func TestMatchedAliasNamesTheAliasARequestCameInUnder(t *testing.T) {
	// The request log's alias column, Usage's alias dimension and the
	// overview's routing graph all read this. A row that records nothing
	// leaves all three empty against a real gateway.
	cfg := &config.Config{Aliases: map[string][]string{"fast": {"groq/shared"}}}
	snap := fullSnap(twoProviders(), cfg, health.Availability{})

	if got := MatchedAlias("fast", snap); got != "fast" {
		t.Errorf("MatchedAlias(alias) = %q, want fast", got)
	}
	// A bare model name is not an alias, and reporting one would attribute
	// traffic to an alias nobody asked for.
	if got := MatchedAlias("shared", snap); got != "" {
		t.Errorf("MatchedAlias(model) = %q, want empty", got)
	}
	if got := MatchedAlias("groq/shared", snap); got != "" {
		t.Errorf("MatchedAlias(provider/model) = %q, want empty", got)
	}
}

func TestMatchedAliasSurvivesASnapshotWithNoConfig(t *testing.T) {
	if got := MatchedAlias("fast", Snapshot{}); got != "" {
		t.Errorf("MatchedAlias = %q, want empty", got)
	}
}
