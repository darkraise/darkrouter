package auth

import (
	"encoding/json"
	"fmt"
	"time"
)

// Token is what a non-static credential stores in place of a bare key.
//
// The whole document is sealed into one ciphertext column rather than split
// across several. Spec §5.2 requires the new pair to be persisted before the
// old is considered replaced, and a single-column write is the only version of
// that a crash cannot tear in half.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Scopes       []string  `json:"scopes,omitempty"`

	// Account is whatever the vendor calls the connected identity, kept only
	// so the dashboard can say which account a credential belongs to. It is
	// display text and nothing routes on it.
	Account string `json:"account,omitempty"`
}

func (t Token) Marshal() ([]byte, error) {
	raw, err := json.Marshal(t)
	if err != nil {
		return nil, fmt.Errorf("marshal token: %w", err)
	}
	return raw, nil
}

func ParseToken(raw []byte) (Token, error) {
	var t Token
	if err := json.Unmarshal(raw, &t); err != nil {
		return Token{}, fmt.Errorf("parse stored token: %w", err)
	}
	return t, nil
}

// Header returns the Authorization value. Vendors are inconsistent about the
// token type; an empty one is Bearer, which is what every OAuth2 provider in
// the preset set actually issues.
func (t Token) Header() string {
	typ := t.TokenType
	if typ == "" {
		typ = "Bearer"
	}
	return typ + " " + t.AccessToken
}

// Expired reports whether the token should be refreshed before use.
//
// The zero expiry means the vendor stated none, which is not the same as
// "expired in 1970": treating it that way would refresh on every request and
// burn a rotating refresh token per call.
func (t Token) Expired(now time.Time, delta time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return false
	}
	return !now.Add(delta).Before(t.ExpiresAt)
}

// Unix returns the expiry for the expires_at column, or nil when there is none.
func (t Token) Unix() *int64 {
	if t.ExpiresAt.IsZero() {
		return nil
	}
	u := t.ExpiresAt.Unix()
	return &u
}
