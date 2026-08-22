package store

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/crypto"
)

// deriveForTest builds a key from the database's stored parameters without
// consulting the verifier. It is the only way to construct a "wrong key" that
// is still structurally valid.
func deriveForTest(ctx context.Context, d *DB, master string) (*crypto.Key, error) {
	saltHex, _, err := getSetting(ctx, d.Read, settingKDFSalt)
	if err != nil {
		return nil, err
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, err
	}
	itersRaw, _, err := getSetting(ctx, d.Read, settingKDFIterations)
	if err != nil {
		return nil, err
	}
	iterations, err := strconv.Atoi(itersRaw)
	if err != nil {
		return nil, err
	}
	return crypto.DeriveKey(master, salt, iterations)
}

func TestOpenKeyringInitializesOnFirstRun(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	k, err := OpenKeyring(ctx, db, "master-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if k == nil {
		t.Fatal("nil key")
	}
	salt, ok, err := getSetting(ctx, db.Read, settingKDFSalt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || salt == "" {
		t.Fatal("salt was not persisted")
	}
	iters, ok, err := getSetting(ctx, db.Read, settingKDFIterations)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || iters == "" {
		t.Fatal("iteration count was not persisted")
	}
}

func TestOpenKeyringAcceptsTheSameMasterKeyOnRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	k1, err := OpenKeyring(ctx, first, "master")
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := k1.Seal([]byte("sk-x"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	k2, err := OpenKeyring(ctx, second, "master")
	if err != nil {
		t.Fatal(err)
	}
	// The re-derived key must open what the first process sealed, or every
	// credential becomes unreadable across a restart.
	got, err := k2.Open(ct, nonce, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "sk-x" {
		t.Errorf("round trip across restart = %q", got)
	}
}

func TestOpenKeyringRejectsTheWrongMasterKey(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := OpenKeyring(ctx, db, "right"); err != nil {
		t.Fatal(err)
	}
	_, err := OpenKeyring(ctx, db, "wrong")
	if err == nil {
		t.Fatal("expected a wrong master key to be rejected")
	}
	if !strings.Contains(err.Error(), "DARKROUTER_MASTER_KEY") {
		t.Errorf("the message must name the variable to fix, got: %v", err)
	}
}

func TestOpenKeyringRequiresAMasterKey(t *testing.T) {
	db := migrated(t)
	_, err := OpenKeyring(context.Background(), db, "")
	if err == nil {
		t.Fatal("expected an empty master key to be rejected")
	}
	if !strings.Contains(err.Error(), "DARKROUTER_MASTER_KEY") {
		t.Errorf("the message must name the variable to set, got: %v", err)
	}
}

func TestOpenKeyringHonoursTheStoredIterationCount(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := OpenKeyring(ctx, db, "master"); err != nil {
		t.Fatal(err)
	}
	got, _, err := getSetting(ctx, db.Read, settingKDFIterations)
	if err != nil {
		t.Fatal(err)
	}
	// A database written with the default must keep using the default even if
	// the constant is raised in a later release.
	if got != "600000" {
		t.Errorf("iterations = %q, want 600000", got)
	}
}

func TestOpenKeyringReportsAnIncompleteKeyring(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := OpenKeyring(ctx, db, "master"); err != nil {
		t.Fatal(err)
	}
	// Simulate a partially written keyring: salt present, verifier gone.
	if _, err := db.Write.ExecContext(ctx,
		`DELETE FROM settings WHERE key = ?`, settingKDFVerifierCiphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenKeyring(ctx, db, "master"); err == nil {
		t.Fatal("expected an incomplete keyring to be reported rather than silently re-initialized")
	}
}
