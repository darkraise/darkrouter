# Darkrouter Phase 1 — Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** A deployable Go binary that proxies OpenAI chat completions — streaming and non-streaming, with tools and vision — to one configured OpenAI-compatible provider, with config hot-reload and Docker packaging.

**Architecture:** Requests enter through an edge `Dialect` that parses the wire format into a canonical IR, pass through a single-candidate executor, and leave through an `Adapter` that renders IR to the upstream wire format. Configuration lives in an `atomic.Pointer` swapped by a directory watcher. Two HTTP listeners run in one process: a proxy port serving `/v1/*` and an admin port serving health and metrics.

**Tech Stack:** Go 1.26, `gopkg.in/yaml.v3`, `github.com/fsnotify/fsnotify`, `github.com/oklog/ulid/v2`. No web framework; `net/http` and `http.ServeMux` only.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase1-foundation.md` (master design: `docs/superpowers/specs/2026-08-22-darkrouter-design.md`)

## Global Constraints

- Go 1.26. Module path `github.com/darkraise/darkrouter`.
- `CGO_ENABLED=0` everywhere. The binary must stay static.
- English only in code, comments, commits, and errors.
- Commits are `<type>(<scope>): <subject>`, subject ≤50 chars, imperative, no period.
- No plaintext credentials in any file. `api_key` accepts `${ENV}` references only.
- The IR types in `internal/ir` are defined complete in Task 1 even where Phase 1 cannot populate them. Adding fields later touches every adapter.
- No `bufio.Writer` may wrap a `http.ResponseWriter` on a streaming path, and the proxy server sets no global `WriteTimeout`.
- Every context is created with `context.WithCancelCause` or `context.WithTimeoutCause`. Phase 2 needs the cause to tell a client disconnect from a Darkrouter deadline.
- Default SSE line cap is 1 MiB (`server.sse.max_line_bytes`). `bufio.Scanner`'s 64 KiB default is too small for tool-call argument deltas.

---

### Task 1: Module scaffold and the canonical IR types

**Files:**
- Create: `go.mod`, `.gitignore`
- Create: `internal/ir/ir.go`
- Test: `internal/ir/ir_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ir.Request`, `ir.Message`, `ir.ContentBlock`, `ir.Response`, `ir.Usage`, `ir.Error`, `ir.StreamEvent`, `ir.Needs`, and the `Role`/`BlockType`/`StopReason`/`EventType`/`ErrorType` string enums. `func (r *Request) Needs() Needs`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Initialize the module**

```bash
cd D:/Repositories/Personal/darkrouter
go mod init github.com/darkraise/darkrouter
printf 'darkrouter\ndarkrouter.exe\n/data/\ndarkrouter.yaml\n' > .gitignore
```

- [ ] **Step 2: Write the failing test**

Create `internal/ir/ir_test.go`:

```go
package ir

import "testing"

func TestNeedsDetectsTools(t *testing.T) {
	r := &Request{Tools: []Tool{{Name: "get_weather"}}}
	if !r.Needs().Tools {
		t.Fatal("expected Tools to be true when the request declares tools")
	}
}

func TestNeedsDetectsVisionFromAnyMessage(t *testing.T) {
	r := &Request{Messages: []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}},
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockImage, Media: &Media{MIME: "image/png"}}}},
	}}
	if !r.Needs().Vision {
		t.Fatal("expected Vision to be true when any message carries an image block")
	}
}

func TestNeedsDetectsReasoning(t *testing.T) {
	r := &Request{Reasoning: &Reasoning{Effort: "high"}}
	if !r.Needs().Reasoning {
		t.Fatal("expected Reasoning to be true when a reasoning budget is set")
	}
}

func TestNeedsIsFalseForPlainTextRequest(t *testing.T) {
	r := &Request{Messages: []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}},
	}}
	n := r.Needs()
	if n.Tools || n.Vision || n.Reasoning {
		t.Fatalf("expected all needs false, got %+v", n)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/ir/ -run TestNeeds -v`
Expected: FAIL — build error, `undefined: Request`.

- [ ] **Step 4: Write the IR types**

Create `internal/ir/ir.go`:

```go
// Package ir holds Darkrouter's canonical request, response, and stream types.
// It has no I/O and depends on no other internal package.
package ir

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type BlockType string

const (
	BlockText             BlockType = "text"
	BlockImage            BlockType = "image"
	BlockAudio            BlockType = "audio"
	BlockDocument         BlockType = "document"
	BlockThinking         BlockType = "thinking"
	BlockRedactedThinking BlockType = "redacted_thinking"
	BlockToolUse          BlockType = "tool_use"
	BlockToolResult       BlockType = "tool_result"
)

type StopReason string

const (
	StopEndTurn       StopReason = "end_turn"
	StopMaxTokens     StopReason = "max_tokens"
	StopToolUse       StopReason = "tool_use"
	StopStopSequence  StopReason = "stop_sequence"
	StopContentFilter StopReason = "content_filter"
	StopPauseTurn     StopReason = "pause_turn"
	StopError         StopReason = "error"
)

type ErrorType string

const (
	ErrInvalidRequest ErrorType = "invalid_request"
	ErrAuthentication ErrorType = "authentication"
	ErrPermission     ErrorType = "permission"
	ErrNotFound       ErrorType = "not_found"
	ErrRateLimit      ErrorType = "rate_limit"
	ErrOverloaded     ErrorType = "overloaded"
	ErrAPI            ErrorType = "api_error"
	ErrContentFilter  ErrorType = "content_filter"
	ErrDarkrouter     ErrorType = "darkrouter"
)

type Media struct {
	MIME string
	Data string // base64
	URL  string
}

type Thinking struct {
	Text      string
	Signature string
	Data      string // redacted payload
}

type ToolUse struct {
	ID    string
	Name  string
	Input json.RawMessage
}

type ToolResult struct {
	ToolUseID string
	Content   string
	IsError   bool
}

type CacheControl struct {
	Type string // "ephemeral"
	TTL  string // "5m" | "1h"
}

type ContentBlock struct {
	Type         BlockType
	Text         string
	Media        *Media
	Thinking     *Thinking
	ToolUse      *ToolUse
	ToolResult   *ToolResult
	CacheControl *CacheControl
	Extra        map[string]json.RawMessage
}

type Message struct {
	Role    Role
	Content []ContentBlock
}

type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type ToolChoice struct {
	Mode string // "auto" | "none" | "any" | "tool"
	Name string
}

type Reasoning struct {
	Effort string // "low" | "medium" | "high"
	Budget int
}

type ResponseFormat struct {
	Type   string // "json_schema"
	Schema json.RawMessage
}

type SafetySetting struct {
	Category  string
	Threshold string
}

type Request struct {
	Model          string
	System         []ContentBlock
	Messages       []Message
	Tools          []Tool
	ToolChoice     *ToolChoice
	MaxTokens      *int
	Temperature    *float64
	TopP           *float64
	TopK           *int
	StopSequences  []string
	Stream         bool
	Reasoning      *Reasoning
	ResponseFormat *ResponseFormat
	Safety         []SafetySetting
	Metadata       map[string]string
	Extra          map[string]json.RawMessage
}

type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	ReasoningTokens  int
	Estimated        bool
}

type Warning struct {
	Field  string
	Target string
	Reason string
}

type Response struct {
	ID         string
	Model      string
	Content    []ContentBlock
	StopReason StopReason
	Usage      Usage
	Warnings   []Warning
	Extra      map[string]json.RawMessage
}

type Error struct {
	Type    ErrorType
	Message string
	Code    string
}

func (e *Error) Error() string { return string(e.Type) + ": " + e.Message }

type EventType string

const (
	EventMessageStart     EventType = "message_start"
	EventBlockStart       EventType = "content_block_start"
	EventContentDelta     EventType = "content_delta"
	EventBlockStop        EventType = "content_block_stop"
	EventMessageDelta     EventType = "message_delta"
	EventMessageStop      EventType = "message_stop"
	EventPing             EventType = "ping"
	EventError            EventType = "error"
)

// Delta carries incremental content for exactly one block kind.
type Delta struct {
	Type      BlockType
	Text      string
	Thinking  string
	ToolInput string // JSON fragment
	ToolID    string
	ToolName  string
}

type StreamEvent struct {
	Type       EventType
	Index      int
	Delta      *Delta
	Usage      *Usage
	StopReason StopReason
	Err        *Error
}

// Needs reports the capabilities a target must have to serve this request.
// The router consults it; it is the reason every request is parsed to IR even
// on the passthrough path.
type Needs struct {
	Tools     bool
	Vision    bool
	Reasoning bool
}

func (r *Request) Needs() Needs {
	n := Needs{
		Tools:     len(r.Tools) > 0,
		Reasoning: r.Reasoning != nil,
	}
	for _, m := range r.Messages {
		for _, b := range m.Content {
			if b.Type == BlockImage {
				n.Vision = true
			}
		}
	}
	return n
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/ir/ -v`
Expected: PASS, four tests.

- [ ] **Step 6: Commit**

```bash
git add go.mod .gitignore internal/ir/
git commit -m "feat(ir): add canonical request and stream types"
```

---

### Task 2: SSE reader

**Files:**
- Create: `internal/sse/reader.go`
- Test: `internal/sse/reader_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `sse.Event{Type, Data, ID, Retry string}`, `func NewReader(r io.Reader, maxLine int) *Reader`, `func (*Reader) Next() (Event, error)` returning `io.EOF` at stream end, `sse.ErrLineTooLong`, and `const sse.Done = "[DONE]"`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/sse/reader_test.go`:

```go
package sse

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func readAll(t *testing.T, body string, maxLine int) []Event {
	t.Helper()
	r := NewReader(strings.NewReader(body), maxLine)
	var got []Event
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			return got
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, ev)
	}
}

