package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

// SaveLastUsed records credential dispatch times. This exists only so a restart
// resumes with the rotation order it had; the in-memory map is authoritative on
// the hot path and nothing reads these rows to make a routing decision.
func (d *DB) SaveLastUsed(ctx context.Context, m map[health.CredKey]time.Time) error {
	if len(m) == 0 {
		return nil
	}
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin last_used save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// UPDATE rather than upsert: a credential missing from provider_keys was
	// deleted, and inserting it back would violate the provider foreign key.
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE provider_keys SET last_used_at = ? WHERE id = ? AND provider_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for ck, at := range m {
		if _, err := stmt.ExecContext(ctx, at.UnixMilli(), ck.KeyID, ck.ProviderID); err != nil {
			return fmt.Errorf("update last_used for %s/%s: %w", ck.ProviderID, ck.KeyID, err)
		}
	}
	return tx.Commit()
}

// LoadLastUsed reads the timestamps for rehydration. Credentials that have
// never been dispatched to are absent rather than zero-valued.
func (d *DB) LoadLastUsed(ctx context.Context) (map[health.CredKey]time.Time, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, id, last_used_at FROM provider_keys WHERE last_used_at IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("load last_used: %w", err)
	}
	defer rows.Close()

	out := map[health.CredKey]time.Time{}
	for rows.Next() {
		var providerID, keyID string
		var at int64
		if err := rows.Scan(&providerID, &keyID, &at); err != nil {
			return nil, fmt.Errorf("scan last_used: %w", err)
		}
		out[health.CredKey{ProviderID: providerID, KeyID: keyID}] = time.UnixMilli(at).UTC()
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load last used: %w", err)
	}
	return out, nil
}
