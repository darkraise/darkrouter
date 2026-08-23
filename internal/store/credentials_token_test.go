package store

import (
	"context"
	"testing"

	"github.com/darkraise/darkrouter/internal/crypto"
)

func credentialFixture(t *testing.T) (*DB, *crypto.Key) {
	t.Helper()
	ctx := context.Background()
	db := migrated(t)
	key, err := OpenKeyring(ctx, db, "test-master-key")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "p")
	return db, key
}

func TestReplaceCredentialSecretIsAtomic(t *testing.T) {
	ctx := context.Background()
	db, key := credentialFixture(t)

	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Label: "sub", Kind: "oauth",
		Secret: `{"access_token":"old"}`, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	exp := int64(1800000000)
	if err := db.ReplaceCredentialSecret(ctx, key, id, `{"access_token":"new"}`, &exp); err != nil {
		t.Fatal(err)
	}

	creds, err := db.Credentials(ctx, key, "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 {
		t.Fatalf("got %d credentials, want 1", len(creds))
	}
	if creds[0].Secret != `{"access_token":"new"}` {
		t.Errorf("secret = %q, want the replacement", creds[0].Secret)
	}
	// The AAD is the credential id, so a replacement that changed the id would
	// silently produce a row nothing can decrypt.
	if creds[0].ID != id {
		t.Errorf("id changed to %q", creds[0].ID)
	}
	if creds[0].ExpiresAt == nil || *creds[0].ExpiresAt != exp {
		t.Errorf("expires_at = %v, want %d", creds[0].ExpiresAt, exp)
	}
}

func TestReplaceCredentialSecretRefusesAMissingRow(t *testing.T) {
	// A credential deleted while a refresh was in flight. Silently succeeding
	// would leave the worker believing it persisted a token that does not exist.
	ctx := context.Background()
	db, key := credentialFixture(t)
	if err := db.ReplaceCredentialSecret(ctx, key, "no-such-id", "x", nil); err == nil {
		t.Fatal("replacing a missing credential must be an error")
	}
}

func TestExpiringCredentialsFindsOnlyItsKind(t *testing.T) {
	ctx := context.Background()
	db, key := credentialFixture(t)

	soon, late := int64(100), int64(9000)
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "oauth", Secret: "a", Enabled: true, ExpiresAt: &soon}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "oauth", Secret: "b", Enabled: true, ExpiresAt: &late}); err != nil {
		t.Fatal(err)
	}
	// A static key has no expiry and must never be handed to a refresh worker.
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "static", Secret: "c", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	got, err := db.ExpiringCredentials(ctx, key, "oauth", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1: %+v", len(got), got)
	}
	if got[0].Secret != "a" {
		t.Errorf("returned the wrong row: %q", got[0].Secret)
	}
}

func TestExpiringCredentialsSkipsDisabledRows(t *testing.T) {
	// A credential already disabled pending reconnection must not be retried
	// by the worker: spec §5.2's "no retries" applies across ticks too.
	ctx := context.Background()
	db, key := credentialFixture(t)
	soon := int64(100)
	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "oauth", Secret: "a", Enabled: true, ExpiresAt: &soon})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DisableCredential(ctx, id, "reconnect required"); err != nil {
		t.Fatal(err)
	}
	got, err := db.ExpiringCredentials(ctx, key, "oauth", 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a disabled credential was returned: %+v", got)
	}
}

func TestDisableCredentialRecordsWhy(t *testing.T) {
	ctx := context.Background()
	db, key := credentialFixture(t)
	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p", Kind: "oauth", Secret: "a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DisableCredential(ctx, id, "reconnection required"); err != nil {
		t.Fatal(err)
	}
	creds, err := db.Credentials(ctx, key, "p")
	if err != nil {
		t.Fatal(err)
	}
	if creds[0].Enabled {
		t.Error("the credential is still enabled")
	}
	if creds[0].Scope != "reconnection required" {
		t.Errorf("scope = %q; the reason was not recorded", creds[0].Scope)
	}
}
