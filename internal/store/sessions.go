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

// CreateSession writes a new session row for one account. The caller mints the
// id; this does not generate one, because the id is a security-relevant value
// and the code that chooses its entropy should be the code that owns it.
func (d *DB) CreateSession(ctx context.Context, id, userID string, ttl time.Duration) error {
	now := time.Now()
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		HashSessionID(id), userID, now.UnixMilli(), now.Add(ttl).UnixMilli()); err != nil {
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
func (d *DB) TouchSession(ctx context.Context, id string, ttl time.Duration) (string, bool, error) {
	now := time.Now()
	hashed := HashSessionID(id)
	var (
		userID  string
		expires int64
	)
	// The join is what makes an orphaned row a 401 rather than a handler
	// holding an empty owner. ON DELETE CASCADE should make it unreachable;
	// a restored backup that de-synced the tables is where that stops being
	// true, and failing closed there costs nothing.
	err := d.Read.QueryRowContext(ctx,
		`SELECT s.user_id, s.expires_at FROM sessions s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.id = ? AND s.expires_at > ? AND s.created_at > ?`,
		hashed, now.UnixMilli(), now.Add(-SessionMaxAge).UnixMilli()).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("touch session: %w", err)
	}
	if time.UnixMilli(expires).Sub(now) < ttl-sessionTouchInterval {
		if _, err := d.Write.ExecContext(ctx,
			`UPDATE sessions SET expires_at = ? WHERE id = ? AND expires_at > ?`,
			now.Add(ttl).UnixMilli(), hashed, now.UnixMilli()); err != nil {
			return "", false, fmt.Errorf("touch session: %w", err)
		}
	}
	return userID, true, nil
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

// RevokeSession removes one of the caller's own rows by its stored (hashed)
// id, which is what a listing hands back. The owner is in the WHERE rather
// than checked by the handler: a store method should not depend on every
// caller remembering to scope it.
func (d *DB) RevokeSession(ctx context.Context, userID, hashedID string) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ? AND user_id = ?`, hashedID, userID)
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
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionRows lists one account's live sessions, newest first. Scoped to the
// caller: a listing that showed every account's sessions would let any user
// revoke any other's, which is a permission model nobody asked for.
func (d *DB) SessionRows(ctx context.Context, userID string, now time.Time) ([]SessionRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, user_id, created_at, expires_at FROM sessions
		  WHERE user_id = ? AND expires_at > ? AND created_at > ?
		  ORDER BY created_at DESC, id`,
		userID, now.UnixMilli(), now.Add(-SessionMaxAge).UnixMilli())
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
		if err := rows.Scan(&r.ID, &r.UserID, &created, &expires); err != nil {
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

// DeleteSessionsExcept revokes every other session belonging to one account.
// It is what a password change uses: anything that also revoked the caller
// would log the operator out of the screen they just used, and anything that
// reached other accounts would sign out people whose password did not change.
func (d *DB) DeleteSessionsExcept(ctx context.Context, userID, keep string) (int, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, HashSessionID(keep))
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	return int(n), nil
}
