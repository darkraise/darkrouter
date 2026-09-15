//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// withStdin feeds input to a command that reads os.Stdin.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = prev
		_ = r.Close()
	})
}

// credentialDB creates a database at path holding one credential sealed under
// master, and returns the credential's id.
func credentialDB(t *testing.T, path, master string) string {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, master)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, priority, enabled, created_at)
		 VALUES ('p', 'openaicompat', 'https://p.example', 0, 1, 0)`); err != nil {
		t.Fatal(err)
	}
	id, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: "p", Secret: "sk-rotate-me", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A running gateway keeps the key it started with and seals later credential
// writes under it, so a rotation beside it leaves rows no single key opens
// after the restart. The gateway's lock is what turns the command away.
func TestRotateKeyRefusesWhileTheGatewayHoldsTheDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	credentialDB(t, path, "old-master")
	unlock, err := store.Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	t.Setenv("DARKROUTER_MASTER_KEY", "old-master")
	withStdin(t, "new-master\n")
	err = runRotateKey([]string{"-db", path})
	if !errors.Is(err, store.ErrDatabaseInUse) {
		t.Fatalf("rotate-key beside a running gateway = %v, want ErrDatabaseInUse", err)
	}

	ctx := context.Background()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := store.OpenKeyring(ctx, db, "old-master"); err != nil {
		t.Errorf("the refused rotation changed the key anyway: %v", err)
	}
}

func TestRotateKeyRotatesAStoppedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	id := credentialDB(t, path, "old-master")

	t.Setenv("DARKROUTER_MASTER_KEY", "old-master")
	withStdin(t, "new-master\n")
	if err := runRotateKey([]string{"-db", path}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := store.OpenKeyring(ctx, db, "new-master")
	if err != nil {
		t.Fatalf("the new key does not open the rotated database: %v", err)
	}
	if secret, err := db.CredentialSecret(ctx, key, id); err != nil || secret != "sk-rotate-me" {
		t.Errorf("credential after rotation = %q, %v", secret, err)
	}

	// Released on the way out, or the gateway could not start afterwards.
	unlock, err := store.Lock(path)
	if err != nil {
		t.Fatalf("rotate-key left the database locked: %v", err)
	}
	_ = unlock()
}

// lockUnavailable makes every lock fail as a mount without lock support does.
func lockUnavailable(t *testing.T) {
	t.Helper()
	prev := lockDatabase
	lockDatabase = func(string) (func() error, error) {
		return nil, fmt.Errorf("lock: %w", store.ErrLockUnavailable)
	}
	t.Cleanup(func() { lockDatabase = prev })
}

// Where the lock cannot be taken, rotate-key cannot tell whether a gateway is
// running, and a rotation beside one corrupts credentials. It refuses unless
// the operator says the gateway is stopped.
func TestRotateKeyRefusesWhenTheDatabaseCannotBeLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	credentialDB(t, path, "old-master")
	lockUnavailable(t)

	t.Setenv("DARKROUTER_MASTER_KEY", "old-master")
	withStdin(t, "new-master\n")
	err := runRotateKey([]string{"-db", path})
	if !errors.Is(err, store.ErrLockUnavailable) || !strings.Contains(err.Error(), "-gateway-stopped") {
		t.Fatalf("rotate-key with no lock available = %v, want a refusal naming -gateway-stopped", err)
	}

	ctx := context.Background()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := store.OpenKeyring(ctx, db, "old-master"); err != nil {
		t.Errorf("the refused rotation changed the key anyway: %v", err)
	}
}

func TestRotateKeyRunsUnlockedOnceTheGatewayIsConfirmedStopped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	credentialDB(t, path, "old-master")
	lockUnavailable(t)

	t.Setenv("DARKROUTER_MASTER_KEY", "old-master")
	withStdin(t, "new-master\n")
	if err := runRotateKey([]string{"-db", path, "-gateway-stopped"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := store.OpenKeyring(ctx, db, "new-master"); err != nil {
		t.Errorf("the new key does not open the rotated database: %v", err)
	}
}

// The flag stands in for a lock that cannot be taken, never for one another
// process holds: that is proof the gateway is running.
func TestGatewayStoppedDoesNotOverrideAHeldLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	credentialDB(t, path, "old-master")
	unlock, err := store.Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	t.Setenv("DARKROUTER_MASTER_KEY", "old-master")
	withStdin(t, "new-master\n")
	if err := runRotateKey([]string{"-db", path, "-gateway-stopped"}); !errors.Is(err, store.ErrDatabaseInUse) {
		t.Fatalf("rotate-key -gateway-stopped beside a held lock = %v, want ErrDatabaseInUse", err)
	}
}

// Refusing to start on a mount without lock support would take the gateway
// down for a check that cannot be made; rotate-key refuses on its own there.
func TestTheGatewayStartsWhenTheDatabaseCannotBeLocked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	lockUnavailable(t)

	// No key, so a gateway that gets past the lock stops at the keyring.
	t.Setenv("DARKROUTER_MASTER_KEY", "")
	done := make(chan error, 1)
	go func() { done <- runServer([]string{"-db", path}) }()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, store.ErrLockUnavailable) ||
			!strings.Contains(err.Error(), "DARKROUTER_MASTER_KEY") {
			t.Fatalf("runServer with no lock available = %v, want it to reach the keyring", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runServer did not return")
	}
}

// The gateway takes the same lock for its lifetime, which is what makes the
// refusal above mean anything, and a second gateway on one database is turned
// away for the same reason.
func TestTheGatewayRefusesADatabaseAnotherProcessHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	unlock, err := store.Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	// No key: were the lock not checked first, startup would stop at the
	// keyring rather than go on to bind the listeners.
	t.Setenv("DARKROUTER_MASTER_KEY", "")
	done := make(chan error, 1)
	go func() { done <- runServer([]string{"-db", path}) }()
	select {
	case err := <-done:
		if !errors.Is(err, store.ErrDatabaseInUse) {
			t.Fatalf("runServer on a held database = %v, want ErrDatabaseInUse", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the gateway started on a database another process holds")
	}
}
