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

// verifyCalls counts bcrypt comparisons, not calls to VerifyPassword: it is
// incremented past the empty-hash guard, so a call that returns early without
// hashing anything does not move it. It exists so a test can assert that an
// unknown username still costs a real comparison, which is what keeps the miss
// from being timeable. Counting entries instead would leave the test green
// while a caller passed an empty hash and answered in microseconds. Nothing in
// the request path reads it.
var verifyCalls atomic.Uint64

// hashCalls counts bcrypt hash generations, for the reason verifyCalls counts
// comparisons: the claim endpoint is unauthenticated, so a claim arriving at a
// console that was claimed months ago must be refused before it pays for one.
// Only a counter separates a refusal that hashed first from one that did not --
// both answer 409, and both answer it fast enough on an idle machine to look
// alike. Nothing in the request path reads it.
var hashCalls atomic.Uint64

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

// HashPassword produces the stored hash for an account's password. Every path
// that writes users.password_hash -- the claim and a password change -- goes
// through it, so the cost parameter has one home.
func HashPassword(password string) (string, error) {
	return hashPasswordAtCost(password, passwordCost)
}

// hashPasswordAtCost lets handler tests use real bcrypt with inexpensive
// credentials. Production always enters through HashPassword at cost 12.
func hashPasswordAtCost(password string, cost int) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password is empty")
	}
	hashCalls.Add(1)
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether password matches hash.
//
// It fails closed on an empty or malformed hash. An account row with no usable
// hash must close the admin port rather than open it, and a helper that
// conflated "nothing stored" with "anything accepted" is how a dashboard ends
// up unauthenticated on a LAN.
func VerifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	verifyCalls.Add(1)
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
