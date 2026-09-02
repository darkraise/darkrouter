package exec

import (
	"context"
	"errors"
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

// noStreamError is the stream error writer for fixtures whose upstream never
// fails after commit.
type noStreamError struct{}

func (noStreamError) WriteStreamError(http.ResponseWriter, *ir.Error) {}

// recordedStreamError captures the terminal error a forwarded stream rendered.
type recordedStreamError struct{ errs []*ir.Error }

func (r *recordedStreamError) WriteStreamError(w http.ResponseWriter, e *ir.Error) {
	r.errs = append(r.errs, e)
	_, _ = w.Write([]byte("data: {\"error\":\"" + e.Message + "\"}\n\n"))
}

func TestForwardStreamBuffersUntilContentThenReplays(t *testing.T) {
	body := "data: ping\n\ndata: ping\n\ndata: c-first\n\ndata: c-second\n\n"
	cw, ac := forwardFixture(t)

	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	// Every byte, in order, including the two pings held back before commit.
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw\n%q\nwant\n%q", got, body)
	}
}

func TestAScannerErrorMidStreamLeavesTheClientStreamIntact(t *testing.T) {
	// spec §7: losing a token count is acceptable, corrupting a response is
	// not. An event the recognizer cannot parse is simply forwarded.
	cw, ac := forwardFixture(t)
	body := "data: c-first\n\ndata: {not json at all\n\ndata: c-second\n\n"
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("the client stream was altered\n got: %q\nwant: %q", got, body)
	}
}

func TestForwardStreamFailsOverOnAPreCommitError(t *testing.T) {
	// Anthropic's overloaded_error under a 200. Nothing has reached the client,
	// so the chain may still move on.
	cw, ac := forwardFixture(t)
	out, ierr := ac.Exec.forwardStream(cw,
		streamResponse("data: ping\n\ndata: e-overloaded\n\n"), ac, fakeForwarder{}, noStreamError{}, false)

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
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false)

	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw %q", got)
	}
}

func TestForwardStreamCopiesRawAfterAPostCommitSplitterOverflow(t *testing.T) {
	// spec §6: past commit the recognizer's opinion no longer matters. The
	// bytes already sitting in the carry still owe the client, and nothing
	// after may be lost either — corrupting a response is worse than losing
	// a token count.
	cw, ac := forwardFixture(t)
	ac.Cfg.Server.SSE.MaxLineBytes = 16

	// No terminator at all, so the whole tail stays in the carry unterminated
	// and exceeds the cap within the same read that carries the commit event.
	body := "data: c-first\n\ndata: " + strings.Repeat("x", 40)
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if !cw.Committed() {
		t.Fatal("the first event should have committed")
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client did not receive every byte\n got: %q\nwant: %q", got, body)
	}
	if len(ac.Rec.Warnings) == 0 {
		t.Error("no warning recorded for the scanner error")
	}
}

// chunkedBody hands out one prepared chunk per Read, so a test can put the
// splitter overflow in the first read and leave real bytes unread behind it.
// A single-read body cannot: the carry flush alone satisfies the assertion
// while the io.Copy leg moves nothing.
type chunkedBody struct{ chunks []string }

func (c *chunkedBody) Read(p []byte) (int, error) {
	if len(c.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.chunks[0])
	if n < len(c.chunks[0]) {
		c.chunks[0] = c.chunks[0][n:]
		return n, nil
	}
	c.chunks = c.chunks[1:]
	return n, nil
}

