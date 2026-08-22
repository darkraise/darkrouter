package router

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/provider"
)

func testCatalog() catalog.Reader {
	return catalog.FromProviders([]provider.Provider{
		{ID: "groq", Models: []string{"llama", "shared", "openai/gpt-oss-120b"}},
		{ID: "cerebras", Models: []string{"shared"}},
	})
}

func testProviders() map[string]bool {
	return map[string]bool{"groq": true, "cerebras": true}
}

func targetsOf(ts []target) [][2]string {
	out := make([][2]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, [2]string{t.ProviderID, t.ModelID})
	}
	return out
}

func TestResolveExactAliasExpandsInOrder(t *testing.T) {
	aliases := map[string][]string{
		"fast": {"cerebras/shared", "groq/shared"},
	}
	got, alias := resolveModel("fast", aliases, testProviders(), testCatalog())
	if alias != "fast" {
		t.Errorf("alias = %q, want fast", alias)
	}
	want := [][2]string{{"cerebras", "shared"}, {"groq", "shared"}}
	if g := targetsOf(got); len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Errorf("targets = %v, want %v", g, want)
	}
}

func TestResolveProviderSlashModel(t *testing.T) {
	got, alias := resolveModel("groq/llama", nil, testProviders(), testCatalog())
	if alias != "" {
		t.Errorf("alias = %q, want empty", alias)
	}
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"groq", "llama"} {
		t.Errorf("targets = %v", g)
	}
}

// The load-bearing case: a slash that is part of the model name, not a prefix.
func TestResolveFallsThroughWhenThePrefixIsNotAProvider(t *testing.T) {
	got, _ := resolveModel("openai/gpt-oss-120b", nil, testProviders(), testCatalog())
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"groq", "openai/gpt-oss-120b"} {
		t.Fatalf("targets = %v, want groq offering the full slashed name", g)
	}
}

func TestResolveBareNameFansOutInProviderOrder(t *testing.T) {
	got, _ := resolveModel("shared", nil, testProviders(), testCatalog())
	g := targetsOf(got)
	if len(g) != 2 || g[0] != [2]string{"groq", "shared"} || g[1] != [2]string{"cerebras", "shared"} {
		t.Errorf("targets = %v, want groq then cerebras", g)
	}
}

func TestResolveUnknownNameYieldsNothing(t *testing.T) {
	got, _ := resolveModel("nope", nil, testProviders(), testCatalog())
	if len(got) != 0 {
		t.Errorf("targets = %v, want none", targetsOf(got))
	}
}

func TestResolveProviderSlashModelTheProviderDoesNotOffer(t *testing.T) {
	// The prefix names a real provider, so rule 2 matches and rule 3 is not
	// tried. The target is produced and filtering rejects it later — that is
	// what makes the skip reason say "this provider", not "no such model".
	got, _ := resolveModel("cerebras/llama", nil, testProviders(), testCatalog())
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"cerebras", "llama"} {
		t.Errorf("targets = %v", g)
	}
}

func TestAliasWinsOverProviderSlashModel(t *testing.T) {
	aliases := map[string][]string{"groq/llama": {"cerebras/shared"}}
	got, alias := resolveModel("groq/llama", aliases, testProviders(), testCatalog())
	if alias != "groq/llama" {
		t.Errorf("alias = %q", alias)
	}
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"cerebras", "shared"} {
		t.Errorf("targets = %v, want the alias expansion", g)
	}
}

// An alias naming another alias is not followed; the inner name goes through
// rules 2 and 3 like any other target.
func TestAliasesAreNotNested(t *testing.T) {
	aliases := map[string][]string{
		"outer": {"inner"},
		"inner": {"groq/llama"},
	}
	got, _ := resolveModel("outer", aliases, testProviders(), testCatalog())
	// "inner" is not a model any provider offers, so it resolves to nothing.
	if len(got) != 0 {
		t.Errorf("targets = %v, want none — nested aliases are not followed", targetsOf(got))
	}
}

func TestAliasWithAnUnknownProviderYieldsThatTargetAnyway(t *testing.T) {
	aliases := map[string][]string{"fast": {"nosuch/model", "groq/llama"}}
	got, _ := resolveModel("fast", aliases, testProviders(), testCatalog())
	// "nosuch" is not a provider, so rule 2 misses and rule 3 finds no offering
	// for the full string. Only the reachable half survives.
	if g := targetsOf(got); len(g) != 1 || g[0] != [2]string{"groq", "llama"} {
		t.Errorf("targets = %v, want only the reachable target", g)
	}
}
