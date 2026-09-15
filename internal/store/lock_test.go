//go:build unix

package store

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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

func withFlock(t *testing.T, f func(fd int, how int) error) {
	t.Helper()
	prev := flock
	flock = f
	t.Cleanup(func() { flock = prev })
}

// Linux emulates flock on NFS with a byte-range lock, and an exclusive
// byte-range lock needs a descriptor open for writing: on a read-only one it
// fails with EBADF, so a data directory on NFS could not be locked at all.
func TestLockOpensTheFileForWritingAsNFSRequires(t *testing.T) {
	withFlock(t, func(fd, how int) error {
		mode, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
		if errno != 0 {
			return errno
		}
		if mode&syscall.O_ACCMODE == syscall.O_RDONLY {
			return syscall.EBADF
		}
		return syscall.Flock(fd, how)
	})
	unlock, err := Lock(filepath.Join(t.TempDir(), "darkrouter.db"))
	if err != nil {
		t.Fatalf("Lock on a filesystem that needs a writable descriptor = %v", err)
	}
	_ = unlock()
}

// A mount without lock support is not another process holding the database,
// and the caller has to be able to tell the two apart: one is safe to run
// beside, the other says nothing either way.
func TestLockReportsAFilesystemWithoutLockSupport(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOLCK} {
		t.Run(errno.Error(), func(t *testing.T) {
			withFlock(t, func(int, int) error { return errno })
			unlock, err := Lock(filepath.Join(t.TempDir(), "darkrouter.db"))
			if !errors.Is(err, ErrLockUnavailable) || errors.Is(err, ErrDatabaseInUse) {
				if unlock != nil {
					_ = unlock()
				}
				t.Fatalf("Lock = %v, want ErrLockUnavailable", err)
			}
		})
	}
}

// A command run as a different uid than the gateway cannot open the gateway's
// lock file for writing, and must still take the lock read-only so that it is
// refused for the right reason. Root bypasses file permissions, so this only
// means something run as another user.
func TestLockFallsBackToReadOnlyWhenTheFileIsNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens a read-only file for writing regardless of its mode")
	}
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	if err := os.WriteFile(lockPath(path), nil, 0o444); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock on a read-only lock file = %v", err)
	}
	_ = unlock()
}
