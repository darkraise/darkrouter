package server

import (
	"context"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/store"
)

// tokenStore binds the keyring to the store's credential methods, so
// internal/auth can persist a rotated token without knowing about encryption.
type tokenStore struct {
	db  *store.DB
	key *crypto.Key
}

func (t tokenStore) ReplaceCredentialSecret(ctx context.Context, id, secret string, expiresAt *int64) error {
	return t.db.ReplaceCredentialSecret(ctx, t.key, id, secret, expiresAt)
}

func (t tokenStore) DisableCredential(ctx context.Context, id, reason string) error {
	return t.db.DisableCredential(ctx, id, reason)
}

// Expiring lists OAuth credentials due for renewal, joined to the provider row
// their strategy is resolved from.
func (t tokenStore) Expiring(ctx context.Context, kind string, before int64) ([]auth.StoredCredential, error) {
	creds, err := t.db.ExpiringCredentials(ctx, t.key, kind, before)
	if err != nil {
		return nil, err
	}
	rows, err := t.db.ProviderRows(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]store.ProviderRow, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]auth.StoredCredential, 0, len(creds))
	for _, c := range creds {
		p, ok := byID[c.ProviderID]
		if !ok {
			// The provider was deleted between the two reads. Its credentials
			// went with it by cascade, so there is nothing to renew.
			continue
		}
		out = append(out, auth.StoredCredential{
			ID: c.ID, ProviderID: c.ProviderID, Kind: c.Kind, Secret: c.Secret,
			Preset: p.Preset, Style: p.AuthStyle,
		})
	}
	return out, nil
}

// presetOAuth resolves a preset id to its OAuth endpoints, translating
// catalog's vocabulary into auth's. The two carry the same fields under
// different names on purpose: auth must not import catalog.
type presetOAuth struct{ presets catalog.Presets }

func (p presetOAuth) OAuthFor(preset string) (auth.OAuthConfig, bool) {
	entry, ok := p.presets[preset]
	if !ok || entry.OAuth == nil {
		return auth.OAuthConfig{}, false
	}
	return auth.OAuthConfig{
		AuthorizeURL: entry.OAuth.AuthorizeURL,
		TokenURL:     entry.OAuth.TokenURL,
		ClientID:     entry.OAuth.ClientID,
		Scopes:       entry.OAuth.Scopes,
	}, true
}
