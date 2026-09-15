package store

import "errors"

// ErrDatabaseInUse reports a database another darkrouter process has locked.
var ErrDatabaseInUse = errors.New("the database is in use by another darkrouter process")

// ErrLockUnavailable reports a filesystem that cannot take the lock at all,
// such as a network mount without lock support. It says nothing about whether
// another process is using the database.
var ErrLockUnavailable = errors.New("the data directory's filesystem does not support file locks")

// lockPath names the advisory lock file beside a database. It is never
// removed: deleting a lock file another process may hold open would let a
// third process lock a different inode under the same name.
func lockPath(dbPath string) string { return dbPath + ".lock" }
