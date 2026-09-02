package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/crypto"
)

// Credential is a provider credential. Secret is plaintext and exists only in
// memory: it is sealed on the way in and unsealed on the way out.
type Credential struct {
	ID         string
	ProviderID string
	Label      string
	Kind       string
	Secret     string
	Enabled    bool
	Scope      string

	// ExpiresAt mirrors the token's expiry into its own column. It is not a
	// secret, and the refresh worker has to find rows expiring soon without
	// decrypting every credential in the database on every tick.
	ExpiresAt *int64
}

// newID returns a ULID. Application-generated ids matter here for the same
// reason they do for requests: the id must exist before the row does, because
// it is the AAD the ciphertext is bound to.
func newID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// AddCredential seals and inserts one credential on the FULL-sync handle.
func (d *DB) AddCredential(ctx context.Context, key *crypto.Key, c Credential) (string, error) {
	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin credential insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := insertCredentialTx(ctx, tx, key, c)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit credential insert: %w", err)
	}
	return id, nil
}

// insertCredentialTx seals and inserts within a caller-provided transaction, so
// the first-run import can write every provider and credential atomically.
func insertCredentialTx(ctx context.Context, e execer, key *crypto.Key, c Credential) (string, error) {
	if c.Secret == "" {
		return "", fmt.Errorf("credential for provider %q has an empty secret", c.ProviderID)
	}
	id := c.ID
	if id == "" {
		id = newID()
	}
	kind := c.Kind
	if kind == "" {
		kind = "static"
	}

	// The id is the additional authenticated data, which is what stops this
	// ciphertext being moved to another row undetected.
	ciphertext, nonce, err := key.Seal([]byte(c.Secret), []byte(id))
	if err != nil {
		return "", err
	}

	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err = e.ExecContext(ctx,
		`INSERT INTO provider_keys (id, provider_id, label, kind, ciphertext, nonce, scope, enabled, expires_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.ProviderID, c.Label, kind, ciphertext, nonce, c.Scope, enabled, c.ExpiresAt)
	if err != nil {
		return "", fmt.Errorf("insert credential for provider %q: %w", c.ProviderID, err)
	}
	return id, nil
}

// Credentials returns every credential for a provider with its secret
// decrypted. A row that fails authentication is an error rather than a skip:
// silently dropping it would present as a provider with no credentials, which
// is indistinguishable from one that was never configured.
func (d *DB) Credentials(ctx context.Context, key *crypto.Key, providerID string) ([]Credential, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, provider_id, label, kind, ciphertext, nonce, scope, enabled, expires_at
		   FROM provider_keys
		  WHERE provider_id = ?
		  ORDER BY id`, providerID)
	if err != nil {
		return nil, fmt.Errorf("list credentials for %q: %w", providerID, err)
	}
	defer rows.Close()
	return scanCredentials(rows, key)
}

// scanCredentials decrypts one result set. A row that fails authentication is
// an error rather than a skip: silently dropping it would present as a provider
// with no credentials, which is indistinguishable from one never configured.
func scanCredentials(rows *sql.Rows, key *crypto.Key) ([]Credential, error) {
	var out []Credential
	for rows.Next() {
		var (
			c          Credential
			ciphertext []byte
			nonce      []byte
			enabled    int
		)
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.Label, &c.Kind,
			&ciphertext, &nonce, &c.Scope, &enabled, &c.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		plaintext, err := key.Open(ciphertext, nonce, []byte(c.ID))
		if err != nil {
			return nil, fmt.Errorf("credential %s on provider %q could not be decrypted: %w",
				c.ID, c.ProviderID, err)
		}
		c.Secret = string(plaintext)
		c.Enabled = enabled == 1
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credentials: %w", err)
	}
	return out, nil
}

// ReplaceCredentialSecret rewrites one credential's sealed payload in place.
//
// In place matters: the ciphertext is bound to the credential id as additional
// authenticated data, so a rotation that inserted a new row and deleted the old
// would change the AAD and produce something nothing can open. It is also what
// makes spec §5.2's ordering hold — one row, one column, one write, so a crash
// leaves either the old pair or the new one and never a mixture.
func (d *DB) ReplaceCredentialSecret(ctx context.Context, key *crypto.Key,
	id, secret string, expiresAt *int64) error {
	return d.replaceSecret(ctx, key, "", id, secret, expiresAt)
}

// ReplaceProviderCredentialSecret is ReplaceCredentialSecret scoped to one
// provider, for a caller whose credential id arrived in a URL beside the
// provider's: an id that exists under another provider must not match.
func (d *DB) ReplaceProviderCredentialSecret(ctx context.Context, key *crypto.Key,
	providerID, id, secret string, expiresAt *int64) error {
	return d.replaceSecret(ctx, key, providerID, id, secret, expiresAt)
}

