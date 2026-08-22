package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/config"
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
