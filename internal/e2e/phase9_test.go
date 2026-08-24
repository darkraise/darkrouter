package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// startWorkers runs the server's background workers, most importantly the
// request log writer: newGateway wires the same *server.Server the real
// binary runs, but every existing e2e test drives it by calling the proxy and
// admin handlers directly, so nothing has ever needed Run to be live. Reading
// a request trace back does need it — the log writer only drains its channel
// into the database from inside Run — so these are the first e2e tests to
// start it.
//
// Discovery and the models.dev sync, also started by Run, are switched off in
// the harness config: they are not what these tests exercise, and their
// background rebuilds have no ordering guarantee against a test's own
// seeding — a sweep's rebuild can read the catalog before a seed commits and
// publish its stale, empty snapshot afterwards, erasing the seeded model from
// routing.
func startWorkers(t *testing.T, g *gateway) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server.Run returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not shut down after cancel")
		}
	})
}

// echoFake records what one upstream saw and answers with a canned body.
type echoFake struct {
	mu     sync.Mutex
	body   []byte
	header http.Header
	path   string
	query  string
	calls  int

	// status and reply are what it answers with.
	status      int
	reply       string
	contentType string
	// refuseIf makes the fake 400 any request whose body contains this string,
	// which is how a strict provider is simulated.
	refuseIf string
	srv      *httptest.Server
}

func newEchoFake(t *testing.T, contentType, reply string) *echoFake {
	t.Helper()
	f := &echoFake{status: http.StatusOK, reply: reply, contentType: contentType}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.body, f.header = raw, r.Header.Clone()
		f.path, f.query = r.URL.EscapedPath(), r.URL.RawQuery
		f.calls++
		refuse := f.refuseIf != "" && strings.Contains(string(raw), f.refuseIf)
		status, reply, ct := f.status, f.reply, f.contentType
		f.mu.Unlock()

		if refuse {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error",` +
				`"message":"unexpected field"}}`))
			return
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *echoFake) seen() (body []byte, header http.Header, path, query string, calls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body, f.header, f.path, f.query, f.calls
}

// seedProvider creates a provider, gives it a credential, and catalogues one
// model on it.
func seedProvider(t *testing.T, g *gateway, id, kind, baseURL, model string, priority int) {
	t.Helper()
	g.mustAdmin(t, "POST", "/api/providers", fmt.Sprintf(
		`{"id":%q,"kind":%q,"base_url":%s,"priority":%d}`,
		id, kind, jsonStr(t, baseURL), priority), http.StatusCreated)
	g.mustAdmin(t, "POST", "/api/providers/"+id+"/keys",
		`{"label":"k","secret":"sk-upstream"}`, http.StatusCreated)
	g.seedModel(t, id, model, "")
}

// waitForTrace polls the trace endpoint until the request log's background
// batcher has flushed the row, or fails the test loudly if it never shows up.
// A bare sleep would either race the 250ms default flush interval or pad every
// test with worst-case latency, so this polls instead.
func waitForTrace(t *testing.T, g *gateway, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last *httptest.ResponseRecorder
	for time.Now().Before(deadline) {
		last = g.admin(t, "GET", "/api/requests/"+requestID, "")
		if last.Code == http.StatusOK {
			return last
		}
		time.Sleep(20 * time.Millisecond)
	}
	status := "no response"
	if last != nil {
		status = fmt.Sprintf("%d %s", last.Code, last.Body.String())
	}
	t.Fatalf("trace for request %s never became readable: %s", requestID, status)
	return nil
}