func TestForwardStreamCopiesTheUnreadRemainderAfterAPostCommitOverflow(t *testing.T) {
	// The overflow happens on the first read; everything in the second read
	// has never been seen by the splitter and can only reach the client
	// through the io.Copy leg. Losing it would truncate a committed stream
	// silently, which is the failure the raw copy exists to prevent.
	cw, ac := forwardFixture(t)
	ac.Cfg.Server.SSE.MaxLineBytes = 16

	first := "data: c-first\n\ndata: " + strings.Repeat("x", 40)
	second := strings.Repeat("y", 30) + "\n\n"
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(&chunkedBody{chunks: []string{first, second}}),
	}

	out, ierr := ac.Exec.forwardStream(cw, resp, ac, fakeForwarder{}, noStreamError{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got, want := recorderBody(cw), first+second; got != want {
		t.Errorf("client did not receive every byte\n got: %q\nwant: %q", got, want)
	}
}

// flakyBody reads through to r, then reports err instead of io.EOF — a
// connection that dies mid-stream rather than closing cleanly.
type flakyBody struct {
	r   io.Reader
	err error
}

func (f *flakyBody) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if err == io.EOF {
		return n, f.err
	}
	return n, err
}

func TestForwardStreamRecordsAPostCommitTransportFailure(t *testing.T) {
	// spec §9: after commit a failure becomes an in-stream error, rendered in
	// the inbound dialect, and the row records it too.
	cw, ac := forwardFixture(t)
	body := "data: c-first\n\n"
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(&flakyBody{r: strings.NewReader(body), err: errors.New("connection reset")}),
	}
	se := &recordedStreamError{}
	out, ierr := ac.Exec.forwardStream(cw, resp, ac, fakeForwarder{}, se, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if !cw.Committed() {
		t.Fatal("the content event should have committed")
	}
	if got := recorderBody(cw); !strings.HasPrefix(got, body) {
		t.Errorf("client saw %q, want it to start with %q", got, body)
	}
	if len(se.errs) != 1 || se.errs[0].Message != msgUpstreamReadFailed {
		t.Errorf("stream errors = %+v, want one fixed upstream-read-failed message", se.errs)
	}
	if strings.Contains(recorderBody(cw), "connection reset") {
		t.Error("the transport error's detail reached the client")
	}
	if len(ac.Rec.Warnings) == 0 {
		t.Error("no warning recorded for the post-commit transport failure")
	}
}

func TestForwardStreamStripsOnlyTheInjectedUsageChunk(t *testing.T) {
	body := "data: c-first\n\ndata: u-usage\n\ndata: [DONE]\n\n"
	cw, ac := forwardFixture(t)
	if _, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, true); ierr != nil {
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
	if _, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false); ierr != nil {
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
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false)
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
	out, _ := ac.Exec.forwardStream(cw, streamResponse(b.String()), ac, fakeForwarder{}, noStreamError{}, false)
	if out != adapter.OutcomeRetryableProvider {
		t.Errorf("outcome = %v, want retryable_provider", out)
	}
	if cw.Committed() {
		t.Error("an over-budget attempt reached the client")
	}
}

