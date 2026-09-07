package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/darkraise/darkrouter/internal/config"
)

// Aliases returns every alias chain, each in its stored order.
//
// An empty result is not the same as "never configured": it means either that
// no alias was ever configured or that the operator deleted the last one, and
// nothing now distinguishes the two.
func (d *DB) Aliases(ctx context.Context) (map[string][]string, error) {
	return aliasesTx(ctx, d.Read)
}

// PutAliases replaces the whole set in one transaction.
//
// Replace rather than merge: a chain the operator deleted has to disappear,
// and a partial write would leave one chain half-rewritten with its fallback
// order silently changed.
func (d *DB) PutAliases(ctx context.Context, aliases map[string][]string) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin alias write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := putAliasesTx(ctx, tx, aliases); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit alias write: %w", err)
	}
	return nil
}

func putAliasesTx(ctx context.Context, tx *sql.Tx, aliases map[string][]string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM aliases`); err != nil {
		return fmt.Errorf("clear aliases: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO aliases (name, seq, target) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare alias insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	// Sorted so a write produces the same rows in the same order every time,
	// which keeps a diff of the database readable.
	names := make([]string, 0, len(aliases))
	for name := range aliases {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		for i, target := range aliases[name] {
			if _, err := stmt.ExecContext(ctx, name, i, target); err != nil {
				return fmt.Errorf("insert alias %q: %w", name, err)
			}
		}
	}
	return nil
}

// settingConfigImportedAt is the marker the retired YAML import left behind.
// The import is gone; the constant stays because ReconcileConfig deletes the
// row on every start, and an orphan row in a table this package owns is what
// it exists to clear.
const settingConfigImportedAt = "config.imported_at"

// OverlayConfig replaces a loaded Config's aliases with the database's,
// leaving every other block as the loader built it.
//
// Installed on config.Store as its overlay, so router, exec, server and admin
// keep reading aliases through the snapshot they already take. It exists
// because LoadConfig reads the 32 scalar keys from the registry and not the
// alias table, which is a table rather than a settings row.
//
// It deliberately does not apply policy. The registry already reads the seven
// policy.* rows, so doing it here a second time would at best repeat the
// loader's work and at worst undo it: a policy set LoadConfig
// reverted for a cross-key rule failure would be reinstated with nothing left
// to revalidate it.
func OverlayConfig(ctx context.Context, d *DB, cfg *config.Config) error {
	aliases, err := d.Aliases(ctx)
	if err != nil {
		return err
	}
	cfg.Aliases = aliases
	return nil
}

// PutModelOverride writes the operator's correction for one (provider, model).
//
// Every column is written, including the nil ones. The row is the whole
// override rather than a patch: a caller that wanted to keep a field reads the
// row first, and leaving a stale value behind because this call did not
// mention it would be the harder failure to see.
func (d *DB) PutModelOverride(ctx context.Context, o ModelOverride) error {
	var surfaces, caps any
	if len(o.Surfaces) > 0 {
		b, err := json.Marshal(o.Surfaces)
		if err != nil {
			return fmt.Errorf("encode override surfaces: %w", err)
		}
		surfaces = string(b)
	}
	if o.Capabilities != nil {
		b, err := json.Marshal(o.Capabilities)
		if err != nil {
			return fmt.Errorf("encode override capabilities: %w", err)
		}
		caps = string(b)
	}
	var window any
	if o.ContextWindow != nil {
		window = *o.ContextWindow
	}

	_, err := d.Write.ExecContext(ctx,
		`INSERT INTO model_overrides
		     (provider_id, model_id, surfaces, capabilities, context_window)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(provider_id, model_id) DO UPDATE SET
		     surfaces = excluded.surfaces,
		     capabilities = excluded.capabilities,
		     context_window = excluded.context_window`,
		o.ProviderID, o.ModelID, surfaces, caps, window)
	if err != nil {
		return fmt.Errorf("write model override %s/%s: %w", o.ProviderID, o.ModelID, err)
	}
	return nil
}

// DeleteModelOverride removes a correction, returning the merged catalog to
// whatever the upstream itself reports.
func (d *DB) DeleteModelOverride(ctx context.Context, providerID, modelID string) error {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM model_overrides WHERE provider_id = ? AND model_id = ?`,
		providerID, modelID)
	if err != nil {
		return fmt.Errorf("delete model override %s/%s: %w", providerID, modelID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete model override %s/%s: %w", providerID, modelID, err)
	}
	if n == 0 {
		return fmt.Errorf("model override %s/%s: %w", providerID, modelID, ErrNotFound)
	}
	return nil
}
