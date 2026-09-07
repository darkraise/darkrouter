package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

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

// policyField binds one dotted settings key to the struct field it carries.
// One table rather than two switch statements, so reading and writing cannot
// drift apart and a new policy field is one entry.
type policyField struct {
	key string
	// get reports the value to store and whether the field is set at all.
	get func(*config.PolicyConfig) (string, bool)
	set func(*config.PolicyConfig, string) error
}

// Durations serialize the way time.ParseDuration reads them, not as a
// nanosecond count: an operator reads these in the settings screen and writes
// them back.
var policyFields = []policyField{
	{
		key: "policy.cooldown.trip_after",
		get: func(p *config.PolicyConfig) (string, bool) {
			if p.Cooldown.TripAfter == nil {
				return "", false
			}
			return strconv.Itoa(*p.Cooldown.TripAfter), true
		},
		set: func(p *config.PolicyConfig, v string) error {
			n, err := strconv.Atoi(v)
			if err != nil {
				return err
			}
			p.Cooldown.TripAfter = &n
			return nil
		},
	},
	{
		key: "policy.cooldown.max",
		get: func(p *config.PolicyConfig) (string, bool) {
			return p.Cooldown.Max.String(), p.Cooldown.Max != 0
		},
		set: func(p *config.PolicyConfig, v string) error {
			d, err := time.ParseDuration(v)
			p.Cooldown.Max = d
			return err
		},
	},
	{
		key: "policy.retry.max_attempts",
		get: func(p *config.PolicyConfig) (string, bool) {
			return strconv.Itoa(p.Retry.MaxAttempts), p.Retry.MaxAttempts != 0
		},
		set: func(p *config.PolicyConfig, v string) error {
			n, err := strconv.Atoi(v)
			p.Retry.MaxAttempts = n
			return err
		},
	},
	{
		key: "policy.timeout.connect",
		get: func(p *config.PolicyConfig) (string, bool) {
			return p.Timeout.Connect.String(), p.Timeout.Connect != 0
		},
		set: func(p *config.PolicyConfig, v string) error {
			d, err := time.ParseDuration(v)
			p.Timeout.Connect = d
			return err
		},
	},
	{
		key: "policy.timeout.first_byte",
		get: func(p *config.PolicyConfig) (string, bool) {
			return p.Timeout.FirstByte.String(), p.Timeout.FirstByte != 0
		},
		set: func(p *config.PolicyConfig, v string) error {
			d, err := time.ParseDuration(v)
			p.Timeout.FirstByte = d
			return err
		},
	},
	{
		key: "policy.timeout.total",
		get: func(p *config.PolicyConfig) (string, bool) {
			return p.Timeout.Total.String(), p.Timeout.Total != 0
		},
		set: func(p *config.PolicyConfig, v string) error {
			d, err := time.ParseDuration(v)
			p.Timeout.Total = d
			return err
		},
	},
	{
		key: "policy.timeout.idle",
		get: func(p *config.PolicyConfig) (string, bool) {
			return p.Timeout.Idle.String(), p.Timeout.Idle != 0
		},
		set: func(p *config.PolicyConfig, v string) error {
			d, err := time.ParseDuration(v)
			p.Timeout.Idle = d
			return err
		},
	},
}

// PutPolicy writes every set field of p, and removes the keys it leaves unset
// so clearing a value in the console restores the file's.
func (d *DB) PutPolicy(ctx context.Context, p config.PolicyConfig) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := putPolicyTx(ctx, tx, p); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit policy write: %w", err)
	}
	return nil
}

// PutConfig writes an alias set and a policy together, so a settings save
// that fails halfway leaves neither half applied. A nil aliases map or policy
// leaves that block untouched.
func (d *DB) PutConfig(ctx context.Context, aliases map[string][]string, policy *config.PolicyConfig) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin config write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if aliases != nil {
		if err := putAliasesTx(ctx, tx, aliases); err != nil {
			return err
		}
	}
	if policy != nil {
		if err := putPolicyTx(ctx, tx, *policy); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit config write: %w", err)
	}
	return nil
}

func putPolicyTx(ctx context.Context, tx *sql.Tx, p config.PolicyConfig) error {
	for _, f := range policyFields {
		v, ok := f.get(&p)
		if !ok {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM settings WHERE key = ?`, f.key); err != nil {
				return fmt.Errorf("clear setting %q: %w", f.key, err)
			}
			continue
		}
		if err := putSetting(ctx, tx, f.key, v); err != nil {
			return err
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