func TestReaderParsesSimpleEvent(t *testing.T) {
	got := readAll(t, "data: hello\n\n", 1024)
	if len(got) != 1 || got[0].Data != "hello" {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderIgnoresCommentLines(t *testing.T) {
	// OpenRouter emits ": OPENROUTER PROCESSING" keepalives. A parser that
	// treats every line as data breaks on the most popular aggregator.
	got := readAll(t, ": OPENROUTER PROCESSING\n\ndata: hello\n\n", 1024)
	if len(got) != 1 || got[0].Data != "hello" {
		t.Fatalf("comment line was not ignored: %+v", got)
	}
}

func TestReaderJoinsMultipleDataLines(t *testing.T) {
	got := readAll(t, "data: one\ndata: two\n\n", 1024)
	if len(got) != 1 || got[0].Data != "one\ntwo" {
		t.Fatalf("got %q", got[0].Data)
	}
}

func TestReaderHandlesCarriageReturnTerminators(t *testing.T) {
	got := readAll(t, "data: a\r\rdata: b\r\r", 1024)
	if len(got) != 2 || got[0].Data != "a" || got[1].Data != "b" {
		t.Fatalf("lone CR not handled: %+v", got)
	}
}

func TestReaderReadsEventAndIDFields(t *testing.T) {
	got := readAll(t, "event: ping\nid: 7\ndata: x\n\n", 1024)
	if len(got) != 1 || got[0].Type != "ping" || got[0].ID != "7" {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderStripsOneOptionalSpaceOnly(t *testing.T) {
	got := readAll(t, "data:  padded\n\n", 1024)
	if got[0].Data != " padded" {
		t.Fatalf("expected exactly one space stripped, got %q", got[0].Data)
	}
}

func TestReaderSurfacesDoneSentinel(t *testing.T) {
	got := readAll(t, "data: [DONE]\n\n", 1024)
	if len(got) != 1 || got[0].Data != Done {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderTreatsEOFWithoutDoneAsNormalEnd(t *testing.T) {
	got := readAll(t, "data: partial\n\n", 1024)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestReaderRejectsOversizedLine(t *testing.T) {
	body := "data: " + strings.Repeat("x", 200) + "\n\n"
	r := NewReader(strings.NewReader(body), 64)
	if _, err := r.Next(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("expected ErrLineTooLong, got %v", err)
	}
}

func TestReaderIgnoresUnknownFields(t *testing.T) {
	got := readAll(t, "foo: bar\ndata: x\n\n", 1024)
	if len(got) != 1 || got[0].Data != "x" {
		t.Fatalf("got %+v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sse/ -v`
Expected: FAIL — build error, `undefined: NewReader`.

- [ ] **Step 3: Write the reader**

Create `internal/sse/reader.go`:

```go
// Package sse implements the subset of the WHATWG EventSource wire format that
// LLM providers actually use, plus the OpenAI "[DONE]" sentinel.
package sse

import (
	"bufio"
	"errors"
	"io"
	"strings"
)

// Done is the OpenAI-dialect stream terminator. It is not a JSON payload.
const Done = "[DONE]"

var ErrLineTooLong = errors.New("sse: line exceeds configured maximum")

type Event struct {
	Type  string
	Data  string
	ID    string
	Retry string
}

type Reader struct {
	sc *bufio.Scanner
}

// NewReader reads events from r, rejecting any line longer than maxLine bytes.
func NewReader(r io.Reader, maxLine int) *Reader {
	sc := bufio.NewScanner(r)
	// The effective cap is max(maxLine, cap(buf)), so the initial buffer must
	// not exceed maxLine or a small cap silently stops being enforced.
	sc.Buffer(make([]byte, 0, min(4096, maxLine)), maxLine)
	sc.Split(splitLines)
	return &Reader{sc: sc}
}

// splitLines splits on LF, CRLF, and a lone CR, all of which the SSE grammar
// permits as line terminators.
func splitLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		switch data[i] {
		case '\n':
			return i + 1, data[:i], nil
		case '\r':
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			if atEOF {
				return i + 1, data[:i], nil
			}
			// A trailing CR may be half of a CRLF; wait for more bytes.
			return 0, nil, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Next returns the next dispatched event, or io.EOF when the stream ends.
func (r *Reader) Next() (Event, error) {
	var (
		ev   Event
		data []string
		seen bool
	)
	for r.sc.Scan() {
		line := r.sc.Text()
		if line == "" {
			if !seen {
				continue // blank line with nothing buffered dispatches nothing
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		}
		if strings.HasPrefix(line, ":") {
			continue // comment, e.g. OpenRouter's ": OPENROUTER PROCESSING"
		}
		seen = true
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue // a bare field name carries an empty value we do not use
		}
		value = strings.TrimPrefix(value, " ") // exactly one optional space
		switch field {
		case "data":
			data = append(data, value)
		case "event":
			ev.Type = value
		case "id":
			ev.ID = value
		case "retry":
			ev.Retry = value
		}
	}
	if err := r.sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return Event{}, ErrLineTooLong
		}
		return Event{}, err
	}
	if seen {
		ev.Data = strings.Join(data, "\n")
		return ev, nil
	}
	return Event{}, io.EOF
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sse/ -v`
Expected: PASS, ten tests.

- [ ] **Step 5: Commit**

```bash
git add internal/sse/
git commit -m "feat(sse): add event reader with line cap"
```

---

### Task 3: SSE writer

**Files:**
- Create: `internal/sse/writer.go`
- Test: `internal/sse/writer_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `func NewWriter(w http.ResponseWriter) *Writer`, `func (*Writer) Send(event, data string) error`, `func (*Writer) SendDone() error`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/sse/writer_test.go`:

```go
package sse

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriterSetsAntiBufferingHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	NewWriter(rec)
	h := rec.Header()
	for k, want := range map[string]string{
		"Content-Type":      "text/event-stream",
		"Cache-Control":     "no-cache",
		"Connection":        "keep-alive",
		"X-Accel-Buffering": "no", // nginx in front of a homelab box is normal
	} {
		if got := h.Get(k); got != want {
			t.Errorf("header %s = %q, want %q", k, got, want)
		}
	}
}

func TestWriterEmitsDataAndBlankLine(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.Send("", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != "data: {\"a\":1}\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriterEmitsNamedEvent(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.Send("ping", "{}"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.Body.String(), "event: ping\ndata: {}") {
		t.Fatalf("got %q", rec.Body.String())
	}
}

func TestWriterSendDoneEmitsSentinel(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.SendDone(); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != "data: [DONE]\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestWriterSplitsMultilineData(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewWriter(rec)
	if err := w.Send("", "one\ntwo"); err != nil {
		t.Fatal(err)
	}
	if got := rec.Body.String(); got != "data: one\ndata: two\n\n" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sse/ -run TestWriter -v`
Expected: FAIL — `undefined: NewWriter`.

- [ ] **Step 3: Write the writer**

Create `internal/sse/writer.go`:

```go
package sse

import (
	"io"
	"net/http"
	"strings"
)

// Writer emits SSE events, flushing after each one. It deliberately does not
// wrap the ResponseWriter in a bufio.Writer: buffering here turns
// time-to-first-token into time-to-completion.
type Writer struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func NewWriter(w http.ResponseWriter) *Writer {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	return &Writer{w: w, rc: http.NewResponseController(w)}
}

// Send writes one event and flushes. An empty event name omits the event field.
func (s *Writer) Send(event, data string) error {
	var b strings.Builder
	if event != "" {
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(data, "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if _, err := io.WriteString(s.w, b.String()); err != nil {
		return err
	}
	// A ResponseWriter that cannot flush is a programming error upstream, not a
	// runtime condition worth failing the stream over.
	_ = s.rc.Flush()
	return nil
}

func (s *Writer) SendDone() error { return s.Send("", Done) }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sse/ -v`
Expected: PASS, fifteen tests.

- [ ] **Step 5: Commit**

```bash
git add internal/sse/writer.go internal/sse/writer_test.go
git commit -m "feat(sse): add flushing event writer"
```

---

### Task 4: Configuration types, loading, and validation

**Files:**
- Create: `internal/config/config.go`, `internal/config/load.go`
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Config` with `Server`, `Providers`, `Policy` fields; `func Parse(data []byte, lookup func(string) (string, bool)) (*Config, error)`; `func Load(path string, lookup func(string) (string, bool)) (*Config, error)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Add the YAML dependency**

```bash
go get gopkg.in/yaml.v3
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/load_test.go`:

```go
package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

const minimal = `
server:
  proxy_listen: :8080
  admin_listen: :8081
providers:
  - id: groq
    kind: openaicompat
    base_url: https://api.groq.com/openai/v1
    api_key: ${GROQ_KEY}
    models: [llama-3.3-70b-versatile]
`

func TestParseAppliesDefaults(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.MaxBodyBytes != 33554432 {
		t.Errorf("MaxBodyBytes = %d", c.Server.MaxBodyBytes)
	}
	if c.Server.SSE.MaxLineBytes != 1048576 {
		t.Errorf("MaxLineBytes = %d", c.Server.SSE.MaxLineBytes)
	}
	if c.Policy.Timeout.FirstByte != 60*time.Second {
		t.Errorf("FirstByte = %v", c.Policy.Timeout.FirstByte)
	}
}

func TestParseInterpolatesRequiredEnv(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Providers[0].APIKey != "sk-x" {
		t.Fatalf("APIKey = %q", c.Providers[0].APIKey)
	}
}

func TestParseRejectsUnresolvedRequiredEnv(t *testing.T) {
	_, err := Parse([]byte(minimal), env(nil))
	if err == nil || !strings.Contains(err.Error(), "GROQ_KEY") {
		t.Fatalf("expected an error naming GROQ_KEY, got %v", err)
	}
}

func TestParseTreatsUnresolvedOptionalEnvAsDisabled(t *testing.T) {
	// The shipped example config references DARKROUTER_PROXY_TOKEN. It must
	// load on a machine that has not set it, with auth simply off.
	withToken := strings.Replace(minimal,
		"  admin_listen: :8081",
		"  admin_listen: :8081\n  proxy_token: ${DARKROUTER_PROXY_TOKEN}", 1)
	c, err := Parse([]byte(withToken), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatalf("optional env must not fail validation: %v", err)
	}
	if c.Server.ProxyToken != "" {
		t.Fatalf("ProxyToken = %q, want empty", c.Server.ProxyToken)
	}
}

func TestParseRejectsUnknownKeys(t *testing.T) {
	_, err := Parse([]byte("server:\n  nonsense: 1\n"), env(nil))
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
}

func TestParseRejectsDuplicateProviderID(t *testing.T) {
	src := minimal + `
  - id: groq
    kind: openaicompat
    base_url: https://example.com/v1
    api_key: ${GROQ_KEY}
    models: [x]
`
	_, err := Parse([]byte(src), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected a duplicate-id error, got %v", err)
	}
}

func TestParseRejectsRelativeBaseURL(t *testing.T) {
	src := strings.Replace(minimal, "https://api.groq.com/openai/v1", "/v1", 1)
	_, err := Parse([]byte(src), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected an absolute-URL error, got %v", err)
	}
}

func TestParseWarnsOnDuplicateModelAcrossProviders(t *testing.T) {
	src := minimal + `
  - id: cerebras
    kind: openaicompat
    base_url: https://api.cerebras.ai/v1
    api_key: ${GROQ_KEY}
    models: [llama-3.3-70b-versatile]
`
	c, err := Parse([]byte(src), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 1 || !strings.Contains(c.Warnings[0], "llama-3.3-70b-versatile") {
		t.Fatalf("warnings = %v", c.Warnings)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/ -v`
Expected: FAIL — `undefined: Parse`.

- [ ] **Step 4: Write the config types**

Create `internal/config/config.go`:

```go
// Package config loads darkrouter.yaml, validates it, and hot-reloads it.
package config

import "time"

type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Providers []ProviderConfig `yaml:"providers"`
	Policy    PolicyConfig     `yaml:"policy"`

	// Warnings are non-fatal findings from validation. They are surfaced on
	// /healthz rather than rejecting the document.
	Warnings []string `yaml:"-"`
}

type ServerConfig struct {
	ProxyListen    string    `yaml:"proxy_listen"`
	AdminListen    string    `yaml:"admin_listen"`
	ProxyToken     string    `yaml:"proxy_token"`
	MaxBodyBytes   int64     `yaml:"max_body_bytes"`
	ShutdownGrace  time.Duration `yaml:"shutdown_grace"`
	SSE            SSEConfig `yaml:"sse"`
}

type SSEConfig struct {
	MaxLineBytes int `yaml:"max_line_bytes"`
}

type ProviderConfig struct {
	ID       string   `yaml:"id"`
	Kind     string   `yaml:"kind"`
	BaseURL  string   `yaml:"base_url"`
	APIKey   string   `yaml:"api_key"`
	Priority int      `yaml:"priority"`
	Models   []string `yaml:"models"`
}

type PolicyConfig struct {
	Timeout TimeoutConfig `yaml:"timeout"`
}

type TimeoutConfig struct {
	Connect   time.Duration `yaml:"connect"`
	FirstByte time.Duration `yaml:"first_byte"`
	Total     time.Duration `yaml:"total"`
	Idle      time.Duration `yaml:"idle"`
}

// RestartOnly names the fields a hot reload cannot apply. A reload changing one
// is accepted with a warning rather than rejected or silently ignored.
var RestartOnly = []string{"server.proxy_listen", "server.admin_listen", "server.max_body_bytes"}
```

- [ ] **Step 5: Write the loader**

Create `internal/config/load.go`:

```go
package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads and parses path. lookup resolves ${ENV} references; pass
// os.LookupEnv in production.
func Load(path string, lookup func(string) (string, bool)) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, lookup)
}

func Parse(data []byte, lookup func(string) (string, bool)) (*Config, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // unknown keys are a validation failure
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	applyDefaults(&c)
	if err := interpolate(&c, lookup); err != nil {
		return nil, err
	}
	if err := validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func applyDefaults(c *Config) {
	if c.Server.ProxyListen == "" {
		c.Server.ProxyListen = ":8080"
	}
	if c.Server.AdminListen == "" {
		c.Server.AdminListen = ":8081"
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 33554432
	}
	if c.Server.ShutdownGrace == 0 {
		c.Server.ShutdownGrace = 30 * time.Second
	}
	if c.Server.SSE.MaxLineBytes == 0 {
		c.Server.SSE.MaxLineBytes = 1048576
	}
	if c.Policy.Timeout.Connect == 0 {
		c.Policy.Timeout.Connect = 10 * time.Second
	}
	if c.Policy.Timeout.FirstByte == 0 {
		c.Policy.Timeout.FirstByte = 60 * time.Second
	}
	if c.Policy.Timeout.Total == 0 {
		c.Policy.Timeout.Total = 10 * time.Minute
	}
	if c.Policy.Timeout.Idle == 0 {
		c.Policy.Timeout.Idle = 120 * time.Second
	}
}

// resolve replaces ${VAR} references. It reports the names it could not resolve
// so the caller can decide whether the field was required.
func resolve(s string, lookup func(string) (string, bool)) (string, []string) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := ref[2 : len(ref)-1]
		if v, ok := lookup(name); ok {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	return out, missing
}

func interpolate(c *Config, lookup func(string) (string, bool)) error {
	// proxy_token is optional: an unset variable means authentication is off,
	// not a broken config. The shipped example references it.
	if v, missing := resolve(c.Server.ProxyToken, lookup); len(missing) > 0 {
		c.Server.ProxyToken = ""
	} else {
		c.Server.ProxyToken = v
	}
	for i := range c.Providers {
		v, missing := resolve(c.Providers[i].APIKey, lookup)
		if len(missing) > 0 {
			return fmt.Errorf("provider %q: unresolved environment reference %s",
				c.Providers[i].ID, strings.Join(missing, ", "))
		}
		c.Providers[i].APIKey = v
	}
	return nil
}

func validate(c *Config) error {
	seen := make(map[string]bool, len(c.Providers))
	models := make(map[string][]string)
	for _, p := range c.Providers {
		if p.ID == "" {
			return fmt.Errorf("provider: id is required")
		}
		if seen[p.ID] {
			return fmt.Errorf("provider %q: duplicate id", p.ID)
		}
		seen[p.ID] = true
		if p.BaseURL == "" {
			return fmt.Errorf("provider %q: base_url is required", p.ID)
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || !u.IsAbs() {
			return fmt.Errorf("provider %q: base_url must be an absolute URL", p.ID)
		}
		if p.Kind == "" {
			return fmt.Errorf("provider %q: kind is required", p.ID)
		}
		for _, m := range p.Models {
			models[m] = append(models[m], p.ID)
		}
	}
	// Ambiguity is the useful case, not an error: phase 3 turns it into a
	// fallback chain. Name it so the resolution is never a surprise.
	for m, ids := range models {
		if len(ids) > 1 {
			c.Warnings = append(c.Warnings,
				fmt.Sprintf("model %q is offered by %s; the highest-priority provider wins", m, strings.Join(ids, ", ")))
		}
	}
	return nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, eight tests.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/config/
git commit -m "feat(config): add loading, interpolation, validation"
```

---

### Task 5: Atomic config store and directory watcher

**Files:**
- Create: `internal/config/store.go`
- Test: `internal/config/store_test.go`

**Interfaces:**
- Consumes: `config.Config`, `config.Parse` from Task 4.
- Produces: `func NewStore(path string, lookup func(string) (string, bool)) (*Store, error)`, `func (*Store) Current() *Config`, `func (*Store) Reload() error`, `func (*Store) Watch(ctx context.Context) error`, `func (*Store) LastError() error`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Add the watcher dependency**

```bash
go get github.com/fsnotify/fsnotify
```

- [ ] **Step 2: Write the failing test**

Create `internal/config/store_test.go`:

```go
package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newTestStore(t *testing.T, body string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	writeFile(t, path, body)
	s, err := NewStore(path, env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestStoreServesCurrentConfig(t *testing.T) {
	s, _ := newTestStore(t, minimal)
	if s.Current().Providers[0].ID != "groq" {
		t.Fatal("unexpected config")
	}
}

func TestReloadAppliesValidChange(t *testing.T) {
	s, path := newTestStore(t, minimal)
	writeFile(t, path, strings.Replace(minimal, "id: groq", "id: renamed", 1))
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Current().Providers[0].ID != "renamed" {
		t.Fatal("reload did not apply")
	}
}

func TestReloadRejectsInvalidAndKeepsPrevious(t *testing.T) {
	s, path := newTestStore(t, minimal)
	writeFile(t, path, "server:\n  nonsense: true\n")
	if err := s.Reload(); err == nil {
		t.Fatal("expected reload to fail")
	}
	if s.Current().Providers[0].ID != "groq" {
		t.Fatal("a rejected reload must leave the previous config live")
	}
	if s.LastError() == nil {
		t.Fatal("expected LastError to record the rejection")
	}
}

func TestReloadWarnsOnRestartOnlyChange(t *testing.T) {
	s, path := newTestStore(t, minimal)
	writeFile(t, path, strings.Replace(minimal, "proxy_listen: :8080", "proxy_listen: :9090", 1))
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range s.Current().Warnings {
		if strings.Contains(w, "proxy_listen") && strings.Contains(w, "restart") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a restart-required warning, got %v", s.Current().Warnings)
	}
}

// Editors that save by rename deliver a rename event for the old inode and
// nothing for the new file. Watching the file itself silently stops working
// after the first save, so the watcher must watch the parent directory.
func TestWatchDetectsRenameStyleSave(t *testing.T) {
	s, path := newTestStore(t, minimal)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Watch(ctx) }()

	tmp := path + ".tmp"
	writeFile(t, tmp, strings.Replace(minimal, "id: groq", "id: vimstyle", 1))
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		if s.Current().Providers[0].ID == "vimstyle" {
			return
		}
		select {
		case <-deadline:
			t.Fatal("watcher did not observe a rename-style save")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestStore -v`
Expected: FAIL — `undefined: NewStore`.

- [ ] **Step 4: Write the store**

Create `internal/config/store.go`:

```go
package config

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounce absorbs the several write events an editor emits for one save, and
// avoids reading a half-written file.
const debounce = 100 * time.Millisecond

// Store holds the live configuration. A request takes one snapshot at entry and
// uses it for its whole lifetime, so a reload cannot change behavior underneath
// an in-flight request.
type Store struct {
	path   string
	lookup func(string) (string, bool)
	cur    atomic.Pointer[Config]
	lastErr atomic.Pointer[error]
}

func NewStore(path string, lookup func(string) (string, bool)) (*Store, error) {
	s := &Store{path: path, lookup: lookup}
	c, err := Load(path, lookup)
	if err != nil {
		return nil, err
	}
	s.cur.Store(c)
	return s, nil
}

func (s *Store) Current() *Config { return s.cur.Load() }

func (s *Store) LastError() error {
	if p := s.lastErr.Load(); p != nil {
		return *p
	}
	return nil
}

// Reload parses and validates the file in full, swapping only on success.
// A file that fails validation is rejected wholesale and the previous
// configuration stays live: a broken edit must never take the gateway down.
func (s *Store) Reload() error {
	next, err := Load(s.path, s.lookup)
	if err != nil {
		s.lastErr.Store(&err)
		return err
	}
	prev := s.cur.Load()
	next.Warnings = append(next.Warnings, restartOnlyWarnings(prev, next)...)
	s.cur.Store(next)
	s.lastErr.Store(nil)
	return nil
}

func restartOnlyWarnings(prev, next *Config) []string {
	var out []string
	if prev == nil {
		return nil
	}
	if prev.Server.ProxyListen != next.Server.ProxyListen {
		out = append(out, "server.proxy_listen changed; takes effect on restart")
	}
	if prev.Server.AdminListen != next.Server.AdminListen {
		out = append(out, "server.admin_listen changed; takes effect on restart")
	}
	if prev.Server.MaxBodyBytes != next.Server.MaxBodyBytes {
		out = append(out, "server.max_body_bytes changed; takes effect on restart")
	}
	return out
}

// Watch reloads on change until ctx is cancelled. It watches the parent
// directory and filters by filename, because a watch on the file itself is lost
// the first time an editor saves by rename.
func (s *Store) Watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	dir, name := filepath.Split(s.path)
	if dir == "" {
		dir = "."
	}
	if err := w.Add(dir); err != nil {
		return fmt.Errorf("watch %s: %w", dir, err)
	}

	var timer *time.Timer
	var fire <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if filepath.Base(ev.Name) != name {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.NewTimer(debounce)
			fire = timer.C
		case <-fire:
			fire = nil
			_ = s.Reload() // the error is recorded in LastError
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			s.lastErr.Store(&err)
		}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/config/ -v -race`
Expected: PASS, thirteen tests, no race reports.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/config/store.go internal/config/store_test.go
git commit -m "feat(config): add atomic store and directory watcher"
```

---

### Task 6: Provider source and model resolution

**Files:**
- Create: `internal/provider/provider.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: `config.Store`, `config.ProviderConfig` from Tasks 4 and 5.
- Produces: `provider.Provider{ID, Kind, BaseURL, APIKey string; Priority int; Models []string}`, `provider.Source` interface with `Providers(context.Context) ([]Provider, error)` and `Revision() uint64`, `func NewYAMLSource(*config.Store) *YAMLSource`, `func Resolve(ps []Provider, model string) (Provider, bool)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/provider/provider_test.go`:

```go
package provider

import "testing"

func TestResolvePicksHighestPriority(t *testing.T) {
	ps := []Provider{
		{ID: "low", Priority: 1, Models: []string{"m"}},
		{ID: "high", Priority: 10, Models: []string{"m"}},
	}
	got, ok := Resolve(ps, "m")
	if !ok || got.ID != "high" {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
}

func TestResolveFallsBackToDeclarationOrderOnEqualPriority(t *testing.T) {
	ps := []Provider{
		{ID: "first", Priority: 5, Models: []string{"m"}},
		{ID: "second", Priority: 5, Models: []string{"m"}},
	}
	got, _ := Resolve(ps, "m")
	if got.ID != "first" {
		t.Fatalf("got %q", got.ID)
	}
}

func TestResolveReportsMiss(t *testing.T) {
	if _, ok := Resolve([]Provider{{ID: "a", Models: []string{"x"}}}, "y"); ok {
		t.Fatal("expected a miss")
	}
}

func TestResolveSkipsProviderWithoutTheModel(t *testing.T) {
	ps := []Provider{
		{ID: "wrong", Priority: 99, Models: []string{"other"}},
		{ID: "right", Priority: 1, Models: []string{"m"}},
	}
	got, _ := Resolve(ps, "m")
	if got.ID != "right" {
		t.Fatalf("got %q", got.ID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -v`
Expected: FAIL — `undefined: Resolve`.

- [ ] **Step 3: Write the provider source**

Create `internal/provider/provider.go`:

```go
// Package provider exposes configured upstreams to the router.
//
// Source is an interface from Phase 1 so that Phase 2 can swap the YAML
// implementation for a SQLite one without touching consumers.
package provider

import (
	"context"
	"hash/fnv"
	"sort"

	"github.com/darkraise/darkrouter/internal/config"
)

type Provider struct {
	ID       string
	Kind     string
	BaseURL  string
	APIKey   string
	Priority int
	Models   []string
}

type Source interface {
	Providers(context.Context) ([]Provider, error)
	Revision() uint64
}

type YAMLSource struct {
	store *config.Store
}

func NewYAMLSource(s *config.Store) *YAMLSource { return &YAMLSource{store: s} }

func (s *YAMLSource) Providers(context.Context) ([]Provider, error) {
	cfg := s.store.Current()
	out := make([]Provider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		out = append(out, Provider{
			ID: p.ID, Kind: p.Kind, BaseURL: p.BaseURL,
			APIKey: p.APIKey, Priority: p.Priority, Models: p.Models,
		})
	}
	return out, nil
}

// Revision changes when the provider set changes, so callers can cache.
func (s *YAMLSource) Revision() uint64 {
	h := fnv.New64a()
	for _, p := range s.store.Current().Providers {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		for _, m := range p.Models {
			_, _ = h.Write([]byte(m))
		}
	}
	return h.Sum64()
}

// Resolve returns the provider serving model, ordered by priority descending
// then declaration order. Phase 3 replaces this with an ordered candidate list;
// Phase 1 takes the first match.
func Resolve(ps []Provider, model string) (Provider, bool) {
	idx := make([]int, 0, len(ps))
	for i, p := range ps {
		for _, m := range p.Models {
			if m == model {
				idx = append(idx, i)
				break
			}
		}
	}
	if len(idx) == 0 {
		return Provider{}, false
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return ps[idx[a]].Priority > ps[idx[b]].Priority
	})
	return ps[idx[0]], true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/provider/ -v`
Expected: PASS, four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/
git commit -m "feat(provider): add source interface and resolution"
```

---

### Task 7: OpenAI edge — request parsing

**Files:**
- Create: `internal/edge/edge.go`, `internal/edge/openai/parse.go`
- Test: `internal/edge/openai/parse_test.go`

**Interfaces:**
- Consumes: `ir` types from Task 1.
- Produces: `edge.Passthrough{Body []byte; ModelField string; Surface string}`, `edge.Dialect` interface, `func openai.ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/edge/openai/parse_test.go`:

```go
package openai

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parse(t *testing.T, body string) (*ir.Request, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req, _, err := ParseRequest(r, 1<<20)
	return req, err
}

func TestParseSimpleTextRequest(t *testing.T) {
	req, err := parse(t, `{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "m" || len(req.Messages) != 1 {
		t.Fatalf("got %+v", req)
	}
	if req.Messages[0].Content[0].Text != "hi" {
		t.Fatalf("content = %+v", req.Messages[0].Content)
	}
}

func TestParseTreatsDeveloperRoleAsSystem(t *testing.T) {
	req, err := parse(t, `{"model":"m","messages":[{"role":"developer","content":"be brief"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if req.Messages[0].Role != ir.RoleSystem {
		t.Fatalf("role = %q", req.Messages[0].Role)
	}
}

func TestParseMultiPartContentWithImage(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"what is this"},
		{"type":"image_url","image_url":{"url":"data:image/png;base64,AAA"}}]}]}`
	req, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	blocks := req.Messages[0].Content
	if len(blocks) != 2 || blocks[1].Type != ir.BlockImage {
		t.Fatalf("blocks = %+v", blocks)
	}
	if !req.Needs().Vision {
		t.Fatal("expected Needs().Vision")
	}
}

func TestParseTools(t *testing.T) {
	body := `{"model":"m","messages":[],"tools":[{"type":"function","function":
		{"name":"get_weather","description":"d","parameters":{"type":"object"}}}],
		"tool_choice":"required"}`
	req, err := parse(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Fatalf("tool_choice = %+v", req.ToolChoice)
	}
	if !req.Needs().Tools {
		t.Fatal("expected Needs().Tools")
	}
}

func TestParseCapturesPassthroughBody(t *testing.T) {
	body := `{"model":"m","messages":[{"role":"user","content":"hi"}]}`
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt.Body) != body || pt.ModelField != "model" {
		t.Fatalf("passthrough = %+v", pt)
	}
}

func TestParseRejectsOversizedBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(strings.Repeat("x", 100)))
	if _, _, err := ParseRequest(r, 10); err == nil {
		t.Fatal("expected an oversized-body error")
	}
}

func TestParseRejectsMalformedJSON(t *testing.T) {
	if _, err := parse(t, `{"model":`); err == nil {
		t.Fatal("expected a parse error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/edge/openai/ -v`
Expected: FAIL — `undefined: ParseRequest`.

- [ ] **Step 3: Write the edge interface**

Create `internal/edge/edge.go`:

```go
// Package edge holds the inbound dialects clients speak to Darkrouter.
package edge

import (
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
)

// Passthrough carries what the Phase 9 fast path needs to forward a request
// without re-rendering it. Phase 1 populates it; nothing consumes it yet.
type Passthrough struct {
	Body       []byte // the raw inbound body, retained for replay across attempts
	ModelField string // top-level JSON key holding the model, or "" when in the URL
	Surface    string
}

type Dialect interface {
	Name() string
	ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *Passthrough, error)
	WriteResponse(w http.ResponseWriter, resp *ir.Response) error
	WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
```

- [ ] **Step 4: Write the parser**

Create `internal/edge/openai/parse.go`:

```go
// Package openai implements the OpenAI chat-completions inbound dialect.
package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireRequest struct {
	Model       string          `json:"model"`
	Messages    []wireMessage   `json:"messages"`
	Tools       []wireTool      `json:"tools"`
	ToolChoice  json.RawMessage `json:"tool_choice"`
	MaxTokens   *int            `json:"max_tokens"`
	Temperature *float64        `json:"temperature"`
	TopP        *float64        `json:"top_p"`
	Stop        []string        `json:"stop"`
	Stream      bool            `json:"stream"`
	Reasoning   *string         `json:"reasoning_effort"`
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type wireTool struct {
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
}

// ParseRequest reads the body fully — retrying requires replaying it — and
// converts it to the canonical IR.
func ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(body)) > maxBody {
		return nil, nil, fmt.Errorf("request body exceeds %d bytes", maxBody)
	}
	var w wireRequest
	if err := json.Unmarshal(body, &w); err != nil {
		return nil, nil, fmt.Errorf("invalid JSON body: %w", err)
	}

	req := &ir.Request{
		Model:         w.Model,
		MaxTokens:     w.MaxTokens,
		Temperature:   w.Temperature,
		TopP:          w.TopP,
		StopSequences: w.Stop,
		Stream:        w.Stream,
	}
	if w.Reasoning != nil {
		req.Reasoning = &ir.Reasoning{Effort: *w.Reasoning}
	}
	for _, m := range w.Messages {
		msg := ir.Message{Role: mapRole(m.Role)}
		blocks, err := parseContent(m.Content)
		if err != nil {
			return nil, nil, err
		}
		msg.Content = blocks
		req.Messages = append(req.Messages, msg)
	}
	for _, t := range w.Tools {
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Function.Name, Description: t.Function.Description, Schema: t.Function.Parameters,
		})
	}
	req.ToolChoice = parseToolChoice(w.ToolChoice)

	return req, &edge.Passthrough{Body: body, ModelField: "model", Surface: "llm"}, nil
}

func mapRole(role string) ir.Role {
	switch role {
	case "system", "developer": // newer clients send "developer" for system
		return ir.RoleSystem
	case "assistant":
		return ir.RoleAssistant
	case "tool", "function":
		return ir.RoleTool
	default:
		return ir.RoleUser
	}
}

// parseContent accepts both the plain-string and multi-part forms.
func parseContent(raw json.RawMessage) ([]ir.ContentBlock, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []ir.ContentBlock{{Type: ir.BlockText, Text: s}}, nil
	}
	var parts []wirePart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("unsupported message content: %w", err)
	}
	out := make([]ir.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "text":
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
		case "image_url":
			if p.ImageURL == nil {
				continue
			}
			out = append(out, ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{URL: p.ImageURL.URL}})
		}
	}
	return out, nil
}

func parseToolChoice(raw json.RawMessage) *ir.ToolChoice {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		switch s {
		case "auto":
			return &ir.ToolChoice{Mode: "auto"}
		case "none":
			return &ir.ToolChoice{Mode: "none"}
		case "required":
			return &ir.ToolChoice{Mode: "any"} // OpenAI "required" == Anthropic "any"
		}
		return nil
	}
	var named struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err == nil && named.Function.Name != "" {
		return &ir.ToolChoice{Mode: "tool", Name: named.Function.Name}
	}
	return nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/edge/... -v`
Expected: PASS, seven tests.

- [ ] **Step 6: Commit**

```bash
git add internal/edge/
git commit -m "feat(edge): add OpenAI request parsing"
```

---

### Task 8: OpenAI edge — response and error writing

**Files:**
- Create: `internal/edge/openai/write.go`
- Test: `internal/edge/openai/write_test.go`

**Interfaces:**
- Consumes: `ir.Response`, `ir.Error` from Task 1.
- Produces: `func WriteResponse(w http.ResponseWriter, resp *ir.Response) error`, `func WriteError(w http.ResponseWriter, e *ir.Error) error`, `func statusFor(t ir.ErrorType) int`.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `internal/edge/openai/write_test.go`:

```go
package openai

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestWriteResponseProducesOpenAIShape(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteResponse(rec, &ir.Response{
		ID:         "req-1",
		Model:      "m",
		Content:    []ir.ContentBlock{{Type: ir.BlockText, Text: "hello"}},
		StopReason: ir.StopEndTurn,
		Usage:      ir.Usage{InputTokens: 3, OutputTokens: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["object"] != "chat.completion" {
		t.Errorf("object = %v", got["object"])
	}
	choices := got["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "hello" {
		t.Errorf("content = %v", msg["content"])
	}
	if choices[0].(map[string]any)["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v", choices[0].(map[string]any)["finish_reason"])
	}
	usage := got["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 8 {
		t.Errorf("total_tokens = %v", usage["total_tokens"])
	}
}

func TestWriteErrorUsesOpenAIErrorObject(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteError(rec, &ir.Error{Type: ir.ErrNotFound, Message: "no such model"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 404 {
		t.Errorf("status = %d", rec.Code)
	}
	var got struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Error.Message != "no such model" || got.Error.Type != "not_found" {
		t.Fatalf("got %+v", got.Error)
	}
}

func TestStatusForMapsEveryErrorType(t *testing.T) {
	for _, tc := range []struct {
		in   ir.ErrorType
		want int
	}{
		{ir.ErrInvalidRequest, 400},
		{ir.ErrAuthentication, 401},
		{ir.ErrPermission, 403},
		{ir.ErrNotFound, 404},
		{ir.ErrRateLimit, 429},
		{ir.ErrOverloaded, 503},
		{ir.ErrContentFilter, 400},
		{ir.ErrAPI, 502},
		{ir.ErrDarkrouter, 502},
	} {
		if got := statusFor(tc.in); got != tc.want {
			t.Errorf("statusFor(%s) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/edge/openai/ -run TestWrite -v`
Expected: FAIL — `undefined: WriteResponse`.

- [ ] **Step 3: Write the writer**

Create `internal/edge/openai/write.go`:

```go
package openai

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
)

func finishReason(s ir.StopReason) string {
	switch s {
	case ir.StopMaxTokens:
		return "length"
	case ir.StopToolUse:
		return "tool_calls"
	case ir.StopContentFilter:
		return "content_filter"
	default:
		return "stop"
	}
}

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	var text strings.Builder
	for _, b := range resp.Content {
		if b.Type == ir.BlockText {
			text.WriteString(b.Text)
		}
	}
	out := map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   resp.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text.String()},
			"finish_reason": finishReason(resp.StopReason),
		}},
		"usage": map[string]any{
			"prompt_tokens":     resp.Usage.InputTokens,
			"completion_tokens": resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.InputTokens + resp.Usage.OutputTokens,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// statusFor maps a canonical error type to the HTTP status an OpenAI client
// expects, so clients handle gateway failures with their existing code.
func statusFor(t ir.ErrorType) int {
	switch t {
	case ir.ErrInvalidRequest, ir.ErrContentFilter:
		return http.StatusBadRequest
	case ir.ErrAuthentication:
		return http.StatusUnauthorized
	case ir.ErrPermission:
		return http.StatusForbidden
	case ir.ErrNotFound:
		return http.StatusNotFound
	case ir.ErrRateLimit:
		return http.StatusTooManyRequests
	case ir.ErrOverloaded:
		return http.StatusServiceUnavailable
	default:
		return http.StatusBadGateway
	}
}

func WriteError(w http.ResponseWriter, e *ir.Error) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusFor(e.Type))
	return json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": e.Message,
			"type":    string(e.Type),
			"code":    e.Code,
		},
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/edge/openai/ -v`
Expected: PASS, ten tests.

- [ ] **Step 5: Commit**

```bash
git add internal/edge/openai/write.go internal/edge/openai/write_test.go
git commit -m "feat(edge): add OpenAI response and error writing"
```

---

### Task 9: OpenAI edge — stream writing and the Dialect implementation

**Files:**
- Create: `internal/edge/openai/stream.go`, `internal/edge/openai/dialect.go`
- Test: `internal/edge/openai/stream_test.go`

**Interfaces:**
- Consumes: `ir.StreamEvent` (Task 1), `sse.Writer` (Task 3), `WriteResponse`/`WriteError` (Task 8).
- Produces: `func WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error`, `type Dialect struct{}` satisfying `edge.Dialect`, `func New() *Dialect`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/edge/openai/stream_test.go`:

```go
package openai

import (
	"iter"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func seq(events ...ir.StreamEvent) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		for _, e := range events {
			if !yield(e, nil) {
				return
			}
		}
	}
}

func TestWriteStreamEmitsDeltasAndDone(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteStream(rec, seq(
		ir.StreamEvent{Type: ir.EventMessageStart},
		ir.StreamEvent{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "he"}},
		ir.StreamEvent{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "llo"}},
		ir.StreamEvent{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	))
	if err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if strings.Count(body, `"delta"`) < 2 {
		t.Fatalf("expected two delta chunks, got:\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream must end with the DONE sentinel, got:\n%s", body)
	}
}

func TestWriteStreamSkipsPings(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := WriteStream(rec, seq(ir.StreamEvent{Type: ir.EventPing})); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rec.Body.String(), `"delta"`) {
		t.Fatal("a ping must not become a client-visible chunk")
	}
}

func TestWriteStreamEmitsUsageWhenPresent(t *testing.T) {
	rec := httptest.NewRecorder()
	err := WriteStream(rec, seq(
		ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 2, OutputTokens: 4}},
		ir.StreamEvent{Type: ir.EventMessageStop},
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), `"total_tokens":6`) {
		t.Fatalf("expected a usage chunk, got:\n%s", rec.Body.String())
	}
}

func TestWriteStreamEmitsInStreamError(t *testing.T) {
	rec := httptest.NewRecorder()
	events := func(yield func(ir.StreamEvent, error) bool) {
		yield(ir.StreamEvent{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "x"}}, nil)
		yield(ir.StreamEvent{}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream died"})
	}
	if err := WriteStream(rec, events); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "upstream died") {
		t.Fatalf("expected an in-stream error, got:\n%s", body)
	}
	if !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatal("an errored stream still terminates with DONE")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/edge/openai/ -run TestWriteStream -v`
Expected: FAIL — `undefined: WriteStream`.

- [ ] **Step 3: Write the stream writer**

Create `internal/edge/openai/stream.go`:

```go
package openai

import (
	"encoding/json"
	"iter"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

func chunk(model string, delta map[string]any, finish any) map[string]any {
	return map[string]any{
		"id":      "chatcmpl-darkrouter",
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	}
}

// WriteStream converts canonical stream events into OpenAI chunks. An error
// yielded by the sequence is terminal: it becomes an in-stream error and the
// stream still ends with the DONE sentinel, because a client that has already
// received content cannot be given a different response.
func WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	s := sse.NewWriter(w)
	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return s.Send("", string(b))
	}

	var model string
	for ev, err := range events {
		if err != nil {
			e, ok := err.(*ir.Error)
			if !ok {
				e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
			}
			if sendErr := send(map[string]any{"error": map[string]any{
				"message": e.Message, "type": string(e.Type), "code": e.Code,
			}}); sendErr != nil {
				return sendErr
			}
			break
		}
		switch ev.Type {
		case ir.EventPing, ir.EventBlockStart, ir.EventBlockStop:
			continue
		case ir.EventMessageStart:
			if err := send(chunk(model, map[string]any{"role": "assistant"}, nil)); err != nil {
				return err
			}
		case ir.EventContentDelta:
			if ev.Delta == nil || ev.Delta.Type != ir.BlockText {
				continue
			}
			if err := send(chunk(model, map[string]any{"content": ev.Delta.Text}, nil)); err != nil {
				return err
			}
		case ir.EventMessageDelta:
			if ev.Usage == nil {
				continue
			}
			c := chunk(model, map[string]any{}, nil)
			c["usage"] = map[string]any{
				"prompt_tokens":     ev.Usage.InputTokens,
				"completion_tokens": ev.Usage.OutputTokens,
				"total_tokens":      ev.Usage.InputTokens + ev.Usage.OutputTokens,
			}
			if err := send(c); err != nil {
				return err
			}
		case ir.EventMessageStop:
			if err := send(chunk(model, map[string]any{}, finishReason(ev.StopReason))); err != nil {
				return err
			}
		}
	}
	return s.SendDone()
}
```

- [ ] **Step 4: Write the Dialect implementation**

Create `internal/edge/openai/dialect.go`:

```go
package openai

import (
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Dialect struct{}

func New() *Dialect { return &Dialect{} }

func (d *Dialect) Name() string { return "openai" }

func (d *Dialect) ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	return ParseRequest(r, maxBody)
}

func (d *Dialect) WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	return WriteResponse(w, resp)
}

func (d *Dialect) WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	return WriteStream(w, events)
}

func (d *Dialect) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return WriteError(w, e)
}

var _ edge.Dialect = (*Dialect)(nil)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/edge/... -v`
Expected: PASS, fourteen tests.

- [ ] **Step 6: Commit**

```bash
git add internal/edge/openai/stream.go internal/edge/openai/dialect.go internal/edge/openai/stream_test.go
git commit -m "feat(edge): add OpenAI stream writer and dialect"
```

---

### Task 10: openaicompat adapter — request building

**Files:**
- Create: `internal/adapter/adapter.go`, `internal/adapter/openaicompat/build.go`
- Test: `internal/adapter/openaicompat/build_test.go`

**Interfaces:**
- Consumes: `ir.Request` (Task 1).
- Produces: `adapter.Target{BaseURL, APIKey, Model string}`, `adapter.Outcome` enum, `adapter.Adapter` interface, `func openaicompat.BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/openaicompat/build_test.go`:

```go
package openaicompat

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func build(t *testing.T, req *ir.Request) map[string]any {
	t.Helper()
	tgt := &adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-x", Model: "up-model"}
	hr, err := BuildRequest(context.Background(), tgt, req)
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://up.example/v1/chat/completions" {
		t.Fatalf("url = %s", hr.URL)
	}
	if hr.Header.Get("Authorization") != "Bearer sk-x" {
		t.Fatalf("auth = %q", hr.Header.Get("Authorization"))
	}
	body, _ := io.ReadAll(hr.Body)
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestBuildUsesTargetModelNotRequestModel(t *testing.T) {
	got := build(t, &ir.Request{Model: "alias-name"})
	if got["model"] != "up-model" {
		t.Fatalf("model = %v", got["model"])
	}
}

func TestBuildInjectsStreamOptionsOnStreamingRequests(t *testing.T) {
	// Without this, OpenAI-compatible providers emit no usage on streams and
	// Phase 2's accounting is blind on the dominant path.
	got := build(t, &ir.Request{Stream: true})
	so, ok := got["stream_options"].(map[string]any)
	if !ok || so["include_usage"] != true {
		t.Fatalf("stream_options = %v", got["stream_options"])
	}
}

func TestBuildOmitsStreamOptionsOnUnaryRequests(t *testing.T) {
	got := build(t, &ir.Request{Stream: false})
	if _, present := got["stream_options"]; present {
		t.Fatal("stream_options must not appear on a unary request")
	}
}

func TestBuildFlattensTextBlocksToStringContent(t *testing.T) {
	got := build(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}},
	}})
	msgs := got["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "hi" {
		t.Fatalf("content = %v", msgs[0])
	}
}

