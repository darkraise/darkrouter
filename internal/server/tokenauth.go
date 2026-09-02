package server

import (
	"context"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// tokenCacheTTL bounds two things at once: how often an accepted token's use
// is recorded, and how long a revoked token keeps working.
const tokenCacheTTL = 5 * time.Second

// proxyTokenSource is the slice of the store the proxy-token check needs.
type proxyTokenSource interface {
	// ProxyTokenValid reports whether the secret names a live token and
	// records its use.
	ProxyTokenValid(ctx context.Context, secret string) (bool, error)
	ProxyTokens(ctx context.Context) ([]store.ProxyToken, error)
}

// tokenAuth answers the per-request proxy-token check from memory.
//
// The store's check writes last_used_at on every hit, which put a SQLite
// write on the hot path of every authenticated request. An accepted token is
// remembered for tokenCacheTTL instead, so the store — and its write — is
// consulted once per window per token. Refusals are never cached: a wrong
// token is a read, and remembering it would only give a guesser a map to
// avoid.
type tokenAuth struct {
	src proxyTokenSource
	now func() time.Time

	mu       sync.Mutex
	accepted map[string]time.Time
	// anyAt is when anyTokens was last read. Whether any token exists at all
	// decides whether an unauthenticated request is allowed, which is asked
	// on every request that carries no valid token.
	anyAt     time.Time
	anyTokens bool
}

func newTokenAuth(src proxyTokenSource) *tokenAuth {
	return &tokenAuth{src: src, now: time.Now, accepted: make(map[string]time.Time)}
}

// accept reports whether secret is a live per-client token.
func (a *tokenAuth) accept(ctx context.Context, secret string) bool {
	if secret == "" || a.src == nil {
		return false
	}
	now := a.now()
	a.mu.Lock()
	at, ok := a.accepted[secret]
	a.mu.Unlock()
	if ok && now.Sub(at) < tokenCacheTTL {
		return true
	}
	valid, err := a.src.ProxyTokenValid(ctx, secret)
	if err != nil || !valid {
		a.mu.Lock()
		delete(a.accepted, secret)
		a.mu.Unlock()
		return false
	}
	a.mu.Lock()
	a.accepted[secret] = now
	a.mu.Unlock()
	return true
}

// configured reports whether any per-client token exists. A store that cannot
// answer is treated as "yes": refusing an unauthenticated request is the safe
// answer when the store cannot say.
func (a *tokenAuth) configured(ctx context.Context) bool {
	if a.src == nil {
		return false
	}
	now := a.now()
	a.mu.Lock()
	if !a.anyAt.IsZero() && now.Sub(a.anyAt) < tokenCacheTTL {
		v := a.anyTokens
		a.mu.Unlock()
		return v
	}
	a.mu.Unlock()

	toks, err := a.src.ProxyTokens(ctx)
	v := err != nil || len(toks) > 0
	a.mu.Lock()
	a.anyAt, a.anyTokens = now, v
	a.mu.Unlock()
	return v
}
