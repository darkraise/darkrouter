//go:build !unix

package store

import "fmt"

// Lock has no advisory lock to take on this platform, so neither the gateway
// nor rotate-key can tell that the other is running. It reports that as a
// filesystem without lock support does: the gateway warns and runs, and
// rotate-key refuses unless told the gateway is stopped.
func Lock(dbPath string) (unlock func() error, err error) {
	return nil, fmt.Errorf("lock %s: no advisory lock on this platform: %w", lockPath(dbPath), ErrLockUnavailable)
}
