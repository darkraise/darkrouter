package store

import (
	"context"
	"testing"
)

func TestClaimFirstUserSucceedsOnlyOnce(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	won, err := db.ClaimFirstUser(ctx, "id-1", "Alice", "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if !won {
		t.Fatal("the first claim on an empty database must win")
	}

	// A second claim loses even with a different name: the claim is for the
	// console, not for the username.
	won, err = db.ClaimFirstUser(ctx, "id-2", "bob", "hash-2")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Error("a second claim won; the console can only be claimed once")
	}

	n, err := db.UserCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("UserCount = %d, want 1", n)
	}
}

func TestClaimFirstUserIsAdmin(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "alice", "hash-1"); err != nil {
		t.Fatal(err)
	}
	u, ok, err := db.UserByID(ctx, "id-1")
	if err != nil || !ok {
		t.Fatalf("UserByID: %v ok=%v", err, ok)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", u.Role, RoleAdmin)
	}
}

func TestUserByUsernameIsCaseInsensitive(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "Alice", "hash-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alice", "alice", "ALICE", "aLiCe"} {
		u, ok, err := db.UserByUsername(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("%q did not match the stored account", name)
		}
		if u.ID != "id-1" {
			t.Errorf("%q matched %q, want id-1", name, u.ID)
		}
	}
	// The display spelling is preserved, not folded.
	u, _, _ := db.UserByUsername(ctx, "alice")
	if u.Username != "Alice" {
		t.Errorf("Username = %q, want the spelling as entered", u.Username)
	}
}

func TestUserByUsernameMissIsNotAnError(t *testing.T) {
	db := migrated(t)
	_, ok, err := db.UserByUsername(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("a miss must not be an error: %v", err)
	}
	if ok {
		t.Error("ok = true for an account that does not exist")
	}
}

func TestSetUserPasswordReplacesTheHash(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "alice", "old"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserPassword(ctx, "id-1", "new"); err != nil {
		t.Fatal(err)
	}
	u, _, _ := db.UserByID(ctx, "id-1")
	if u.PasswordHash != "new" {
		t.Errorf("hash = %q, want %q", u.PasswordHash, "new")
	}
}
