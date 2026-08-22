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
	store, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(store)
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
