package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// DefaultRefreshDelta is how far ahead of expiry a token is renewed.
//
// oauth2.ReuseTokenSource has its own delta, but it is ten seconds and
// unexported. Spec §4.2 requires that no request fails on an expiry race, and
// a minute covers a slow upstream on a request that started just inside the
// window. Holding our own constant is also what makes the behavior testable
// rather than an assertion about somebody else's package.
const DefaultRefreshDelta = time.Minute

// cloudPlatformScope is the only scope Vertex inference needs. Narrower scopes
// exist but are not honored uniformly across the aiplatform surface.
const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// gcpSource is the single cache in front of the JWT exchange, holding our
// expiry delta and a mutex so concurrent requests finding an expiring token
// perform one exchange rather than one each.
//
// It builds a fresh oauth2.TokenSource for each exchange rather than holding
// one, and that is not an oversight. jwt.Config.TokenSource returns a
// ReuseTokenSource whose own expiry delta is ten seconds and unexported: a
// long-lived one keeps answering with a token forty seconds from expiry, so
// wrapping it would make DefaultRefreshDelta decorative. A fresh source starts
// empty and always performs the exchange, which puts the decision here where
// spec §4.2 asks for it — and where a test can reach it.
type gcpSource struct {
	mu     waitMutex
	newSrc func(context.Context) oauth2.TokenSource
	tok    *oauth2.Token
	delta  time.Duration
	now    func() time.Time
}

func (g *gcpSource) Token(ctx context.Context) (string, error) {
	if err := g.mu.lock(ctx); err != nil {
		return "", err
	}
	if g.tok != nil && g.tok.AccessToken != "" {
		if g.tok.Expiry.IsZero() || g.now().Add(g.delta).Before(g.tok.Expiry) {
			tok := g.tok.AccessToken
			g.mu.unlock()
			return tok, nil
		}
	}
	return detach(ctx, func(ctx context.Context) (string, error) {
		defer g.mu.unlock()
		tok, err := g.newSrc(ctx).Token()
		if err != nil {
			// The wrapper's message names the exchange; oauth2's own error
			// carries the endpoint's response body, which is the useful half.
			// Neither carries the key.
			return "", fmt.Errorf("service-account token exchange failed: %w", err)
		}
		g.tok = tok
		return tok.AccessToken, nil
	})
}

// gcpSA resolves a service-account credential.
//
// The source is cached per credential because the executor resolves once per
// attempt: rebuilding it each time would sign a fresh JWT and make a fresh
// round trip to Google for every single request.
func (m *Manager) gcpSA(ctx context.Context, t Target, c Credential) (Authorizer, error) {
	if t.Project == "" {
		return nil, fmt.Errorf("provider %q uses gcp-sa but declares no project", t.ProviderID)
	}
	cfg, err := google.JWTConfigFromJSON([]byte(c.Secret), cloudPlatformScope)
	if err != nil {
		// The document is malformed. The error from JWTConfigFromJSON names
		// the field, never the key material.
		return nil, fmt.Errorf("provider %q: service-account key: %w", t.ProviderID, err)
	}

	key := c.ID + ":" + fingerprint(c.Secret)
	m.mu.Lock()
	src, ok := m.gcp[key]
	if !ok {
		// The JWT exchange posts through whatever client the context names and
		// ignores the context otherwise, so the client's own timeout is the
		// only bound it has. Unnamed, oauth2 falls back to http.DefaultClient,
		// which has none.
		client := boundedClient(m.deps.HTTP)
		src = &gcpSource{
			mu: newWaitMutex(),
			newSrc: func(ctx context.Context) oauth2.TokenSource {
				return cfg.TokenSource(context.WithValue(ctx, oauth2.HTTPClient, client))
			},
			delta: DefaultRefreshDelta,
			now:   time.Now,
		}
		m.gcp[key] = src
	}
	m.mu.Unlock()

	return func(ctx context.Context, r *http.Request) error {
		tok, err := src.Token(ctx)
		if err != nil {
			return err
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		return nil
	}, nil
}

// boundedClient is c with a timeout, or the bounded fallback when c is nil.
func boundedClient(c *http.Client) *http.Client {
	if c == nil {
		return tokenClient
	}
	if c.Timeout > 0 {
		return c
	}
	bounded := *c
	bounded.Timeout = tokenTimeout
	return &bounded
}

// fingerprint keys the cache on the credential's content as well as its id, so
// replacing a key in place invalidates the cached source rather than serving
// tokens minted from the old one until restart.
func fingerprint(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:8])
}