// attemptPaths reads the trace for a request id and returns the path of each
// attempt in order. This is the only way the fast path is observable from
// outside the process, which is why the attempt row carries a path column.
func attemptPaths(t *testing.T, g *gateway, requestID string) []string {
	t.Helper()
	w := waitForTrace(t, g, requestID)
	var body struct {
		Attempts []struct {
			Path string `json:"path"`
		} `json:"attempts"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(body.Attempts))
	for _, a := range body.Attempts {
		out = append(out, a.Path)
	}
	return out
}

func TestClaudeCodeShapedRequestTakesTheFastPath(t *testing.T) {
	// spec §11 criterion 1: Claude Code against an Anthropic provider takes the
	// passthrough path, and a request carrying a parameter the IR does not
	// model reaches the provider intact.
	g := newGateway(t)
	startWorkers(t, g)
	f := newEchoFake(t, "application/json", `{"id":"msg_1","type":"message","role":"assistant",
		"content":[{"type":"text","text":"hi"}],"model":"claude-model","stop_reason":"end_turn",
		"usage":{"input_tokens":40,"output_tokens":6,"cache_read_input_tokens":12}}`)
	seedProvider(t, g, "ant", "anthropic", f.srv.URL, "claude-model", 10)

	w := g.proxy(t, "/v1/messages", `{"model":"claude-model","max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"some_parameter_shipped_last_week":{"nested":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", w.Code, w.Body.String())
	}

	body, header, _, _, _ := f.seen()
	if !strings.Contains(string(body), "some_parameter_shipped_last_week") {
		t.Errorf("the unmodelled field was dropped: %s", body)
	}
	if got := header.Get("x-api-key"); got != "sk-upstream" {
		t.Errorf("x-api-key = %q, want the target's", got)
	}
	if got := header.Get("Authorization"); got != "" {
		t.Errorf("an inbound credential header was forwarded: %q", got)
	}

	id := w.Header().Get("X-Darkrouter-Request")
	if got := attemptPaths(t, g, id); len(got) != 1 || got[0] != "passthrough" {
		t.Errorf("attempt paths = %v, want [passthrough]", got)
	}
}

