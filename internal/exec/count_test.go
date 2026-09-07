package exec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/anthropic"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/config"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
)

// countExecutor builds an executor over one provider of the given kind.
func countExecutor(t *testing.T, kind, upstreamURL string) *Executor {
	t.Helper()
	cfgStore := config.NewStoreOf(testConfig(t, func(c *config.Config) {
		c.Providers = []config.ProviderConfig{{
			ID: "fake", Kind: kind, BaseURL: upstreamURL, APIKey: "sk", Models: []string{"m"},
		}}
	}))
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

// countExecutorWith is countExecutor with collaborators, a preset and a
// timeout policy, for the paths the native count shares with the loop.
func countExecutorWith(t *testing.T, kind, upstreamURL, preset string, deps Deps,
	tune func(*config.Config)) *Executor {

	t.Helper()
	cfgStore := config.NewStoreOf(testConfig(t, func(c *config.Config) {
		c.Providers = []config.ProviderConfig{{
			ID: "fake", Kind: kind, Preset: preset, BaseURL: upstreamURL,
			APIKey: "sk", Models: []string{"m"},
		}}
		if tune != nil {
			tune(c)
		}
	}))
	return New(cfgStore, provider.NewYAMLSource(cfgStore), map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropic.New(),
	}, deps)
}

var fakeCountKey = health.Key{ProviderID: "fake", KeyID: "", Model: "m"}

func TestHandleCountGivesUpAtTheAttemptDeadline(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}))
	defer up.Close()

	e := countExecutorWith(t, "anthropic", up.URL, "", Deps{}, func(c *config.Config) {
		c.Policy.Timeout.Connect = 5 * time.Millisecond
		c.Policy.Timeout.FirstByte = 200 * time.Millisecond
		c.Policy.Timeout.Total = time.Second
	})
	start := time.Now()
	rec := postCount(t, e, "anthropic", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != 200 || rec.Header().Get("X-Darkrouter-Estimated") != "true" {
		t.Fatalf("code = %d estimated = %q; a stalled native count must fall back",
			rec.Code, rec.Header().Get("X-Darkrouter-Estimated"))
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Errorf("the native count ran %v, past the attempt deadline", time.Since(start))
	}
}

func TestHandleCountSkipsACoolingTargetAndRecordsFailures(t *testing.T) {
	calls := 0
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(503)
	}))
	defer up.Close()

	b := health.New(3, time.Hour)
	e := countExecutorWith(t, "anthropic", up.URL, "", Deps{Health: b, Fleet: b}, nil)
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	for i := 0; i < 3; i++ {
		if rec := postCount(t, e, "anthropic", body); rec.Code != 200 {
			t.Fatalf("code = %d", rec.Code)
		}
	}
	if calls != 3 {
		t.Fatalf("upstream called %d times, want 3", calls)
	}
	if b.Available(fakeCountKey) {
		t.Fatal("three 503s on the counting endpoint did not trip the breaker")
	}
	postCount(t, e, "anthropic", body)
	if calls != 3 {
		t.Error("a cooling target was still asked to count")
	}
}

// headerResolver stands in for a non-static credential strategy.
type headerResolver struct{}

func (headerResolver) For(context.Context, auth.Target, auth.Credential) (auth.Authorizer, error) {
	return func(_ context.Context, hr *http.Request) error {
		hr.Header.Set("Authorization", "Bearer resolved")
		return nil
	}, nil
}

func TestHandleCountAppliesTheAuthorizer(t *testing.T) {
	var got string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":7}`))
	}))
	defer up.Close()

	e := countExecutorWith(t, "anthropic", up.URL, "anthropic-oauth", Deps{Auth: headerResolver{}}, nil)
	rec := postCount(t, e, "anthropic", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "7") {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if got != "Bearer resolved" {
		t.Errorf("Authorization = %q; the resolved credential must be applied", got)
	}
}

func TestHandleCountCapsTheResponseBody(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pad":"` + strings.Repeat("x", 70<<10) + `","input_tokens":7}`))
	}))
	defer up.Close()

	e := countExecutor(t, "anthropic", up.URL)
	rec := postCount(t, e, "anthropic", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != 200 || rec.Header().Get("X-Darkrouter-Estimated") != "true" {
		t.Fatalf("code = %d estimated = %q; an oversized count body must fall back",
			rec.Code, rec.Header().Get("X-Darkrouter-Estimated"))
	}
}
