package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesPragmasToEveryPooledConnection(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Hold several connections open at once so the pool is forced to create
	// more than one. A pragma set with a PRAGMA statement after Open would
	// apply to whichever connection ran it and leave the rest with foreign
	// keys off — a bug that surfaces as missing constraint enforcement rather
	// than as an error.
	const n = 4
	conns := make([]*sql.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, err := db.Read.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	for i, c := range conns {
		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if fk != 1 {
			t.Errorf("connection %d: foreign_keys = %d, want 1", i, fk)
		}
		var busy int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if busy != 5000 {
			t.Errorf("connection %d: busy_timeout = %d, want 5000", i, busy)
		}
	}
}

func TestOpenUsesWALAndIncrementalVacuum(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var mode string
	if err := db.Write.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	// auto_vacuum only takes effect on a database with no tables yet, so this
	// asserts it was set in the DSN of the first connection rather than later.
	var av int
	if err := db.Write.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&av); err != nil {
		t.Fatal(err)
	}
	if av != 2 {
		t.Errorf("auto_vacuum = %d, want 2 (incremental)", av)
	}
}

func TestOpenSerializesWrites(t *testing.T) {
	db := openTest(t)
	if got := db.Write.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("write handle MaxOpenConnections = %d, want 1", got)
	}
	if got := db.Sync.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("sync handle MaxOpenConnections = %d, want 1", got)
	}
}

func TestSyncHandleRunsFullSynchronous(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	var s int
	if err := db.Sync.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != 2 {
		t.Errorf("sync handle synchronous = %d, want 2 (FULL)", s)
	}
	if err := db.Write.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != 1 {
		t.Errorf("write handle synchronous = %d, want 1 (NORMAL)", s)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close returned %v, want nil", err)
	}
}
