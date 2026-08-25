package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"io"
	"iter"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
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

// newPricedExecutor returns an Executor whose catalog knows the pricing for
// ("groq", "m"), for tests that call priceRecord directly rather than
// driving a request through Handle.
func newPricedExecutor(t *testing.T) *Executor {
	t.Helper()
	return &Executor{deps: Deps{Catalog: catalogOf(catalog.Model{
		ProviderID: "groq", ModelID: "m",
		Pricing: catalog.Pricing{
			InputMicrosPerMTok: 1_000_000, OutputMicrosPerMTok: 1_000_000, Known: true,
		},
	})}}
}

func TestServedAttemptCarriesTheRequestUsage(t *testing.T) {
	e := newPricedExecutor(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		TokensIn: 1000, TokensOut: 500,
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "openai", Model: "x", Outcome: "error"},
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success"},
		},
	}
	e.priceRecord(rec)

	if rec.Attempts[1].TokensIn != 1000 || rec.Attempts[1].TokensOut != 500 {
		t.Fatalf("served attempt must carry the request's usage, got %d/%d",
			rec.Attempts[1].TokensIn, rec.Attempts[1].TokensOut)
	}
	if rec.Attempts[0].TokensIn != 0 {
		t.Fatalf("a failed attempt must not inherit the served attempt's usage")
	}
}

func TestAnAttemptWithNoTokensIsUnpricedNotFree(t *testing.T) {
	// A confident zero is indistinguishable from a real zero downstream, and
	// the rollup treats a non-NULL cost as authoritative.
	e := newPricedExecutor(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "groq", Model: "m", Outcome: "error"},
		},
	}
	e.priceRecord(rec)

	if rec.Attempts[0].CostMicros != nil {
		t.Fatalf("an attempt that recorded no tokens must stay unpriced, got %d",
			*rec.Attempts[0].CostMicros)
	}
}

func TestARetriedProviderAttributesUsageToTheAttemptThatServed(t *testing.T) {
	// The pre-commit 400 retry re-attempts the same provider and model, so
	// two attempt rows carry identical provider and model and only the
	// second one served.
	e := newPricedExecutor(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		TokensIn: 400, TokensOut: 200,
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "groq", Model: "m", Outcome: "fatal"},
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success"},
		},
	}
	e.priceRecord(rec)

	if rec.Attempts[0].TokensIn != 0 {
		t.Fatalf("the rejected attempt must stay at zero, got %d", rec.Attempts[0].TokensIn)
	}
	if rec.Attempts[1].TokensIn != 400 || rec.Attempts[1].TokensOut != 200 {
		t.Fatalf("the serving attempt must carry the usage, got %d/%d",
			rec.Attempts[1].TokensIn, rec.Attempts[1].TokensOut)
	}
}

// newExecutorWith is newExecutor with the knobs the phase 2 tests need. A zero
// total leaves the default of 10m in place. It is a thin wrapper over
// newExecutorRaw, the one place that writes the fixture's YAML — newExecutorFor
// below is the other caller, for the shape phase 9's tests need.
func newExecutorWith(t *testing.T, upstreamURL string, deps Deps, total time.Duration) *Executor {
	t.Helper()
	return newExecutorRaw(t, []providerSpec{{id: "fake", kind: "openaicompat",
		upstreamURL: upstreamURL, models: []string{"m"}}}, "sk",
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, deps, total, nil)
}

// providerSpec is one provider row for newExecutorRaw. priority of 0 leaves
// the config default in place, since that is indistinguishable from omitting
// the field entirely.
type providerSpec struct {
	id, kind, upstreamURL string
	models                []string
	priority              int
}

