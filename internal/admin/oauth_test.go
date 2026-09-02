package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// fakeAuthServer is a token endpoint that records what it was asked and can be
// made to refuse. It rotates its refresh token on every refresh, which spec
// §5.2 says many vendors do.
type fakeAuthServer struct {
	mu        sync.Mutex
	code      string
	verifier  string
	redirect  string
	refreshes int
	status    int
	errBody   string
}

func newFakeAuthServer(t *testing.T) (*fakeAuthServer, *httptest.Server) {
	t.Helper()
	f := &fakeAuthServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()

		if f.status != 0 && f.status != http.StatusOK {
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(f.errBody))
			return
		}
		if r.Form.Get("grant_type") == "refresh_token" {
			f.refreshes++
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"access_token":"the-access-token-%d","refresh_token":"the-refresh-token-%d","token_type":"Bearer","expires_in":3600}`,
				f.refreshes, f.refreshes)
			return
		}
		f.code = r.Form.Get("code")
		f.verifier = r.Form.Get("code_verifier")
		f.redirect = r.Form.Get("redirect_uri")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"the-access-token","refresh_token":"the-refresh-token","token_type":"Bearer","expires_in":3600,"account":{"email_address":"me@example.com"}}`))
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeAuthServer) refreshCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.refreshes
}

// oauthPresets is the shipped preset set with anthropic-oauth's endpoints
// redirected at the fake. Copying rather than mutating Embedded() matters: the
// embedded map is process-wide and shared with every other test.
func oauthPresets(tokenURL string, redirect catalog.Redirect) catalog.Presets {
	out := catalog.Presets{}
	for id, p := range catalog.Embedded() {
		out[id] = p
	}
	base := out["anthropic-oauth"]
	base.OAuth = &catalog.OAuth{
		AuthorizeURL: "https://claude.ai/oauth/authorize",
		TokenURL:     tokenURL,
		ClientID:     "test-client",
		Scopes:       []string{"user:inference"},
		Redirect:     redirect,
	}
	out["anthropic-oauth"] = base
	return out
}

// serverWithFakeAuthServer builds an admin server whose anthropic-oauth preset
// points at a fake token endpoint. The redirect style defaults to manual, which
// spec §5.1 calls the path that always works.
func serverWithFakeAuthServer(t *testing.T) (*Server, *http.Cookie, string, *fakeAuthServer) {
	t.Helper()
	return serverWithRedirectStyle(t, catalog.Redirect{Style: "manual"})
}

func serverWithRedirectStyle(t *testing.T, redirect catalog.Redirect) (
	*Server, *http.Cookie, string, *fakeAuthServer) {

	t.Helper()
	fake, srv := newFakeAuthServer(t)
	db := storetest.Migrated(t)
	key, err := store.OpenKeyring(context.Background(), db, "master")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Deps{
		DB: db, PasswordHash: testHash(),
		Config: configStoreFor(t, ""), Key: key,
		Presets: oauthPresets(srv.URL, redirect),
		Src:     provider.NewSQLSource(db, key),
		Breaker: health.New(3, time.Minute),
		Flows:   auth.NewFlowStore(time.Minute),
		HTTP:    srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cookie, token := login(t, s)
	return s, cookie, token, fake
}

// oauthProvider creates a provider from the anthropic-oauth preset, the only
// shipped preset declaring an OAuth block.
func oauthProvider(t *testing.T, s *Server, cookie *http.Cookie, token string) string {
	t.Helper()
	w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"sub","preset":"anthropic-oauth"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create oauth provider: %d %s", w.Code, w.Body.String())
	}
	return "sub"
}

