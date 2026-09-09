package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Roles. The column exists so account management has somewhere to record who
// may manage accounts; nothing in the request path reads it.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// User is one console account.
type User struct {
	ID           string
	Username     string
	UsernameLC   string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

// normalizeUsername is the case-folding rule, in one place. Go rather than
// SQLite's lower() or COLLATE NOCASE: both fold ASCII only, and a rule the
// reader can see beats one hidden in an index definition.
func normalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// UserCount reports how many accounts exist. Zero means the console has not
// been claimed, which is what puts the first-run screen in front of a visitor.
func (d *DB) UserCount(ctx context.Context) (int, error) {
	var n int
	if err := d.Read.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// ClaimFirstUser creates the founding admin, and reports whether this caller
// is the one that created it.
//
// The emptiness test lives inside the INSERT rather than in a read followed by
// a write, so two browsers racing at first boot cannot both be told they won.
// SQLite serializes writers, so the loser's INSERT sees the winner's row and
// affects nothing.
func (d *DB) ClaimFirstUser(ctx context.Context, id, username, hash string) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, username_lc, password_hash, role, created_at)
		 SELECT ?, ?, ?, ?, ?, ?
		  WHERE NOT EXISTS (SELECT 1 FROM users)`,
		id, username, normalizeUsername(username), hash, RoleAdmin, time.Now().UnixMilli())
	if err != nil {
		return false, fmt.Errorf("claim first user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim first user: %w", err)
	}
	return n > 0, nil
}

const userColumns = `id, username, username_lc, password_hash, role, created_at`

func scanUser(row *sql.Row) (User, bool, error) {
	var (
		u       User
		created int64
	)
	err := row.Scan(&u.ID, &u.Username, &u.UsernameLC, &u.PasswordHash, &u.Role, &created)
	if errors.Is(err, sql.ErrNoRows) {
		// A miss is not an error: an unknown username renders a login failure,
		// a database fault renders a 500, and the caller must tell them apart.
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("scan user: %w", err)
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	return u, true, nil
}

// UserByUsername looks an account up by its folded name.
func (d *DB) UserByUsername(ctx context.Context, username string) (User, bool, error) {
	return scanUser(d.Read.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username_lc = ?`,
		normalizeUsername(username)))
}

// UserByID looks an account up by its id, which is what a session carries.
func (d *DB) UserByID(ctx context.Context, id string) (User, bool, error) {
	return scanUser(d.Read.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// SetUserPassword replaces one account's hash.
func (d *DB) SetUserPassword(ctx context.Context, id, hash string) error {
	if _, err := d.Write.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
		return fmt.Errorf("set user password: %w", err)
	}
	return nil
}