// newExecutorRaw writes the fixture config and builds the store every
// executor fixture in this package needs, varying only in how many providers
// it lists, what each serves and at what priority, the credential resolved
// for them, which adapters are wired, any aliases mapping a client-visible
// name onto a provider/model pair, and — phase 2 only — a total timeout tight
// enough to exercise the budget gate.
func newExecutorRaw(t testing.TB, specs []providerSpec, apiKeySecret string,
	adapters map[string]adapter.Adapter, deps Deps, total time.Duration,
	aliases map[string][]string) *Executor {

	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n"
	for _, s := range specs {
		body += "  - id: " + s.id + "\n    kind: " + s.kind + "\n    base_url: " + s.upstreamURL +
			"\n    api_key: ${K}\n    models: [" + strings.Join(s.models, ", ") + "]\n"
		if s.priority != 0 {
			body += "    priority: " + strconv.Itoa(s.priority) + "\n"
		}
	}
	if total > 0 {
		// connect and first_byte must be set alongside total: the budget gate
		// refuses an attempt unless the remaining total covers connect +
		// first_byte, so leaving the 70s defaults would mean a short total
		// starts no attempt at all.
		body += "policy:\n  timeout:\n    connect: 5ms\n    first_byte: " +
			(total / 4).String() + "\n    total: " + total.String() + "\n"
	}
	if len(aliases) > 0 {
		body += "aliases:\n"
		for name, targets := range aliases {
			body += "  " + name + ": [" + strings.Join(targets, ", ") + "]\n"
		}
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return apiKeySecret, true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), adapters, deps)
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
	q          router.Query
	builds     int
	responds   int
	lastInfo   adapter.ModelInfo
	lastTarget adapter.Target
	buildWarn  string
	onRespond  func(cw *CommitWriter) (adapter.Outcome, *ir.Error)
}

func (p *probeOp) Query() router.Query { return p.q }

func (p *probeOp) Dialect() string { return "probe" }

func (p *probeOp) Build(ctx context.Context, tgt *adapter.Target, ad adapter.Adapter) (*http.Request, []ir.Warning, error) {
	p.builds++
	p.lastInfo = tgt.Info
	p.lastTarget = *tgt
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
	// Assigned only when non-nil: a typed nil in the interface field reads as
	// present and would be dereferenced on the routing path.
	deps := Deps{Log: rec}
	if cat != nil {
		deps.Catalog = cat
	}
	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: probe
    base_url: `+url+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"probe": openaicompat.New()}, deps)
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

func TestAnEmbeddingRequestSkipsAChatOnlyAdapter(t *testing.T) {
	// anthropic declares no surfaces, so it defaults to llm. Its preset could
	// still claim embeddings — the catalog describes the upstream, not what
	// Darkrouter can speak — and routing there would fail at the provider.
	upstream := unaryUpstream()
	defer upstream.Close()

	cat := &catalog.Store{}
	cat.Set(catalog.NewSnapshot([]catalog.Model{{
		ProviderID: "p", ModelID: "m", State: catalog.StateLive,
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
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
    models: [m]
`, map[string]adapter.Adapter{"anthropic": anthropicadapter.New()},
		Deps{Catalog: cat, Log: rec})

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceEmbedding}}
	w := httptest.NewRecorder()
	e.RunSurface(w, httptest.NewRequest("POST", "/v1/embeddings", nil), op, e.store.Current())

	if op.builds != 0 {
		t.Errorf("the op built %d requests; a chat-only adapter must be filtered before any attempt", op.builds)
	}
	got := rec.only(t)
	var found bool
	for _, s := range got.Skips {
		if strings.Contains(s, "adapter_surface") {
			found = true
		}
	}
	if !found {
		t.Errorf("skips = %v; the trace does not explain why nothing routed", got.Skips)
	}
}

