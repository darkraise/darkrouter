package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/admin"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/store"
)

func newTestServer(t *testing.T, extraServer string) *Server {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\n" + extraServer +
		"providers:\n  - id: fake\n    kind: openaicompat\n" +
		"    base_url: https://up.example/v1\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return serverBackedBy(t, cfgStore)
}

func TestHealthzReportsConfigValidity(t *testing.T) {
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var got struct {
		ConfigValid bool     `json:"config_valid"`
		Warnings    []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.ConfigValid {
		t.Fatal("expected config_valid true")
	}
}

func TestReadyzReturns200(t *testing.T) {
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestModelsListsProviderModels(t *testing.T) {
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if !strings.Contains(rec.Body.String(), `"m"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestProxyTokenIsEnforcedWhenConfigured(t *testing.T) {
	s := newTestServer(t, "  proxy_token: secret\n")
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}

	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer secret")
	rec2 := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec2, r)
	if rec2.Code != 200 {
		t.Fatalf("authorized code = %d", rec2.Code)
	}
}

func TestProxyTokenIsOptional(t *testing.T) {
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d; an unset token means auth is off", rec.Code)
	}
}

func TestProxyTokenRejectsWrongToken(t *testing.T) {
	s := newTestServer(t, "  proxy_token: secret\n")
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
}

func TestProxyHandlerRoutesEveryDialect(t *testing.T) {
	s := newTestServer(t, "")
	h := s.ProxyHandler()

	cases := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"openai chat", "POST", "/v1/chat/completions", `{"model":"nope","messages":[]}`},
		{"anthropic messages", "POST", "/v1/messages", `{"model":"nope","max_tokens":1,"messages":[]}`},
		{"gemini generate", "POST", "/v1beta/models/nope:generateContent", `{"contents":[]}`},
		{"gemini stream", "POST", "/v1beta/models/nope:streamGenerateContent?alt=sse", `{"contents":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body)))
			if rec.Header().Get("X-Darkrouter-Request") == "" {
				t.Errorf("no request id: the handler did not reach exec (%d %s)",
					rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGeminiListingIsServed(t *testing.T) {
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1beta/models", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "supportedGenerationMethods") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestGeminiAuthUsesItsOwnCredentialForm(t *testing.T) {
	s := newTestServer(t, "  proxy_token: secret\n")
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1beta/models?key=secret", nil))
	if rec.Code == 401 {
		t.Fatalf("?key= must authenticate a Gemini client: %s", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec2, httptest.NewRequest("GET", "/v1beta/models?key=wrong", nil))
	if rec2.Code != 401 {
		t.Errorf("wrong key = %d, want 401", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "UNAUTHENTICATED") {
		t.Errorf("body = %s; the rejection is written in Google's vocabulary", rec2.Body.String())
	}
}

func TestAnthropicAuthUsesItsOwnCredentialForm(t *testing.T) {
	s := newTestServer(t, "  proxy_token: secret\n")
	r := httptest.NewRequest("POST", "/v1/messages",
		strings.NewReader(`{"model":"nope","max_tokens":1,"messages":[]}`))
	r.Header.Set("x-api-key", "wrong")
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, r)
	if rec.Code != 401 {
		t.Fatalf("code = %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "authentication_error") {
		t.Errorf("body = %s; the rejection is written in Anthropic's vocabulary", rec.Body.String())
	}
}

// serverWithCatalog builds a server and pins its catalog, so the listing tests
// do not depend on discovery having run.
func serverWithCatalog(t *testing.T, body string, models []catalog.Model) *Server {
	t.Helper()
	db, key, cfgStore := serverFixtureWith(t, body)
	srv, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv.Catalog().Set(catalog.NewSnapshot(models, []string{"p"}))
	return srv
}

func TestModelsListsAliasesFirstThenTheCatalog(t *testing.T) {
	srv := serverWithCatalog(t, `
server:
  proxy_listen: "127.0.0.1:0"
  admin_listen: "127.0.0.1:0"
catalog:
  models_dev_url: http://127.0.0.1:1/api.json
  sync_timeout: 200ms
  discovery:
    enabled: false
aliases:
  fast: [p/quick]
  smart: [p/deep]
`, []catalog.Model{
		{ProviderID: "p", ModelID: "deep", State: catalog.StateLive, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "p", ModelID: "quick", State: catalog.StateLive, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "p", ModelID: "retired", State: catalog.StateRemovedUpstream, Surfaces: []ir.Surface{ir.SurfaceLLM}},
		{ProviderID: "p", ModelID: "flaky", State: catalog.StateStale, Surfaces: []ir.Surface{ir.SurfaceLLM}},
	})

	w := httptest.NewRecorder()
	srv.ProxyHandler().ServeHTTP(w, httptest.NewRequest("GET", "/v1/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var out struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Object != "list" {
		t.Errorf("object = %q", out.Object)
	}

	ids := make([]string, len(out.Data))
	for i, d := range out.Data {
		ids[i] = d.ID
	}
	// Aliases first, sorted for determinism, before anything discovered.
	if len(ids) < 2 || ids[0] != "fast" || ids[1] != "smart" {
		t.Errorf("ids = %v; aliases are not listed first", ids)
	}
	if out.Data[0].OwnedBy != "darkrouter" {
		t.Errorf("alias owned_by = %q", out.Data[0].OwnedBy)
	}
	has := func(want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}
	if !has("quick") || !has("deep") {
		t.Errorf("ids = %v; a live model is missing", ids)
	}
	// Stale stays listed, retired does not.
	if !has("flaky") {
		t.Errorf("ids = %v; a stale model was excluded", ids)
	}
	if has("retired") {
		t.Errorf("ids = %v; a removed_upstream model was listed", ids)
	}
}

func TestGeminiModelsReadsTheCatalog(t *testing.T) {
	srv := serverWithCatalog(t, `
server:
  proxy_listen: "127.0.0.1:0"
  admin_listen: "127.0.0.1:0"
catalog:
  models_dev_url: http://127.0.0.1:1/api.json
  sync_timeout: 200ms
  discovery:
    enabled: false
`, []catalog.Model{
		{ProviderID: "p", ModelID: "gemini-2.5-pro", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, ContextWindow: 1048576, MaxOutputTokens: 65536},
		{ProviderID: "p", ModelID: "retired", State: catalog.StateRemovedUpstream,
			Surfaces: []ir.Surface{ir.SurfaceLLM}},
	})

	w := httptest.NewRecorder()
	srv.ProxyHandler().ServeHTTP(w, httptest.NewRequest("GET", "/v1beta/models", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "models/gemini-2.5-pro") {
		t.Errorf("body = %s", body)
	}
	if !strings.Contains(body, "1048576") {
		t.Errorf("token limits missing from %s", body)
	}
	if strings.Contains(body, "retired") {
		t.Errorf("a removed_upstream model was listed: %s", body)
	}
}

func TestTheProxyPortIgnoresASessionCookie(t *testing.T) {
	// Cookies are not port-scoped: a browser logged into the admin port sends
	// that cookie to the proxy port too. If the proxy ever honored one, every
	// logged-in operator's browser would be an authenticated proxy client for
	// any page they visited. Nothing reads cookies here today; this is what
	// keeps it that way.
	s := newTestServer(t, "  proxy_token: secret\n")

	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.AddCookie(&http.Cookie{Name: "darkrouter_session", Value: "a-valid-looking-session"})
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401; a cookie authenticated a proxy request", rec.Code)
	}
}

func TestTheProxyPortStillAcceptsItsBearerToken(t *testing.T) {
	// The other half: refusing the cookie must not refuse the token.
	s := newTestServer(t, "  proxy_token: secret\n")

	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer secret")
	r.AddCookie(&http.Cookie{Name: "darkrouter_session", Value: "irrelevant"})
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, r)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("code = %d; the bearer token was refused", rec.Code)
	}
}

func TestTheAdminPortServesTheAPIClosed(t *testing.T) {
	// One request proves both that the API is mounted and that it is closed.
	s := newTestServer(t, "")
	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/overview", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("code = %d, want 401", rec.Code)
	}
}

func TestHealthEndpointsStayUnauthenticated(t *testing.T) {
	// A container orchestrator and a Prometheus scraper read these. Putting
	// them behind a session breaks both, and the admin mux is mounted at the
	// root so nothing else may shadow their exact paths.
	s := newTestServer(t, "")
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		rec := httptest.NewRecorder()
		s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s code = %d, want 200", path, rec.Code)
		}
	}
}

func TestAMissingPasswordHashWarnsRatherThanFailingStartup(t *testing.T) {
	// The gateway's job is proxying. Refusing to start because the optional
	// dashboard has no password would take a working proxy down over a feature
	// the operator may not use.
	t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", "")
	s := newTestServer(t, "")

	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	var body struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range body.Warnings {
		if strings.Contains(w, "PASSWORD_HASH") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; an operator cannot tell the dashboard is closed", body.Warnings)
	}
}

func TestALoginWorksAgainstAConfiguredHash(t *testing.T) {
	// The other half: a configured hash opens the dashboard, so the warning
	// above is about configuration rather than a permanently closed port.
	hash, err := admin.HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", hash)
	s := newTestServer(t, "")

	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"hunter2"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rec.Code, rec.Body.String())
	}
}

