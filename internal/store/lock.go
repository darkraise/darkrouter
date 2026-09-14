package store

import "errors"

// ErrDatabaseInUse reports a database another darkrouter process has locked.
var ErrDatabaseInUse = errors.New("the database is in use by another darkrouter process")

// lockPath names the advisory lock file beside a database. It is never
// removed: deleting a lock file another process may hold open would let a
// third process lock a different inode under the same name.
func lockPath(dbPath string) string { return dbPath + ".lock" }