func TestTheSameRequestFailsOverThroughTheIRPath(t *testing.T) {
	// spec §11 criterion 2: the same request failing over to a Groq-shaped
	// target translates correctly through the IR path.
	g := newGateway(t)
	startWorkers(t, g)
	dead := newEchoFake(t, "application/json", "")
	dead.status = http.StatusServiceUnavailable
	back := newEchoFake(t, "application/json", `{"id":"c","object":"chat.completion",
		"model":"shared-model","choices":[{"index":0,"message":{"role":"assistant",
		"content":"from the fallback"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":40,"completion_tokens":6}}`)

	seedProvider(t, g, "ant", "anthropic", dead.srv.URL, "shared-model", 99)
	seedProvider(t, g, "back", "openaicompat", back.srv.URL, "shared-model", 1)

	w := g.proxy(t, "/v1/messages", `{"model":"shared-model","max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"some_parameter_shipped_last_week":{"nested":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", w.Code, w.Body.String())
	}
	// The client speaks Anthropic and gets an Anthropic-shaped body back, even
	// though an openaicompat provider served it.
	if !strings.Contains(w.Body.String(), "from the fallback") ||
		!strings.Contains(w.Body.String(), `"type":"message"`) {
		t.Errorf("body = %s", w.Body.String())
	}
	id := w.Header().Get("X-Darkrouter-Request")
	got := attemptPaths(t, g, id)
	if len(got) != 2 || got[0] != "passthrough" || got[1] != "ir" {
		t.Errorf("attempt paths = %v, want [passthrough ir]", got)
	}
}

func TestAGeminiClientPassesThroughWithItsBodyUntouched(t *testing.T) {
	// spec §11 criterion 3.
	g := newGateway(t)
	startWorkers(t, g)
	f := newEchoFake(t, "application/json", `{"candidates":[{"content":{"role":"model",
		"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}`)
	seedProvider(t, g, "gem", "gemini", f.srv.URL, "gemini-2.5-pro", 10)

	inbound := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`
	w := g.proxy(t, "/v1beta/models/gemini-2.5-pro:generateContent?key=proxy-token", inbound)
	if w.Code != http.StatusOK {
		t.Fatalf("chat: %d %s", w.Code, w.Body.String())
	}

	body, _, path, query, _ := f.seen()
	if string(body) != inbound {
		t.Errorf("the body was rewritten\n got: %s\nwant: %s", body, inbound)
	}
	if !strings.HasSuffix(path, "/models/gemini-2.5-pro:generateContent") {
		t.Errorf("upstream path = %s", path)
	}
	if strings.Contains(query, "key=") {
		t.Errorf("the inbound credential reached the upstream URL: %s", query)
	}
	id := w.Header().Get("X-Darkrouter-Request")
	if got := attemptPaths(t, g, id); len(got) != 1 || got[0] != "passthrough" {
		t.Errorf("attempt paths = %v", got)
	}
}

func TestUsageAgreesAcrossPathsOnAssembledRequests(t *testing.T) {
	// spec §11 criterion 4, at the level an operator sees it: two requests, one
	// per path, against upstreams reporting the same counts.
	g := newGateway(t)
	startWorkers(t, g)
	ant := newEchoFake(t, "application/json", `{"id":"msg_1","type":"message","role":"assistant",
		"content":[{"type":"text","text":"hi"}],"model":"m-fast","stop_reason":"end_turn",
		"usage":{"input_tokens":40,"output_tokens":6,"cache_read_input_tokens":12}}`)
	other := newEchoFake(t, "application/json", `{"id":"msg_1","type":"message","role":"assistant",
		"content":[{"type":"text","text":"hi"}],"model":"m-ir","stop_reason":"end_turn",
		"usage":{"input_tokens":40,"output_tokens":6,"cache_read_input_tokens":12}}`)

	seedProvider(t, g, "ant", "anthropic", ant.srv.URL, "m-fast", 10)
	// A Gemini client reaching an Anthropic provider cannot pass through, so
	// this request takes the IR path against an identical upstream.
	seedProvider(t, g, "ant2", "anthropic", other.srv.URL, "m-ir", 10)

	fast := g.proxy(t, "/v1/messages", chatBody("m-fast"))
	irp := g.proxy(t, "/v1beta/models/m-ir:generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	for _, w := range []*httptest.ResponseRecorder{fast, irp} {
		if w.Code != http.StatusOK {
			t.Fatalf("chat: %d %s", w.Code, w.Body.String())
		}
	}

	fastID, irpID := fast.Header().Get("X-Darkrouter-Request"), irp.Header().Get("X-Darkrouter-Request")

	// The usage comparison below means nothing unless the two requests really
	// took different paths — otherwise it is IR compared against IR.
	if got := attemptPaths(t, g, fastID); len(got) != 1 || got[0] != "passthrough" {
		t.Errorf("fast request attempt paths = %v, want [passthrough]", got)
	}
	if got := attemptPaths(t, g, irpID); len(got) != 1 || got[0] != "ir" {
		t.Errorf("ir request attempt paths = %v, want [ir]", got)
	}

	a := traceUsage(t, g, fastID)
	b := traceUsage(t, g, irpID)
	if a != b {
		t.Errorf("usage differs: passthrough %+v, ir %+v", a, b)
	}
	if a.In != 40 || a.Out != 6 {
		t.Errorf("usage = %+v, want 40/6", a)
	}
}

func TestAStrictProvidersRejectionIsRetriedThroughTheIRPath(t *testing.T) {
	// spec §11 criterion 6: a strict provider's 400 is retried through the IR
	// path rather than returned to the client.
	g := newGateway(t)
	startWorkers(t, g)
	f := newEchoFake(t, "application/json", `{"id":"msg_1","type":"message","role":"assistant",
		"content":[{"type":"text","text":"served"}],"model":"m","stop_reason":"end_turn",
		"usage":{"input_tokens":4,"output_tokens":2}}`)
	f.refuseIf = "some_parameter_shipped_last_week"
	seedProvider(t, g, "ant", "anthropic", f.srv.URL, "m", 10)

	w := g.proxy(t, "/v1/messages", `{"model":"m","max_tokens":64,
		"messages":[{"role":"user","content":"hi"}],
		"some_parameter_shipped_last_week":{"nested":true}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("the client got %d, want the IR retry to have served it: %s", w.Code, w.Body)
	}
	if _, _, _, _, calls := f.seen(); calls != 2 {
		t.Errorf("upstream calls = %d, want 2", calls)
	}
	id := w.Header().Get("X-Darkrouter-Request")
	got := attemptPaths(t, g, id)
	if len(got) != 2 || got[0] != "passthrough" || got[1] != "ir" {
		t.Errorf("attempt paths = %v, want [passthrough ir]", got)
	}
}

// traceUsage reads the token counts off one request row. The trace endpoint
// does not expose cache-read counts, so only the fields it actually returns
// are asserted here.
type usageCounts struct{ In, Out int64 }

func traceUsage(t *testing.T, g *gateway, requestID string) usageCounts {
	t.Helper()
	w := waitForTrace(t, g, requestID)
	var body struct {
		TokensIn  int64 `json:"tokens_in"`
		TokensOut int64 `json:"tokens_out"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return usageCounts{In: body.TokensIn, Out: body.TokensOut}
}
