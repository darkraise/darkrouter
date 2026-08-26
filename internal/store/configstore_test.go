package store

import (
	"context"
	"testing"
)

func TestAliasesRoundTripInChainOrder(t *testing.T) {
	// The chain order is the fallback order, so a test that only counted rows
	// would pass on a table that shuffled them.
	ctx := context.Background()
	db := migrated(t)

	want := map[string][]string{
		"fast":  {"groq/llama", "cerebras/llama", "together/llama"},
		"smart": {"anthropic/opus", "openai/gpt"},
	}
	if err := db.PutAliases(ctx, want); err != nil {
		t.Fatal(err)
	}

	got, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d aliases, want %d", len(got), len(want))
	}
	for name, chain := range want {
		if len(got[name]) != len(chain) {
			t.Fatalf("%s: got %v, want %v", name, got[name], chain)
		}
		for i, target := range chain {
			if got[name][i] != target {
				t.Errorf("%s[%d] = %q, want %q", name, i, got[name][i], target)
			}
		}
	}
}

func TestPutAliasesReplacesRatherThanMerges(t *testing.T) {
	// A chain the operator deleted has to actually disappear.
	ctx := context.Background()
	db := migrated(t)

	if err := db.PutAliases(ctx, map[string][]string{
		"keep": {"a/one"},
		"drop": {"b/two"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutAliases(ctx, map[string][]string{"keep": {"a/one"}}); err != nil {
		t.Fatal(err)
	}

	got, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["drop"]; ok {
		t.Error("a deleted alias survived the replace")
	}
	if len(got["keep"]) != 1 {
		t.Errorf("keep = %v, want one target", got["keep"])
	}
}

func TestAliasesAreEmptyOnAFreshDatabase(t *testing.T) {
	got, err := migrated(t).Aliases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("fresh database has %d aliases", len(got))
	}
}
