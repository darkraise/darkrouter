package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuthConfig is the preset's OAuth block, restated here so internal/auth does
// not import catalog. The admin package translates.
type OAuthConfig struct {
	AuthorizeURL string
	TokenURL     string
	ClientID     string
	Scopes       []string
}

// OAuthPresets resolves a preset id to its OAuth endpoints.
type OAuthPresets interface {
	OAuthFor(preset string) (OAuthConfig, bool)
}

// TokenStore persists a refreshed credential. It is the narrow half of *store.DB
// this package needs, which keeps auth from importing store.
type TokenStore interface {
	ReplaceCredentialSecret(ctx context.Context, id, secret string, expiresAt *int64) error
	DisableCredential(ctx context.Context, id, reason string) error
}

// ErrNeedsReconnect marks a terminal refusal. The credential is disabled and no
// retry follows: hammering a refused refresh endpoint is how an account gets
// locked rather than recovered.
var ErrNeedsReconnect = errors.New("this account must be reconnected")

const reconnectReason = "reconnect required: the provider refused the refresh"

type ExchangeInput struct {
	Code        string
	Verifier    string
	RedirectURI string
}

// ExchangeCode trades an authorization code for a token pair.
func ExchangeCode(ctx context.Context, c *http.Client, cfg OAuthConfig, in ExchangeInput) (Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {in.Code},
		"client_id":     {cfg.ClientID},
		"redirect_uri":  {in.RedirectURI},
		"code_verifier": {in.Verifier},
	}
	return postToken(ctx, c, cfg.TokenURL, form)
}

func refreshToken(ctx context.Context, c *http.Client, cfg OAuthConfig, refresh string) (Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"client_id":     {cfg.ClientID},
	}
	return postToken(ctx, c, cfg.TokenURL, form)
}

type wireToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Account      struct {
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func postToken(ctx context.Context, c *http.Client, tokenURL string, form url.Values) (Token, error) {
	if c == nil {
		c = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.Do(req)
	if err != nil {
		// Network error: transient by definition, so no terminal marker.
		return Token{}, fmt.Errorf("token endpoint unreachable: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Token{}, err
	}

	var w wireToken
	// A body that is not JSON is still a failure worth reporting by status.
	_ = json.Unmarshal(raw, &w)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if terminal(resp.StatusCode, w.Error) {
			return Token{}, fmt.Errorf("%w: %s", ErrNeedsReconnect, describe(w))
		}
		return Token{}, fmt.Errorf("token endpoint returned %s: %s", resp.Status, describe(w))
	}
	if w.AccessToken == "" {
		return Token{}, fmt.Errorf("%w: the token endpoint returned no access token", ErrNeedsReconnect)
	}

	tok := Token{
		AccessToken:  w.AccessToken,
		RefreshToken: w.RefreshToken,
		TokenType:    w.TokenType,
		Account:      w.Account.EmailAddress,
	}
	if w.ExpiresIn > 0 {
		tok.ExpiresAt = time.Now().Add(time.Duration(w.ExpiresIn) * time.Second)
	}
	if w.Scope != "" {
		tok.Scopes = strings.Fields(w.Scope)
	}
	return tok, nil
}

// terminal separates the two failure modes spec §5.2 distinguishes. Getting
// this wrong is bad in both directions: a transient 500 treated as terminal
// turns a five-minute outage into a manual reconnection, and a terminal refusal
// treated as transient hammers the endpoint until the account locks.
func terminal(status int, code string) bool {
	switch code {
	case "invalid_grant", "invalid_client", "unauthorized_client", "invalid_scope":
		return true
	}
	// A bare 400 or 401 with no recognizable code is still a refusal of this
	// credential rather than an outage.
	return status == http.StatusBadRequest || status == http.StatusUnauthorized
}

// describe renders the provider's own words without any token material: the
// error code and description are the useful half of the body.
func describe(w wireToken) string {
	switch {
	case w.Error != "" && w.ErrorDescription != "":
		return w.Error + ": " + w.ErrorDescription
	case w.Error != "":
		return w.Error
	}
	return "no error detail"
}
