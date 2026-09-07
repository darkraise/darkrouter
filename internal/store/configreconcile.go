package store

import (
	"context"
	"fmt"

	"github.com/darkraise/darkrouter/internal/config"
)

// ReconcileConfig deletes stored rows whose value equals the compiled default,
// so an upgraded database and a fresh one describe the same state.
//
// This makes startup write to the database, which it did not do before. It is
// safe because there is one process and one SQLite file, and it is a no-op on
// a database that has just been migrated.
//
// It deliberately loses one distinction: a value an operator set explicitly
// which happens to equal the default becomes indistinguishable from one never
// set, so it will float if a later release changes that default. The
// alternative is carrying a "set to the default on purpose" flag through the
// registry, the API and the console to serve a case nobody has asked for.
//
// The returned count covers the redundant setting rows only; the import marker
// is deleted unconditionally and is not counted.
func ReconcileConfig(ctx context.Context, d *DB) (int, error) {
	defaults := &config.Config{}
	config.ApplyDefaults(defaults)
	want := ConfigRowsFor(defaults)

	// configRows is already filtered to the registry. The loop below does not
	// rely on that: it rejects an unknown key on its own, so the filter is a
	// narrowing rather than the thing keeping foreign rows alive.
	stored, err := configRows(ctx, d)
	if err != nil {
		return 0, err
	}

	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleted := 0
	for key, value := range stored {
		w, known := want[key]
		if !known || w != value {
			continue
		}
		// Keyed on the value as well as the key: the read above ran on d.Read,
		// outside this transaction, so the value is a precondition rather than
		// a fact.
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ? AND value = ?`, key, value); err != nil {
			return 0, fmt.Errorf("delete redundant setting %q: %w", key, err)
		}
		deleted++
	}
	// The import it marked cannot happen again; leaving it behind is an
	// orphan row in a table this package now claims to own.
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, settingConfigImportedAt); err != nil {
		return 0, fmt.Errorf("delete the import marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit reconciliation: %w", err)
	}
	return deleted, nil
}
