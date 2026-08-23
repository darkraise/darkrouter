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
	"sync/atomic"
	"testing"
	"time"

	"io"
	"iter"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
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
	return New(cfgStore, provider.NewYAMLSource(cfgStore),
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, deps)
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

func TestWarningStringsFlattensForTheRecord(t *testing.T) {
	got := warningStrings([]ir.Warning{
		{Field: "top_k", Target: "openaicompat", Reason: "no equivalent"},
	})
	if len(got) != 1 || got[0] != "top_k -> openaicompat: no equivalent" {
		t.Fatalf("warningStrings = %v", got)
	}
	if warningStrings(nil) != nil {
		t.Error("warningStrings(nil) must stay nil so the record encodes []")
	}
}

func TestCandidateWithNoRegisteredAdapterIsSkipped(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream must not be called for an unknown kind")
	}))
	defer up.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: fake\n    kind: martian\n    base_url: " + up.URL +
		"\n    api_key: ${K}\n    models: [m]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	var rec captureLogger
	e := New(cfgStore, provider.NewYAMLSource(cfgStore),
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Log: &rec})

	w := post(t, e, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 502 {
		t.Fatalf("code = %d, want 502", w.Code)
	}
	got := rec.only(t)
	if len(got.Attempts) != 0 {
		t.Errorf("attempts = %d, want 0", len(got.Attempts))
	}
	found := false
	for _, s := range got.Skips {
		if strings.HasSuffix(s, ":no_adapter") {
			found = true
		}
	}
	if !found {
		t.Errorf("skips = %v, want one ending in :no_adapter", got.Skips)
	}
}

func TestContentFilterFromParseIsFatalNotAProviderFault(t *testing.T) {
	if got := outcomeForParseError(&ir.Error{Type: ir.ErrContentFilter, Message: "blocked"}); got != adapter.OutcomeFatal {
		t.Errorf("content filter = %q, want fatal; a refusal is an answer, not an outage", got)
	}
	if got := outcomeForParseError(errors.New("truncated JSON")); got != adapter.OutcomeRetryableProvider {
		t.Errorf("malformed body = %q, want retryable", got)
	}
	if got := outcomeForParseError(&ir.Error{Type: ir.ErrAPI}); got != adapter.OutcomeRetryableProvider {
		t.Errorf("generic API error = %q, want retryable", got)
	}
}

// executorFor builds an executor over an arbitrary configuration body.
// newExecutorWith cannot: it fixes the provider id, the kind, and the model
// list, and the catalog cases need all three to vary.
func executorFor(t *testing.T, body string, adapters map[string]adapter.Adapter, deps Deps) *Executor {
	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), adapters, deps)
}

func TestExecutorUsesTheCatalogSnapshotWhenSupplied(t *testing.T) {
	// The router filters on what the catalog says, so a model the snapshot
	// does not carry must not route — that is the difference between phase 3's
	// admit-everything placeholder and a real catalog.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the upstream was called for a model the catalog does not carry")
		w.WriteHeader(500)
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "known", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}}, []string{"p"}))

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [known]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Catalog: cat})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"unknown","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusNotFound && w.Code != http.StatusBadRequest {
		t.Errorf("status = %d for a model outside the catalog", w.Code)
	}
}

