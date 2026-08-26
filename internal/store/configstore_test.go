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