func startFlow(t *testing.T, s *Server, cookie *http.Cookie, token, id, body string) struct {
	AuthorizeURL  string `json:"authorize_url"`
	State         string `json:"state"`
	RedirectURI   string `json:"redirect_uri"`
	Style         string `json:"style"`
	ListenerError string `json:"listener_error"`
} {
	t.Helper()
	var out struct {
		AuthorizeURL  string `json:"authorize_url"`
		State         string `json:"state"`
		RedirectURI   string `json:"redirect_uri"`
		Style         string `json:"style"`
		ListenerError string `json:"listener_error"`
	}
	w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/oauth/start", body)
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStartReturnsAnAuthorizeURL(t *testing.T) {
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{"label":"personal"}`)

	u, err := url.Parse(start.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("response_type = %q", q.Get("response_type"))
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q; plain PKCE is worthless here",
			q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("no code_challenge")
	}
	if q.Get("state") != start.State {
		t.Error("the state in the URL and the response disagree")
	}
	if q.Get("client_id") == "" {
		t.Error("no client_id; the preset's value did not reach the URL")
	}
	// The verifier must never leave the server.
	if strings.Contains(start.AuthorizeURL, "code_verifier") {
		t.Fatal("the PKCE verifier is in the authorize URL")
	}
}

func TestStartRefusesANonOAuthProvider(t *testing.T) {
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"groq","preset":"groq"}`); w.Code != http.StatusCreated {
		t.Fatalf("seed: %d %s", w.Code, w.Body.String())
	}
	w := do(t, s, cookie, token, "POST", "/api/providers/groq/oauth/start", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCompleteExchangesThePastedURL(t *testing.T) {
	// The operator pastes the whole redirected URL. Asking for the code alone
	// makes them parse a query string by eye.
	s, cookie, token, fake := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{"label":"personal"}`)

	pasted := "http://localhost/callback?code=the-code&state=" + url.QueryEscape(start.State)
	body, _ := json.Marshal(map[string]string{"redirected_url": pasted})
	w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/oauth/complete", string(body))
	if w.Code != http.StatusCreated {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}
	var done struct {
		CredentialID string `json:"credential_id"`
		Account      string `json:"account"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &done); err != nil {
		t.Fatal(err)
	}
	if done.CredentialID == "" {
		t.Fatal("no credential was created")
	}
	if done.Account != "me@example.com" {
		t.Errorf("account = %q", done.Account)
	}

	fake.mu.Lock()
	code, verifier := fake.code, fake.verifier
	fake.mu.Unlock()
	if verifier == "" {
		t.Error("the token exchange did not send a code_verifier")
	}
	if code != "the-code" {
		t.Errorf("the exchange sent code %q", code)
	}

	// Spec §4.1: no credential material in any response.
	raw := do(t, s, cookie, token, "GET", "/api/providers", "").Body.String()
	for _, secret := range []string{"the-refresh-token", "the-access-token"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("%q appeared in GET /api/providers", secret)
		}
	}
}

func TestCompleteRefusesAMismatchedState(t *testing.T) {
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	startFlow(t, s, cookie, token, id, `{}`)

	body, _ := json.Marshal(map[string]string{
		"redirected_url": "http://localhost/callback?code=c&state=not-the-state"})
	w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/oauth/complete", string(body))
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCompleteRefusesAReusedState(t *testing.T) {
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{}`)

	pasted := "http://localhost/callback?code=c&state=" + url.QueryEscape(start.State)
	body, _ := json.Marshal(map[string]string{"redirected_url": pasted})

	if w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/oauth/complete",
		string(body)); w.Code != http.StatusCreated {
		t.Fatalf("first complete: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/oauth/complete",
		string(body)); w.Code == http.StatusCreated {
		t.Error("a state was accepted twice")
	}
}

func TestCompleteSurfacesTheProvidersError(t *testing.T) {
	// The redirect carries error=access_denied when the operator clicks Deny.
	// Reporting "no code" would send them looking for a bug.
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{}`)

	pasted := "http://localhost/callback?error=access_denied&error_description=User+denied&state=" +
		url.QueryEscape(start.State)
	body, _ := json.Marshal(map[string]string{"redirected_url": pasted})
	w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/oauth/complete", string(body))
	if w.Code == http.StatusCreated {
		t.Fatal("a denied authorization must not succeed")
	}
	if !strings.Contains(w.Body.String(), "access_denied") {
		t.Errorf("the error did not reach the operator: %s", w.Body.String())
	}
}

func TestCompleteRequiresCSRF(t *testing.T) {
	s, cookie, _, _ := serverWithFakeAuthServer(t)
	r := httptest.NewRequest("POST", "/api/providers/sub/oauth/complete", strings.NewReader("{}"))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	// No CSRF header.
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestStartRequiresASession(t *testing.T) {
	s, _, _, _ := serverWithFakeAuthServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/api/providers/sub/oauth/start", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAManualPresetGetsNoListener(t *testing.T) {
	// The out-of-band redirect: nothing to listen on, and the paste box is the
	// whole flow.
	s, cookie, token, _ := serverWithFakeAuthServer(t)
	id := oauthProvider(t, s, cookie, token)
	start := startFlow(t, s, cookie, token, id, `{}`)
	if start.Style != "manual" {
		t.Errorf("style = %q", start.Style)
	}
	if start.RedirectURI != "urn:ietf:wg:oauth:2.0:oob" {
		t.Errorf("redirect_uri = %q", start.RedirectURI)
	}
}

var _ = config.Config{}
