//go:build unix

package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
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
	withNoLockBackoff(t)
	// ENOSYS is what FUSE and 9p mounts answer.
	for _, errno := range []syscall.Errno{syscall.ENOTSUP, syscall.EOPNOTSUPP, syscall.ENOLCK, syscall.ENOSYS} {
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

func withNoLockBackoff(t *testing.T) {
	t.Helper()
	prev := noLockBackoff
	noLockBackoff = make([]time.Duration, len(prev))
	t.Cleanup(func() { noLockBackoff = prev })
}

// The same fallback as above, reached as root by refusing the writable open.
func withWriteOpenRefused(t *testing.T) {
	t.Helper()
	prev := openLockFile
	openLockFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if flag&syscall.O_ACCMODE != syscall.O_RDONLY {
			return nil, &os.PathError{Op: "open", Path: name, Err: syscall.EACCES}
		}
		return prev(name, flag, perm)
	}
	t.Cleanup(func() { openLockFile = prev })
}

func TestLockFallsBackToReadOnlyWhenTheWritableOpenIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	if err := os.WriteFile(lockPath(path), nil, 0o444); err != nil {
		t.Fatal(err)
	}
	withWriteOpenRefused(t)
	unlock, err := Lock(path)
	if err != nil {
		t.Fatalf("Lock on a read-only lock file = %v", err)
	}
	_ = unlock()
}

// On NFS the read-only fallback cannot lock at all: the byte-range emulation
// fails with EBADF. That is neither a filesystem without locks, which would
// let a gateway run unlocked, nor proof that another process holds it.
func TestLockReportsAReadOnlyDescriptorItCannotLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.db")
	if err := os.WriteFile(lockPath(path), nil, 0o444); err != nil {
		t.Fatal(err)
	}
	withWriteOpenRefused(t)
	withFlock(t, func(int, int) error { return syscall.EBADF })

	unlock, err := Lock(path)
	if unlock != nil {
		_ = unlock()
	}
	if err == nil || errors.Is(err, ErrLockUnavailable) || errors.Is(err, ErrDatabaseInUse) {
		t.Fatalf("Lock = %v, want a refusal that is neither sentinel", err)
	}
	if !strings.Contains(err.Error(), "in use or not lockable by this user") {
		t.Errorf("Lock = %q, want it to name both possibilities", err)
	}
}

// NFS answers ENOLCK while lockd is briefly unreachable, which is not a mount
// that cannot lock.
func TestLockRetriesATransientENOLCK(t *testing.T) {
	withNoLockBackoff(t)
	calls := 0
	withFlock(t, func(fd, how int) error {
		if calls++; calls <= 2 {
			return syscall.ENOLCK
		}
		return syscall.Flock(fd, how)
	})
	unlock, err := Lock(filepath.Join(t.TempDir(), "darkrouter.db"))
	if err != nil {
		t.Fatalf("Lock after two ENOLCKs = %v, want the lock", err)
	}
	_ = unlock()
}

func TestLockGivesUpOnAPersistentENOLCK(t *testing.T) {
	withNoLockBackoff(t)
	calls := 0
	withFlock(t, func(int, int) error {
		calls++
		return syscall.ENOLCK
	})
	unlock, err := Lock(filepath.Join(t.TempDir(), "darkrouter.db"))
	if unlock != nil {
		_ = unlock()
	}
	if !errors.Is(err, ErrLockUnavailable) {
		t.Fatalf("Lock = %v, want ErrLockUnavailable", err)
	}
	if want := len(noLockBackoff) + 1; calls != want {
		t.Errorf("flock calls = %d, want %d", calls, want)
	}
}
