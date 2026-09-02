package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// SessionMaxAge bounds a session's life from creation regardless of use. A
// sliding expiry alone means a cookie exercised every few days lives forever,
// and a copied cookie with it.
const SessionMaxAge = 30 * 24 * time.Hour

// sessionTouchInterval is how often a live session's expiry is actually
// written. Every authenticated request slides the expiry, and the console
// polls several endpoints every few seconds; writing on each one would put a
// row update on the single writer for every poll.
const sessionTouchInterval = 5 * time.Minute

// HashSessionID is the form a session id takes at rest. The cookie carries
// the raw id; the table holds its digest, so a copy of the database file does
// not carry every live credential in it.
func HashSessionID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// CreateSession writes a new session row. The caller mints the id; this does not
// generate one, because the id is a security-relevant value and the code that
// chooses its entropy should be the code that owns the decision.
func (d *DB) CreateSession(ctx context.Context, id string, ttl time.Duration) error {
	now := time.Now()
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (id, created_at, expires_at) VALUES (?, ?, ?)`,
		HashSessionID(id), now.UnixMilli(), now.Add(ttl).UnixMilli()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// TouchSession validates a session and slides its expiry.
//
// The expiry test lives in the WHERE of both statements rather than in a read
// followed by a comparison, so an expired row can never be extended by the
// call that just decided it was dead. The extension is skipped when the
// expiry already sits within sessionTouchInterval of where it would land.
//
// A miss is reported as false rather than as an error, because a missing session
// and a database failure are different things to the caller: the first renders
// the login screen, the second is a 500.
func (d *DB) TouchSession(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	now := time.Now()
	hashed := HashSessionID(id)
	var expires int64
	err := d.Read.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions
		  WHERE id = ? AND expires_at > ? AND created_at > ?`,
		hashed, now.UnixMilli(), now.Add(-SessionMaxAge).UnixMilli()).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	next := now.Add(ttl)
	if next.Sub(time.UnixMilli(expires)) < sessionTouchInterval {
		return true, nil
	}
	res, err := d.Write.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE id = ? AND expires_at > ?`,
		next.UnixMilli(), hashed, now.UnixMilli())
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
	if _, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ?`, HashSessionID(id)); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// RevokeSession removes a row by its stored (hashed) id, which is what a
// listing hands back. It reports whether a row went.
func (d *DB) RevokeSession(ctx context.Context, hashedID string) (bool, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, hashedID)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	return n > 0, nil
}

// SweepSessions prunes rows that have expired or outlived SessionMaxAge, and
// reports how many went. It runs at startup and on the retention ticker:
// sessions outlive the process, so nothing else would ever remove them.
func (d *DB) SweepSessions(ctx context.Context) (int, error) {
	now := time.Now()
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ? OR created_at <= ?`,
		now.UnixMilli(), now.Add(-SessionMaxAge).UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	return int(n), nil
}

// SessionRow is one live admin session. ID is the stored digest, not the
// credential the cookie carries; a caller compares with HashSessionID.
type SessionRow struct {
	ID        string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionRows lists sessions that have not expired, newest first.
func (d *DB) SessionRows(ctx context.Context, now time.Time) ([]SessionRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, created_at, expires_at FROM sessions
		  WHERE expires_at > ? AND created_at > ?
		  ORDER BY created_at DESC, id`,
		now.UnixMilli(), now.Add(-SessionMaxAge).UnixMilli())
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
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return out, nil
}

// DeleteSessionsExcept revokes every session but one. It is what a password
// change uses: anything that also revoked the caller would log the operator
// out of the screen they just used.
func (d *DB) DeleteSessionsExcept(ctx context.Context, keep string) (int, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE id <> ?`, HashSessionID(keep))
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	return int(n), nil
}
