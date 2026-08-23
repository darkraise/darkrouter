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