func TestAChatRequestStillRoutesToAChatOnlyAdapter(t *testing.T) {
	// The obvious regression: constraining the map must not exclude the kind
	// that only ever served llm.
	upstream := unaryUpstream()
	defer upstream.Close()

	e := executorFor(t, `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: openaicompat
    base_url: `+upstream.URL+`
    api_key: sk
    models: [m]
`, map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, Deps{})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

// executorForPreset is executorForOp with a preset named on the provider row.
// The model is a parameter because the provider's own models list gates
// routing, and each caller declares a different one in its catalog.
func executorForPreset(t *testing.T, url, preset, model string, cat *catalog.Store) (*Executor, *captureLogger) {
	t.Helper()
	rec := &captureLogger{}
	deps := Deps{Log: rec}
	if cat != nil {
		deps.Catalog = cat
	}
	body := `
server:
  proxy_listen: ":0"
providers:
  - id: p
    kind: probe
`
	if preset != "" {
		body += "    preset: " + preset + "\n"
	}
	body += `    base_url: ` + url + `
    api_key: sk
    models: [` + model + `]
`
	e := executorFor(t, body, map[string]adapter.Adapter{"probe": openaicompat.New()}, deps)
	return e, rec
}

func TestTheTargetCarriesThePresetRerankPath(t *testing.T) {
	// Spec §3.1: providers expose rerank at differing URLs, so the path is
	// data. The adapter is handed a resolved target and must not have to reach
	// into the shipped preset file to build a URL.
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	op := &probeOp{q: router.Query{Model: "rerank-v3.5", Surface: ir.SurfaceRerank}}
	e, _ := executorForPreset(t, upstream.URL, "cohere", "rerank-v3.5",
		catalogWith("p", "rerank-v3.5", ir.SurfaceRerank))
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op, e.store.Current())

	if op.lastTarget.RerankPath != "/v2/rerank" {
		t.Errorf("RerankPath = %q, want the cohere preset's quirk value", op.lastTarget.RerankPath)
	}
}

func TestAProviderWithNoPresetCarriesNoRerankPath(t *testing.T) {
	// An uncatalogued provider is a base URL and a key. Guessing a rerank path
	// for it would post a rerank body at whatever URL the guess produced.
	upstream := httptest.NewServer(jsonOK())
	defer upstream.Close()

	op := &probeOp{q: router.Query{Model: "m", Surface: ir.SurfaceRerank}}
	e, _ := executorForPreset(t, upstream.URL, "", "m", catalogWith("p", "m", ir.SurfaceRerank))
	e.RunSurface(httptest.NewRecorder(), httptest.NewRequest("POST", "/probe", nil), op, e.store.Current())

	if op.lastTarget.RerankPath != "" {
		t.Errorf("RerankPath = %q, want empty", op.lastTarget.RerankPath)
	}
}

func TestHandleRefusesACompressedBodyWith415(t *testing.T) {
	e := newExecutor(t, "https://unused.example/v1")
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	r.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	e.Handle(w, r, openaiedge.New())

	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", w.Code)
	}
	if !strings.Contains(w.Body.String(), "content-encoding") {
		t.Errorf("the error does not name the cause: %s", w.Body)
	}
}

