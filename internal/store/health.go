package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

// SaveHealth replaces the persisted breaker state with the given snapshot.
func (d *DB) SaveHealth(ctx context.Context, entries []health.Entry) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin health save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A full replace rather than an upsert: an entry vanishes when its breaker
	// closes, and a stale row would be rehydrated as a phantom cooldown.
	if _, err := tx.ExecContext(ctx, `DELETE FROM health`); err != nil {
		return fmt.Errorf("clear health: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO health
		   (provider_id, key_id, model, cooling_until, backoff_level, consecutive_failures, updated_at)
		 VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()
	for _, e := range entries {
		var cooling *int64
		if !e.CoolingUntil.IsZero() {
			v := e.CoolingUntil.UnixMilli()
			cooling = &v
		}
		if _, err := stmt.ExecContext(ctx,
			e.Key.ProviderID, e.Key.KeyID, e.Key.Model,
			cooling, e.BackoffLevel, e.ConsecutiveFailures, now,
		); err != nil {
			return fmt.Errorf("insert health for %s/%s/%s: %w",
				e.Key.ProviderID, e.Key.KeyID, e.Key.Model, err)
		}
	}
	return tx.Commit()
}

// LoadHealth reads the persisted state for rehydration at startup.
func (d *DB) LoadHealth(ctx context.Context) ([]health.Entry, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, key_id, model, cooling_until, backoff_level, consecutive_failures
		   FROM health`)
	if err != nil {
		return nil, fmt.Errorf("load health: %w", err)
	}
	defer rows.Close()

	var out []health.Entry
	for rows.Next() {
		var (
			e       health.Entry
			cooling *int64
		)
		if err := rows.Scan(&e.Key.ProviderID, &e.Key.KeyID, &e.Key.Model,
			&cooling, &e.BackoffLevel, &e.ConsecutiveFailures); err != nil {
			return nil, fmt.Errorf("scan health: %w", err)
		}
		if cooling != nil {
			// UTC because the breaker compares against time.Now(), and a
			// location-carrying value would still compare correctly but read
			// confusingly in the admin UI later.
			e.CoolingUntil = time.UnixMilli(*cooling).UTC()
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load health: %w", err)
	}
	return out, nil
}