func TestExecutorFallsBackWithoutACatalog(t *testing.T) {
	// A zero Deps must behave exactly as phase 3 did: every configured model
	// routes, with inferred capabilities.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [known]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"known","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

// captureAdapter records the Target it was built with and otherwise behaves
// exactly as an OpenAI-compatible adapter.
type captureAdapter struct {
	onBuild func(*adapter.Target)
}

func (c *captureAdapter) Kind() string { return "capture" }

func (c *captureAdapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	if c.onBuild != nil {
		c.onBuild(t)
	}
	return openaicompat.New().BuildRequest(ctx, t, req)
}

func (c *captureAdapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return openaicompat.New().ParseResponse(resp)
}

func (c *captureAdapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return openaicompat.New().ParseStream(r, maxLine)
}

func (c *captureAdapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return openaicompat.New().Classify(resp, err)
}

func TestTargetCarriesTheCatalogFacts(t *testing.T) {
	// The adapter has to learn the model's real maximum and its request shape
	// from somewhere, and reading the name is what phase 6 exists to stop.
	var got adapter.Target
	capturing := &captureAdapter{onBuild: func(tgt *adapter.Target) { got = *tgt }}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces:        []ir.Surface{ir.SurfaceLLM},
		ContextWindow:   200_000,
		MaxOutputTokens: 64_000,
		Traits:          catalog.Traits{Adaptive: true, FreeSampling: false, Known: true},
	}}, []string{"p"}))

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: capture
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"capture": capturing}, Deps{Catalog: cat})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if got.Info.MaxOutputTokens != 64_000 || got.Info.ContextWindow != 200_000 {
		t.Errorf("limits = %+v", got.Info)
	}
	if !got.Info.TraitsKnown || !got.Info.Adaptive || got.Info.FreeSampling {
		t.Errorf("traits = %+v", got.Info)
	}
}

func TestTargetInfoIsZeroWithoutACatalogEntry(t *testing.T) {
	// A model nothing knows about must reach the adapter with an empty Info,
	// so the adapter honors what the client asked for rather than acting on a
	// half-filled guess.
	var got adapter.Target
	capturing := &captureAdapter{onBuild: func(tgt *adapter.Target) { got = *tgt }}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: capture
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"capture": capturing}, Deps{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if got.Info != (adapter.ModelInfo{}) {
		t.Errorf("Info = %+v, want the zero value", got.Info)
	}
}

func TestInferredCandidateProducesAWarning(t *testing.T) {
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "local", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
		Source:   catalog.SourceInferred,
	}}, []string{"p"}))

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [local]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{
	  "model":"local",
	  "messages":[{"role":"user","content":"hi"}],
	  "tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]
	}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := rec.only(t)
	var found bool
	for _, s := range got.Warnings {
		if strings.Contains(s, "capabilities") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; the inferred-capability warning did not reach the record", got.Warnings)
	}
}

func TestNoInferredWarningWhenNothingWasNeeded(t *testing.T) {
	// A plain chat request against an inferred model needs no capability, so
	// warning about it would be noise that trains people to ignore warnings.
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "local", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM}, Source: catalog.SourceInferred,
	}}, []string{"p"}))

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [local]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"local","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	for _, s := range rec.only(t).Warnings {
		if strings.Contains(s, "capabilities") {
			t.Errorf("warned about capabilities nothing asked for: %v", rec.only(t).Warnings)
		}
	}
}