func TestASameDialectCandidateTakesTheFastPath(t *testing.T) {
	var seen struct {
		body []byte
		auth string
		hdr  http.Header
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.body, _ = io.ReadAll(r.Body)
		seen.auth, seen.hdr = r.Header.Get("x-api-key"), r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`))
	}))
	defer up.Close()

	// An anthropic-inbound request to an anthropic provider, carrying a
	// parameter the IR does not model.
	body := `{"model":"target-model","max_tokens":16,
	          "messages":[{"role":"user","content":"hi"}],
	          "some_parameter_shipped_last_week":{"nested":true}}`
	rec, w := runChat(t, up.URL, "anthropic", body) // see the helper note below

	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if len(rec.Attempts) != 1 || rec.Attempts[0].Path != PathPassthrough {
		t.Fatalf("attempts = %+v", rec.Attempts)
	}
	// The unmodelled parameter reached the provider. This is the phase.
	if !bytes.Contains(seen.body, []byte("some_parameter_shipped_last_week")) {
		t.Errorf("the unmodelled field was dropped: %s", seen.body)
	}
	if seen.auth != "sk-upstream" {
		t.Errorf("x-api-key = %q, want the target's", seen.auth)
	}
	if got := seen.hdr.Get("Authorization"); got != "" {
		t.Errorf("the inbound credential was forwarded: %q", got)
	}
	if rec.TokensIn != 4 || rec.TokensOut != 2 {
		t.Errorf("usage = %d/%d, want 4/2", rec.TokensIn, rec.TokensOut)
	}
}

func TestACrossDialectCandidateTakesTheIRPath(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c","object":"chat.completion","choices":[{"index":0,
			"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":4,"completion_tokens":2}}`))
	}))
	defer up.Close()

	// Anthropic in, openaicompat out.
	rec, w := runChatKind(t, up.URL, "anthropic", "openaicompat",
		`{"model":"target-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if rec.Attempts[0].Path != PathIR {
		t.Errorf("path = %q, want ir", rec.Attempts[0].Path)
	}
}

func TestAPreCommit400IsRetriedThroughTheIRPath(t *testing.T) {
	// spec §9: a strict provider rejecting a field the IR path would have
	// dropped must not become a hard failure with no failover.
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if bytes.Contains(raw, []byte("some_parameter_shipped_last_week")) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error",
				"message":"unexpected field"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`))
	}))
	defer up.Close()

	rec, w := runChat(t, up.URL, "anthropic",
		`{"model":"target-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}],
		  "some_parameter_shipped_last_week":{"nested":true}}`)

	if w.Code != 200 {
		t.Fatalf("the client got %d, want the IR retry to have served it: %s", w.Code, w.Body)
	}
	if calls != 2 {
		t.Errorf("upstream calls = %d, want 2", calls)
	}
	if len(rec.Attempts) != 2 {
		t.Fatalf("attempts = %+v, want both recorded", rec.Attempts)
	}
	if rec.Attempts[0].Path != PathPassthrough || rec.Attempts[1].Path != PathIR {
		t.Errorf("paths = %q, %q", rec.Attempts[0].Path, rec.Attempts[1].Path)
	}
}

func TestAPreCommit400RetryWarnsThatTheBodyWasTranslated(t *testing.T) {
	// The retry drops the field that caused the rejection and then reports
	// plain success. Spec §3 rests the fidelity argument on such a field being
	// dropped "with a warning, which is honest", §10 requires the differential
	// corpus to assert the IR path recorded one, and §11's second done
	// criterion requires warnings "recorded for the dropped field". A client
	// that sent a valid parameter its provider's plan does not cover otherwise
	// gets a 200 and believes the parameter took effect.
	//
	// The warning cannot name the field: encoding/json discards unknown
	// top-level keys at the edge parser, so by this point nothing knows what
	// was lost. It can say the body was translated, which is what the client
	// needs to know.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if bytes.Contains(raw, []byte("some_parameter_shipped_last_week")) {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error",
				"message":"unexpected field"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`))
	}))
	defer up.Close()

	rec, w := runChat(t, up.URL, "anthropic",
		`{"model":"target-model","max_tokens":16,"messages":[{"role":"user","content":"hi"}],
		  "some_parameter_shipped_last_week":{"nested":true}}`)

	if w.Code != 200 {
		t.Fatalf("the client got %d, want the IR retry to have served it: %s", w.Code, w.Body)
	}
	var found string
	for _, got := range rec.Warnings {
		if strings.Contains(got, "passthrough") {
			found = got
		}
	}
	if found == "" {
		t.Fatalf("warnings = %v, want one recording that the forwarded body was "+
			"rejected and translated instead", rec.Warnings)
	}
	if !strings.Contains(found, "translated") {
		t.Errorf("warning = %q, want it to say the body was translated", found)
	}
}

func TestAGeminiArrayFormStreamIsServedThroughTheIRPath(t *testing.T) {
	// A Gemini client that omits ?alt=sse asks for a chunked JSON array. That
	// form carries no SSE event boundary, so the fast path cannot find the
	// commit and usage signals it needs and eligibility excludes it. The
	// predicate and the array writer are each unit-tested; this drives the path
	// between them, which is what a response over the pre-commit cap used to
	// fail the whole candidate chain on.
	var seenAlt string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAlt = r.URL.Query().Get("alt")
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		for _, chunk := range []string{
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"ba"}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"nana"}]},` +
				`"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,` +
				`"candidatesTokenCount":2}}`,
		} {
			_, _ = w.Write([]byte("data: " + chunk + "\n\n"))
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer up.Close()

	inbound := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	rec, w := runGemini(t, up.URL, "gemini-2.0-flash:streamGenerateContent", "gemini-2.0-flash", inbound)

	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if rec.Attempts[0].Path != PathIR {
		t.Fatalf("path = %q, want ir: array-form streaming is not forwardable", rec.Attempts[0].Path)
	}
	// The IR path always asks Google for SSE, whatever the client asked for.
	if seenAlt != "sse" {
		t.Errorf("upstream alt = %q, want sse", seenAlt)
	}

	body := w.Body.String()
	if !strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Fatalf("client body is not a JSON array: %q", body)
	}
	if strings.Contains(body, "data: ") {
		t.Errorf("client asked for the array form but got SSE framing: %q", body)
	}
	var chunks []struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(body), &chunks); err != nil {
		t.Fatalf("client body is not valid JSON: %v\n%s", err, body)
	}
	var text string
	for _, c := range chunks {
		for _, cand := range c.Candidates {
			for _, part := range cand.Content.Parts {
				text += part.Text
			}
		}
	}
	if text != "banana" {
		t.Errorf("reassembled text = %q, want %q", text, "banana")
	}
	if rec.TokensIn != 3 || rec.TokensOut != 2 {
		t.Errorf("usage = %d/%d, want 3/2", rec.TokensIn, rec.TokensOut)
	}
}

