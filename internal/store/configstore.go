package store

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// Aliases returns every alias chain, each in its stored order.
//
// An empty result is not the same as "never configured": an operator may have
// deleted every alias through the console. ConfigImported is what separates
// the two.
func (d *DB) Aliases(ctx context.Context) (map[string][]string, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT name, target FROM aliases ORDER BY name, seq`)
	if err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]string{}
	for rows.Next() {
		var name, target string
		if err := rows.Scan(&name, &target); err != nil {
			return nil, fmt.Errorf("scan alias: %w", err)
		}
		out[name] = append(out[name], target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	return out, nil
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit alias write: %w", err)
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

// PolicyOverrides returns only the policy keys the database actually carries.
//
// Only what is set: a caller has to tell "not overridden" from "set to zero",
// which is what lets the config screen name the source of each value.
func (d *DB) PolicyOverrides(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range policyFields {
		v, ok, err := getSetting(ctx, d.Read, f.key)
		if err != nil {
			return nil, err
		}
		if ok {
			out[f.key] = v
		}
	}
	return out, nil
}

// PutPolicy writes every set field of p, and removes the keys it leaves unset
// so clearing a value in the console restores the file's.
func (d *DB) PutPolicy(ctx context.Context, p config.PolicyConfig) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin policy write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit policy write: %w", err)
	}
	return nil
}

// ApplyPolicy overlays stored overrides onto a loaded policy, leaving any
// field the database does not carry exactly as the file set it.
func ApplyPolicy(p *config.PolicyConfig, overrides map[string]string) error {
	for _, f := range policyFields {
		v, ok := overrides[f.key]
		if !ok {
			continue
		}
		if err := f.set(p, v); err != nil {
			return fmt.Errorf("stored %s is unusable: %w", f.key, err)
		}
	}
	return nil
}