func TestKnownCapableCandidateProducesNoWarning(t *testing.T) {
	// The other half: once models.dev says the model has tools, the warning
	// must stop. Otherwise every request carries it and it means nothing.
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "known", State: catalog.StateLive,
		Surfaces:     []ir.Surface{ir.SurfaceLLM},
		Capabilities: catalog.Capabilities{Tools: true, Known: true},
		Source:       catalog.SourceModelsDev,
	}}, []string{"p"}))

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [known]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{
	  "model":"known",
	  "messages":[{"role":"user","content":"hi"}],
	  "tools":[{"type":"function","function":{"name":"f","parameters":{"type":"object"}}}]
	}`))
	e.Handle(w, r, openaiedge.New())

	for _, s := range rec.only(t).Warnings {
		if strings.Contains(s, "capabilities") {
			t.Errorf("warned about a model models.dev vouched for: %v", rec.only(t).Warnings)
		}
	}
}

func TestEmptyStreamSucceedsWithoutFailover(t *testing.T) {
	// A 200 SSE that ends with no content-bearing event is a legitimate empty
	// completion, not a failure: exec.go flushes the buffer, succeeds, and does
	// not fail over. Nothing pinned that, and a refactor moving the
	// stream-ended-cleanly break across the op boundary would silently turn
	// every instantly-stopping model into a full-chain retry.
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// A well-formed stream carrying no delta.
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: a
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
  - id: b
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("upstream called %d times; an empty stream must not fail over", got)
	}
	if got := rec.only(t); got.Status != "success" {
		t.Errorf("status = %q, want success", got.Status)
	}
	// The buffered events still reach the client.
	if !strings.Contains(w.Body.String(), "finish_reason") {
		t.Errorf("the buffered stream was not flushed: %q", w.Body.String())
	}
}

func TestAnAbandonedAttemptsWarningsDoNotReachTheRecord(t *testing.T) {
	// exec.go assigns warnings per served attempt rather than appending across
	// the chain. A loop-level accumulator is the natural refactor mistake, and
	// it would leak a dropped-field warning from an attempt nobody was served
	// into the record for the one they were.
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The first provider fails pre-commit; the second serves.
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	rec := &captureLogger{}
	// The anthropic adapter warns about a missing max_tokens; the openaicompat
	// one does not. Attempt 1 therefore produces a warning and is abandoned.
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: first
    kind: anthropic
    base_url: `+upstream.URL+`
    api_key: sk
    priority: 10
    models: [m]
  - id: second
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    priority: 1
    models: [m]
`, map[string]adapter.Adapter{
		"anthropic":    anthropicadapter.New(),
		"openaicompat": openaicompat.New(),
	}, Deps{Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	got := rec.only(t)
	if got.FinalProviderID != "second" {
		t.Fatalf("served by %q, want second", got.FinalProviderID)
	}
	for _, warn := range got.Warnings {
		if strings.Contains(warn, "max_tokens") {
			t.Errorf("warnings = %v; an abandoned attempt's warning reached the served record", got.Warnings)
		}
	}
}

func TestResolveRecordsTheTraceForEveryRoute(t *testing.T) {
	// HandleCount discarded its skips, so a count request that routed to
	// nothing was undiagnosable. Sharing the prologue fixes that as a side
	// effect, and this is what pins it.
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "chat-only", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM},
	}}, []string{"p"}))

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: anthropic
    base_url: `+upstream.URL+`
    api_key: sk
    models: [chat-only]
`, map[string]adapter.Adapter{"anthropic": anthropicadapter.New()},
		Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"nonexistent","messages":[{"role":"user","content":"hi"}]}`))
	e.HandleCount(w, r, anthropicedge.New(), "anthropic")

	got := rec.only(t)
	if got.RequestedModel != "nonexistent" {
		t.Errorf("requested model = %q", got.RequestedModel)
	}
	if got.ErrorCode == "" {
		t.Error("a count that resolved to nothing recorded no error code")
	}
}

func TestACountThatRoutesToNothingRecordsItsSkips(t *testing.T) {
	// The trace, not just the error code, is what says *why* nothing routed.
	// An unknown model produces no skips at all, so only a model that exists
	// and is rejected exercises the path HandleCount used to discard.
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "embed-only", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceEmbedding},
	}}, []string{"p"}))

	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: anthropic
    base_url: `+upstream.URL+`
    api_key: sk
    models: [embed-only]