func TestAGeminiRequestRewritesTheURLAndNotTheBody(t *testing.T) {
	var seen struct {
		path, query string
		body        []byte
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.path, seen.query = r.URL.EscapedPath(), r.URL.RawQuery
		seen.body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"role":"model",
			"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":1}}`))
	}))
	defer up.Close()

	inbound := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	// The client asked for one model; the target serves another.
	rec, w := runGemini(t, up.URL, "gemini-2.0-flash:generateContent", "gemini-2.5-pro", inbound)

	if w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if rec.Attempts[0].Path != PathPassthrough {
		t.Fatalf("path = %q", rec.Attempts[0].Path)
	}
	if !strings.HasSuffix(seen.path, "/models/gemini-2.5-pro:generateContent") {
		t.Errorf("path = %s", seen.path)
	}
	// The body is untouched even though the model changed: it was never in it.
	if !bytes.Equal(seen.body, inbound) {
		t.Errorf("body = %s, want it untouched", seen.body)
	}
	if strings.Contains(seen.query, "key=") {
		t.Errorf("the inbound credential reached the upstream URL: %s", seen.query)
	}
}

func TestASameNameModelForwardsByteIdenticalBytes(t *testing.T) {
	var seen []byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer up.Close()

	// Whitespace and key order that no re-encoding would reproduce.
	inbound := "{ \"messages\" : [ {\"role\":\"user\",\"content\":\"hi\"} ],\n" +
		"  \"model\":\"target-model\" ,  \"max_tokens\" : 16 }"
	if _, w := runChat(t, up.URL, "anthropic", inbound); w.Code != 200 {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	if string(seen) != inbound {
		t.Errorf("bytes were rewritten\n got: %q\nwant: %q", seen, inbound)
	}
}

func TestAnInStreamOverloadedErrorFailsOverBeforeCommit(t *testing.T) {
	// Anthropic delivers overloaded_error as an SSE event under a 200. The
	// status line says nothing is wrong, so only the recognizer can tell.
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\"," +
			"\"message\":{\"id\":\"m\",\"model\":\"x\",\"usage\":{\"input_tokens\":1}}}\n\n" +
			"event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"," +
			"\"message\":\"Overloaded\"}}\n\n"))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\"," +
			"\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"served\"}}\n\n"))
	}))
	defer second.Close()

	rec, w := runChatTwoProviders(t, first.URL, second.URL, "anthropic",
		`{"model":"target-model","max_tokens":16,"stream":true,
		  "messages":[{"role":"user","content":"hi"}]}`)

	if !strings.Contains(w.Body.String(), "served") {
		t.Errorf("the second provider did not serve it: %s", w.Body)
	}
	if len(rec.Attempts) != 2 {
		t.Errorf("attempts = %+v", rec.Attempts)
	}
	if w.Header().Get("X-Darkrouter-Attempts") != "2" {
		t.Errorf("X-Darkrouter-Attempts = %q", w.Header().Get("X-Darkrouter-Attempts"))
	}
}

func TestARewriteFailureDowngradesInPlace(t *testing.T) {
	// The op is built by hand rather than driven through Handle. Every body
	// rewriteForward can reject — malformed JSON, or no top-level model — is
	// already rejected earlier, at the dialect parse or at routing, so no
	// inbound request reaches this branch through a real dialect today. It is
	// defensive code for a future dialect, and this is what reaches it: an
	// ir.Request that routes, paired with a passthrough body that cannot be
	// rewritten.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`))
	}))
	defer up.Close()

	cap := &capture{}
	e := newExecutorFor(t, "anthropic", up.URL, Deps{Log: cap})

	op := &chatOp{
		d: anthropicedge.New(),
		req: &ir.Request{
			Model:    "target-model",
			Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		},
		pt: &edge.Passthrough{
			// No top-level "model" key, so rewriteForward cannot rewrite it —
			// but ModelField is set, so forwardable still calls in.
			Body:       []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
			ModelField: "model", Surface: ir.SurfaceLLM,
		},
	}
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	e.RunSurface(httptest.NewRecorder(), r, op, e.store.Current())

	rec := cap.rec
	if len(rec.Attempts) != 1 {
		// A rewrite failure happens before any upstream connection, so it must
		// not burn a second slot from the retry budget.
		t.Fatalf("attempts = %+v, want exactly 1", rec.Attempts)
	}
	if rec.Attempts[0].Path != PathIR {
		t.Errorf("path = %q, want ir — an unrewritable body must not be forwarded", rec.Attempts[0].Path)
	}
	found := false
	for _, w := range rec.Warnings {
		if strings.Contains(w, "could not be forwarded") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want one naming the failed forward", rec.Warnings)
	}
}

