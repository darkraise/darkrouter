// Package admin serves the operator dashboard: the REST API, session
// authentication, the embedded SPA, and the Vite reverse proxy used in dev.
//
// It is separate from internal/server, which owns the proxy port, because the
// two have opposite security postures. The proxy port is open on the LAN behind
// an optional bearer token; the admin port requires a session and must never
// honor one on the proxy side, since cookies are not port-scoped.
package admin

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// passwordCost is spec §3's bcrypt cost. It is a named constant rather than a
// literal so a downgrade is a visible edit rather than a typo.
const passwordCost = 12

// HashPassword produces a hash for DARKROUTER_ADMIN_PASSWORD_HASH. It exists so
// an operator can generate one with the binary they already have rather than
// installing a second tool.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password is empty")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether password matches hash.
//
// It fails closed on an empty or malformed hash. An unconfigured
// DARKROUTER_ADMIN_PASSWORD_HASH must close the admin port rather than open it,
// and a helper that conflated "nothing configured" with "anything accepted" is
// how a dashboard ends up unauthenticated on a LAN.
func VerifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
