// Package storetest holds the database helpers other packages' tests share.
//
// It is a separate package rather than a non-test file in store so the
// production binary does not link the testing package: a helper that takes a
// *testing.T has to live somewhere only tests import.
package storetest

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

// migratedImage runs the real migrations once per test binary. Close checkpoints
// the WAL before the database bytes are read; no live handles are shared.
// Migration tests in package store still exercise fresh databases directly.
var migratedImage = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "darkrouter-test-schema-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	db, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// Migrated opens an isolated copy of the migrated schema in a temp directory.
// Replaying every migration for every API test dominates race-test runtime.
// Copying the closed database keeps the same schema and independent data while
// avoiding that repeated work.
func Migrated(t *testing.T) *store.DB {
	t.Helper()
	image, err := migratedImage()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.db")
	if err := os.WriteFile(path, image, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
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