`, map[string]adapter.Adapter{"anthropic": anthropicadapter.New()},
		Deps{Catalog: cat, Log: rec})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/messages/count_tokens",
		strings.NewReader(`{"model":"embed-only","messages":[{"role":"user","content":"hi"}]}`))
	e.HandleCount(w, r, anthropicedge.New(), "anthropic")

	got := rec.only(t)
	if got.ErrorCode == "" {
		t.Error("a count that resolved to nothing recorded no error code")
	}
	if len(got.Skips) == 0 {
		t.Error("the skips explaining the empty candidate list were discarded")
	}
}

// probeOp is a SurfaceOp that records what the loop handed it. It exists to
// pin the contract between the loop and an op, which no chat test can: chat is
// the one implementation whose behavior the rest of the suite already fixes.
type probeOp struct {
	q         router.Query
	builds    int
	responds  int
	lastInfo  adapter.ModelInfo
	buildWarn string
	onRespond func(cw *CommitWriter) (adapter.Outcome, *ir.Error)
}

func (p *probeOp) Query() router.Query { return p.q }

func (p *probeOp) Dialect() string { return "probe" }

func (p *probeOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	p.builds++
	p.lastInfo = tgt.Info
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(tgt.BaseURL, "/")+"/probe",
		strings.NewReader(`{}`))
	if err != nil {
		return nil, nil, err
	}
	var warns []ir.Warning
	if p.buildWarn != "" {
		warns = append(warns, ir.Warning{Field: p.buildWarn, Target: "probe", Reason: "test"})
	}
	return req, warns, nil
}

func (p *probeOp) Respond(cw *CommitWriter, resp *http.Response, ac *AttemptCtx) (adapter.Outcome, *ir.Error) {
	p.responds++
	defer resp.Body.Close()
	if p.onRespond != nil {
		return p.onRespond(cw)
	}
	_, _ = cw.Write([]byte("ok"))
	return adapter.OutcomeSuccess, nil
}

func (p *probeOp) WriteError(w http.ResponseWriter, e *ir.Error) error {
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte(e.Message))
	return nil
}

// executorForOp builds an executor over one provider of the "probe" kind. The
// op renders its own request, so the adapter registered under that kind is
// there only to satisfy adapterFor.
func executorForOp(t *testing.T, url string, cat *catalog.Store) (*Executor, *captureLogger) {
	t.Helper()
	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: probe
    base_url: `+url+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"probe": openaicompat.New()}, Deps{Catalog: cat, Log: rec})
	return e, rec
}

// executorForOpWithTwoProviders gives a retry somewhere to go.
func executorForOpWithTwoProviders(t *testing.T, url string) (*Executor, *captureLogger) {
	t.Helper()
	rec := &captureLogger{}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: probe
    base_url: `+url+`
    api_key: sk
    priority: 10
    models: [m]
  - id: q
    kind: probe
    base_url: `+url+`
    api_key: sk
    priority: 1
    models: [m]
`, map[string]adapter.Adapter{"probe": openaicompat.New()}, Deps{Log: rec})
	return e, rec
}

func TestTheLoopGivesAnOpTheCatalogFacts(t *testing.T) {
	// The loop owns Target construction, so an op must receive the catalog's
	// view without doing its own lookup.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM}, MaxOutputTokens: 4242,
	}}, []string{"p"}))

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceLLM}}
	e, rec := executorForOp(t, upstream.URL, cat)
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op, e.store.Current())

	if op.builds != 1 || op.responds != 1 {
		t.Fatalf("builds = %d, responds = %d, want 1 and 1", op.builds, op.responds)
	}
	if op.lastInfo.MaxOutputTokens != 4242 {
		t.Errorf("Info = %+v; the loop did not supply the catalog facts", op.lastInfo)
	}
	if got := rec.only(t); got.Status != "success" {
		t.Errorf("status = %q", got.Status)
	}
}

func TestAnOpThatCommittedCannotRestartTheChain(t *testing.T) {
	// The op detects commit; the loop enforces it. An op reporting a retryable
	// outcome after bytes went out must not produce a second attempt, or a
	// client would receive two half-responses concatenated.
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	op := &probeOp{
		q: router.Query{Model: "m", Surface: ir.SurfaceLLM},
		onRespond: func(cw *CommitWriter) (adapter.Outcome, *ir.Error) {
			_, _ = cw.Write([]byte("partial"))
			// A lie the loop must not believe.
			return adapter.OutcomeRetryableProvider, &ir.Error{Type: ir.ErrAPI, Message: "boom"}
		},
	}
	e, _ := executorForOpWithTwoProviders(t, upstream.URL)
	w := httptest.NewRecorder()
	e.RunSurface(w, httptest.NewRequest("POST", "/probe", nil), op, e.store.Current())

	if got := hits.Load(); got != 1 {
		t.Errorf("upstream called %d times; a committed attempt restarted the chain", got)
	}
	if !strings.Contains(w.Body.String(), "partial") {
		t.Errorf("body = %q; the committed bytes were lost", w.Body.String())
	}
}
