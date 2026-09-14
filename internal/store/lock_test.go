//go:build unix

package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// A gateway keeps the master key it started with and seals every later
// credential write under it. A rotation run beside it leaves those rows
// sealed under a key the next start no longer has, so the lock the gateway
// holds has to turn a second process away rather than merely exist.
func TestASecondLockOnADatabaseIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}

	if again, err := Lock(path); !errors.Is(err, ErrDatabaseInUse) {
		if again != nil {
			_ = again()
		}
		t.Fatalf("second Lock = %v, want ErrDatabaseInUse", err)
	}

	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	after, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	_ = after()
}