// policy.cooldown is hot-editable, and the edit has to reach the breaker
// without a restart.
func TestACooldownEditReachesTheBreakerWithoutARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	write := func(tripAfter string) {
		body := "server:\n  proxy_listen: :0\n  admin_listen: :0\n" +
			"policy:\n  cooldown:\n    trip_after: " + tripAfter + "\n" +
			"providers:\n  - id: fake\n    kind: openaicompat\n" +
			"    base_url: https://up.example/v1\n    api_key: ${K}\n    models: [m]\n"
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("3")
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	s := serverBackedBy(t, cfgStore)

	k := health.Key{ProviderID: "fake", KeyID: "k", Model: "m"}
	fail := health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503}
	s.breaker.Record(k, fail)
	if !s.breaker.Available(k) {
		t.Fatal("one 5xx cooled the target under trip_after 3")
	}

	write("1")
	if err := cfgStore.Reload(); err != nil {
		t.Fatal(err)
	}
	s.breaker.Record(k, fail)
	if s.breaker.Available(k) {
		t.Fatal("the edited trip_after was not applied to the running breaker")
	}
}

func TestReadyzReports503WhenTheConfigIsInvalid(t *testing.T) {
	s := newTestServer(t, "")
	s.store.RecordError(errors.New("bad edit"))
	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bad edit") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestReadyzReports503WhenTheDatabaseIsGone(t *testing.T) {
	s := newTestServer(t, "")
	_ = s.db.Close()
	rec := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
}

