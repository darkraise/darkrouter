package store

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func seededProvider(t *testing.T, db *DB, id string) {
	t.Helper()
	_, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES (?, 'openaicompat', 'https://x', 0)`, id)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")

	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "groq", Label: "primary", Secret: "sk-secret-value", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("no id returned")
	}

	got, err := db.Credentials(ctx, key, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d credentials, want 1", len(got))
	}
	if got[0].Secret != "sk-secret-value" {
		t.Errorf("secret = %q", got[0].Secret)
	}
	if got[0].ID != id || got[0].Label != "primary" || !got[0].Enabled {
		t.Errorf("credential = %+v", got[0])
	}
}

// A done criterion: credentials must be unreadable in the raw database file.
func TestCredentialIsNotReadableInTheDatabaseFile(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	const secret = "sk-plaintext-must-not-appear"
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "groq", Secret: secret, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Checkpoint so the row is in the main database file rather than only the WAL.
	if _, err := db.Write.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(db.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the plaintext secret appears in the database file")
	}
}

// A done criterion: a swapped ciphertext must fail to decrypt.
func TestSwappedCiphertextFailsToDecrypt(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	idA, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "aaa", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	idB, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "bbb", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// Move A's sealed bytes into B's row. Both are valid ciphertexts under the
	// same key, so only the AAD binding can catch this.
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE provider_keys SET ciphertext = (SELECT ciphertext FROM provider_keys WHERE id = ?),
		                          nonce      = (SELECT nonce FROM provider_keys WHERE id = ?)
		 WHERE id = ?`, idA, idA, idB); err != nil {
		t.Fatal(err)
	}

	_, err = db.Credentials(ctx, key, "groq")
	if err == nil {
		t.Fatal("expected a swapped ciphertext to be rejected")
	}
	if !strings.Contains(err.Error(), idB) {
		t.Errorf("the error should name the offending row, got: %v", err)
	}
}

func TestCredentialsFailsUnderTheWrongKey(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "sk", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	other, err := deriveForTest(ctx, db, "different-master")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credentials(ctx, other, "groq"); err == nil {
		t.Fatal("expected decryption under a foreign key to fail")
	}
}

func TestCredentialsAreOrderedAndScopedToTheProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	seededProvider(t, db, "other")
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "other", Secret: "b", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Credentials(ctx, key, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Secret != "a" {
		t.Fatalf("expected only groq's credential, got %+v", got)
	}
}