// capture keeps the one record a run produces. A Logger must never block, and
// this one cannot.
type capture struct{ rec *store.RequestRecord }

func (c *capture) Log(r *store.RequestRecord) { c.rec = r }

// forwardableAdapters is every adapter kind the passthrough fast path can
// address, wired once for every fixture below that needs Handle to reach it.
func forwardableAdapters() map[string]adapter.Adapter {
	return map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.New(),
	}
}

// newExecutorFor is newExecutorWith over a chosen provider kind, with every
// forwardable adapter wired. It takes testing.TB so benchmarks can use it too.
func newExecutorFor(t testing.TB, kind, upstreamURL string, deps Deps) *Executor {
	t.Helper()
	return newExecutorForModels(t, kind, upstreamURL, []string{"target-model"}, nil, deps)
}

// newExecutorForModels is newExecutorFor parameterized on the model the lone
// provider "up" serves and, when set, an alias mapping some other
// client-visible name onto it — the shape a client asking for a model the
// provider does not itself call by that name needs, so the rewrite has
// something to actually change.
func newExecutorForModels(t testing.TB, kind, upstreamURL string, models []string,
	aliases map[string][]string, deps Deps) *Executor {

	t.Helper()
	return newExecutorRaw(t, []providerSpec{
		{id: "up", kind: kind, upstreamURL: upstreamURL, models: models},
	}, "sk-upstream", forwardableAdapters(), deps, 0, aliases)
}