func TestListenerTimeoutsAreSet(t *testing.T) {
	cfg := config.TimeoutConfig{Total: 10 * time.Second}
	read, idle := listenerTimeouts(cfg)
	if read != 60*time.Second || idle != 120*time.Second {
		t.Errorf("timeouts = %v/%v, want 60s/120s", read, idle)
	}
	cfg.Total = 10 * time.Minute
	if read, _ = listenerTimeouts(cfg); read != 20*time.Minute {
		t.Errorf("read timeout = %v, want twice a long total", read)
	}
}

// The catalog layer's own tests prove a LiteLLM candidate wins where one is
// offered; none of them prove the gateway ever offers it. Only this wiring
// makes the synced index reach a routed model's price, so it is tested where
// it lives.
func TestTheSyncedLiteLLMIndexReachesTheRoutedCatalog(t *testing.T) {
	const model = "zzz-only-the-index-knows-me"
	idx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"groq/` + model + `": {"litellm_provider": "groq",` +
			`"input_cost_per_token": 5.9e-07, "output_cost_per_token": 7.9e-07}}`))
	}))
	defer idx.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\n" +
		"catalog:\n  litellm_url: " + idx.URL + "\n" +
		"providers:\n  - id: groq\n    preset: groq\n    kind: openaicompat\n" +
		"    base_url: https://api.groq.com/openai/v1\n    api_key: ${K}\n" +
		"    models: [" + model + "]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	s := serverBackedBy(t, cfgStore)

	ctx := context.Background()
	if err := s.db.RecordDiscoverySuccess(ctx, "groq",
		[]store.DiscoveredModel{{ModelID: model}}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if s.litellmSync == nil {
		t.Fatal("the price syncer is nil with the refresh left at its default")
	}
	if err := s.litellmSync.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}

	m, ok := s.cat.Snapshot().Lookup("groq", model)
	if !ok {
		t.Fatalf("%s is not in the served snapshot", model)
	}
	if m.Pricing.Source != catalog.SourceLiteLLM || m.Pricing.InputMicrosPerMTok != 590_000 {
		t.Errorf("pricing = %+v, want the index's 590000 at source litellm: the "+
			"synced index never reaches the catalog the router serves", m.Pricing)
	}
}