func (d *DB) replaceSecret(ctx context.Context, key *crypto.Key,
	providerID, id, secret string, expiresAt *int64) error {

	if secret == "" {
		return fmt.Errorf("refusing to store an empty credential for %s", id)
	}
	ciphertext, nonce, err := key.Seal([]byte(secret), []byte(id))
	if err != nil {
		return fmt.Errorf("seal credential %s: %w", id, err)
	}
	q := `UPDATE provider_keys SET ciphertext = ?, nonce = ?, expires_at = ? WHERE id = ?`
	args := []any{ciphertext, nonce, expiresAt, id}
	if providerID != "" {
		q += ` AND provider_id = ?`
		args = append(args, providerID)
	}
	res, err := d.Sync.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("replace credential %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("replace credential %s: %w", id, err)
	}
	if n == 0 {
		// The credential was deleted while a refresh was in flight. Silently
		// succeeding would leave the worker believing it had persisted a token
		// that does not exist.
		return fmt.Errorf("credential %s no longer exists: %w", id, ErrNotFound)
	}
	return nil
}

// CredentialSummary is a credential without its secret: everything a listing
// or a count needs, readable without touching the keyring.
type CredentialSummary struct {
	ID         string
	ProviderID string
	Label      string
	Kind       string
	Enabled    bool
	Scope      string
	ExpiresAt  *int64
}

// CredentialSummaries returns every provider's credentials keyed by provider
// id, in one query and without decrypting. An overview polled every few
// seconds must not unseal every secret in the database each time it counts
// them.
func (d *DB) CredentialSummaries(ctx context.Context) (map[string][]CredentialSummary, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, provider_id, label, kind, enabled, scope, expires_at
		   FROM provider_keys ORDER BY provider_id, id`)
	if err != nil {
		return nil, fmt.Errorf("list credential summaries: %w", err)
	}
	defer rows.Close()

	out := map[string][]CredentialSummary{}
	for rows.Next() {
		var c CredentialSummary
		var enabled int
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.Label, &c.Kind,
			&enabled, &c.Scope, &c.ExpiresAt); err != nil {
			return nil, fmt.Errorf("scan credential summary: %w", err)
		}
		c.Enabled = enabled == 1
		out[c.ProviderID] = append(out[c.ProviderID], c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list credential summaries: %w", err)
	}
	return out, nil
}

// ExpiringCredentials returns every enabled credential of one kind whose expiry
// is at or before before, decrypted, oldest first.
func (d *DB) ExpiringCredentials(ctx context.Context, key *crypto.Key,
	kind string, before int64) ([]Credential, error) {

	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, provider_id, label, kind, ciphertext, nonce, scope, enabled, expires_at
		   FROM provider_keys
		  WHERE kind = ? AND enabled = 1 AND expires_at IS NOT NULL AND expires_at <= ?
		  ORDER BY expires_at`, kind, before)
	if err != nil {
		return nil, fmt.Errorf("list expiring credentials: %w", err)
	}
	defer rows.Close()
	return scanCredentials(rows, key)
}

// DisableCredential takes a credential out of rotation and records why.
//
// The reason lands in scope, which is the column master design §11 already
// gives to per-credential text and which nothing else writes for an OAuth row.
// Adding a column for a string the dashboard renders once would be schema churn
// for a display concern.
func (d *DB) DisableCredential(ctx context.Context, id, reason string) error {
	_, err := d.Sync.ExecContext(ctx,
		`UPDATE provider_keys SET enabled = 0, scope = ? WHERE id = ?`, reason, id)
	if err != nil {
		return fmt.Errorf("disable credential %s: %w", id, err)
	}
	return nil
}

type sealedRow struct {
	id         string
	ciphertext []byte
	nonce      []byte
}

// allCredentialRows returns every sealed row across all providers. Rotation
// needs the raw bytes rather than the plaintext, so it does not share
// Credentials' decryption path.
func allCredentialRows(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}) ([]sealedRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, ciphertext, nonce FROM provider_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list sealed credentials: %w", err)
	}
	defer rows.Close()

	var out []sealedRow
	for rows.Next() {
		var r sealedRow
		if err := rows.Scan(&r.id, &r.ciphertext, &r.nonce); err != nil {
			return nil, fmt.Errorf("scan sealed credential: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sealed credentials: %w", err)
	}
	return out, nil
}

// SetCredentialEnabled turns one credential on or off without deleting it, so
// an operator can take a key out of rotation and put it back without pasting
// the secret again. It reports whether the credential exists.
func (d *DB) SetCredentialEnabled(ctx context.Context, providerID, keyID string, enabled bool) (bool, error) {
	res, err := d.Sync.ExecContext(ctx,
		`UPDATE provider_keys SET enabled = ? WHERE id = ? AND provider_id = ?`,
		enabled, keyID, providerID)
	if err != nil {
		return false, fmt.Errorf("set credential %s enabled: %w", keyID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("set credential %s enabled: %w", keyID, err)
	}
	return n > 0, nil
}
