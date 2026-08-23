package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// CreateSession writes a new session row. The caller mints the id; this does not
// generate one, because the id is a security-relevant value and the code that
// chooses its entropy should be the code that owns the decision.
func (d *DB) CreateSession(ctx context.Context, id string, ttl time.Duration) error {
	now := time.Now()
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (id, created_at, expires_at) VALUES (?, ?, ?)`,
		id, now.UnixMilli(), now.Add(ttl).UnixMilli()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// TouchSession validates a session and slides its expiry in one statement.
//
// The expiry test lives in the WHERE rather than in a read followed by a
// comparison: two concurrent requests on a session expiring this instant must
// not disagree about whether it is alive, and a read-then-write leaves exactly
// that window open. It also means an expired row can never be extended by the
// call that just decided it was dead.
//
// A miss is reported as false rather than as an error, because a missing session
// and a database failure are different things to the caller: the first renders
// the login screen, the second is a 500.
func (d *DB) TouchSession(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	now := time.Now()
	res, err := d.Write.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE id = ? AND expires_at > ?`,
		now.Add(ttl).UnixMilli(), id, now.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	return n > 0, nil
}

// DeleteSession removes the row. Spec §3: logout deletes rather than only
// clearing the cookie, because a cleared cookie leaves a valid session id in the
// database for anyone who copied it.
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	if _, err := d.Write.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SweepSessions prunes expired rows and reports how many went. It runs at
// startup: sessions outlive the process, so nothing else would ever remove them.
func (d *DB) SweepSessions(ctx context.Context) (int, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	return int(n), nil
}

// ProviderRow is a provider as the admin API sees it: the database row without
// its credentials, which are never returned together because no endpoint may
// reveal credential material.
type ProviderRow struct {
	ID        string
	Name      string
	Preset    string
	Kind      string
	BaseURL   string
	AuthStyle string
	Priority  int
	Enabled   bool
	Region    string
	Project   string
}

// ProviderPatch is a partial update. Every field is a pointer because a partial
// update has to distinguish "set this to its zero value" from "do not touch
// it", and priority 0 is a legal value meaning last resort.
type ProviderPatch struct {
	Name     *string `json:"name"`
	BaseURL  *string `json:"base_url"`
	Priority *int    `json:"priority"`
	Enabled  *bool   `json:"enabled"`
	Region   *string `json:"region"`
	Project  *string `json:"project"`
}

func (d *DB) CreateProvider(ctx context.Context, p ProviderRow) error {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	if p.AuthStyle == "" {
		// Matches the column default. Sent explicitly so a row created here has
		// the same shape as one created by ImportFromConfig.
		p.AuthStyle = "bearer"
	}
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO providers
		   (id, name, preset, kind, base_url, auth_style, priority, enabled,
		    region, project, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Preset, p.Kind, p.BaseURL, p.AuthStyle,
		p.Priority, enabled, p.Region, p.Project, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("create provider %q: %w", p.ID, err)
	}
	return nil
}

// UpdateProvider applies a partial update, and reports an error when the id does
// not exist rather than succeeding silently. A PATCH against a deleted provider
// that returned success is how a settings screen shows an edit that never
// happened.
func (d *DB) UpdateProvider(ctx context.Context, id string, patch ProviderPatch) error {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 7)
	// Fixed order rather than a map range: the generated SQL has to be
	// deterministic or SQLite's statement cache misses on every call.
	if patch.Name != nil {
		sets, args = append(sets, "name = ?"), append(args, *patch.Name)
	}
	if patch.BaseURL != nil {
		sets, args = append(sets, "base_url = ?"), append(args, *patch.BaseURL)
	}
	if patch.Priority != nil {
		sets, args = append(sets, "priority = ?"), append(args, *patch.Priority)
	}
	if patch.Enabled != nil {
		v := 0
		if *patch.Enabled {
			v = 1
		}
		sets, args = append(sets, "enabled = ?"), append(args, v)
	}
	if patch.Region != nil {
		sets, args = append(sets, "region = ?"), append(args, *patch.Region)
	}
	if patch.Project != nil {
		sets, args = append(sets, "project = ?"), append(args, *patch.Project)
	}
	if len(sets) == 0 {
		// An empty patch is a client bug, not a no-op to absorb: it means the
		// UI sent a form it did not fill in.
		return fmt.Errorf("update provider %q: the patch names no fields", id)
	}
	args = append(args, id)

	res, err := d.Write.ExecContext(ctx,
		`UPDATE providers SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("update provider %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update provider %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("update provider %q: no such provider", id)
	}
	return nil
}

// DeleteProvider removes the provider. Its credentials, models and discovery
// state go with it through the schema's own ON DELETE CASCADE, which foreign
// keys being on makes real — relisting the children here would be a second
// place to update when a table is added.
//
// It runs on the Sync handle rather than Write because the cascade removes
// credential rows, and credential durability is what that handle exists for: a
// delete lost to a power failure leaves a secret the operator believes is gone.
func (d *DB) DeleteProvider(ctx context.Context, id string) error {
	res, err := d.Sync.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("delete provider %q: no such provider", id)
	}
	return nil
}

// DeleteCredential removes one key. On the Sync handle for the same reason
// AddCredential writes there: a credential change lost to a power failure is
// the one kind of lost write that matters here.
func (d *DB) DeleteCredential(ctx context.Context, providerID, keyID string) error {
	res, err := d.Sync.ExecContext(ctx,
		`DELETE FROM provider_keys WHERE provider_id = ? AND id = ?`, providerID, keyID)
	if err != nil {
		return fmt.Errorf("delete credential %q: %w", keyID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete credential %q: %w", keyID, err)
	}
	if n == 0 {
		return fmt.Errorf("delete credential %q: no such credential", keyID)
	}
	return nil
}

// ProviderRows lists every provider, ordered by priority descending then id so
// two calls return the same order and the settings screen does not reshuffle
// between polls.
func (d *DB) ProviderRows(ctx context.Context) ([]ProviderRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, name, preset, kind, base_url, auth_style, priority, enabled,
		        region, project
		   FROM providers ORDER BY priority DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	out := make([]ProviderRow, 0)
	for rows.Next() {
		var p ProviderRow
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &p.Preset, &p.Kind, &p.BaseURL,
			&p.AuthStyle, &p.Priority, &enabled, &p.Region, &p.Project); err != nil {
			return nil, fmt.Errorf("list providers: %w", err)
		}
		p.Enabled = enabled != 0
		out = append(out, p)
	}
	return out, rows.Err()
}
