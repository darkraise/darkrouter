package server

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// The auth manager re-reads an account only when a write reports
// ErrCredentialChanged. A conflict that reached it as any other error would
// leave the account holding a pair the row no longer names.
func TestTokenStoreReportsARowThatMovedOnAsChanged(t *testing.T) {
	ctx := context.Background()
	db := storetest.Migrated(t)
	key, err := store.OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, priority, enabled, created_at)
		 VALUES ('p', 'openaicompat', 'https://p.example', 0, 1, 0)`); err != nil {
		t.Fatal(err)
	}
	id, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: "p", Kind: "oauth", Secret: "current", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ts := tokenStore{db: db, key: key}

	if err := ts.ReplaceCredentialSecret(ctx, id, "earlier", "next", nil); !errors.Is(err, auth.ErrCredentialChanged) {
		t.Errorf("replace over a different secret = %v, want ErrCredentialChanged", err)
	}
	if err := ts.DisableCredential(ctx, id, "earlier", "why"); !errors.Is(err, auth.ErrCredentialChanged) {
		t.Errorf("disable over a different secret = %v, want ErrCredentialChanged", err)
	}
	if _, err := ts.CredentialSecret(ctx, "gone"); !errors.Is(err, auth.ErrCredentialChanged) {
		t.Errorf("read of a deleted credential = %v, want ErrCredentialChanged", err)
	}
	if err := ts.ReplaceCredentialSecret(ctx, id, "current", "next", nil); err != nil {
		t.Errorf("replace over the current secret: %v", err)
	}
}
