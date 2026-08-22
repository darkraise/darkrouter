package store

import (
	"context"
	"errors"
	"testing"
)

func TestRotateReEncryptsEveryCredential(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	seededProvider(t, db, "other")
	if _, err := db.AddCredential(ctx, oldKey, Credential{ProviderID: "groq", Secret: "sk-a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, oldKey, Credential{ProviderID: "other", Secret: "sk-b", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	if err := RotateMasterKey(ctx, db, oldKey, "new-master"); err != nil {
		t.Fatal(err)
	}

	newKey, err := OpenKeyring(ctx, db, "new-master")
	if err != nil {
		t.Fatalf("new master key rejected after rotation: %v", err)
	}
	got, err := db.Credentials(ctx, newKey, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Secret != "sk-a" {
		t.Errorf("groq credential after rotation = %+v", got)
	}
	got, err = db.Credentials(ctx, newKey, "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Secret != "sk-b" {
		t.Errorf("other credential after rotation = %+v", got)
	}

	if _, err := OpenKeyring(ctx, db, "old-master"); err == nil {
		t.Error("the old master key still opens the database after rotation")
	}
}

func TestInterruptedRotationRollsBack(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	for _, s := range []string{"sk-a", "sk-b", "sk-c"} {
		if _, err := db.AddCredential(ctx, oldKey, Credential{ProviderID: "groq", Secret: s, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}

	boom := errors.New("simulated crash")
	err = rotateWithHook(ctx, db, oldKey, "new-master", func(i int) error {
		if i == 1 { // fail after the second credential is re-sealed
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected the injected failure, got %v", err)
	}

	// Nothing may have changed: the old key must still open the keyring and
	// all three credentials, or a crash would leave them unrecoverable.
	if _, err := OpenKeyring(ctx, db, "old-master"); err != nil {
		t.Fatalf("old master key no longer works after a rolled-back rotation: %v", err)
	}
	got, err := db.Credentials(ctx, oldKey, "groq")
	if err != nil {
		t.Fatalf("credentials unreadable after a rolled-back rotation: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d credentials, want 3", len(got))
	}
	if _, err := OpenKeyring(ctx, db, "new-master"); err == nil {
		t.Error("the new master key works despite the rotation being rolled back")
	}
}

func TestRotateRejectsAnEmptyNewKey(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	if err := RotateMasterKey(ctx, db, oldKey, ""); err == nil {
		t.Fatal("expected an empty new master key to be rejected")
	}
}

func TestRotateWithNoCredentialsStillRewritesTheVerifier(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	if err := RotateMasterKey(ctx, db, oldKey, "new-master"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenKeyring(ctx, db, "new-master"); err != nil {
		t.Errorf("verifier was not rotated on an empty credential set: %v", err)
	}
}