func TestBuildEmitsMultiPartContentForImages(t *testing.T) {
	got := build(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "what"},
			{Type: ir.BlockImage, Media: &ir.Media{URL: "https://x/y.png"}},
		}},
	}})
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 2 || parts[1].(map[string]any)["type"] != "image_url" {
		t.Fatalf("parts = %v", parts)
	}
}

func TestBuildEmitsTools(t *testing.T) {
	got := build(t, &ir.Request{Tools: []ir.Tool{
		{Name: "f", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)},
	}})
	tools := got["tools"].([]any)
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "f" {
		t.Fatalf("tools = %v", tools)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/... -v`
Expected: FAIL — `undefined: BuildRequest`.

- [ ] **Step 3: Write the adapter interface**

Create `internal/adapter/adapter.go`:

```go
// Package adapter holds the outbound provider kinds.
package adapter

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
)

type Target struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Outcome is the classification that drives failover. Phase 1 has nowhere to
// fail over to, but defining the full taxonomy now keeps Phase 3 from having to
// revisit every adapter.
type Outcome string

const (
	OutcomeSuccess             Outcome = "success"
	OutcomeRetryableProvider   Outcome = "retryable_provider"
	OutcomeRetryableCredential Outcome = "retryable_credential"
	OutcomeRetryableModel      Outcome = "retryable_model"
	OutcomeFatal               Outcome = "fatal"
	OutcomeClientCancelled     Outcome = "client_cancelled"
)

