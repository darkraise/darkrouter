//go:build unix

package store

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Lock takes the exclusive process lock on the database at dbPath, without
// waiting, and returns its release. It fails with ErrDatabaseInUse while
// another process holds it.
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
	// Read-only and world-readable: flock needs no write access, and a
	// command run as a different uid than the gateway must still be able to
	// open the file to be refused rather than fail for an unrelated reason.
	f, err := os.OpenFile(path, os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("%s: %w", dbPath, ErrDatabaseInUse)
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f.Close, nil
}
