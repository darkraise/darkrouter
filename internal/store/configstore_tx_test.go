package store

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkrouter/internal/config"
)

// Both blocks or neither. The write path is where this lives now; the check
// stays because a half-applied save is the failure, not the function that
// used to make it.
func TestWriteConfigWritesBothBlocksOrNeither(t *testing.T) {
	db, ctx := migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Aliases: map[string][]string{"fast": {"groq/llama"}},
		Set:     map[string]string{"policy.retry.max_attempts": "20"},
	})
	if err == nil {
		t.Fatal("WriteConfig accepted a retry count past the cap")
	}
	aliases, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Errorf("the alias half of a refused write landed: %v", aliases)
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
