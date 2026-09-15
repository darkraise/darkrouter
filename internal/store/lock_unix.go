//go:build unix

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

var flock = syscall.Flock

// Lock takes the exclusive process lock on the database at dbPath, without
// waiting, and returns its release. It fails with ErrDatabaseInUse while
// another process holds it, and with ErrLockUnavailable on a filesystem that
// cannot take it at all, where the caller decides whether running unlocked is
// safe.
//
// The gateway holds it for its whole run, and rotate-key for its own. A
// running gateway keeps the key it derived at startup and seals every later
// credential write under it, so a rotation beside it would leave rows sealed
// under a key the next start no longer has.
//
// An flock on a file in the data directory, rather than anything inside
// SQLite, because it has to hold across processes that share nothing but that
// directory: a `docker exec` into the gateway's container, or a second
// container with the same volume mounted. The kernel drops it when the holder
// exits, however it exits.
func Lock(dbPath string) (unlock func() error, err error) {
	path := lockPath(dbPath)
	// Read-write, because Linux emulates flock on NFS with a byte-range lock,
	// and an exclusive one fails with EBADF on a read-only descriptor. A
	// command run as a different uid than the gateway may not be able to open
	// the file for writing, and falls back to read-only so that a local lock
	// still refuses it for the right reason rather than an unrelated one.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if errors.Is(err, os.ErrPermission) {
		f, err = os.OpenFile(path, os.O_RDONLY, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		switch {
		case errors.Is(err, syscall.EWOULDBLOCK):
			return nil, fmt.Errorf("%s: %w", dbPath, ErrDatabaseInUse)
		case errors.Is(err, syscall.ENOTSUP), errors.Is(err, syscall.EOPNOTSUPP),
			errors.Is(err, syscall.ENOLCK):
			return nil, fmt.Errorf("lock %s: %w: %w", path, ErrLockUnavailable, err)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f.Close, nil
}
