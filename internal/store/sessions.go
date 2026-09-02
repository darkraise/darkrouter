package store

import (
	"context"
	"fmt"
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

// SessionRow is one live admin session. The id is the credential the cookie
// carries, so a caller that renders these must not show it in full.
type SessionRow struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionRows lists sessions that have not expired, newest first.
func (d *DB) SessionRows(ctx context.Context, now time.Time) ([]SessionRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, created_at, expires_at FROM sessions
		  WHERE expires_at > ? ORDER BY created_at DESC, id`, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []SessionRow{}
	for rows.Next() {
		var (
			r                SessionRow
			created, expires int64
		)
		if err := rows.Scan(&r.ID, &created, &expires); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		r.CreatedAt = time.UnixMilli(created).UTC()
		r.ExpiresAt = time.UnixMilli(expires).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteSessionsExcept revokes every session but one. It is what a password
// change uses: anything that also revoked the caller would log the operator
// out of the screen they just used.
func (d *DB) DeleteSessionsExcept(ctx context.Context, keep string) (int, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE id <> ?`, keep)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
