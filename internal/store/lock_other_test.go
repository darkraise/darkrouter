//go:build !unix

package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// With no lock to take, a gateway cannot be detected, and rotate-key has to
// hear that rather than a success that lets it rotate beside one.
func TestLockReportsThatThisPlatformCannotLock(t *testing.T) {
	unlock, err := Lock(filepath.Join(t.TempDir(), "darkrouter.db"))
	if !errors.Is(err, ErrLockUnavailable) || errors.Is(err, ErrDatabaseInUse) {
		if unlock != nil {
			_ = unlock()
		}
		t.Fatalf("Lock = %v, want ErrLockUnavailable", err)
	}
}