type Adapter interface {
	Kind() string
	BuildRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, error)
	ParseResponse(resp *http.Response) (*ir.Response, error)
	ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]
	Classify(resp *http.Response, err error) Outcome
}
```

- [ ] **Step 4: Write the request builder**

Create `internal/adapter/openaicompat/build.go`:

```go
// Package openaicompat speaks the OpenAI wire format to any compatible upstream.
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	body := map[string]any{
		"model":    t.Model,
		"messages": renderMessages(req),
	}
	if req.MaxTokens != nil {
		body["max_tokens"] = *req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.StopSequences) > 0 {
		body["stop"] = req.StopSequences
	}
	if req.Reasoning != nil && req.Reasoning.Effort != "" {
		body["reasoning_effort"] = req.Reasoning.Effort
	}
	if len(req.Tools) > 0 {
		body["tools"] = renderTools(req.Tools)
	}
	if req.ToolChoice != nil {
		body["tool_choice"] = renderToolChoice(req.ToolChoice)
	}
	if req.Stream {
		body["stream"] = true
		// Compatible providers report no stream usage unless asked.
		body["stream_options"] = map[string]any{"include_usage": true}
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil
}

func renderMessages(req *ir.Request) []any {
	out := make([]any, 0, len(req.Messages)+1)
	if len(req.System) > 0 {
		var b strings.Builder
		for _, blk := range req.System {
			b.WriteString(blk.Text)
		}
		out = append(out, map[string]any{"role": "system", "content": b.String()})
	}
	for _, m := range req.Messages {
		out = append(out, map[string]any{"role": string(m.Role), "content": renderContent(m.Content)})
	}
	return out
}

