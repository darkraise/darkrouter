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
	"sync/atomic"

	"golang.org/x/crypto/bcrypt"
)

// passwordCost is spec §3's bcrypt cost. It is a named constant rather than a
// literal so a downgrade is a visible edit rather than a typo.
const passwordCost = 12

// verifyCalls counts password comparisons. It exists so a test can assert that
// an unknown username still costs a bcrypt comparison, which is what keeps the
// miss from being timeable. Nothing in the request path reads it.
var verifyCalls atomic.Uint64

// dummyHash is compared against when a username does not resolve, so a miss
// costs the same work as a hit. Generated once at startup rather than being a
// constant, so it carries this build's cost parameter.
var dummyHash = func() string {
	h, err := HashPassword("darkrouter-dummy-password-never-valid")
	if err != nil {
		panic("cannot hash the dummy password: " + err.Error())
	}
	return h
}()

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
	verifyCalls.Add(1)
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
