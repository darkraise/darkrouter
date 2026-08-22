package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
)

const settingProvidersImportedAt = "providers_imported_at"

// ImportResult reports what the import decided. Imported is false for the
// ordinary case of a database that has already been through it.
type ImportResult struct {
	Imported  bool
	Providers int
	At        time.Time
}

// ImportedAt returns when the first-run import ran, if it ever did.
func ImportedAt(ctx context.Context, d *DB) (time.Time, bool, error) {
	raw, ok, err := getSetting(ctx, d.Read, settingProvidersImportedAt)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("stored import marker is not a timestamp: %w", err)
	}
	return t, true, nil
}

// ImportFromConfig moves the YAML providers block into SQLite, once.
func ImportFromConfig(ctx context.Context, d *DB, key *crypto.Key, cfg *config.Config) (ImportResult, error) {
	return importWithHook(ctx, d, key, cfg, nil)
}

// importWithHook is ImportFromConfig with an injection point after each
// provider, so a test can prove the whole import is one transaction.
func importWithHook(ctx context.Context, d *DB, key *crypto.Key, cfg *config.Config,
	afterEach func(i int) error) (ImportResult, error) {

	// All three conditions must hold. The empty-table guard alone is not
	// enough, because a crash mid-import would falsify it.
	if len(cfg.Providers) == 0 {
		return ImportResult{}, nil
	}
	if _, ok, err := ImportedAt(ctx, d); err != nil {
		return ImportResult{}, err
	} else if ok {
		return ImportResult{}, nil
	}
	var existing int
	if err := d.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&existing); err != nil {
		return ImportResult{}, fmt.Errorf("count providers: %w", err)
	}
	if existing > 0 {
		return ImportResult{}, nil
	}

	// Validate before opening the transaction so an abort costs nothing. An
	// unresolvable ${ENV} reference reaches here as an empty string; importing
	// a provider with no credential would produce a provider that fails every
	// request instead of a clear startup error.
	for _, p := range cfg.Providers {
		if p.APIKey == "" {
			return ImportResult{}, fmt.Errorf(
				"provider %q has no api_key after environment resolution; "+
					"nothing was imported. Set the referenced variable and start again", p.ID)
		}
	}

	now := time.Now().UTC()
	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i, p := range cfg.Providers {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO providers (id, name, kind, base_url, priority, enabled, created_at)
			 VALUES (?, ?, ?, ?, ?, 1, ?)`,
			p.ID, p.ID, p.Kind, p.BaseURL, p.Priority, now.UnixMilli()); err != nil {
			return ImportResult{}, fmt.Errorf("import provider %q: %w", p.ID, err)
		}
		for _, m := range p.Models {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO models (provider_id, model_id, capabilities_source)
				 VALUES (?, ?, 'inferred')`, p.ID, m); err != nil {
				return ImportResult{}, fmt.Errorf("import model %q on %q: %w", m, p.ID, err)
			}
		}
		if _, err := insertCredentialTx(ctx, tx, key, Credential{
			ProviderID: p.ID, Label: "imported", Kind: "static",
			Secret: p.APIKey, Enabled: true,
		}); err != nil {
			return ImportResult{}, err
		}
		if afterEach != nil {
			if err := afterEach(i); err != nil {
				return ImportResult{}, err
			}
		}
	}

	// The marker is written inside the same transaction as the rows it
	// describes. Written afterwards, a crash between them would leave providers
	// present with no marker, and the next start would see a non-empty table
	// and skip the import silently.
	if err := putSetting(ctx, tx, settingProvidersImportedAt, now.Format(time.RFC3339)); err != nil {
		return ImportResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, fmt.Errorf("commit import: %w", err)
	}

	return ImportResult{Imported: true, Providers: len(cfg.Providers), At: now}, nil
}

// StaleBlockWarning returns the warning to show when darkrouter.yaml still
// carries a providers block that is no longer the source of truth. Editing it
// and expecting effect is the obvious mistake, and silence is the wrong answer.
func StaleBlockWarning(ctx context.Context, d *DB, cfg *config.Config) (string, error) {
	if len(cfg.Providers) == 0 {
		return "", nil
	}
	at, ok, err := ImportedAt(ctx, d)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return fmt.Sprintf(
		"providers were imported into the database on %s; the providers: block in "+
			"darkrouter.yaml is now ignored and can be deleted. Manage providers "+
			"through the database instead",
		at.Format("2006-01-02")), nil
}
