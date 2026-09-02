package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

func TestPutConfigWritesBothBlocksOrNeither(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	trip := 3
	policy := config.PolicyConfig{
		Cooldown: config.CooldownConfig{TripAfter: &trip, Max: time.Minute},
		Retry:    config.RetryConfig{MaxAttempts: 2},
	}
	if err := db.PutConfig(ctx, map[string][]string{"fast": {"a/m"}}, &policy); err != nil {
		t.Fatal(err)
	}
	aliases, err := db.Aliases(ctx)
	if err != nil || len(aliases["fast"]) != 1 {
		t.Fatalf("aliases = %v, %v", aliases, err)
	}
	overrides, err := db.PolicyOverrides(ctx)
	if err != nil || overrides["policy.retry.max_attempts"] != "2" {
		t.Fatalf("overrides = %v, %v", overrides, err)
	}
	// A nil block leaves what is there.
	if err := db.PutConfig(ctx, nil, &config.PolicyConfig{Retry: config.RetryConfig{MaxAttempts: 5}}); err != nil {
		t.Fatal(err)
	}
	aliases, _ = db.Aliases(ctx)
	overrides, _ = db.PolicyOverrides(ctx)
	if len(aliases["fast"]) != 1 || overrides["policy.retry.max_attempts"] != "5" {
		t.Errorf("after a policy-only write: aliases %v, overrides %v", aliases, overrides)
	}
}

func TestDeleteModelOverrideReportsAMiss(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.DeleteModelOverride(ctx, "p", "m"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete of a missing override err = %v, want ErrNotFound", err)
	}
}

func TestInitSettingKeepsTheFirstValue(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if got, err := db.InitSetting(ctx, "k", "first"); err != nil || got != "first" {
		t.Fatalf("InitSetting = %q, %v", got, err)
	}
	if got, err := db.InitSetting(ctx, "k", "second"); err != nil || got != "first" {
		t.Fatalf("second InitSetting = %q, %v, want the first value", got, err)
	}
}

func TestRequestTraceJoinsTheCredentialLabel(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, ProviderRow{ID: "b", Kind: "openaicompat", BaseURL: "https://x", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{ID: "k2", ProviderID: "b", Label: "primary", Secret: "s", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	db.SeedFailoverTraceForTest(t, "01LABEL")
	tr, ok, err := db.RequestTrace(ctx, "01LABEL")
	if err != nil || !ok {
		t.Fatalf("trace = %v, %v", ok, err)
	}
	if tr.Attempts[1].KeyLabel != "primary" {
		t.Errorf("attempt 2 label = %q, want primary", tr.Attempts[1].KeyLabel)
	}
	if tr.Attempts[0].KeyLabel != "" || tr.Attempts[0].KeyID != "k1" {
		t.Errorf("attempt 1 with a deleted key = %+v, want empty label and the id", tr.Attempts[0])
	}
}
