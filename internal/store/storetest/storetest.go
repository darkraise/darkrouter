// Package storetest holds the database helpers other packages' tests share.
//
// It is a separate package rather than a non-test file in store so the
// production binary does not link the testing package: a helper that takes a
// *testing.T has to live somewhere only tests import.
package storetest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

// Migrated opens a migrated database in a temp directory.
//
// It mirrors store's own openTest plus migrated helpers exactly; if those
// grow a step, this has to grow it too. The alternative — every package
// reimplementing Open plus Migrate — is how two packages end up testing
// against differently-shaped databases.
func Migrated(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

// WriteBatch persists request records synchronously.
//
// The normal path is an asynchronous channel drained by a worker, and a test
// that enqueued and slept would be timing-dependent for no reason. It is the
// same code path the worker uses, so what it writes is what production writes.
func WriteBatch(t *testing.T, db *store.DB, rows []*store.RequestRecord) {
	t.Helper()
	w := store.NewLogWriter(db, store.LogOptions{})
	if _, err := w.WriteBatch(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
}

// SeedFailoverTrace writes the two-attempt trace the admin handler tests read.
func SeedFailoverTrace(t *testing.T, db *store.DB, id string) {
	t.Helper()
	WriteBatch(t, db, store.FailoverTraceFixture(id))
}
