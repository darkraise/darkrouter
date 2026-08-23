package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

// listenerTTL bounds how long a temporary redirect listener stays bound. It
// matches the flow store's own expiry: a state that can no longer be claimed
// has no listener worth keeping.
const listenerTTL = 10 * time.Minute

type oauthStartBody struct {
	Label string `json:"label"`
}

// handleOAuthStart begins an authorization-code flow with PKCE.
//
// The response carries the authorize URL rather than redirecting to it. The
// caller is a fetch from the SPA, and a 302 to a vendor's consent screen would
// be followed by the fetch rather than by the browser.
func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	preset, cfg, ok := s.oauthConfig(r.Context(), id)
	if !ok {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("provider %q is not configured for an OAuth credential", id))
		return
	}

	var body oauthStartBody
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)

	verifier, err := auth.NewVerifier()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sessionID := sessionFrom(r.Context())
	flow := auth.Flow{
		ProviderID: id, SessionID: sessionID, Verifier: verifier,
		Label: body.Label, RedirectURI: redirectURI(cfg), CreatedAt: time.Now(),
	}
	state, err := s.deps.Flows.Begin(flow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", flow.RedirectURI)
	q.Set("scope", strings.Join(cfg.Scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", auth.Challenge(verifier))
	// S256 only. Plain is permitted by RFC 7636 and worthless: anyone who
	// intercepts the redirect could exchange the code themselves.
	q.Set("code_challenge_method", "S256")

	out := map[string]any{
		"authorize_url": cfg.AuthorizeURL + "?" + q.Encode(),
		"state":         state,
		"redirect_uri":  flow.RedirectURI,
		// The UI shows a paste box for "manual" and also waits for the listener
		// on "localhost". Both validate identically on the server.
		"style":  cfg.Redirect.Style,
		"preset": preset,
	}
	if cfg.Redirect.Style == "localhost" && cfg.Redirect.Port != 0 {
		if err := s.startListener(flow, cfg.Redirect.Port, sessionID, listenerTTL); err != nil {
			// Not fatal. The paste path always works, spec §5.1, and telling
			// the operator to use it beats failing the whole flow over a busy
			// port — often busy because the vendor's own CLI holds it.
			out["style"] = "manual"
			out["listener_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// redirectURI is what the vendor registered, not Darkrouter's admin origin.
//
// Spec §5.1: a homelab admin origin is rejected at the authorize step before
// any code exists, which is why the paste path is the default rather than the
// fallback.
func redirectURI(cfg catalog.OAuth) string {
	if cfg.Redirect.Style == "localhost" && cfg.Redirect.Port != 0 {
		return fmt.Sprintf("http://localhost:%d/callback", cfg.Redirect.Port)
	}
	// The out-of-band page. The operator still pastes the resulting URL back.
	return "urn:ietf:wg:oauth:2.0:oob"
}

type oauthCompleteBody struct {
	// RedirectedURL is the whole URL the browser landed on. Asking for the code
	// alone makes the operator parse a query string by eye, and taking the URL
	// means state travels with it — which is what makes this path validate
	// identically to the listener path.
	RedirectedURL string `json:"redirected_url"`
}

func (s *Server) handleOAuthComplete(w http.ResponseWriter, r *http.Request) {
	var body oauthCompleteBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	code, state, err := parseRedirected(body.RedirectedURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	credID, label, account, err := s.completeOAuth(r.Context(), code, state, sessionFrom(r.Context()))
	if err != nil {
		writeError(w, statusForOAuth(err), err.Error())
		return
	}
	// The token is never echoed. What comes back is what the dashboard shows.
	writeJSON(w, http.StatusCreated, map[string]any{
		"credential_id": credID, "label": label, "account": account,
	})
}

// parseRedirected pulls the code and state out of a pasted URL, and surfaces
// the provider's own refusal rather than reporting a missing code.
func parseRedirected(raw string) (code, state string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("paste the whole URL the browser landed on")
	}
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", fmt.Errorf("that does not look like a URL: %w", perr)
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		desc := q.Get("error_description")
		if desc == "" {
			return "", "", fmt.Errorf("the provider refused the authorization: %s", e)
		}
		return "", "", fmt.Errorf("the provider refused the authorization: %s (%s)", e, desc)
	}
	code, state = q.Get("code"), q.Get("state")
	if code == "" || state == "" {
		return "", "", fmt.Errorf("that URL carries no authorization code")
	}
	return code, state, nil
}

// completeOAuth is shared by the paste path and the listener path, which is how
// spec §7's "the manual-paste path validating identically to the listener path"
// is true by construction rather than by inspection.
func (s *Server) completeOAuth(ctx context.Context, code, state, sessionID string) (
	credID, label, account string, err error) {

	flow, err := s.deps.Flows.Claim(state, sessionID)
	if err != nil {
		return "", "", "", err
	}
	_, cfg, ok := s.oauthConfig(ctx, flow.ProviderID)
	if !ok {
		return "", "", "", fmt.Errorf("this provider is no longer configured for OAuth")
	}

	tok, err := auth.ExchangeCode(ctx, s.httpClient(), authConfig(cfg), auth.ExchangeInput{
		Code: code, Verifier: flow.Verifier, RedirectURI: flow.RedirectURI,
	})
	if err != nil {
		return "", "", "", err
	}

	raw, err := tok.Marshal()
	if err != nil {
		return "", "", "", err
	}
	label = flow.Label
	if label == "" {
		label = tok.Account
	}
	if label == "" {
		label = "subscription"
	}
	credID, err = s.deps.DB.AddCredential(ctx, s.deps.Key, store.Credential{
		ProviderID: flow.ProviderID, Label: label, Kind: "oauth",
		Secret: string(raw), Enabled: true, ExpiresAt: tok.Unix(),
	})
	if err != nil {
		return "", "", "", err
	}
	s.reloadProviders(ctx)
	return credID, label, tok.Account, nil
}

// statusForOAuth maps a completion failure to a status. A refused state is the
// caller's problem; a token endpoint that would not answer is the provider's.
func statusForOAuth(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case strings.Contains(err.Error(), "token endpoint"):
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

// oauthConfig resolves the provider's preset OAuth block.
func (s *Server) oauthConfig(ctx context.Context, providerID string) (string, catalog.OAuth, bool) {
	rows, err := s.deps.DB.ProviderRows(ctx)
	if err != nil {
		return "", catalog.OAuth{}, false
	}
	for _, p := range rows {
		if p.ID != providerID {
			continue
		}
		preset, ok := s.deps.Presets[p.Preset]
		if !ok || preset.OAuth == nil {
			return "", catalog.OAuth{}, false
		}
		return p.Preset, *preset.OAuth, true
	}
	return "", catalog.OAuth{}, false
}

// authConfig translates the preset's OAuth block into the shape internal/auth
// takes. The two carry the same fields under different names on purpose: auth
// must not import catalog, which imports provider, which imports store.
func authConfig(c catalog.OAuth) auth.OAuthConfig {
	return auth.OAuthConfig{
		AuthorizeURL: c.AuthorizeURL, TokenURL: c.TokenURL,
		ClientID: c.ClientID, Scopes: c.Scopes,
	}
}

// startListener is replaced in Task 13. Until then a localhost preset falls
// back to the paste path, which spec §5.1 says always works — so this is a
// degraded flow rather than a broken one.
func (s *Server) startListener(auth.Flow, int, string, time.Duration) error {
	return fmt.Errorf("the localhost redirect listener is not available")
}

func (s *Server) httpClient() *http.Client {
	if s.deps.HTTP != nil {
		return s.deps.HTTP
	}
	return http.DefaultClient
}
