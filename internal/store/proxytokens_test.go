package store

import (
	"context"
	"strings"
	"testing"
)

func TestProxyTokenIsReturnedOnceAndStoredHashed(t *testing.T) {
	// A token readable from the database is one a backup leaks.
	ctx := context.Background()
	db := migrated(t)

	tok, err := db.CreateProxyToken(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if tok.Secret == "" {
		t.Fatal("creation did not return the secret")
	}

	var stored string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT hash FROM proxy_tokens WHERE id = ?`, tok.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, tok.Secret) || stored == tok.Secret {
		t.Error("the plaintext token is in the database")
	}

	listed, err := db.ProxyTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed %d tokens", len(listed))
	}
	if listed[0].Secret != "" {
		t.Error("listing reproduced the secret")
	}
	if listed[0].Prefix == "" || strings.HasPrefix(tok.Secret, listed[0].Prefix) == false {
		t.Errorf("prefix %q does not identify the token", listed[0].Prefix)
	}
}

func TestProxyTokenValidatesThenRevokes(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)

	tok, err := db.CreateProxyToken(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := db.ProxyTokenValid(ctx, tok.Secret)
	if err != nil || !ok {
		t.Fatalf("a fresh token did not validate: ok=%v err=%v", ok, err)
	}
	if ok, _ := db.ProxyTokenValid(ctx, "dr_wrong"); ok {
		t.Error("a wrong token validated")
	}

	removed, err := db.DeleteProxyToken(ctx, tok.ID)
	if err != nil || !removed {
		t.Fatalf("delete reported removed=%v err=%v", removed, err)
	}
	if ok, _ := db.ProxyTokenValid(ctx, tok.Secret); ok {
		t.Error("a revoked token still validates")
	}
	if removed, _ := db.DeleteProxyToken(ctx, "nosuch"); removed {
		t.Error("deleting an unknown id reported a removal")
	}
}

func TestProxyTokenRecordsItsUse(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)
	tok, err := db.CreateProxyToken(ctx, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ProxyTokenValid(ctx, tok.Secret); err != nil {
		t.Fatal(err)
	}
	listed, err := db.ProxyTokens(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if listed[0].LastUsedAt == nil {
		t.Error("last_used_at is unset after a successful check")
	}
}
