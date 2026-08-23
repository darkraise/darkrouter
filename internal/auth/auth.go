// Package auth resolves a provider's credential into an authorization applied
// to an already-built request.
//
// It exists because authentication is orthogonal to payload shape, master
// design §6.1: a Claude subscription speaks Anthropic Messages and an OpenAI
// one does not, so OAuth cannot be an adapter kind. A preset declares a kind
// and an auth style independently, and this package is where the second half
// lives.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// Authorizer attaches a credential to a request whose body is already
// materialized. It runs after the executor's makeReplayable and before the
// request is sent, which is the only point at which SigV4 can hash a payload
// that will not change afterwards.
//
// A nil Authorizer means the adapter's own header is correct — the static
// styles. That is deliberate: making the static path the zero value keeps every
// caller from branching on a style it does not otherwise care about.
type Authorizer func(ctx context.Context, r *http.Request) error

// ErrUnsupportedStyle is returned for a style no strategy implements. It is an
// error rather than a bearer fallback: a provider row naming a style this build
// does not have would otherwise send its credential in a header the upstream
// ignores, and present as an authentication failure with no cause.
var ErrUnsupportedStyle = errors.New("unsupported auth style")

// Target is what a strategy needs to know about the provider. It is a plain
// struct rather than a provider.Provider for the same reason adapter.Target is:
// provider imports config and store, and a credential strategy has no business
// reaching either.
type Target struct {
	ProviderID string
	Style      string
	Region     string
	Project    string
	Location   string
	// Preset names the shipped entry, so the OAuth strategy can reach the
	// token endpoint and client id without importing catalog.
	Preset string
}

// Credential is one usable credential. Secret is the decrypted plaintext: a
// bare key for a static style, a JSON service-account document for gcp-sa, and
// a marshalled Token for oauth.
type Credential struct {
	ID     string
	Kind   string
	Secret string
}

// Deps carries the collaborators the non-static strategies need. A zero Deps is
// valid and yields a Manager that serves static styles only, which is what
// every existing test wants.
type Deps struct {
	// Tokens persists a refreshed OAuth token. Nil disables refresh, which
	// makes an expiring token an error rather than a silent 401.
	Tokens TokenStore
	// OAuth resolves a preset id to its OAuth endpoints. Nil means no preset
	// data, which the oauth strategy reports rather than guesses around.
	OAuth OAuthPresets
	// HTTP is the client the strategies use for token exchange. Nil uses
	// http.DefaultClient.
	HTTP *http.Client
}

// Manager resolves a (target, credential) pair into an Authorizer and caches
// what is expensive to rebuild — a signer, a token source, a per-account mutex.
type Manager struct {
	deps Deps

	mu    sync.Mutex
	gcp   map[string]*gcpSource
	oauth map[string]*oauthAccount
}

func NewManager(d Deps) *Manager {
	return &Manager{
		deps:  d,
		gcp:   map[string]*gcpSource{},
		oauth: map[string]*oauthAccount{},
	}
}

// For returns the authorizer for one credential, or nil for a static style.
func (m *Manager) For(ctx context.Context, t Target, c Credential) (Authorizer, error) {
	if IsStatic(t.Style) {
		return nil, nil
	}
	switch t.Style {
	case StyleSigV4:
		return m.sigv4(ctx, t, c)
	case StyleGCPSA:
		return m.gcpSA(ctx, t, c)
	case StyleOAuth:
		return m.oauthFor(ctx, t, c)
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedStyle, t.Style)
}

// Placeholders replaced in Tasks 3, 8 and 14. Returning the unsupported error
// rather than nil keeps a half-wired build honest: a provider configured for a
// strategy this commit does not have fails at resolution, naming the style.
func (m *Manager) oauthFor(context.Context, Target, Credential) (Authorizer, error) {
	return nil, fmt.Errorf("%w: %s", ErrUnsupportedStyle, StyleOAuth)
}

// Placeholder types, given their real definitions in Tasks 8 and 14.
type oauthAccount struct{}
