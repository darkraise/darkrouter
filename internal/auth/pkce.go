package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// verifierBytes yields a 43-character verifier, the RFC 7636 minimum, from 32
// bytes of entropy.
const verifierBytes = 32

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// NewVerifier returns a PKCE code verifier.
func NewVerifier() (string, error) { return randomURLSafe(verifierBytes) }

// Challenge is the S256 transformation. Plain is permitted by the RFC and is
// worthless: anyone who intercepts the redirect could exchange the code.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Flow is one in-progress connect attempt.
type Flow struct {
	ProviderID string
	// SessionID binds the flow to the admin session that started it. Spec
	// §5.1: the callback is a state-changing GET reachable via a cross-site
	// top-level redirect, so this check and the unguessable state are the only
	// things standing between a victim's gateway and an attacker's account.
	SessionID string
	Verifier  string
	Label     string
	// RedirectURI is echoed to the token endpoint, which validates that it
	// matches the one the authorize step carried.
	RedirectURI string
	CreatedAt   time.Time
}

var (
	ErrUnknownState = errors.New("unknown or already-used state")
	ErrWrongSession = errors.New("this authorization was not started by this session")
	ErrStateExpired = errors.New("this authorization expired; start it again")
)

// FlowStore holds in-progress flows in memory.
//
// In memory deliberately: a flow lives for the minute or two an operator spends
// in a consent screen, and persisting it would put a PKCE verifier on disk for
// no benefit. A restart mid-flow means clicking Connect again, which is a
// better failure than a verifier surviving into a backup.
type FlowStore struct {
	ttl time.Duration

	mu    sync.Mutex
	flows map[string]Flow
}

func NewFlowStore(ttl time.Duration) *FlowStore {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	return &FlowStore{ttl: ttl, flows: map[string]Flow{}}
}

// Begin records a flow and returns its state parameter.
func (s *FlowStore) Begin(f Flow) (string, error) {
	state, err := randomURLSafe(32)
	if err != nil {
		return "", err
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[state] = f
	return state, nil
}

// Claim consumes a state and returns its flow.
//
// A session mismatch does NOT consume the state: the legitimate operator's own
// callback may still be on its way, and letting an attacker's failed attempt
// invalidate it turns a blocked attack into a denial of service.
func (s *FlowStore) Claim(state, sessionID string) (Flow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, ok := s.flows[state]
	if !ok {
		return Flow{}, ErrUnknownState
	}
	if time.Since(f.CreatedAt) > s.ttl {
		delete(s.flows, state)
		return Flow{}, ErrStateExpired
	}
	// Constant time for the same reason the CSRF check is: the comparison is
	// against a value an attacker supplies and can iterate on.
	if subtle.ConstantTimeCompare([]byte(f.SessionID), []byte(sessionID)) != 1 {
		return Flow{}, ErrWrongSession
	}
	delete(s.flows, state)
	return f, nil
}

// Sweep drops expired flows. Called from the same worker that sweeps sessions.
func (s *FlowStore) Sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for state, f := range s.flows {
		if now.Sub(f.CreatedAt) > s.ttl {
			delete(s.flows, state)
		}
	}
}
