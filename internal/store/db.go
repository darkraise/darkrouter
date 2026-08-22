// Package store owns the SQLite database: its handles, its schema, and the
// background workers that read and write it.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sync"

	_ "modernc.org/sqlite"
)

// DB holds the three handles Phase 2 needs.
//
// Splitting them is a correctness requirement rather than a tuning choice.
// SQLite permits a single writer, so every mutation shares one connection;
// letting many goroutines contend produces SQLITE_BUSY under exactly the load
// where you least want it. WAL is what makes the reader pool safe against that
// writer.
type DB struct {
	// Write carries every mutation except credentials, at synchronous=NORMAL.
	// An OS crash can lose the last few log rows, which costs nothing.
	Write *sql.DB
	// Read is a pool. Queries must be short-lived: a long-running read
	// transaction pins the WAL and blocks checkpointing.
	Read *sql.DB
	// Sync is a second single-writer handle at synchronous=FULL, used only for
	// credential writes and key rotation. Without it, a power loss after a
	// rotation commits but before the WAL syncs rolls the rotation back while
	// the operator has already changed DARKROUTER_MASTER_KEY — and the next
	// startup fails the verifier with no way to tell why.
	Sync *sql.DB

	Path string

	closeOnce sync.Once
	closeErr  error
}

// commonPragmas are applied through the DSN so every pooled connection carries
// them. Setting them once after opening leaves the pool's other connections
// with foreign keys silently off.
//
// auto_vacuum is here rather than in a migration because it only takes effect
// on a database that has no tables yet; applying it later is a no-op and
// retention deletes would then grow the file without bound.
const commonPragmas = "_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(on)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=auto_vacuum(incremental)"

func dsn(path, synchronous string) string {
	// EscapedPath leaves separators intact while escaping the characters the
	// DSN's query string would otherwise consume, notably '?' and '#'.
	// url.PathEscape is wrong here: it escapes '/' too, which breaks every
	// absolute path.
	escaped := (&url.URL{Path: path}).EscapedPath()
	return "file:" + escaped +
		"?" + commonPragmas + "&_pragma=synchronous(" + synchronous + ")"
}

// Open creates the database file if it does not exist and returns all three
// handles. It does not apply migrations; call Migrate.
func Open(path string) (*DB, error) {
	write, err := sql.Open("sqlite", dsn(path, "NORMAL"))
	if err != nil {
		return nil, fmt.Errorf("open write handle: %w", err)
	}
	write.SetMaxOpenConns(1)

	read, err := sql.Open("sqlite", dsn(path, "NORMAL"))
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open read handle: %w", err)
	}
	read.SetMaxOpenConns(4)

	syncH, err := sql.Open("sqlite", dsn(path, "FULL"))
	if err != nil {
		_ = write.Close()
		_ = read.Close()
		return nil, fmt.Errorf("open sync handle: %w", err)
	}
	syncH.SetMaxOpenConns(1)

	db := &DB{Write: write, Read: read, Sync: syncH, Path: path}
	// sql.Open is lazy, so a bad path surfaces here rather than at the first
	// query, where it would be reported as a request failure.
	if err := write.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// Close closes all three handles. It is safe to call more than once, because
// the server's shutdown path and a failed Open both reach it.
func (d *DB) Close() error {
	d.closeOnce.Do(func() {
		d.closeErr = errors.Join(d.Sync.Close(), d.Read.Close(), d.Write.Close())
	})
	return d.closeErr
}
