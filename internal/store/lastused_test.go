package store

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

func TestLastUsedRoundTrip(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	id1, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "a", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "b", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	older := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	newer := time.Now().UTC().Truncate(time.Millisecond)
	if err := db.SaveLastUsed(ctx, map[health.CredKey]time.Time{
		{ProviderID: "groq", KeyID: id1}: older,
		{ProviderID: "groq", KeyID: id2}: newer,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.LoadLastUsed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if !got[health.CredKey{ProviderID: "groq", KeyID: id1}].Equal(older) {
		t.Errorf("id1 = %s, want %s", got[health.CredKey{ProviderID: "groq", KeyID: id1}], older)
	}
	// The ordering is the whole point; assert it survived rather than just the
	// individual values.
	a := got[health.CredKey{ProviderID: "groq", KeyID: id1}]
	b := got[health.CredKey{ProviderID: "groq", KeyID: id2}]
	if !a.Before(b) {
		t.Errorf("ordering lost: %s is not before %s", a, b)
	}
}

func TestLoadLastUsedSkipsNeverUsedCredentials(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")
	seededProvider(t, db, "groq")
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "a", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	got, err := db.LoadLastUsed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// A NULL last_used_at means never used. Returning it as the zero time would
	// be correct but noisy; absence says the same thing and keeps the map small.
	if len(got) != 0 {
		t.Fatalf("got %v, want an empty map", got)
	}
}

// A credential deleted between the snapshot and the save must not be recreated.
func TestSaveLastUsedIgnoresUnknownCredentials(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.SaveLastUsed(ctx, map[health.CredKey]time.Time{
		{ProviderID: "gone", KeyID: "deleted"}: time.Now(),
	}); err != nil {
		t.Fatalf("saving a vanished credential must be a no-op, not an error: %v", err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM provider_keys`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("provider_keys = %d, want 0 — a deleted credential must not be resurrected", n)
	}
}

func TestSaveLastUsedWithNothingIsANoOp(t *testing.T) {
	db := migrated(t)
	if err := db.SaveLastUsed(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}
