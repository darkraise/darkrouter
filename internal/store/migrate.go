package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded files and asserts their versions are
// contiguous from 1. Contiguity is what lets len(migrations) stand in for "the
// newest version this binary understands"; with a gap, a database at version 3
// against files 1 and 4 would look current.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		digits, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: expected <version>_<description>.sql", e.Name())
		}
		v, err := strconv.Atoi(digits)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf(
				"migrations must be contiguous from 1: found version %d at position %d",
				m.version, i+1)
		}
	}
	return out, nil
}

// Migrate applies every migration newer than the recorded version. It is safe
// to call on every startup.
func (d *DB) Migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}

	// schema_version is created by the migrator rather than by a migration,
	// because the version has to be readable before any migration has run.
	if _, err := d.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	err = d.Write.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := d.Write.ExecContext(ctx,
			`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("seed schema_version: %w", err)
		}
		current = 0
	case err != nil:
		return fmt.Errorf("read schema_version: %w", err)
	}

	if latest := len(ms); current > latest {
		return fmt.Errorf(
			"database schema version %d is newer than this binary understands (%d); "+
				"this is a rollback deploy, and an old binary against a new schema "+
				"corrupts data quietly rather than failing",
			current, latest)
	}

	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := d.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// applyMigration runs one migration and its version bump in a single
// transaction, so an interrupted migration leaves the version unchanged and the
// next startup retries it from a consistent state.
func (d *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, m.version); err != nil {
		return err
	}
	return tx.Commit()
}