func TestForwardStreamForwardsOnlyAllowlistedHeaders(t *testing.T) {
	cw, ac := forwardFixture(t)
	resp := streamResponse("data: c-first\n\n")
	resp.Header.Set("Content-Type", "text/event-stream")
	resp.Header.Set("Content-Encoding", "gzip")
	resp.Header.Set("Content-Length", "999")
	resp.Header.Set("Connection", "keep-alive")
	resp.Header.Set("Keep-Alive", "timeout=5")
	resp.Header.Set("X-Request-Id", "upstream-id")
	resp.Header.Set("Cache-Control", "no-store")
	resp.Header.Set("X-RateLimit-Remaining-Tokens", "12")
	resp.Header.Set("Set-Cookie", "session=abc")
	resp.Header.Set("Access-Control-Allow-Origin", "*")
	resp.Header.Set("Server", "upstream/1.0")

	if _, ierr := ac.Exec.forwardStream(cw, resp, ac, fakeForwarder{}, noStreamError{}, false); ierr != nil {
		t.Fatal(ierr)
	}
	h := cw.Header()
	for k, want := range map[string]string{
		"Content-Type": "text/event-stream", "X-Request-Id": "upstream-id",
		"Cache-Control": "no-store", "X-RateLimit-Remaining-Tokens": "12",
	} {
		if h.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, h.Get(k), want)
		}
	}
	for _, k := range []string{"Content-Encoding", "Content-Length", "Connection", "Keep-Alive",
		"Set-Cookie", "Access-Control-Allow-Origin", "Server"} {
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

func TestForwardStreamCommitsAPreCommitUnterminatedTail(t *testing.T) {
	// The provider never sent the trailing blank line after the content
	// event. Without sp.flush() at end of stream, the buffered ping would
	// reach the client and the content that follows it never would.
	body := "data: ping\n\ndata: c-first\n"
	cw, ac := forwardFixture(t)
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if !cw.Committed() {
		t.Error("the unterminated content event should have committed")
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw\n%q\nwant\n%q", got, body)
	}
}

func TestForwardStreamDeliversAPostCommitUnterminatedTail(t *testing.T) {
	// The second event, after commit, arrives without a trailing blank line.
	// A dropped flush would silently swallow it.
	body := "data: c-first\n\ndata: c-second\n"
	cw, ac := forwardFixture(t)
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw\n%q\nwant\n%q", got, body)
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

// usageForwarder answers with a fixed usage for any body that mentions one.
type usageForwarder struct{ fakeForwarder }

func (usageForwarder) RecognizeUsage(body []byte) *ir.Usage {
	if !strings.Contains(string(body), "usage") {
		return nil
	}
	return &ir.Usage{InputTokens: 11, OutputTokens: 7}
}

func TestForwardUnaryWritesTheBodyVerbatim(t *testing.T) {
	body := `{"id":"x","choices":[{"message":{"content":"a < b && c > d"}}],"usage":{}}`
	cw, ac := forwardFixture(t)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	out, ierr := ac.Exec.forwardUnary(cw, resp, ac, usageForwarder{})
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("client saw\n%q\nwant\n%q", got, body)
	}
	if ac.Rec.TokensIn != 11 || ac.Rec.TokensOut != 7 {
		t.Errorf("usage = %d/%d", ac.Rec.TokensIn, ac.Rec.TokensOut)
	}
	if ac.Rec.FinalProviderID != "p" {
		t.Errorf("FinalProviderID = %q", ac.Rec.FinalProviderID)
	}
}

func TestForwardUnaryRecordsUnknownUsageRatherThanEstimating(t *testing.T) {
	// spec §7: an estimate silently mixed into real accounting makes the whole
	// ledger untrustworthy.
	cw, ac := forwardFixture(t)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"x"}`)),
	}
	if _, ierr := ac.Exec.forwardUnary(cw, resp, ac, usageForwarder{}); ierr != nil {
		t.Fatal(ierr)
	}
	if ac.Rec.TokensIn != 0 || ac.Rec.TokensOut != 0 {
		t.Errorf("tokens invented: %d/%d", ac.Rec.TokensIn, ac.Rec.TokensOut)
	}
	if len(ac.Rec.Warnings) == 0 {
		t.Error("nothing on the row says the count is unknown")
	}
}

func TestForwardUnaryDropsEncodingHeaders(t *testing.T) {
	cw, ac := forwardFixture(t)
	resp := &http.Response{
		StatusCode: 201,
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"Content-Length":   []string{"999"},
			"Content-Encoding": []string{"gzip"},
		},
		Body: io.NopCloser(strings.NewReader(`{"usage":{}}`)),
	}
	if _, ierr := ac.Exec.forwardUnary(cw, resp, ac, usageForwarder{}); ierr != nil {
		t.Fatal(ierr)
	}
	rr := cw.w.(*httptest.ResponseRecorder)
	if rr.Code != 201 {
		t.Errorf("status = %d, want the upstream's 201", rr.Code)
	}
	if h := cw.Header(); h.Get("Content-Length") != "" || h.Get("Content-Encoding") != "" {
		t.Errorf("encoding headers forwarded: %v", h)
	}
}

func TestForwardUnaryBreachesCapButSendsWholeBody(t *testing.T) {
	// A body larger than maxForwardedUnaryBytes must be sent in full, even though
	// its usage cannot be extracted from the buffered part. This is the core
	// design decision: losing a token count is acceptable, corrupting a response
	// is not.
	old := maxForwardedUnaryBytes
	defer func() { maxForwardedUnaryBytes = old }()

	cap := int64(32)
	maxForwardedUnaryBytes = cap

	// Build a body larger than the cap. First 30 bytes are within the cap,
	// the rest breaches it. All of it must reach the client.
	buffered := `{"id":"x","usage":{`          // 18 bytes
	overflow := strings.Repeat("a", 25) + `}}` // 27 bytes
	fullBody := buffered + overflow            // 45 bytes total
	if len(fullBody) <= int(cap) {
		t.Fatalf("body size %d must exceed cap %d", len(fullBody), cap)
	}

	cw, ac := forwardFixture(t)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(fullBody)),
	}

	out, ierr := ac.Exec.forwardUnary(cw, resp, ac, usageForwarder{})
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}

	// The client must receive every byte.
	if got := recorderBody(cw); got != fullBody {
		t.Errorf("client body corrupted: got %q, want %q", got, fullBody)
	}

	// Usage extraction failed, so tokens are not recorded.
	if ac.Rec.TokensIn != 0 || ac.Rec.TokensOut != 0 {
		t.Errorf("tokens recorded when buffer was breached: %d/%d", ac.Rec.TokensIn, ac.Rec.TokensOut)
	}

	// The row explains why the count is missing.
	if len(ac.Rec.Warnings) == 0 {
		t.Error("no warning explaining why usage was not extracted")
	}
}

func TestForwardUnaryBoundaryExactlyAtCap(t *testing.T) {
	// A body of exactly maxForwardedUnaryBytes bytes must be treated as not
	// oversized, so its usage is parsed normally.
	old := maxForwardedUnaryBytes
	defer func() { maxForwardedUnaryBytes = old }()

	cap := int64(40)
	maxForwardedUnaryBytes = cap

	// Create a body of exactly cap bytes that includes "usage" so the
	// forwarder can extract it. Base string is 34 bytes, so add 6 spaces.
	body := `{"id":"x","choices":[],"usage":{}}` + strings.Repeat(" ", 6) // 40 bytes
	if int64(len(body)) != cap {
		t.Fatalf("body size %d must equal cap %d", len(body), cap)
	}

	cw, ac := forwardFixture(t)
	resp := &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	out, ierr := ac.Exec.forwardUnary(cw, resp, ac, usageForwarder{})
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}

	// At exactly the cap, usage is parsed normally.
	if ac.Rec.TokensIn != 11 || ac.Rec.TokensOut != 7 {
		t.Errorf("usage not parsed at cap: %d/%d", ac.Rec.TokensIn, ac.Rec.TokensOut)
	}

	// No warning about the buffer; the body fit.
	for _, w := range ac.Rec.Warnings {
		if strings.Contains(w, "exceeded the buffer") {
			t.Errorf("false warning about buffer for body at cap: %s", w)
		}
	}
}

func TestForwardStreamStripsAnInjectedUsageChunkOnAnEmptyCompletion(t *testing.T) {
	// No content ever arrives, so commit never happens through the content
	// branch — the stream ends and the deferred commit() call at EOF fires
	// instead. The injected summary chunk must not survive into that replay:
	// the client never asked for it, and forwarding it would be a fourth body
	// mutation.
	body := "data: u-usage\n\ndata: [DONE]\n\n"
	cw, ac := forwardFixture(t)
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, noStreamError{}, true)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != "data: [DONE]\n\n" {
		t.Errorf("client saw %q, want the usage chunk stripped", got)
	}
	// Stripped from the client's view, kept in the ledger, same as the
	// post-commit case.
	if ac.Rec.TokensIn != 3 || ac.Rec.TokensOut != 4 {
		t.Errorf("usage = %d/%d, want 3/4", ac.Rec.TokensIn, ac.Rec.TokensOut)
	}
}
