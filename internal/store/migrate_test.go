package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func migrated(t *testing.T) *DB {
	t.Helper()
	db := openTest(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrateCreatesEveryTable(t *testing.T) {
	db := migrated(t)
	want := []string{
		"providers", "provider_keys", "models", "model_overrides",
		"requests", "request_attempts", "request_bodies",
		"health", "usage_daily", "sessions", "settings",
	}
	for _, table := range want {
		var name string
		err := db.Read.QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		var v int
		if err := db.Read.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != 1 {
			t.Errorf("run %d: version = %d, want 1", i, v)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	// Simulate a rollback deploy: the file was written by a future binary.
	if _, err := db.Write.ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatal(err)
	}
	err := db.Migrate(ctx)
	if err == nil {
		t.Fatal("expected a newer schema to be refused")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("error should name the cause, got %v", err)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	// provider_keys references providers(id); this provider does not exist.
	_, err := db.Write.ExecContext(ctx,
		`INSERT INTO provider_keys (id, provider_id, ciphertext, nonce)
		 VALUES ('k1', 'nope', x'00', x'00')`)
	if err == nil {
		t.Fatal("expected the foreign key to reject an orphan credential")
	}
}

func TestMigrationsAreContiguous(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations were embedded")
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Errorf("migration %d has version %d, want %d", i, m.version, i+1)
		}
	}
}
