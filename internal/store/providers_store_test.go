package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestProviderByIDReadsOneRowOrErrNotFound(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, ProviderRow{ID: "a", Kind: "openaicompat", BaseURL: "https://a", Priority: 3, Enabled: true, FreeModelsOnly: true}); err != nil {
		t.Fatal(err)
	}
	p, err := db.ProviderByID(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "a" || p.Priority != 3 || !p.Enabled || !p.FreeModelsOnly || p.AuthStyle != "bearer" {
		t.Errorf("ProviderByID = %+v", p)
	}
	if _, err := db.ProviderByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing provider err = %v, want ErrNotFound", err)
	}
}

func TestProviderMutationsReportSentinels(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	row := ProviderRow{ID: "dup", Kind: "openaicompat", BaseURL: "https://x", Enabled: true}
	if err := db.CreateProvider(ctx, row); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, row); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate create err = %v, want ErrConflict", err)
	}
	name := "n"
	if err := db.UpdateProvider(ctx, "missing", ProviderPatch{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update missing err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteProvider(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing err = %v, want ErrNotFound", err)
	}
	if err := db.DeleteCredential(ctx, "dup", "missing"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete missing credential err = %v, want ErrNotFound", err)
	}
}

func TestCredentialSummariesNeedNoKey(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := db.CreateProvider(ctx, ProviderRow{ID: id, Kind: "openaicompat", BaseURL: "https://x", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	exp := time.Now().Add(time.Hour).Unix()
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "a", Label: "one", Secret: "s1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "a", Label: "two", Secret: "s2", Kind: "oauth", Scope: "sc", ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	got, err := db.CredentialSummaries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["a"]) != 2 || len(got["b"]) != 0 {
		t.Fatalf("summaries = %+v", got)
	}
	var sawOAuth bool
	for _, c := range got["a"] {
		if c.Kind == "oauth" {
			sawOAuth = true
			if c.Enabled || c.Scope != "sc" || c.ExpiresAt == nil || *c.ExpiresAt != exp || c.Label != "two" {
				t.Errorf("oauth summary = %+v", c)
			}
		}
	}
	if !sawOAuth {
		t.Error("the oauth credential was not summarised")
	}
}

func TestReplaceProviderCredentialSecretIsScoped(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b"} {
		if err := db.CreateProvider(ctx, ProviderRow{ID: id, Kind: "openaicompat", BaseURL: "https://x", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	credID, err := db.AddCredential(ctx, key, Credential{ProviderID: "a", Secret: "old", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	err = db.ReplaceProviderCredentialSecret(ctx, key, "b", credID, "new", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("replace under the wrong provider err = %v, want ErrNotFound", err)
	}
	if err := db.ReplaceProviderCredentialSecret(ctx, key, "a", credID, "new", nil); err != nil {
		t.Fatal(err)
	}
	creds, err := db.Credentials(ctx, key, "a")
	if err != nil {
		t.Fatal(err)
	}
	if creds[0].Secret != "new" {
		t.Errorf("secret = %q after a scoped replace", creds[0].Secret)
	}
}

func TestSettingsAccessors(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, ok, err := db.GetSetting(ctx, "k"); err != nil || ok {
		t.Fatalf("GetSetting before write = %v, %v", ok, err)
	}
	if err := db.PutSetting(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := db.PutSetting(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	v, ok, err := db.GetSetting(ctx, "k")
	if err != nil || !ok || v != "v2" {
		t.Fatalf("GetSetting = %q, %v, %v", v, ok, err)
	}
	if err := db.DeleteSetting(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.GetSetting(ctx, "k"); ok {
		t.Error("the setting survived DeleteSetting")
	}
}
