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
		`INSERT INTO provider_keys (id, provider_id, label, kind, ciphertext, nonce, scope, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.ProviderID, c.Label, kind, ciphertext, nonce, c.Scope, enabled)
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
		`SELECT id, provider_id, label, kind, ciphertext, nonce, scope, enabled
		   FROM provider_keys
		  WHERE provider_id = ?
		  ORDER BY id`, providerID)
	if err != nil {
		return nil, fmt.Errorf("list credentials for %q: %w", providerID, err)
	}
	defer rows.Close()

	var out []Credential
	for rows.Next() {
		var (
			c          Credential
			ciphertext []byte
			nonce      []byte
			enabled    int
		)
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.Label, &c.Kind,
			&ciphertext, &nonce, &c.Scope, &enabled); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		plaintext, err := key.Open(ciphertext, nonce, []byte(c.ID))
		if err != nil {
			return nil, fmt.Errorf("credential %s on provider %q could not be decrypted: %w",
				c.ID, providerID, err)
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
	return out, rows.Err()
}
