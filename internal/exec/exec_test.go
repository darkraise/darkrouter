package exec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

func newExecutor(t *testing.T, upstreamURL string) *Executor {
	t.Helper()
	return newExecutorWith(t, upstreamURL, Deps{}, 0)
}

// newExecutorWith is newExecutor with the knobs the phase 2 tests need. A zero
// total leaves the default of 10m in place.
func newExecutorWith(t *testing.T, upstreamURL string, deps Deps, total time.Duration) *Executor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: fake\n    kind: openaicompat\n    base_url: " + upstreamURL +
		"\n    api_key: ${K}\n    models: [m]\n"
	if total > 0 {
		// connect and first_byte must be set alongside total: the budget gate
		// refuses an attempt unless the remaining total covers connect +
		// first_byte, so leaving the 70s defaults would mean a short total
		// starts no attempt at all.
		body += "policy:\n  timeout:\n    connect: 5ms\n    first_byte: " +
			(total / 4).String() + "\n    total: " + total.String() + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), openaicompat.New(), deps)
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

type captureLogger struct {
	mu      sync.Mutex
	records []*store.RequestRecord
}

func (c *captureLogger) Log(r *store.RequestRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
}

func (c *captureLogger) only(t *testing.T) *store.RequestRecord {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.records) != 1 {
		t.Fatalf("got %d records, want 1", len(c.records))
	}
	return c.records[0]
}

func unaryUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
			{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`))
	}))
}

func streamUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5,\"total_tokens\":8}}\n\n" +
				"data: [DONE]\n\n"))
	}))
}

func TestHandleLogsASuccessfulRequest(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	logger := &captureLogger{}
	e := newExecutorWith(t, up.URL, Deps{Log: logger}, 0)

	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	r := logger.only(t)
	if r.Status != "success" {
		t.Errorf("Status = %q, want success", r.Status)
	}
	if r.ID == "" || r.ID != rec.Header().Get("X-Darkrouter-Request") {
		t.Errorf("record id %q does not match the response header", r.ID)
	}
	if r.Dialect != "openai" || r.Surface != "llm" {
		t.Errorf("dialect/surface = %q/%q", r.Dialect, r.Surface)
	}
	if r.RequestedModel != "m" || r.FinalModel != "m" || r.FinalProviderID != "fake" {
		t.Errorf("record = %+v", r)
	}
	if r.TokensIn != 3 || r.TokensOut != 5 {
		t.Errorf("tokens: in=%d out=%d, want 3/5", r.TokensIn, r.TokensOut)
	}
	if r.CostMicros != nil {
		t.Error("CostMicros must stay nil until phase 6 supplies pricing")
	}
	if r.TotalMs == nil || r.TTFTMs == nil {
		t.Error("TotalMs and TTFTMs must both be recorded")
	}
	if len(r.Attempts) != 1 {
		t.Fatalf("got %d attempts, want 1", len(r.Attempts))
	}
	a := r.Attempts[0]
	if a.Seq != 0 || a.ProviderID != "fake" || a.Outcome != "success" || a.StatusCode != 200 {
		t.Errorf("attempt = %+v", a)
	}
}

func TestHandleLogsAnUpstreamFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer up.Close()
	logger := &captureLogger{}
	e := newExecutorWith(t, up.URL, Deps{Log: logger}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if len(r.Attempts) != 1 || r.Attempts[0].Outcome != "retryable_provider" {
		t.Fatalf("attempts = %+v", r.Attempts)
	}
	if r.Attempts[0].StatusCode != 503 {
		t.Errorf("StatusCode = %d, want 503", r.Attempts[0].StatusCode)
	}
}

// A request that never reaches an upstream still produces a record, or the
// spend figures silently omit every misrouted request.
func TestHandleLogsAnUnknownModelWithNoAttempts(t *testing.T) {
	logger := &captureLogger{}
	e := newExecutorWith(t, "https://unused.example/v1", Deps{Log: logger}, 0)

	post(t, e, `{"model":"nope","messages":[]}`)

	r := logger.only(t)
	if r.Status != "error" {
		t.Errorf("Status = %q", r.Status)
	}
	if len(r.Attempts) != 0 {
		t.Errorf("got %d attempts, want 0", len(r.Attempts))
	}
	if r.ErrorCode == "" {
		t.Error("ErrorCode was not recorded")
	}
}

func TestHandleRecordsTTFTAndUsageOnAStream(t *testing.T) {
	up := streamUpstream()
	defer up.Close()
	logger := &captureLogger{}
	e := newExecutorWith(t, up.URL, Deps{Log: logger}, 0)

	post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if r.Status != "success" {
		t.Errorf("Status = %q", r.Status)
	}
	if r.TTFTMs == nil {
		t.Fatal("TTFT was not recorded on a stream")
	}
	if r.TotalMs == nil || *r.TTFTMs > *r.TotalMs {
		t.Errorf("TTFT %v exceeds total %v", r.TTFTMs, r.TotalMs)
	}
	// Usage arrives on a late event; without the tap it would be lost.
	if r.TokensOut != 5 {
		t.Errorf("stream usage not captured: TokensOut = %d, want 5", r.TokensOut)
	}
}

func TestHandleWithNoLoggerDoesNotPanic(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	e := newExecutorWith(t, up.URL, Deps{}, 0)
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}

type captureHealth struct {
	mu      sync.Mutex
	keys    []health.Key
	signals []health.Signal
}

func (c *captureHealth) Record(k health.Key, s health.Signal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, k)
	c.signals = append(c.signals, s)
}

func (c *captureHealth) only(t *testing.T) (health.Key, health.Signal) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.signals) != 1 {
		t.Fatalf("got %d signals, want 1", len(c.signals))
	}
	return c.keys[0], c.signals[0]
}

func TestHandleRecordsHealthOnAProviderFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer up.Close()
	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	k, s := h.only(t)
	if k.ProviderID != "fake" || k.Model != "m" {
		t.Errorf("key = %+v", k)
	}
	if s.Outcome != adapter.OutcomeRetryableProvider || s.StatusCode != 503 {
		t.Errorf("signal = %+v", s)
	}
	if s.HasRetryAfter {
		t.Error("no Retry-After was sent, but one was recorded")
	}
}

func TestHandleForwardsRetryAfter(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(429)
	}))
	defer up.Close()
	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	_, s := h.only(t)
	if s.StatusCode != 429 || !s.HasRetryAfter || s.RetryAfter != 42*time.Second {
		t.Errorf("signal = %+v", s)
	}
}

func TestHandleRecordsSuccess(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	_, s := h.only(t)
	if s.Outcome != adapter.OutcomeSuccess {
		t.Errorf("Outcome = %q, want success", s.Outcome)
	}
}

// A done criterion: a client disconnect leaves every provider healthy.
func TestClientDisconnectIsNotAProviderFailure(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the upstream open until the client has gone
		w.WriteHeader(200)
	}))
	defer up.Close()
	defer close(release)

	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"ping"}]}`))
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Handle(rec, r, openaiedge.New())
	}()
	time.Sleep(50 * time.Millisecond)
	cancel() // the client hung up
	<-done

	_, s := h.only(t)
	if s.Outcome != adapter.OutcomeClientCancelled {
		t.Fatalf("Outcome = %q, want client_cancelled — a disconnect must never "+
			"count against a provider", s.Outcome)
	}
}

// A Darkrouter-imposed deadline is a provider timeout and must be recorded.
func TestDarkrouterDeadlineIsAProviderFailure(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(200)
	}))
	defer up.Close()
	defer close(release)

	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 80*time.Millisecond)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	_, s := h.only(t)
	if s.Outcome != adapter.OutcomeRetryableProvider {
		t.Fatalf("Outcome = %q, want retryable_provider — a Darkrouter deadline "+
			"is a provider timeout, not a client disconnect", s.Outcome)
	}
}

func TestHandleWithNoHealthRecorderDoesNotPanic(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	e := newExecutorWith(t, up.URL, Deps{}, 0)
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}
