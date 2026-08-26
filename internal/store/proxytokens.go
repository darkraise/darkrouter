package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// proxyTokenPrefix marks a Darkrouter proxy credential so one found in a log
// or a config file is identifiable without being tried.
const proxyTokenPrefix = "dr_"

// ProxyToken is one per-client credential. Secret is populated only by
// CreateProxyToken: it is never read back, because the column holds a hash.
type ProxyToken struct {
	ID         string
	Name       string
	Prefix     string
	CreatedAt  time.Time
	LastUsedAt *time.Time
	Secret     string
}

func hashProxyToken(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// CreateProxyToken mints a credential and returns it once. The plaintext is
// not stored, so this return value is the only chance the caller has to show
// it -- a token readable from the database is one a backup leaks.
func (d *DB) CreateProxyToken(ctx context.Context, name string) (ProxyToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ProxyToken{}, fmt.Errorf("generate proxy token: %w", err)
	}
	secret := proxyTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)

	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return ProxyToken{}, fmt.Errorf("generate proxy token id: %w", err)
	}
	tok := ProxyToken{
		ID:        hex.EncodeToString(idBytes),
		Name:      name,
		Prefix:    secret[:len(proxyTokenPrefix)+6],
		CreatedAt: time.Now().UTC(),
		Secret:    secret,
	}
	_, err := d.Write.ExecContext(ctx,
		`INSERT INTO proxy_tokens (id, name, prefix, hash, created_at)
		 VALUES (?, ?, ?, ?, ?)`,
		tok.ID, tok.Name, tok.Prefix, hashProxyToken(secret), tok.CreatedAt.Unix())
	if err != nil {
		return ProxyToken{}, fmt.Errorf("store proxy token: %w", err)
	}
	return tok, nil
}

// ProxyTokens lists what exists without reproducing any of it.
func (d *DB) ProxyTokens(ctx context.Context) ([]ProxyToken, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, name, prefix, created_at, last_used_at
		   FROM proxy_tokens ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list proxy tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []ProxyToken{}
	for rows.Next() {
		var (
			t        ProxyToken
			created  int64
			lastUsed sql.NullInt64
		)
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &created, &lastUsed); err != nil {
			return nil, fmt.Errorf("scan proxy token: %w", err)
		}
		t.CreatedAt = time.Unix(created, 0).UTC()
		if lastUsed.Valid {
			u := time.Unix(lastUsed.Int64, 0).UTC()
			t.LastUsedAt = &u
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteProxyToken revokes one. It reports whether a row was removed so the
// caller can answer 404 rather than pretending an unknown id was revoked.
func (d *DB) DeleteProxyToken(ctx context.Context, id string) (bool, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM proxy_tokens WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete proxy token: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ProxyTokenValid reports whether the presented secret names a live token, and
// records the use.
//
// Looked up by hash rather than compared row by row: the index makes it one
// lookup, and matching a full-length digest leaks nothing about how close a
// wrong guess was.
func (d *DB) ProxyTokenValid(ctx context.Context, secret string) (bool, error) {
	if secret == "" {
		return false, nil
	}
	var id string
	err := d.Read.QueryRowContext(ctx,
		`SELECT id FROM proxy_tokens WHERE hash = ?`, hashProxyToken(secret)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check proxy token: %w", err)
	}
	// Best effort: a failed touch must not refuse a request that authenticated.
	_, _ = d.Write.ExecContext(ctx,
		`UPDATE proxy_tokens SET last_used_at = ? WHERE id = ?`,
		time.Now().UTC().Unix(), id)
	return true, nil
}
