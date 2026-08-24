package exec

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/sse"
	"github.com/darkraise/darkrouter/internal/store"
)

// fakeForwarder recognizes a deliberately trivial wire format: an event whose
// data begins with "c" is content, "e" is an error, "u" is usage-only, and
// anything else is neither. The forwarder under test cares about the
// classification, never about how a real vendor spells it.
type fakeForwarder struct{}

func (fakeForwarder) BuildForward(context.Context, *adapter.Target, *adapter.Forward) (*http.Request, error) {
	return nil, nil
}

func (fakeForwarder) RecognizeEvent(ev sse.Event) adapter.RawEvent {
	switch {
	case strings.HasPrefix(ev.Data, "c"):
		return adapter.RawEvent{Content: true}
	case strings.HasPrefix(ev.Data, "e"):
		return adapter.RawEvent{ErrPayload: ev.Data}
	case strings.HasPrefix(ev.Data, "u"):
		return adapter.RawEvent{UsageOnly: true, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 4}}
	}
	return adapter.RawEvent{}
}

func (fakeForwarder) RecognizeUsage([]byte) *ir.Usage { return nil }

func TestForwardStreamBuffersUntilContentThenReplays(t *testing.T) {
	body := "data: ping\n\ndata: ping\n\ndata: c-first\n\ndata: c-second\n\n"
	cw, ac := forwardFixture(t)

	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	// Every byte, in order, including the two pings held back before commit.
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw\n%q\nwant\n%q", got, body)
	}
}

func TestForwardStreamFailsOverOnAPreCommitError(t *testing.T) {
	// Anthropic's overloaded_error under a 200. Nothing has reached the client,
	// so the chain may still move on.
	cw, ac := forwardFixture(t)
	out, ierr := ac.Exec.forwardStream(cw,
		streamResponse("data: ping\n\ndata: e-overloaded\n\n"), ac, fakeForwarder{}, false)

	if out != adapter.OutcomeRetryableProvider {
		t.Errorf("outcome = %v, want retryable_provider", out)
	}
	if ierr == nil {
		t.Fatal("no error to serve if this was the last candidate")
	}
	if cw.Committed() {
		t.Error("bytes reached the client before the error")
	}
}

func TestForwardStreamPassesAPostCommitErrorThrough(t *testing.T) {
	// After commit the recognizer's opinion no longer matters: the client
	// already has bytes and a second attempt would concatenate two halves.
	cw, ac := forwardFixture(t)
	body := "data: c-first\n\ndata: e-overloaded\n\n"
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, false)

	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw %q", got)
	}
}

func TestForwardStreamStripsOnlyTheInjectedUsageChunk(t *testing.T) {
	body := "data: c-first\n\ndata: u-usage\n\ndata: [DONE]\n\n"
	cw, ac := forwardFixture(t)
	if _, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, true); ierr != nil {
		t.Fatal(ierr)
	}
	if got := recorderBody(cw); got != "data: c-first\n\ndata: [DONE]\n\n" {
		t.Errorf("client saw %q", got)
	}
	// Stripped from the client's view, kept in the ledger. That is the whole
	// point of the injection.
	if ac.Rec.TokensIn != 3 || ac.Rec.TokensOut != 4 {
		t.Errorf("usage = %d/%d, want 3/4", ac.Rec.TokensIn, ac.Rec.TokensOut)
	}
}

func TestForwardStreamKeepsAUsageChunkTheClientAskedFor(t *testing.T) {
	body := "data: c-first\n\ndata: u-usage\n\ndata: [DONE]\n\n"
	cw, ac := forwardFixture(t)
	if _, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, false); ierr != nil {
		t.Fatal(ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw %q, want the chunk kept", got)
	}
}

func TestForwardStreamCommitsAnEmptyCompletion(t *testing.T) {
	// A stream that ends without content is a model that stopped immediately,
	// not a fault. Failing over would burn the whole chain on every one.
	body := "data: ping\n\ndata: [DONE]\n\n"
	cw, ac := forwardFixture(t)
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw %q", got)
	}
}

func TestForwardStreamRefusesAnOversizedPreCommitBuffer(t *testing.T) {
	cw, ac := forwardFixture(t)
	ac.Cfg.Server.SSE.MaxPrecommitBytes = 32

	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("data: ping-padding-padding\n\n")
	}
	out, _ := ac.Exec.forwardStream(cw, streamResponse(b.String()), ac, fakeForwarder{}, false)
	if out != adapter.OutcomeRetryableProvider {
		t.Errorf("outcome = %v, want retryable_provider", out)
	}
	if cw.Committed() {
		t.Error("an over-budget attempt reached the client")
	}
}

func TestForwardStreamDropsHopByHopAndEncodingHeaders(t *testing.T) {
	cw, ac := forwardFixture(t)
	resp := streamResponse("data: c-first\n\n")
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Header.Set("Content-Encoding", "gzip")
	resp.Header.Set("Content-Length", "999")
	resp.Header.Set("Connection", "keep-alive")
	resp.Header.Set("Keep-Alive", "timeout=5")
	resp.Header.Set("X-Request-Id", "upstream-id")

	if _, ierr := ac.Exec.forwardStream(cw, resp, ac, fakeForwarder{}, false); ierr != nil {
		t.Fatal(ierr)
	}
	h := cw.Header()
	if h.Get("Content-Type") != "text/event-stream" {
		t.Errorf("Content-Type = %q", h.Get("Content-Type"))
	}
	if h.Get("X-Request-Id") != "upstream-id" {
		t.Error("a dialect-meaningful header was dropped")
	}
	for _, k := range []string{"Content-Encoding", "Content-Length", "Connection", "Keep-Alive"} {
		if h.Get(k) != "" {
			t.Errorf("%s was forwarded: %q", k, h.Get(k))
		}
	}
	// Darkrouter's own diagnostics are added after the copy, so an upstream
	// echoing one cannot spoof it.
	if h.Get("X-Darkrouter-Provider") == "" {
		t.Error("diagnostics missing at commit")
	}
}

// forwardFixture builds the smallest AttemptCtx forwardStream reads, over a
// recorder. It uses the same executor constructor the rest of this package's
// tests use.
func forwardFixture(t *testing.T) (*CommitWriter, *AttemptCtx) {
	t.Helper()
	e := newExecutor(t, "https://unused.example/v1")
	rec := &store.RequestRecord{ID: "req", TS: time.Now()}
	cfg := &config.Config{}
	cfg.Server.SSE.MaxLineBytes = 1 << 20
	cfg.Server.SSE.MaxPrecommitBytes = 1 << 20
	w := httptest.NewRecorder()
	cw := NewCommitWriter(w)
	return cw, &AttemptCtx{
		Exec: e, Cfg: cfg, Cand: router.Candidate{ProviderID: "p", Model: "m"},
		Rec: rec, Seq: 1, Timer: time.NewTimer(time.Hour),
	}
}

func streamResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// recorderBody reaches the recorder behind the CommitWriter.
func recorderBody(cw *CommitWriter) string {
	return cw.w.(*httptest.ResponseRecorder).Body.String()
}
