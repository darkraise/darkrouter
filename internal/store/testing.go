package store

import (
	"context"
	"path/filepath"
	"testing"
)

// MigratedForTest opens a migrated database in a temp directory.
//
// It lives in a non-test file because internal/admin's tests need it and Go
// does not export helpers from _test.go files across package boundaries. The
// alternative — every package reimplementing Open plus Migrate — is how two
// packages end up testing against differently-shaped databases.
//
// It mirrors this package's own openTest plus migrated helpers exactly; if
// those grow a step, this has to grow it too.
func MigratedForTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

// WriteBatchForTest persists request records synchronously.
//
// It exists because internal/admin's tests need a populated request log and the
// normal path is an asynchronous channel drained by a worker — a test that
// enqueued and slept would be timing-dependent for no reason. It is the same
// code path the worker uses, so what it writes is what production writes.
func (d *DB) WriteBatchForTest(t *testing.T, rows []*RequestRecord) {
	t.Helper()
	w := NewLogWriter(d, LogOptions{})
	if _, err := w.writeBatch(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
}
