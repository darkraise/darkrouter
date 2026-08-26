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

func TestPolicyOverridesRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)

	trip := 5
	want := config.PolicyConfig{
		Cooldown: config.CooldownConfig{TripAfter: &trip, Max: 20 * time.Minute},
		Retry:    config.RetryConfig{MaxAttempts: 4},
		Timeout: config.TimeoutConfig{
			Connect:   3 * time.Second,
			FirstByte: 45 * time.Second,
			Total:     9 * time.Minute,
			Idle:      30 * time.Second,
		},
	}
	if err := db.PutPolicy(ctx, want); err != nil {
		t.Fatal(err)
	}

	var got config.PolicyConfig
	overrides, err := db.PolicyOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyPolicy(&got, overrides); err != nil {
		t.Fatal(err)
	}
	if got.Retry.MaxAttempts != want.Retry.MaxAttempts {
		t.Errorf("max_attempts = %d, want %d", got.Retry.MaxAttempts, want.Retry.MaxAttempts)
	}
	if got.Timeout != want.Timeout {
		t.Errorf("timeout = %+v, want %+v", got.Timeout, want.Timeout)
	}
	if got.Cooldown.Max != want.Cooldown.Max {
		t.Errorf("cooldown.max = %v, want %v", got.Cooldown.Max, want.Cooldown.Max)
	}
	if got.Cooldown.TripAfter == nil || *got.Cooldown.TripAfter != trip {
		t.Errorf("trip_after = %v, want %d", got.Cooldown.TripAfter, trip)
	}
}

func TestPolicyOverridesReportOnlyWhatIsSet(t *testing.T) {
	// A caller has to tell "not overridden" from "set to zero", or the config
	// screen cannot say which values came from the database.
	ctx := context.Background()
	db := migrated(t)

	got, err := db.PolicyOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("fresh database reports %d overrides: %v", len(got), got)
	}
}

func TestApplyPolicyLeavesUnsetFieldsAlone(t *testing.T) {
	// A half-populated table must not zero a timeout the file supplied.
	loaded := config.PolicyConfig{
		Retry:   config.RetryConfig{MaxAttempts: 3},
		Timeout: config.TimeoutConfig{Connect: 10 * time.Second, Total: 5 * time.Minute},
	}
	if err := ApplyPolicy(&loaded, map[string]string{
		"policy.retry.max_attempts": "7",
	}); err != nil {
		t.Fatal(err)
	}
	if loaded.Retry.MaxAttempts != 7 {
		t.Errorf("max_attempts = %d, want 7", loaded.Retry.MaxAttempts)
	}
	if loaded.Timeout.Connect != 10*time.Second {
		t.Errorf("connect = %v, want it untouched", loaded.Timeout.Connect)
	}
	if loaded.Timeout.Total != 5*time.Minute {
		t.Errorf("total = %v, want it untouched", loaded.Timeout.Total)
	}
}

func TestApplyPolicyRejectsAnUnparseableValue(t *testing.T) {
	var p config.PolicyConfig
	if err := ApplyPolicy(&p, map[string]string{"policy.timeout.connect": "soon"}); err == nil {
		t.Fatal("expected an error for a value that is not a duration")
	}
}

func TestImportConfigOnceTakesTheYamlThenStopsTaking(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)

	trip := 3
	first := &config.Config{
		Aliases: map[string][]string{"fast": {"groq/a", "groq/b"}},
		Policy: config.PolicyConfig{
			Retry:    config.RetryConfig{MaxAttempts: 2},
			Cooldown: config.CooldownConfig{TripAfter: &trip},
		},
	}
	res, err := ImportConfigOnce(ctx, db, first)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Imported || res.Aliases != 1 {
		t.Fatalf("first import took %+v", res)
	}

	// Different YAML, same database: the file has stopped being authoritative.
	second := &config.Config{
		Aliases: map[string][]string{"other": {"openai/c"}},
		Policy:  config.PolicyConfig{Retry: config.RetryConfig{MaxAttempts: 9}},
	}
	res, err = ImportConfigOnce(ctx, db, second)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatalf("second import took %+v; the database is authoritative", res)
	}

	got, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["fast"]) != 2 || len(got["other"]) != 0 {
		t.Errorf("aliases = %v, want only the first import", got)
	}
}

func TestImportConfigOnceDoesNotReimportAnEmptiedSet(t *testing.T) {
	// An operator who deleted every alias through the console must not have
	// the file's silently reimported on the next restart.
	ctx := context.Background()
	db := migrated(t)
	cfg := &config.Config{Aliases: map[string][]string{"fast": {"groq/a"}}}

	if _, err := ImportConfigOnce(ctx, db, cfg); err != nil {
		t.Fatal(err)
	}
	if err := db.PutAliases(ctx, map[string][]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportConfigOnce(ctx, db, cfg); err != nil {
		t.Fatal(err)
	}

	got, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("an emptied set was reimported: %v", got)
	}
}

func TestOverlayConfigReplacesAliasesAndPolicyOnly(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)
	if err := db.PutAliases(ctx, map[string][]string{"db": {"groq/x"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.PutPolicy(ctx, config.PolicyConfig{
		Retry: config.RetryConfig{MaxAttempts: 6},
	}); err != nil {
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
	if cfg.Policy.Retry.MaxAttempts != 6 {
		t.Errorf("max_attempts = %d, want 6", cfg.Policy.Retry.MaxAttempts)
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
