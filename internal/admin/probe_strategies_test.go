package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

type probeReply struct {
	OK         bool   `json:"ok"`
	Probe      string `json:"probe"`
	ModelCount int    `json:"model_count"`
	LatencyMs  int64  `json:"latency_ms"`
	Error      string `json:"error"`
}

func probeProvider(t *testing.T, s *Server, cookie *http.Cookie, token, id string) probeReply {
	t.Helper()
	w := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("probe: %d %s", w.Code, w.Body.String())
	}
	var out probeReply
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// fakeAWS serves the two control-plane listings and records the Authorization
// header it saw.
type fakeAWS struct {
	mu     sync.Mutex
	status int
	authz  string
}

func newFakeAWS(t *testing.T) (*fakeAWS, *httptest.Server) {
	t.Helper()
	f := &fakeAWS{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.authz = r.Header.Get("Authorization")
		status := f.status
		f.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/foundation-models":
			_, _ = w.Write([]byte(`{"modelSummaries":[{"modelId":"amazon.titan-v1",
			  "inferenceTypesSupported":["ON_DEMAND"],"modelLifecycle":{"status":"ACTIVE"}}]}`))
		case "/inference-profiles":
			_, _ = w.Write([]byte(`{"inferenceProfileSummaries":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

// strategyServer builds an admin server with a live auth manager, so the probe
// exercises the real strategy path rather than a stub.
func strategyServer(t *testing.T, presets catalog.Presets, client *http.Client) (
	*Server, *http.Cookie, string, *store.DB) {

	t.Helper()
	db := storetest.Migrated(t)
	key, err := store.OpenKeyring(context.Background(), db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if presets == nil {
		presets = catalog.Embedded()
	}
	s, err := New(Deps{
		DB: db, PasswordHash: testHash(),
		Config: configStoreFor(t, ""), Key: key, Presets: presets,
		Src:     provider.NewSQLSource(db, key),
		Breaker: health.New(3, time.Minute),
		Auth: auth.NewManager(auth.Deps{
			HTTP:  client,
			OAuth: testPresetOAuth{presets},
		}),
		HTTP: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	cookie, token := login(t, s)
	return s, cookie, token, db
}

func bedrockProvider(t *testing.T, s *Server, cookie *http.Cookie, token, baseURL string) string {
	t.Helper()
	body := fmt.Sprintf(
		`{"id":"bed","name":"bed","kind":"bedrock","base_url":%q,"auth_style":"sigv4","region":"us-east-1"}`,
		baseURL)
	if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	secret := `{\"access_key_id\":\"AKIDEXAMPLE\",\"secret_access_key\":\"CANARY-AWS-SECRET\"}`
	if w := do(t, s, cookie, token, "POST", "/api/providers/bed/keys",
		`{"label":"primary","secret":"`+secret+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	return "bed"
}

func TestSigV4ProbeReportsTheModelCount(t *testing.T) {
	// The same signal an openaicompat probe gives: a number the operator can
	// sanity-check against what they expect the account to have.
	aws, srv := newFakeAWS(t)
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if !got.OK {
		t.Fatalf("probe failed: %s", got.Error)
	}
	if got.ModelCount == 0 {
		t.Error("model_count = 0")
	}
	if got.Probe != "listing" {
		t.Errorf("probe = %q", got.Probe)
	}
	aws.mu.Lock()
	authz := aws.authz
	aws.mu.Unlock()
	if !strings.HasPrefix(authz, "AWS4-HMAC-SHA256 ") {
		t.Errorf("the listing was not signed: %q", authz)
	}
}

func TestSigV4ProbeNamesAPermissionFailure(t *testing.T) {
	// "It doesn't work" is not actionable. A 403 means the key is valid and
	// the policy is not, which is a different fix from a wrong region.
	aws, srv := newFakeAWS(t)
	aws.status = http.StatusForbidden
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	id := bedrockProvider(t, s, cookie, token, srv.URL)

	got := probeProvider(t, s, cookie, token, id)
	if got.OK {
		t.Fatal("a 403 must not report success")
	}
	if got.Probe != "permission" {
		t.Errorf("probe = %q, want permission", got.Probe)
	}
}

func TestSigV4ProbeRefusesWithoutARegion(t *testing.T) {
	// Signing for the wrong region is a 403 that reads as a bad key.
	_, srv := newFakeAWS(t)
	s, cookie, token, _ := strategyServer(t, nil, srv.Client())
	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"bed2","kind":"bedrock","base_url":"`+srv.URL+`","auth_style":"sigv4"}`); w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "POST", "/api/providers/bed2/keys",
		`{"label":"k","secret":"{\"access_key_id\":\"A\",\"secret_access_key\":\"B\"}"}`); w.Code != http.StatusCreated {
		t.Fatalf("key: %d %s", w.Code, w.Body.String())
	}
	got := probeProvider(t, s, cookie, token, "bed2")
	if got.OK {
		t.Fatal("a provider with no region must not probe OK")
	}
	if !strings.Contains(got.Error, "region") {
		t.Errorf("the error should name the cause: %q", got.Error)
	}
}

func TestOAuthProbeRefreshes(t *testing.T) {
	fake, srv := newFakeAuthServer(t)
	s, cookie, token, db := strategyServer(t,
		oauthPresets(srv.URL, catalog.Redirect{Style: "manual"}), srv.Client())
	id := oauthProvider(t, s, cookie, token)
	seedOAuthCredential(t, db, s, id, -time.Minute)

	got := probeProvider(t, s, cookie, token, id)
	if !got.OK {
		t.Fatalf("probe failed: %s", got.Error)
	}
	if got.Probe != "refresh" {
		t.Errorf("probe = %q, want refresh", got.Probe)
	}
	if fake.refreshCount() == 0 {
		t.Error("the probe did not refresh")
	}
}

func TestOAuthProbeReportsAnExpiredGrant(t *testing.T) {
	fake, srv := newFakeAuthServer(t)
	fake.mu.Lock()
	fake.status, fake.errBody = http.StatusBadRequest, `{"error":"invalid_grant"}`
	fake.mu.Unlock()

	s, cookie, token, db := strategyServer(t,
		oauthPresets(srv.URL, catalog.Redirect{Style: "manual"}), srv.Client())
	id := oauthProvider(t, s, cookie, token)
	seedOAuthCredential(t, db, s, id, -time.Minute)

	got := probeProvider(t, s, cookie, token, id)
	if got.OK {
		t.Fatal("a refused refresh must not report success")
	}
	if !strings.Contains(strings.ToLower(got.Error), "reconnect") {
		t.Errorf("the operator must be told to reconnect: %q", got.Error)
	}
}

func TestNoProbeResponseCarriesCredentialMaterial(t *testing.T) {
	_, srv := newFakeAuthServer(t)
	s, cookie, token, db := strategyServer(t,
		oauthPresets(srv.URL, catalog.Redirect{Style: "manual"}), srv.Client())
	id := oauthProvider(t, s, cookie, token)
	seedOAuthCredential(t, db, s, id, -time.Minute)

	raw := do(t, s, cookie, token, "POST", "/api/providers/"+id+"/test", "").Body.String()
	for _, secret := range []string{"rt-canary", "at-canary", "the-refresh-token", "the-access-token"} {
		if strings.Contains(raw, secret) {
			t.Errorf("the probe response carries %q:\n%s", secret, raw)
		}
	}
}

// seedOAuthCredential writes a token document directly, which is what a
// completed connect flow leaves behind.
func seedOAuthCredential(t *testing.T, db *store.DB, s *Server, providerID string, expiresIn time.Duration) string {
	t.Helper()
	tok := auth.Token{
		AccessToken: "at-canary", RefreshToken: "rt-canary",
		ExpiresAt: time.Now().Add(expiresIn),
	}
	raw, err := tok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.AddCredential(context.Background(), s.deps.Key, store.Credential{
		ProviderID: providerID, Label: "personal", Kind: "oauth",
		Secret: string(raw), Enabled: true, ExpiresAt: tok.Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// testPresetOAuth resolves a preset id to its OAuth endpoints, mirroring what
// the server wires in production. Declared here rather than imported because
// internal/server imports internal/admin, not the other way round.
type testPresetOAuth struct{ presets catalog.Presets }

func (p testPresetOAuth) OAuthFor(preset string) (auth.OAuthConfig, bool) {
	entry, ok := p.presets[preset]
	if !ok || entry.OAuth == nil {
		return auth.OAuthConfig{}, false
	}
	return auth.OAuthConfig{
		AuthorizeURL: entry.OAuth.AuthorizeURL, TokenURL: entry.OAuth.TokenURL,
		ClientID: entry.OAuth.ClientID, Scopes: entry.OAuth.Scopes,
	}, true
}
