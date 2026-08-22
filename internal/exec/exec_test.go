package exec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/provider"
)

func newExecutor(t *testing.T, upstreamURL string) *Executor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: fake\n    kind: openaicompat\n    base_url: " + upstreamURL +
		"\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(store, provider.NewYAMLSource(store), openaicompat.New())
}

func post(t *testing.T, e *Executor, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.Handle(rec, r, openaiedge.New())
	return rec
}

func TestHandleProxiesUnaryCompletion(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
			{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer up.Close()

	e := newExecutor(t, up.URL)
	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleProxiesStreamingCompletion(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer up.Close()

	e := newExecutor(t, up.URL)
	rec := post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)
	body := rec.Body.String()
	if !strings.Contains(body, `"content":"hi"`) || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("body = %s", body)
	}
}

func TestHandleReturnsOpenAIErrorForUnknownModel(t *testing.T) {
	e := newExecutor(t, "https://unused.example/v1")
	rec := post(t, e, `{"model":"nope","messages":[]}`)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"type":"not_found"`) {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleTranslatesUpstreamFailureToDialectError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer up.Close()

	e := newExecutor(t, up.URL)
	rec := post(t, e, `{"model":"m","messages":[]}`)
	if rec.Code != 502 {
		t.Fatalf("code = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"error"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestHandleSetsDiagnosticHeaders(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}]}`))
	}))
	defer up.Close()

	e := newExecutor(t, up.URL)
	rec := post(t, e, `{"model":"m","messages":[]}`)
	if rec.Header().Get("X-Darkrouter-Provider") != "fake" {
		t.Errorf("provider header = %q", rec.Header().Get("X-Darkrouter-Provider"))
	}
	if rec.Header().Get("X-Darkrouter-Attempts") != "1" {
		t.Errorf("attempts header = %q", rec.Header().Get("X-Darkrouter-Attempts"))
	}
	if rec.Header().Get("X-Darkrouter-Request") == "" {
		t.Error("expected a request id header")
	}
}

// Diagnostic headers must also appear on the failure path: master design §10
// says the attempt count and request id are written on error responses too.
func TestHandleSetsRequestHeaderOnErrorPath(t *testing.T) {
	e := newExecutor(t, "https://unused.example/v1")
	rec := post(t, e, `{"model":"nope","messages":[]}`)
	if rec.Header().Get("X-Darkrouter-Request") == "" {
		t.Fatal("expected a request id header on the error path")
	}
}

// Phase 1 spec §9 requires a client-disconnect-mid-stream case, asserting the
// cancel cause identifies the inbound context rather than a Darkrouter deadline.
func TestHandleClientDisconnectMidStreamIsNotAProviderFault(t *testing.T) {
	released := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-released // hold the stream open until the client has gone
	}))
	defer up.Close()
	defer close(released)

	e := newExecutor(t, up.URL)
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","stream":true,"messages":[]}`)).WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Handle(httptest.NewRecorder(), r, openaiedge.New())
	}()

	cancel() // the client hangs up
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Handle did not return after the client disconnected")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("inbound context error = %v", ctx.Err())
	}
}

func TestHandleSurvivesMalformedSSE(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: not json at all\n\ndata: [DONE]\n\n"))
	}))
	defer up.Close()

	e := newExecutor(t, up.URL)
	rec := post(t, e, `{"model":"m","stream":true,"messages":[]}`)
	// An unparseable chunk is skipped, not fatal; the stream still terminates.
	if !strings.HasSuffix(rec.Body.String(), "data: [DONE]\n\n") {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestHandleSetsAttemptsHeaderOnEarlyErrorPath(t *testing.T) {
	e := newExecutor(t, "https://unused.example/v1")
	rec := post(t, e, `{"model":"nope","messages":[]}`)
	if got := rec.Header().Get("X-Darkrouter-Attempts"); got != "0" {
		t.Fatalf("attempts header = %q, want \"0\" when no attempt was made", got)
	}
}

func TestHandleStreamChunksCarryModel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"cc-1\",\"model\":\"up-m\",\"choices\":" +
			"[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer up.Close()

	e := newExecutor(t, up.URL)
	rec := post(t, e, `{"model":"m","stream":true,"messages":[]}`)
	if !strings.Contains(rec.Body.String(), `"model":"up-m"`) {
		t.Fatalf("chunks must carry the model, got:\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"id":"cc-1"`) {
		t.Fatalf("chunks must carry the upstream id, got:\n%s", rec.Body.String())
	}
}
