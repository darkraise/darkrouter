package server

import (
	"context"
	"sync"
	"time"
)

// tokenCacheTTL bounds two things at once: how often an accepted token's use
// is recorded, and how long a revoked token keeps working.
const tokenCacheTTL = 5 * time.Second

// proxyTokenSource is the slice of the store the proxy-token check needs.
type proxyTokenSource interface {
	// ProxyTokenValid reports whether the secret names a live token and
	// records its use.
	ProxyTokenValid(ctx context.Context, secret string) (bool, error)
	ProxyTokensIssued(ctx context.Context) (bool, error)
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
	// anyAt is when anyTokens was last read. Whether a token was ever issued
	// decides whether an unauthenticated request is allowed, which is asked
	// on every request that carries no valid token.
	anyAt     time.Time
	anyTokens bool
	// issued latches once the store has said a token was issued: issuance is
	// permanent, so no later answer may turn authentication back off. A store
	// error refuses for one window but does not latch, since it proves nothing.
	issued bool
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

// configured reports whether a per-client token has ever been issued, whether
// or not any is still live. A store that cannot answer is treated as "yes":
// refusing an unauthenticated request is the safe answer when the store cannot
// say.
func (a *tokenAuth) configured(ctx context.Context) bool {
	if a.src == nil {
		return false
	}
	now := a.now()
	a.mu.Lock()
	if a.issued {
		a.mu.Unlock()
		return true
	}
	if !a.anyAt.IsZero() && now.Sub(a.anyAt) < tokenCacheTTL {
		v := a.anyTokens
		a.mu.Unlock()
		return v
	}
	a.mu.Unlock()

	issued, err := a.src.ProxyTokensIssued(ctx)
	v := err != nil || issued
	a.mu.Lock()
	a.anyAt, a.anyTokens = now, v
	if err == nil && issued {
		a.issued = true
	}
	a.mu.Unlock()
	return v
}