// renderContent emits a plain string when every block is text, and the
// multi-part form otherwise. Some compatible providers reject the multi-part
// form for text-only messages.
func renderContent(blocks []ir.ContentBlock) any {
	onlyText := true
	for _, b := range blocks {
		if b.Type != ir.BlockText {
			onlyText = false
			break
		}
	}
	if onlyText {
		var b strings.Builder
		for _, blk := range blocks {
			b.WriteString(blk.Text)
		}
		return b.String()
	}
	parts := make([]any, 0, len(blocks))
	for _, blk := range blocks {
		switch blk.Type {
		case ir.BlockText:
			parts = append(parts, map[string]any{"type": "text", "text": blk.Text})
		case ir.BlockImage:
			if blk.Media == nil {
				continue
			}
			url := blk.Media.URL
			if url == "" && blk.Media.Data != "" {
				url = "data:" + blk.Media.MIME + ";base64," + blk.Media.Data
			}
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}})
		}
	}
	return parts
}

func renderTools(tools []ir.Tool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Schema,
			},
		})
	}
	return out
}

func renderToolChoice(tc *ir.ToolChoice) any {
	switch tc.Mode {
	case "auto", "none":
		return tc.Mode
	case "any":
		return "required"
	case "tool":
		return map[string]any{"type": "function", "function": map[string]any{"name": tc.Name}}
	}
	return "auto"
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/adapter/... -v`
Expected: PASS, six tests.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/
git commit -m "feat(adapter): add openaicompat request builder"
```

---

### Task 11: openaicompat adapter — response and stream parsing

**Files:**
- Create: `internal/adapter/openaicompat/parse.go`
- Test: `internal/adapter/openaicompat/parse_test.go`

**Interfaces:**
- Consumes: `sse.Reader` (Task 2), `ir` types (Task 1).
- Produces: `func ParseResponse(resp *http.Response) (*ir.Response, error)`, `func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/openaicompat/parse_test.go`:

```go
package openaicompat

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func collect(t *testing.T, body string) []ir.StreamEvent {
	t.Helper()
	var got []ir.StreamEvent
	for ev, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got = append(got, ev)
	}
	return got
}

func TestParseResponseExtractsTextAndUsage(t *testing.T) {
	body := `{"id":"x","model":"m","choices":[{"message":{"content":"hello"},
		"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3}}`
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	got, err := ParseResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	if got.Content[0].Text != "hello" || got.StopReason != ir.StopEndTurn {
		t.Fatalf("got %+v", got)
	}
	if got.Usage.InputTokens != 2 || got.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", got.Usage)
	}
}

func TestParseResponseMapsFinishReasons(t *testing.T) {
	for wire, want := range map[string]ir.StopReason{
		"stop":           ir.StopEndTurn,
		"length":         ir.StopMaxTokens,
		"tool_calls":     ir.StopToolUse,
		"content_filter": ir.StopContentFilter,
	} {
		body := `{"choices":[{"message":{"content":""},"finish_reason":"` + wire + `"}]}`
		resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
		got, err := ParseResponse(resp)
		if err != nil {
			t.Fatal(err)
		}
		if got.StopReason != want {
			t.Errorf("%s -> %s, want %s", wire, got.StopReason, want)
		}
	}
}

func TestParseStreamEmitsBlockLifecycleForText(t *testing.T) {
	body := "data: {\"choices\":[{\"delta\":{\"content\":\"he\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"llo\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: [DONE]\n\n"
	got := collect(t, body)

	var starts, deltas, stops, msgStop int
	for _, ev := range got {
		switch ev.Type {
		case ir.EventBlockStart:
			starts++
		case ir.EventContentDelta:
			deltas++
		case ir.EventBlockStop:
			stops++
		case ir.EventMessageStop:
			msgStop++
		}
	}
	if starts != 1 || deltas != 2 || stops != 1 || msgStop != 1 {
		t.Fatalf("lifecycle counts wrong: starts=%d deltas=%d stops=%d stop=%d\n%+v",
			starts, deltas, stops, msgStop, got)
	}
}

func TestParseStreamAccumulatesToolCallFragmentsByIndex(t *testing.T) {
	// OpenAI streams tool arguments as JSON string fragments indexed by
	// tool_calls[].index; each index is its own block.
	body := "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\"," +
		"\"function\":{\"name\":\"f\",\"arguments\":\"{\\\"a\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0," +
		"\"function\":{\"arguments\":\"1}\"}}]}}]}\n\n" +
		"data: [DONE]\n\n"
	got := collect(t, body)

	var frag strings.Builder
	var name, id string
	for _, ev := range got {
		if ev.Delta != nil && ev.Delta.Type == ir.BlockToolUse {
			frag.WriteString(ev.Delta.ToolInput)
			if ev.Delta.ToolName != "" {
				name = ev.Delta.ToolName
			}
			if ev.Delta.ToolID != "" {
				id = ev.Delta.ToolID
			}
		}
	}
	if frag.String() != `{"a":1}` || name != "f" || id != "c1" {
		t.Fatalf("fragments=%q name=%q id=%q", frag.String(), name, id)
	}
}

func TestParseStreamEmitsUsageChunk(t *testing.T) {
	body := "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"
	got := collect(t, body)
	for _, ev := range got {
		if ev.Type == ir.EventMessageDelta && ev.Usage != nil && ev.Usage.InputTokens == 5 {
			return
		}
	}
	t.Fatalf("no usage event in %+v", got)
}

func TestParseStreamIgnoresKeepaliveComments(t *testing.T) {
	body := ": OPENROUTER PROCESSING\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\ndata: [DONE]\n\n"
	got := collect(t, body)
	if len(got) == 0 {
		t.Fatal("keepalive comment broke the stream")
	}
}

func TestParseStreamSurfacesUpstreamErrorPayload(t *testing.T) {
	body := "data: {\"error\":{\"message\":\"overloaded\",\"type\":\"server_error\"}}\n\n"
	var sawErr bool
	for _, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected an in-stream error to surface as a sequence error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/openaicompat/ -run TestParse -v`
Expected: FAIL — `undefined: ParseResponse`.

- [ ] **Step 3: Write the parsers**

Create `internal/adapter/openaicompat/parse.go`:

```go
package openaicompat

import (
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	PromptDetails    struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:     u.PromptTokens,
		OutputTokens:    u.CompletionTokens,
		CacheReadTokens: u.PromptDetails.CachedTokens,
		ReasoningTokens: u.CompletionDetails.ReasoningTokens,
	}
}

func stopReason(s string) ir.StopReason {
	switch s {
	case "length":
		return ir.StopMaxTokens
	case "tool_calls", "function_call":
		return ir.StopToolUse
	case "content_filter":
		return ir.StopContentFilter
	default:
		return ir.StopEndTurn
	}
}

func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer resp.Body.Close()
	var w struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage wireUsage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, err
	}
	out := &ir.Response{ID: w.ID, Model: w.Model, Usage: w.Usage.toIR()}
	if len(w.Choices) > 0 {
		c := w.Choices[0]
		out.StopReason = stopReason(c.FinishReason)
		if c.Message.Content != "" {
			out.Content = append(out.Content, ir.ContentBlock{Type: ir.BlockText, Text: c.Message.Content})
		}
		for _, tc := range c.Message.ToolCalls {
			out.Content = append(out.Content, ir.ContentBlock{
				Type: ir.BlockToolUse,
				ToolUse: &ir.ToolUse{
					ID: tc.ID, Name: tc.Function.Name, Input: tc.Function.Arguments,
				},
			})
		}
	}
	return out, nil
}

