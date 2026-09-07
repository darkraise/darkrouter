package store

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
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

func TestOverlayConfigReplacesAliasesOnly(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)
	if err := db.PutAliases(ctx, map[string][]string{"db": {"groq/x"}}); err != nil {
		t.Fatal(err)
	}
	if err := putSetting(ctx, db.Write, "policy.retry.max_attempts", "6"); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Aliases:   map[string][]string{"from-file": {"openai/y"}},
		Policy:    config.PolicyConfig{Retry: config.RetryConfig{MaxAttempts: 2}},
		Providers: []config.ProviderConfig{{ID: "groq"}},
		Log:       config.LogConfig{Retention: 72 * time.Hour},
	}
	if err := OverlayConfig(ctx, db, cfg); err != nil {
		t.Fatal(err)
	}

	if _, ok := cfg.Aliases["from-file"]; ok {
		t.Error("the file's aliases survived the overlay")
	}
	if len(cfg.Aliases["db"]) != 1 {
		t.Errorf("aliases = %v, want the database's", cfg.Aliases)
	}
	// Policy is the registry's, not the overlay's. Reapplying the same seven
	// rows here would reinstate a set LoadConfig had reverted for a cross-key
	// rule failure, with nothing left to revalidate it.
	if cfg.Policy.Retry.MaxAttempts != 2 {
		t.Errorf("max_attempts = %d; the overlay must leave policy alone",
			cfg.Policy.Retry.MaxAttempts)
	}
	if len(cfg.Providers) != 1 || cfg.Log.Retention != 72*time.Hour {
		t.Error("the overlay touched a block that is not its own")
	}
}

func TestModelOverrideRoundTripsPerField(t *testing.T) {
	// The table is per-field: an override that sets capabilities must not
	// silently reset the context window someone else set.
	ctx := context.Background()
	db := migrated(t)
	seedProviderRow(t, db, "groq")

	win := 128000
	if err := db.PutModelOverride(ctx, ModelOverride{
		ProviderID: "groq", ModelID: "m", ContextWindow: &win,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutModelOverride(ctx, ModelOverride{
		ProviderID: "groq", ModelID: "m",
		Surfaces: []string{"llm"}, ContextWindow: &win,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.ModelOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d overrides, want 1", len(got))
	}
	if got[0].ContextWindow == nil || *got[0].ContextWindow != win {
		t.Errorf("context_window = %v, want %d", got[0].ContextWindow, win)
	}
	if len(got[0].Surfaces) != 1 || got[0].Surfaces[0] != "llm" {
		t.Errorf("surfaces = %v", got[0].Surfaces)
	}
}

func TestDeleteModelOverrideRemovesTheRow(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)
	seedProviderRow(t, db, "groq")

	win := 8192
	if err := db.PutModelOverride(ctx, ModelOverride{
		ProviderID: "groq", ModelID: "m", ContextWindow: &win,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteModelOverride(ctx, "groq", "m"); err != nil {
		t.Fatal(err)
	}
	got, err := db.ModelOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the override survived the delete: %+v", got)
	}
}

func seedProviderRow(t *testing.T, db *DB, id string) {
	t.Helper()
	if err := db.CreateProvider(context.Background(), ProviderRow{
		ID: id, Name: id, Kind: "openaicompat", BaseURL: "http://127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}
}