// newExecutorForTwo is newExecutorFor over two same-kind providers serving the
// same model, "up" ahead of "second" at the given priority so failover order
// is deterministic rather than relying on config file order.
func newExecutorForTwo(t testing.TB, kind, urlA, urlB string, priorityA int, deps Deps) *Executor {
	t.Helper()
	return newExecutorRaw(t, []providerSpec{
		{id: "up", kind: kind, upstreamURL: urlA, models: []string{"target-model"}, priority: priorityA},
		{id: "second", kind: kind, upstreamURL: urlB, models: []string{"target-model"}},
	}, "sk-upstream", forwardableAdapters(), deps, 0, nil)
}

// dispatchChat drives one inbound chat body through Handle for the given
// dialect and returns the record the logger captured alongside the recorder.
func dispatchChat(t *testing.T, e *Executor, cap *capture, dialect, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	var d edge.Dialect
	target := "/v1/chat/completions"
	switch dialect {
	case "anthropic":
		d, target = anthropicedge.New(), "/v1/messages"
	default:
		d = openaiedge.New()
	}
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.Handle(w, r, d)
	return cap.rec, w
}

// runChatKind drives one inbound body through Handle and returns the record the
// logger captured alongside the recorder.
func runChatKind(t *testing.T, upstreamURL, dialect, kind, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	cap := &capture{}
	e := newExecutorFor(t, kind, upstreamURL, Deps{Log: cap})
	return dispatchChat(t, e, cap, dialect, body)
}

// runChat is runChatKind with the kind the dialect passes through to.
func runChat(t *testing.T, upstreamURL, dialect, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	return runChatKind(t, upstreamURL, dialect, forwardKinds[dialect], body)
}

// runChatModel is runChat for a provider that serves servedModel under a name
// the client's body does not use, wired through an alias so routing actually
// resolves the request onto it and the rewrite has a model to change.
func runChatModel(t *testing.T, upstreamURL, dialect, servedModel, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal([]byte(body), &probe); err != nil || probe.Model == "" {
		t.Fatalf("body has no top-level model field: %s", body)
	}
	cap := &capture{}
	e := newExecutorForModels(t, forwardKinds[dialect], upstreamURL, []string{servedModel},
		map[string][]string{probe.Model: {"up/" + servedModel}}, Deps{Log: cap})
	return dispatchChat(t, e, cap, dialect, body)
}

// runChatTwoProviders is runChat over two providers of the same kind, the
// first at a high enough priority that it is always tried first — the shape a
// pre-commit failover from one candidate to the next needs.
func runChatTwoProviders(t *testing.T, urlA, urlB, dialect, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	cap := &capture{}
	e := newExecutorForTwo(t, forwardKinds[dialect], urlA, urlB, 99, Deps{Log: cap})
	return dispatchChat(t, e, cap, dialect, body)
}

// runGemini drives one inbound Gemini body through Handle. pathValue is the
// "model:method" URL segment the client sent, mirroring how the mux would
// have set it; servedModel is what the provider is configured to answer to,
// aliased in when it differs so the passthrough's URL rewrite has something
// to rewrite. It uses geminiedge.NewFor(r) rather than New() so a client's
// ?alt=sse selection is read the way Handle's real caller reads it.
func runGemini(t *testing.T, upstreamURL, pathValue, servedModel string, body []byte) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	clientModel, _ := geminiedge.ExtractModel(pathValue)
	var aliases map[string][]string
	if clientModel != servedModel {
		aliases = map[string][]string{clientModel: {"up/" + servedModel}}
	}
	cap := &capture{}
	e := newExecutorForModels(t, "gemini", upstreamURL, []string{servedModel}, aliases, Deps{Log: cap})

	// A real client's key travels in the query string here, which is exactly
	// what must not survive into the upstream URL.
	r := httptest.NewRequest("POST", "/v1beta/models/"+pathValue+"?key=client-key", bytes.NewReader(body))
	r.SetPathValue("model", pathValue)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	e.Handle(w, r, geminiedge.NewFor(r))
	return cap.rec, w
}
