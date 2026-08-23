package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/darkraise/darkrouter/internal/store"
)

// csrfSecretKey is the settings row holding the HMAC secret.
const csrfSecretKey = "csrf_secret"

// CSRF derives per-session tokens.
//
// Spec §3 rejects naive double-submit: an attacker who can set a cookie for the
// host defeats it, and on a plain-HTTP LAN — the default homelab posture, since
// there is no TLS configuration — an active network attacker can do exactly
// that. Binding the token to the session by HMAC means a planted token is
// invalid for the session it arrives with.
//
// The token is derived rather than stored. A column on sessions could drift from
// the session it names — deleted separately, restored from a backup separately.
// A derivation cannot.
type CSRF struct {
	secret []byte
}

// NewCSRF loads the secret, creating it on first use.
//
// The secret lives in the database rather than in the process so tokens survive
// a restart; a per-process secret would log the operator out on every deploy. It
// is its own value rather than the credential encryption key, because reusing an
// encryption key for authentication tags violates key separation for no gain.
func NewCSRF(ctx context.Context, db *store.DB) (*CSRF, error) {
	var enc string
	err := db.Read.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, csrfSecretKey).Scan(&enc)
	if err == nil {
		secret, derr := base64.StdEncoding.DecodeString(enc)
		if derr != nil {
			return nil, fmt.Errorf("csrf secret is not valid base64: %w", derr)
		}
		return &CSRF{secret: secret}, nil
	}

	secret := make([]byte, 32)
	if _, rerr := rand.Read(secret); rerr != nil {
		return nil, fmt.Errorf("generate csrf secret: %w", rerr)
	}
	// INSERT OR IGNORE rather than INSERT: two processes opening the same
	// database concurrently must not fail, and whichever wrote first wins.
	if _, werr := db.Write.ExecContext(ctx,
		`INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`,
		csrfSecretKey, base64.StdEncoding.EncodeToString(secret)); werr != nil {
		return nil, fmt.Errorf("store csrf secret: %w", werr)
	}
	// Re-read rather than trusting the local value: if the ignore fired, the
	// other process's secret is the one in the database and the one every
	// outstanding token was minted under.
	if rerr := db.Read.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, csrfSecretKey).Scan(&enc); rerr != nil {
		return nil, fmt.Errorf("read back csrf secret: %w", rerr)
	}
	stored, derr := base64.StdEncoding.DecodeString(enc)
	if derr != nil {
		return nil, fmt.Errorf("csrf secret is not valid base64: %w", derr)
	}
	return &CSRF{secret: stored}, nil
}

// Token is the token for one session.
func (c *CSRF) Token(sessionID string) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Valid reports whether token belongs to sessionID.
//
// hmac.Equal rather than ==: a byte-by-byte comparison that returns early leaks
// how much of a forged token was correct, which is enough to construct one.
func (c *CSRF) Valid(sessionID, token string) bool {
	if sessionID == "" || token == "" {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(sessionID))
	return hmac.Equal(got, mac.Sum(nil))
}