type wireChunk struct {
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			Reasoning string `json:"reasoning_content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

// ParseStream reconstructs block structure from OpenAI's flat deltas. The state
// machine opens a block when a delta first carries a given kind and closes it
// when the kind changes or the stream ends. Tool calls are indexed, so each
// index accumulates into its own block.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		reader := sse.NewReader(r, maxLine)
		open := make(map[int]bool) // block index -> open
		nextIdx := 0
		textIdx := -1
		started := false

		closeAll := func() bool {
			for idx := range open {
				if !yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: idx}, nil) {
					return false
				}
			}
			open = map[int]bool{}
			return true
		}

		for {
			ev, err := reader.Next()
			if errors.Is(err, io.EOF) {
				_ = closeAll()
				return
			}
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			if ev.Data == sse.Done {
				if !closeAll() {
					return
				}
				return
			}
			var c wireChunk
			if err := json.Unmarshal([]byte(ev.Data), &c); err != nil {
				continue // a chunk we cannot parse is not a reason to kill the stream
			}
			if c.Error != nil {
				yield(ir.StreamEvent{}, &ir.Error{
					Type: ir.ErrAPI, Message: c.Error.Message, Code: c.Error.Code,
				})
				return
			}
			if !started {
				started = true
				if !yield(ir.StreamEvent{Type: ir.EventMessageStart}, nil) {
					return
				}
			}
			if c.Usage != nil {
				u := c.Usage.toIR()
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &u}, nil) {
					return
				}
			}
			for _, ch := range c.Choices {
				d := ch.Delta
				if d.Content != "" {
					if textIdx < 0 {
						textIdx = nextIdx
						nextIdx++
						open[textIdx] = true
						if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: textIdx,
							Delta: &ir.Delta{Type: ir.BlockText}}, nil) {
							return
						}
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: textIdx,
						Delta: &ir.Delta{Type: ir.BlockText, Text: d.Content}}, nil) {
						return
					}
				}
				if d.Reasoning != "" {
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta,
						Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: d.Reasoning}}, nil) {
						return
					}
				}
				for _, tc := range d.ToolCalls {
					idx := 1000 + tc.Index // keep tool blocks in their own index space
					if !open[idx] {
						open[idx] = true
						if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: idx,
							Delta: &ir.Delta{Type: ir.BlockToolUse, ToolID: tc.ID, ToolName: tc.Function.Name}}, nil) {
							return
						}
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx,
						Delta: &ir.Delta{
							Type: ir.BlockToolUse, ToolID: tc.ID,
							ToolName: tc.Function.Name, ToolInput: tc.Function.Arguments,
						}}, nil) {
						return
					}
				}
				if ch.FinishReason != nil {
					if !closeAll() {
						return
					}
					if !yield(ir.StreamEvent{Type: ir.EventMessageStop,
						StopReason: stopReason(*ch.FinishReason)}, nil) {
						return
					}
				}
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapter/... -v`
Expected: PASS, thirteen tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/openaicompat/parse.go internal/adapter/openaicompat/parse_test.go
git commit -m "feat(adapter): add openaicompat response and stream parsing"
```

---

### Task 12: openaicompat adapter — outcome classification

**Files:**
- Create: `internal/adapter/openaicompat/classify.go`
- Test: `internal/adapter/openaicompat/classify_test.go`

**Interfaces:**
- Consumes: `adapter.Outcome` (Task 10).
- Produces: `func Classify(resp *http.Response, err error) adapter.Outcome`, `type Adapter struct{}` satisfying `adapter.Adapter`, `func New() *Adapter`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/openaicompat/classify_test.go`:

```go
package openaicompat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestClassifyStatusCodes(t *testing.T) {
	for code, want := range map[int]adapter.Outcome{
		200: adapter.OutcomeSuccess,
		400: adapter.OutcomeFatal,
		401: adapter.OutcomeRetryableCredential,
		402: adapter.OutcomeRetryableCredential,
		403: adapter.OutcomeRetryableCredential,
		404: adapter.OutcomeRetryableModel,
		408: adapter.OutcomeRetryableProvider,
		413: adapter.OutcomeFatal,
		422: adapter.OutcomeFatal,
		429: adapter.OutcomeRetryableProvider,
		500: adapter.OutcomeRetryableProvider,
		503: adapter.OutcomeRetryableProvider,
		301: adapter.OutcomeRetryableProvider, // redirects are never followed
	} {
		if got := Classify(&http.Response{StatusCode: code}, nil); got != want {
			t.Errorf("status %d -> %s, want %s", code, got, want)
		}
	}
}

func TestClassifyClientCancellationIsNotAProviderFault(t *testing.T) {
	// Marking a provider unhealthy because someone pressed Ctrl-C is a
	// self-inflicted outage.
	inbound, cancel := context.WithCancelCause(context.Background())
	cancel(context.Canceled)
	if got := ClassifyWithContext(nil, inbound.Err(), inbound); got != adapter.OutcomeClientCancelled {
		t.Fatalf("got %s", got)
	}
}

func TestClassifyTransportErrorsAreRetryable(t *testing.T) {
	for _, err := range []error{
		errors.New("dial tcp: connection refused"),
		&net.DNSError{Err: "no such host", IsNotFound: true},
	} {
		if got := Classify(nil, err); got != adapter.OutcomeRetryableProvider {
			t.Errorf("%v -> %s", err, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/adapter/openaicompat/ -run TestClassify -v`
Expected: FAIL — `undefined: Classify`.

- [ ] **Step 3: Write the classifier**

Create `internal/adapter/openaicompat/classify.go`:

```go
package openaicompat

import (
	"context"
	"errors"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Classify buckets an upstream result. The default buckets matter as much as the
// listed codes: without them, DNS failures and TLS errors get classified
// differently by different call sites.
func Classify(resp *http.Response, err error) adapter.Outcome {
	if err != nil {
		return adapter.OutcomeRetryableProvider
	}
	if resp == nil {
		return adapter.OutcomeRetryableProvider
	}
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return adapter.OutcomeSuccess
	case code == 401, code == 402, code == 403:
		return adapter.OutcomeRetryableCredential
	case code == 404:
		return adapter.OutcomeRetryableModel
	case code == 408, code == 429:
		return adapter.OutcomeRetryableProvider
	case code >= 300 && code < 400:
		// Redirects are never followed: Go's client converts a redirected POST
		// into a body-less GET.
		return adapter.OutcomeRetryableProvider
	case code >= 500:
		return adapter.OutcomeRetryableProvider
	default:
		return adapter.OutcomeFatal
	}
}

// ClassifyWithContext distinguishes a client disconnect from a Darkrouter
// deadline. Both surface as context.Canceled at the transport layer, so the
// inbound context's own state is the only reliable discriminator.
func ClassifyWithContext(resp *http.Response, err error, inbound context.Context) adapter.Outcome {
	if err != nil && errors.Is(inbound.Err(), context.Canceled) {
		return adapter.OutcomeClientCancelled
	}
	return Classify(resp, err)
}

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Kind() string { return "openaicompat" }

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	return BuildRequest(ctx, t, req)
}

func (a *Adapter) ParseResponse(resp *http.Response) (*ir.Response, error) {
	return ParseResponse(resp)
}

func (a *Adapter) ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return ParseStream(r, maxLine)
}

func (a *Adapter) Classify(resp *http.Response, err error) adapter.Outcome {
	return Classify(resp, err)
}

var _ adapter.Adapter = (*Adapter)(nil)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/adapter/... -v`
Expected: PASS, sixteen tests.

- [ ] **Step 5: Commit**

```bash
git add internal/adapter/openaicompat/classify.go internal/adapter/openaicompat/classify_test.go
git commit -m "feat(adapter): add outcome classification"
```

---

### Task 13: Single-candidate execution path

**Files:**
- Create: `internal/exec/exec.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `edge.Dialect` (Task 9), `adapter.Adapter` (Tasks 10–12), `provider.Source` and `provider.Resolve` (Task 6), `config.Store` (Task 5).
- Produces: `func New(store *config.Store, src provider.Source, ad adapter.Adapter) *Executor`, `func (*Executor) Handle(w http.ResponseWriter, r *http.Request, d edge.Dialect)`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/exec/exec_test.go`:

```go
package exec

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"os"
	"strings"
	"testing"

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
```

- [ ] **Step 2: Add the ULID dependency**

```bash
go get github.com/oklog/ulid/v2
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/exec/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 4: Write the executor**

Create `internal/exec/exec.go`:

```go
// Package exec drives a request to an upstream. Phase 1 handles exactly one
// candidate; Phase 3 wraps this same call sequence in an attempt loop rather
// than restructuring it.
package exec

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

type Executor struct {
	store  *config.Store
	src    provider.Source
	ad     adapter.Adapter
	client *http.Client
}

func New(store *config.Store, src provider.Source, ad adapter.Adapter) *Executor {
	return &Executor{
		store: store, src: src, ad: ad,
		client: &http.Client{
			// Go follows redirects by default, silently turning a redirected
			// POST into a body-less GET.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

func (e *Executor) Handle(w http.ResponseWriter, r *http.Request, d edge.Dialect) {
	cfg := e.store.Current() // one snapshot for this request's whole lifetime
	reqID := ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
	w.Header().Set("X-Darkrouter-Request", reqID)

	req, _, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}

	providers, err := e.src.Providers(r.Context())
	if err != nil {
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}
	p, ok := provider.Resolve(providers, req.Model)
	if !ok {
		_ = d.WriteError(w, &ir.Error{
			Type:    ir.ErrNotFound,
			Message: fmt.Sprintf("no configured provider offers model %q", req.Model),
		})
		return
	}

	// The upstream context derives from the inbound one, so a client hanging up
	// cancels the upstream call. WithCancelCause is used from the outset because
	// Phase 2 needs the cause to tell a disconnect from a Darkrouter deadline.
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	ctx, cancelTimeout := context.WithTimeoutCause(ctx, cfg.Policy.Timeout.Total,
		fmt.Errorf("darkrouter: total timeout exceeded"))
	defer cancelTimeout()

	tgt := &adapter.Target{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: req.Model}
	hr, err := e.ad.BuildRequest(ctx, tgt, req)
	if err != nil {
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}

	resp, err := e.client.Do(hr)
	outcome := e.ad.Classify(resp, err)

	w.Header().Set("X-Darkrouter-Provider", p.ID)
	w.Header().Set("X-Darkrouter-Model", req.Model)
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(1))

	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			resp.Body.Close()
		}
		_ = d.WriteError(w, errorFor(outcome, err))
		return
	}

	if req.Stream {
		defer resp.Body.Close()
		_ = d.WriteStream(w, e.ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes))
		return
	}

	out, err := e.ad.ParseResponse(resp)
	if err != nil {
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrAPI, Message: err.Error()})
		return
	}
	out.ID = reqID
	_ = d.WriteResponse(w, out)
}

