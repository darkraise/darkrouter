package exec

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/anthropic"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	"github.com/darkraise/darkrouter/internal/provider"
)

// countExecutor builds an executor over one provider of the given kind.
func countExecutor(t *testing.T, kind, upstreamURL string) *Executor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: fake\n    kind: " + kind + "\n    base_url: " + upstreamURL +
		"\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropic.New(),
	}, Deps{})
}

func postCount(t *testing.T, e *Executor, kind, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.HandleCount(rec, r, anthropicedge.New(), kind)
	return rec
}

func TestHandleCountForwardsToANativeTarget(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":2095}`))
	}))
	defer up.Close()

	e := countExecutor(t, "anthropic", up.URL)
	rec := postCount(t, e, "anthropic",
		`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(gotPath, "count_tokens") {
		t.Errorf("upstream path = %q", gotPath)
	}
	if !strings.Contains(rec.Body.String(), "2095") {
		t.Errorf("body = %s; the provider's real count must be returned", rec.Body.String())
	}
	if rec.Header().Get("X-Darkrouter-Estimated") != "" {
		t.Error("a native count is not an estimate")
	}
}

func TestHandleCountEstimatesForAForeignTarget(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an openaicompat target has no counting endpoint and must not be called")
	}))
	defer up.Close()

	e := countExecutor(t, "openaicompat", up.URL)
	rec := postCount(t, e, "anthropic",
		`{"model":"m","messages":[{"role":"user","content":"hello world"}]}`)

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Darkrouter-Estimated") != "true" {
		t.Error("an estimate must say so in the header; the body cannot carry a marker")
	}
	if !strings.Contains(rec.Body.String(), "input_tokens") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestHandleCountFallsBackWhenTheNativeCallFails(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer up.Close()

	e := countExecutor(t, "anthropic", up.URL)
	rec := postCount(t, e, "anthropic",
		`{"model":"m","messages":[{"role":"user","content":"hello world"}]}`)

	if rec.Code != 200 {
		t.Fatalf("code = %d; counting is advisory and must not fail the client", rec.Code)
	}
	if rec.Header().Get("X-Darkrouter-Estimated") != "true" {
		t.Error("the fallback is an estimate and must say so")
	}
}

func TestHandleCountReportsAnUnknownModel(t *testing.T) {
	e := countExecutor(t, "anthropic", "https://unused.example/v1")
	rec := postCount(t, e, "anthropic", `{"model":"nope","messages":[]}`)
	if rec.Code != 404 {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not_found_error") {
		t.Errorf("body = %s; the error is written in the inbound dialect", rec.Body.String())
	}
}