func errorFor(o adapter.Outcome, err error) *ir.Error {
	msg := "upstream request failed"
	if err != nil {
		msg = err.Error()
	}
	switch o {
	case adapter.OutcomeRetryableCredential:
		return &ir.Error{Type: ir.ErrAuthentication, Message: "upstream rejected the credential"}
	case adapter.OutcomeRetryableModel:
		return &ir.Error{Type: ir.ErrNotFound, Message: "upstream does not serve this model"}
	case adapter.OutcomeFatal:
		return &ir.Error{Type: ir.ErrInvalidRequest, Message: msg}
	case adapter.OutcomeClientCancelled:
		return &ir.Error{Type: ir.ErrDarkrouter, Message: "client cancelled the request"}
	default:
		return &ir.Error{Type: ir.ErrAPI, Message: msg}
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/exec/ -v -race`
Expected: PASS, five tests, no race reports.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/exec/
git commit -m "feat(exec): add single-candidate execution path"
```

---

### Task 14: Servers, health endpoints, and graceful shutdown

**Files:**
- Create: `internal/server/server.go`, `cmd/darkrouter/main.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: everything from Tasks 4–13.
- Produces: `func New(store *config.Store) *Server`, `func (*Server) ProxyHandler() http.Handler`, `func (*Server) AdminHandler() http.Handler`, `func (*Server) Run(ctx context.Context) error`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/server/server_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -v`
Expected: FAIL — `undefined: New`.

- [ ] **Step 3: Write the server**

Create `internal/server/server.go`:

```go
// Package server wires the components into two listeners and owns shutdown.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/provider"
)

type Server struct {
	store *config.Store
	src   provider.Source
	ex    *exec.Executor
}

func New(store *config.Store) *Server {
	src := provider.NewYAMLSource(store)
	return &Server{store: store, src: src, ex: exec.New(store, src, openaicompat.New())}
}

func (s *Server) ProxyHandler() http.Handler {
	mux := http.NewServeMux()
	d := openaiedge.New()
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, d)
	})
	mux.HandleFunc("GET /v1/models", s.handleModels)
	return s.withProxyAuth(mux)
}

// withProxyAuth enforces the optional bearer token. Comparison is constant-time.
func (s *Server) withProxyAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := s.store.Current().Server.ProxyToken
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got, _ := parseBearer(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"message": "invalid proxy token", "type": "authentication",
			}})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func parseBearer(h string) (string, bool) {
	const p = "Bearer "
	if len(h) > len(p) && h[:len(p)] == p {
		return h[len(p):], true
	}
	return "", false
}

// handleModels lists configured models. Aliases would be listed first, but
// Phase 1 has none; Phase 6 replaces the backing with the catalog.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ps, err := s.src.Providers(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	seen := map[string]bool{}
	data := []any{}
	for _, p := range ps {
		for _, m := range p.Models {
			if seen[m] {
				continue
			}
			seen[m] = true
			data = append(data, map[string]any{
				"id": m, "object": "model", "owned_by": p.ID,
			})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.Current()
		body := map[string]any{
			"config_valid": s.store.LastError() == nil,
			"warnings":     cfg.Warnings,
		}
		if err := s.store.LastError(); err != nil {
			body["config_error"] = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte("# Phase 2 populates this endpoint.\n"))
	})
	return mux
}

// Run starts both listeners and blocks until ctx is cancelled, then drains.
func (s *Server) Run(ctx context.Context) error {
	cfg := s.store.Current()

	proxy := &http.Server{
		Addr:    cfg.Server.ProxyListen,
		Handler: s.ProxyHandler(),
		// No WriteTimeout: it would kill long streams at a fixed age. Slowloris
		// protection comes from ReadHeaderTimeout instead.
		ReadHeaderTimeout: 10 * time.Second,
		// BaseContext must not be tied to ctx, or in-flight streams die the
		// instant shutdown begins rather than draining.
		BaseContext: func(l net.Listener) context.Context { return context.Background() },
	}
	admin := &http.Server{
		Addr:              cfg.Server.AdminListen,
		Handler:           s.AdminHandler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() { errCh <- ignoreClosed(proxy.ListenAndServe()) }()
	go func() { errCh <- ignoreClosed(admin.ListenAndServe()) }()
	go func() { _ = s.store.Watch(ctx) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	drain, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownGrace)
	defer cancel()
	// Stop accepting proxy connections first, then the admin port.
	_ = proxy.Shutdown(drain)
	_ = admin.Shutdown(drain)
	return nil
}

func ignoreClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
```

- [ ] **Step 4: Write the entrypoint**

Create `cmd/darkrouter/main.go`:

```go
// Command darkrouter runs the gateway.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/server"
)

func main() {
	path := flag.String("config", "darkrouter.yaml", "path to the configuration file")
	flag.Parse()

	store, err := config.NewStore(*path, os.LookupEnv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := store.Current()
	log.Printf("darkrouter listening: proxy %s admin %s",
		cfg.Server.ProxyListen, cfg.Server.AdminListen)
	for _, w := range cfg.Warnings {
		log.Printf("config warning: %s", w)
	}

	if err := server.New(store).Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Print("darkrouter stopped")
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./... -race`
Expected: PASS across every package.

- [ ] **Step 6: Verify it builds and starts**

```bash
go build ./cmd/darkrouter
```
Expected: a `darkrouter` binary with no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/server/ cmd/
git commit -m "feat(server): add listeners, health, graceful shutdown"
```

---

### Task 15: Docker packaging and the example configuration

**Files:**
- Create: `Dockerfile`, `compose.yml`, `darkrouter.example.yaml`, `README.md`

**Interfaces:**
- Consumes: the `cmd/darkrouter` entrypoint from Task 14.
- Produces: nothing consumed by later tasks.

**Implementer:** dcc-superpower-companions:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

- [ ] **Step 1: Write the example configuration**

Create `darkrouter.example.yaml`:

```yaml
server:
  proxy_listen: :8080
  admin_listen: :8081
  # Optional. An unset variable means proxy authentication is off, which is why
  # this file loads on a machine that has never set it.
  proxy_token: ${DARKROUTER_PROXY_TOKEN}
  max_body_bytes: 33554432
  shutdown_grace: 30s
  sse:
    max_line_bytes: 1048576

providers:
  - id: groq
    kind: openaicompat
    base_url: https://api.groq.com/openai/v1
    api_key: ${GROQ_KEY}
    priority: 10
    models:
      - llama-3.3-70b-versatile

policy:
  timeout:
    connect: 10s
    first_byte: 60s
    total: 10m
    idle: 120s
```

- [ ] **Step 2: Write the Dockerfile**

Create `Dockerfile`:

```dockerfile
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=0 keeps the binary static, which is what lets the final image be
# scratch-adjacent and what modernc.org/sqlite is chosen for in Phase 2.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/darkrouter ./cmd/darkrouter

FROM alpine:3.21
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 darkrouter
COPY --from=build /out/darkrouter /usr/local/bin/darkrouter
USER darkrouter
WORKDIR /data
EXPOSE 8080 8081
ENTRYPOINT ["darkrouter", "-config", "/data/darkrouter.yaml"]
```

- [ ] **Step 3: Write the compose file**

Create `compose.yml`:

```yaml
services:
  darkrouter:
    build: .
    image: darkrouter:dev
    ports:
      - "8080:8080"
      - "8081:8081"
    environment:
      GROQ_KEY: ${GROQ_KEY}
      DARKROUTER_PROXY_TOKEN: ${DARKROUTER_PROXY_TOKEN:-}
    volumes:
      - ./data:/data
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8081/readyz"]
      interval: 30s
      timeout: 3s
      retries: 3
```

- [ ] **Step 4: Write the README**

Create `README.md`:

````markdown
# Darkrouter

A self-hosted LLM gateway. One endpoint, many providers, deterministic failover.

## Status

Phase 1: OpenAI chat completions proxied to one OpenAI-compatible provider, with
config hot-reload. See `docs/superpowers/specs/README.md` for the full design and
the phase roadmap.

## Run

```bash
mkdir -p data
cp darkrouter.example.yaml data/darkrouter.yaml
export GROQ_KEY=your-key
docker compose up --build
```

Then:

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"llama-3.3-70b-versatile","stream":true,
       "messages":[{"role":"user","content":"say hi"}]}'
```

The response carries `X-Darkrouter-Provider`, `X-Darkrouter-Model`,
`X-Darkrouter-Attempts`, and `X-Darkrouter-Request` so routing is visible from a
terminal.

## Develop

```bash
go test ./... -race
go build ./cmd/darkrouter
```
````

- [ ] **Step 5: Verify the image builds and serves**

```bash
docker build -t darkrouter:dev .
mkdir -p data && cp darkrouter.example.yaml data/darkrouter.yaml
docker run --rm -d --name dr-smoke -p 8081:8081 -e GROQ_KEY=placeholder -v "$PWD/data:/data" darkrouter:dev
curl -sf http://localhost:8081/readyz && echo OK
docker rm -f dr-smoke
```
Expected: `OK`. The container is removed by the last command — leaving it running holds port 8081.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile compose.yml darkrouter.example.yaml README.md
git commit -m "chore(docker): add image, compose, example config"
```

---

## Verification

After Task 15, the whole phase is verified by:

```bash
go test ./... -race          # every package, no race reports
go build ./cmd/darkrouter    # static binary
docker build -t darkrouter:dev .
```

Done criteria from the spec that these commands do not cover, and which need a
manual check against a real provider:

- A streaming `curl` returns tokens incrementally with time-to-first-token close to the provider's own.
- The streamed response reports token usage, because `stream_options` was injected.
- Saving `darkrouter.yaml` from vim changes behavior without a restart. (`TestWatchDetectsRenameStyleSave` covers the mechanism; the manual check confirms it end to end.)
- An invalid edit is rejected, the gateway keeps serving, and `/healthz` shows `config_valid: false` with the error.
