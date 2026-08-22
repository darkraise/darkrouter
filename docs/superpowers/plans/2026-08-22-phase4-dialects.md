# Darkrouter Phase 4 — Dialects Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Make Anthropic Messages and Google Gemini first-class in both directions — inbound dialects clients speak to Darkrouter and outbound adapters Darkrouter speaks to providers — so Claude Code and Gemini CLI point at the gateway natively and any dialect can fail over to any other without silently losing a field.

**Architecture:** Three edge dialects and three outbound adapters meet at the canonical IR, which is a superset rather than OpenAI's shape with extras. Every translation that cannot express a field appends an `ir.Warning` instead of dropping silently, and those warnings reach `requests.warnings_json`. A `xlate` package holds the conversions all three adapters share — system-block collection, effort-to-budget, cache-breakpoint capping — so two implementers cannot choose differently for the same request. A golden-file suite runs every case in both directions across every dialect and adapter kind.

**Tech Stack:** Go 1.26.1, the Phase 1–3 dependencies, plus `github.com/tiktoken-go/tokenizer` v0.8.1 for the local token estimator. `CGO_ENABLED=0` for the shipped binary; `CGO_ENABLED=1` locally so `-race` works.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase4-dialects.md` (master design: `docs/superpowers/specs/2026-08-22-darkrouter-design.md`; the design wins wherever they disagree)

## Global Constraints

- Go 1.26. Module path `github.com/darkraise/darkrouter`. The shipped binary builds with `CGO_ENABLED=0`.
- English only in code, comments, commits, and errors.
- Commits are `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no trailing period.
- **Every task ends green.** `export PATH=$PATH:/usr/local/go/bin` first; the toolchain is not on `PATH`. Run `go test ./... -race -count=1` and `go vet ./...` before committing.
- **Silent loss is the failure this phase exists to prevent.** An adapter that cannot express an IR field appends an `ir.Warning` naming the field, the target kind, and the reason. A translation branch that drops something without a warning is a defect even when the output is otherwise correct.
- **System content placement.** The OpenAI edge leaves `system` and `developer` messages inline in `Messages` with `ir.RoleSystem`, because OpenAI permits several and their position is meaningful. The Anthropic and Gemini edges put their single top-level system field in `ir.Request.System`. Every adapter needing one system field calls `xlate.CollectSystem`, which concatenates `req.System` first and then every `RoleSystem` message in order, and warns when a system message appeared after the first non-system message.
- **Effort and budget convert by one fixed table, never per adapter.** `low` = 4096, `medium` = 16384, `high` = 32768, clamped to the model's maximum output tokens when known. The reverse bands at the midpoints — under 10240 is `low`, under 24576 is `medium`, otherwise `high` — so `BudgetEffort(EffortBudget(e, 0)) == e`. It lives in `xlate` so the same request reasons identically against every target.
- **Anthropic reasoning takes one of two mutually exclusive shapes, chosen by model generation.** `thinking: {type:"enabled", budget_tokens}` is a 400 on Claude 4.7 and later; `thinking: {type:"adaptive"}` with `output_config.effort` is a 400 on Claude 4.5 and earlier. Until Phase 6's catalog exists, the generation is read off the model name, and an unrecognized name is honored as the client spelled it and warned about. The effort table above still governs the budget wherever a budget is what gets sent.
- **Anthropic requires `max_tokens`.** When the IR carries none, substitute 4096 and record a warning. The catalog cannot supply the model's real maximum until Phase 6.
- **Anthropic thinking blocks and Gemini thought signatures round-trip verbatim.** Text, signature, and order are preserved unmodified. A re-serialized signature that differs by one byte loses the model's reasoning state on the next turn.
- **Gemini's assistant role is `model`.** Emitting `assistant` is an API error, not a silent mismatch.
- **Gemini declares every function inside `tools[0].functionDeclarations`.** One entry per function silently disables function calling rather than erroring.
- **Gemini stream chunks are incremental, not cumulative.** Each chunk carries new content to append. Diffing successive chunks produces garbage; the review ledger records this as finding F1.
- **Unknown enum values are tolerated.** An unmapped stop reason becomes `end_turn` with a warning; an unknown SSE event type is ignored. Anthropic explicitly warns clients that new event types will appear.
- **Gemini tool-call identity is positional.** `functionCall` and `functionResponse` carry only optional ids that most models omit. Preserve an upstream id when present, otherwise synthesize one from the turn index and call position, and match a response to its call by position within the turn — never by name, which breaks on parallel calls to the same function.
- **At most four `cache_control` breakpoints reach Anthropic.** A fifth is a 400 the client cannot diagnose, so Darkrouter forwards four and drops the rest with a warning.
- Every new package gets a package comment. Comments explain why, never what.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/ir/ir.go` | Gains the fields the mapping tables need: block-valued tool results, streamed thinking signatures, file references |
| `internal/edge/edge.go` | `Passthrough.Surface` typed against `ir.Surface`; `Dialect` gains `ProxyToken` |
| `internal/adapter/adapter.go` | `BuildRequest` returns the warnings it produced |
| `internal/adapter/xlate/` | Conversions every adapter shares: system collection, effort and budget, cache-breakpoint capping, tool-id synthesis |
| `internal/adapter/openaicompat/` | Grows from the Phase 1 subset to the full mapping table |
| `internal/adapter/anthropic/` | Anthropic Messages outbound: build, parse, stream, classify |
| `internal/adapter/gemini/` | Gemini `generateContent` outbound: build, media inlining, parse, stream, classify |
| `internal/edge/openai/` | Grows to render tool calls and reasoning, and to parse tool messages |
| `internal/edge/anthropic/` | `/v1/messages` inbound: parse, write, stream, errors |
| `internal/edge/gemini/` | `/v1beta` inbound: path model extraction, `alt=sse` versus JSON array, listing |
| `internal/tokenize/` | The bundled BPE estimator and the characters-divided-by-four fallback |
| `internal/exec/exec.go` | Adapter registry keyed by provider kind; warnings onto the record; the count-tokens path |
| `internal/server/server.go` | The six new routes and per-dialect inbound authentication |
| `internal/golden/` | The golden-file suite and its fixtures, in both directions |

## What this phase deliberately does not do

- **No passthrough.** Every request here goes through the IR. Phase 9 adds the fast path, and its differential suite compares against these fixtures.
- **No auxiliary surfaces.** Embeddings, images, audio, rerank, and moderations are Phase 5.
- **No `bedrock` or `vertex` adapters.** Phase 8 adds them and extends the golden suite with their fixtures.
- **No Anthropic-shaped `GET /v1/models`.** That path already serves the OpenAI listing and the spec's route table does not ask for a second shape on it. Claude Code does not require it.
- **No real catalog data.** `max_output_tokens` is unknown until Phase 6, so the Anthropic `max_tokens` substitution uses a constant and says so in a warning.

---

### Task 1: IR fields the mapping tables need

**Files:**
- Modify: `internal/ir/ir.go`
- Modify: `internal/exec/commit.go` (`eventBytes`)
- Test: `internal/ir/ir_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `ir.ToolResult{ToolUseID string; Content []ContentBlock; IsError bool}` with `func (t *ToolResult) Text() string`; `ir.Media{MIME, Data, URL, FileID string}`; `ir.Delta` gains `Signature string`; `ir.Request` gains `ParallelToolCalls *bool`; `ir.StreamEvent` gains `Warnings []Warning`; `func (w Warning) String() string`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

`ToolResult.Content` becomes a block slice rather than a string because spec §7
requires a tool result carrying an image to survive translation. Anthropic's
`tool_result.content` is already an array of text and image blocks; a string
cannot hold one, and the hoist-into-a-following-user-message rule for OpenAI and
Gemini has nothing to hoist if the image was flattened away at parse time.

`Media.FileID` is separate from `URL` because the three dialects mean different
things by a reference: OpenAI's `file_id`, Gemini's `fileData.fileUri`, and
Anthropic's `file` source are provider-side handles, while `URL` is a public
address the target may or may not be able to fetch. Collapsing them makes the
Gemini rule in spec §4.2 — inline a public URL, pass a Files API URI through —
impossible to express.

`Delta.Signature` exists because Anthropic streams a thinking block's signature
as its own `signature_delta`, arriving after the thinking text. Without the
field the signature is unreachable from the stream and reasoning state dies on
the next turn.

`StreamEvent.Warnings` exists because the streaming path otherwise has nowhere to
record loss. Spec §4.5 requires an unmapped stop reason to degrade *with a
warning*, and `ir.Response.Warnings` serves only the unary path — so on the path
every CLI uses by default, that warning had no channel at all. Master design §5
makes silent loss the specific failure this phase exists to prevent.

`Needs()` must look inside tool results. A conversation whose only image is
attached to a tool result needs vision just as much as one with an image in a
user turn, and the router would otherwise route it to a text-only model.

- [ ] **Step 1: Write the failing tests**

Append to `internal/ir/ir_test.go`:

```go
func TestToolResultTextConcatenatesTextBlocks(t *testing.T) {
	tr := &ToolResult{
		ToolUseID: "call_1",
		Content: []ContentBlock{
			{Type: BlockText, Text: "42"},
			{Type: BlockImage, Media: &Media{MIME: "image/png", Data: "AAAA"}},
			{Type: BlockText, Text: " degrees"},
		},
	}
	if got := tr.Text(); got != "42 degrees" {
		t.Errorf("Text() = %q, want %q", got, "42 degrees")
	}
}

func TestToolResultTextOnNilIsEmpty(t *testing.T) {
	var tr *ToolResult
	if got := tr.Text(); got != "" {
		t.Errorf("Text() = %q, want empty", got)
	}
}

func TestWarningStringNamesFieldTargetAndReason(t *testing.T) {
	w := Warning{Field: "cache_control", Target: "gemini", Reason: "no equivalent"}
	if got := w.String(); got != "cache_control -> gemini: no equivalent" {
		t.Errorf("String() = %q", got)
	}
}

func TestStreamEventCarriesWarnings(t *testing.T) {
	ev := StreamEvent{
		Type: EventMessageStop,
		Warnings: []Warning{{
			Field: "finishReason", Target: "gemini", Reason: "unrecognized value",
		}},
	}
	if len(ev.Warnings) != 1 || ev.Warnings[0].Field != "finishReason" {
		t.Errorf("warnings = %+v", ev.Warnings)
	}
}

func TestNeedsFindsVisionInsideAToolResult(t *testing.T) {
	r := &Request{Messages: []Message{{
		Role: RoleTool,
		Content: []ContentBlock{{
			Type: BlockToolResult,
			ToolResult: &ToolResult{
				ToolUseID: "call_1",
				Content: []ContentBlock{
					{Type: BlockImage, Media: &Media{MIME: "image/png", Data: "AAAA"}},
				},
			},
		}},
	}}}
	if !r.Needs().Vision {
		t.Error("Needs().Vision = false; an image inside a tool result still needs vision")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/ir/ -run 'ToolResult|Warning|Needs|StreamEvent' -v`
Expected: compile failure — `tr.Text undefined`, `w.String undefined`, and `Content` being a `string` rejects the composite literal.

- [ ] **Step 3: Change the types**

In `internal/ir/ir.go`, replace the `Media`, `ToolResult`, and `Delta` declarations and extend `Request`:

```go
type Media struct {
	MIME string
	Data string // base64
	URL  string

	// FileID is a provider-side handle — an OpenAI file_id, a Gemini
	// fileData.fileUri, an Anthropic file source. It is not interchangeable
	// with URL: a target that accepts its own handle will reject a public
	// address and vice versa.
	FileID string
}

type ToolResult struct {
	ToolUseID string
	Content   []ContentBlock
	IsError   bool
}

// Text flattens the text blocks. OpenAI tool messages and Gemini
// functionResponse payloads are text-only, so both need this and both must
// separately account for whatever it leaves behind.
func (t *ToolResult) Text() string {
	if t == nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range t.Content {
		if blk.Type == BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}
```

```go
// Delta carries incremental content for exactly one block kind.
type Delta struct {
	Type      BlockType
	Text      string
	Thinking  string
	Signature string // thinking-block signature fragment
	ToolInput string // JSON fragment
	ToolID    string
	ToolName  string
}
```

Add to `StreamEvent`, below its `Model` field:

```go
	// Warnings record what this event's translation could not express. The
	// unary path carries them on ir.Response; without this the streaming path,
	// which is what every CLI uses by default, could only lose things silently.
	Warnings []Warning
```

Add to `Request`, after `ToolChoice`:

```go
	// ParallelToolCalls is a pointer because false and unset differ: Anthropic
	// inverts it into disable_parallel_tool_use, and sending that unasked
	// changes behavior.
	ParallelToolCalls *bool
```

Add `"strings"` to the import block, which currently holds only `encoding/json`.

- [ ] **Step 4: Add `Warning.String` and fix `Needs`**

```go
func (w Warning) String() string {
	return w.Field + " -> " + w.Target + ": " + w.Reason
}
```

Replace `Needs` with a version that descends into tool results:

```go
func (r *Request) Needs() Needs {
	n := Needs{
		Tools:     len(r.Tools) > 0,
		Reasoning: r.Reasoning != nil,
	}
	for _, m := range r.Messages {
		if blocksHaveImage(m.Content) {
			n.Vision = true
		}
	}
	return n
}

// blocksHaveImage descends one level into tool results. A tool that returns a
// screenshot needs vision exactly as much as a user who attaches one, and the
// router would otherwise pick a text-only model for it.
func blocksHaveImage(blocks []ContentBlock) bool {
	for _, b := range blocks {
		if b.Type == BlockImage {
			return true
		}
		if b.ToolResult != nil {
			for _, inner := range b.ToolResult.Content {
				if inner.Type == BlockImage {
					return true
				}
			}
		}
	}
	return false
}
```

- [ ] **Step 5: Charge the signature against the pre-commit buffer**

In `internal/exec/commit.go`, `eventBytes` must count the new field or a
provider streaming enormous signatures escapes the cap:

```go
	if ev.Delta != nil {
		n += len(ev.Delta.Text) + len(ev.Delta.Thinking) + len(ev.Delta.Signature) +
			len(ev.Delta.ToolInput) + len(ev.Delta.ToolID) + len(ev.Delta.ToolName)
	}
	for _, w := range ev.Warnings {
		n += len(w.Field) + len(w.Target) + len(w.Reason)
	}
```

- [ ] **Step 6: Run the suite**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./...`
Expected: PASS everywhere, no vet output. Nothing consumes `ToolResult.Content` yet, so the type change compiles cleanly.

- [ ] **Step 7: Commit**

```bash
git add internal/ir/ internal/exec/commit.go
git commit -m "feat(ir): carry blocks, file ids, and signatures"
```

---

### Task 2: Type `edge.Passthrough.Surface`

**Files:**
- Modify: `internal/edge/edge.go`
- Modify: `internal/edge/openai/parse.go`
- Modify: `internal/exec/exec.go` (surface resolution in `Handle`)
- Test: `internal/edge/openai/parse_test.go`

**Interfaces:**
- Consumes: `ir.Surface` from Phase 3.
- Produces: `edge.Passthrough{Body []byte; ModelField string; Surface ir.Surface}`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Phase 3 left this a plain string with `ir.ParseSurface` re-validating it on every
request, which is a runtime check standing in for a compile-time one. Master
design §5.1 types it. Doing it now rather than later matters because Phase 4
writes two more producers of this struct, and each would otherwise learn the
untyped habit.

- [ ] **Step 1: Write the failing test**

Append to `internal/edge/openai/parse_test.go`:

```go
func TestParseRequestReportsTheLLMSurfaceTyped(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt.Surface != ir.SurfaceLLM {
		t.Errorf("Surface = %q, want %q", pt.Surface, ir.SurfaceLLM)
	}
}
```

Confirm `internal/edge/openai/parse_test.go` already imports `net/http/httptest`,
`strings`, and `github.com/darkraise/darkrouter/internal/ir`; add whichever it lacks.

- [ ] **Step 2: Run the test to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -run TypedSurface -v`
Expected: FAIL — `invalid operation: pt.Surface (variable of type string) != ir.SurfaceLLM`.

- [ ] **Step 3: Type the field**

In `internal/edge/edge.go`:

```go
// Passthrough carries what the Phase 9 fast path needs to forward a request
// without re-rendering it. Phase 1 populates it; nothing consumes it yet.
type Passthrough struct {
	Body       []byte // the raw inbound body, retained for replay across attempts
	ModelField string // top-level JSON key holding the model, or "" when in the URL
	Surface    ir.Surface
}
```

In `internal/edge/openai/parse.go`, the final return becomes:

```go
	return req, &edge.Passthrough{Body: body, ModelField: "model", Surface: ir.SurfaceLLM}, nil
```

- [ ] **Step 4: Drop the re-validation in exec**

In `internal/exec/exec.go`, `Handle` currently re-parses the string. Replace:

```go
	surface := ir.SurfaceLLM
	if pt != nil {
		if s, ok := ir.ParseSurface(pt.Surface); ok {
			surface = s
		}
	}
```

with:

```go
	// A dialect that returns no passthrough, or leaves the surface unset, is
	// serving chat. Parsing a string here was Phase 3 checking at runtime what
	// the type system can check at compile time.
	surface := ir.SurfaceLLM
	if pt != nil && pt.Surface != "" {
		surface = pt.Surface
	}
```

`ir.ParseSurface` stays: Phase 6 reads surfaces out of catalog rows, which are
strings from SQLite.

- [ ] **Step 5: Run the suite**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./...`
Expected: PASS, no vet output.

- [ ] **Step 6: Commit**

```bash
git add internal/edge/ internal/exec/exec.go
git commit -m "refactor(edge): type Passthrough.Surface"
```

---

### Task 3: `BuildRequest` returns the warnings it produced

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/openaicompat/build.go`, `internal/adapter/openaicompat/classify.go`
- Modify: `internal/exec/exec.go`
- Test: `internal/adapter/openaicompat/build_test.go`, `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `ir.Warning` and `ir.Warning.String()` from Task 1.
- Produces: `Adapter.BuildRequest(context.Context, *Target, *ir.Request) (*http.Request, []ir.Warning, error)`; `openaicompat.BuildRequest` with the same shape; `exec.warningStrings([]ir.Warning) []string`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: the repository returns values rather than mutating shared state, and a sink would accumulate abandoned attempts' warnings

Spec §5 is the whole reason this phase exists: a field an adapter cannot express
is recorded, never silently dropped. The return value is the mechanism. A sink
passed in through `Target` or the context was rejected because a request is
re-rendered on every attempt, and a shared sink would accumulate the abandoned
attempts' warnings into the record of the one that committed.

That same re-rendering is why exec **assigns** rather than appends: `rec.Warnings`
is replaced at the moment a response commits, so it describes the translation the
client actually received.

- [ ] **Step 1: Write the failing tests**

Append to `internal/adapter/openaicompat/build_test.go`:

```go
func TestBuildRequestReturnsNoWarningsForAPlainRequest(t *testing.T) {
	_, warns, err := BuildRequest(context.Background(),
		&adapter.Target{BaseURL: "https://x.example/v1", Model: "m"},
		&ir.Request{Messages: []ir.Message{{
			Role:    ir.RoleUser,
			Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
		}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}
```

Append to `internal/exec/exec_test.go`:

```go
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
```

`internal/exec/exec_test.go` needs `github.com/darkraise/darkrouter/internal/ir`
in its import block; it is not there yet.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/openaicompat/ ./internal/exec/ -run 'Warning' -v`
Expected: compile failure — `BuildRequest` returns two values, and `warningStrings` is undefined.

- [ ] **Step 3: Widen the interface**

In `internal/adapter/adapter.go`:

```go
type Adapter interface {
	Kind() string
	// BuildRequest returns the rendered HTTP request and every IR field this
	// kind could not express. Master design §5: a dropped field is a fact the
	// trace view must be able to show.
	BuildRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, []ir.Warning, error)
	// ParseResponse takes ownership of resp.Body and always closes it.
	ParseResponse(resp *http.Response) (*ir.Response, error)
	ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]
	Classify(resp *http.Response, err error) Outcome
}

// BodyClassifier refines Classify for the one case a status line cannot
// express: a 400 that means "I do not have that model". An adapter implements
// it only when its upstreams distinguish the two, and exec type-asserts.
type BodyClassifier interface {
	ClassifyBody(resp *http.Response, body []byte, err error) Outcome
}
```

- [ ] **Step 4: Thread it through openaicompat**

In `internal/adapter/openaicompat/build.go`, change the signature and every
return. Task 8 and Task 9 fill the slice; here it is threaded and empty:

```go
func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	var warns []ir.Warning
	body := map[string]any{
		"model":    t.Model,
		"messages": renderMessages(req),
	}
```

Every existing `return nil, err` becomes `return nil, warns, err`, and the final
success return becomes `return hr, warns, nil`.

In `internal/adapter/openaicompat/classify.go`, update the method and declare the
body classifier:

```go
func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	return BuildRequest(ctx, t, req)
}

func (a *Adapter) ClassifyBody(resp *http.Response, body []byte, err error) adapter.Outcome {
	return ClassifyBody(resp, body, err)
}

var _ adapter.BodyClassifier = (*Adapter)(nil)
```

- [ ] **Step 5: Record the warnings in exec**

In `internal/exec/exec.go`, `attempt` gains the third return value and passes the
warnings to the two places a response commits:

```go
	hr, warns, err := e.ad.BuildRequest(ctx, tgt, req)
	if err != nil {
		return adapter.OutcomeFatal, 0, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}
	}
```

In the streaming branch, hand them on:

```go
	if req.Stream {
		return e.attemptStream(w, d, cfg, c, resp, statusCode, rec, seq, timer, warns)
	}
```

In the unary success path, immediately before `e.writeDiagnostics`:

```go
	// Assigned, not appended: the request is re-rendered per attempt, and the
	// record must describe the translation the client actually received rather
	// than every attempt that was abandoned on the way there.
	rec.Warnings = warningStrings(append(warns, out.Warnings...))
```

`attemptStream` takes `warns []ir.Warning` as its final parameter and assigns in
the commit block, next to `rec.FinalProviderID`:

```go
	rec.Warnings = warningStrings(warns)
```

That assignment covers a client that disconnects mid-stream. Events can also
carry warnings of their own — an unmapped stop reason, for instance — so collect
those too. Declare a slice at the top of `attemptStream`:

```go
	// Warnings raised by the events themselves, as distinct from the ones the
	// request rendering produced.
	var streamWarns []ir.Warning
```

append to it beside each of the two `applyUsage(rec, ev.Usage)` calls — the
phase-one drain loop and the live loop inside the `events` closure, but **not**
the buffered replay, whose events phase one already saw:

```go
			streamWarns = append(streamWarns, ev.Warnings...)
```

and supersede the commit-time value once the stream is done, immediately after
`_ = d.WriteStream(w, events)`:

```go
	rec.Warnings = warningStrings(append(warns, streamWarns...))
```

Add the helper at the bottom of `internal/exec/exec.go`:

```go
// warningStrings flattens for the request row, whose warnings column is a JSON
// array of strings. Nil stays nil so the column encodes [] rather than null.
func warningStrings(ws []ir.Warning) []string {
	if len(ws) == 0 {
		return nil
	}
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.String())
	}
	return out
}
```

- [ ] **Step 6: Run the suite**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./...`
Expected: PASS, no vet output.

- [ ] **Step 7: Commit**

```bash
git add internal/adapter/ internal/exec/
git commit -m "feat(adapter): return build-time warnings"
```

---

### Task 4: Adapter registry keyed by provider kind

**Files:**
- Modify: `internal/exec/exec.go`
- Modify: `internal/server/server.go` (`New`)
- Test: `internal/exec/exec_test.go` (`newExecutorWith`), `internal/exec/loop_test.go`

**Interfaces:**
- Consumes: `adapter.Adapter` and `adapter.BodyClassifier` from Task 3, `router.Candidate.Kind` from Phase 3.
- Produces: `exec.New(store *config.Store, src provider.Source, adapters map[string]adapter.Adapter, deps Deps) *Executor`; `func (e *Executor) adapterFor(kind string) (adapter.Adapter, bool)`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: master design §6.1 fixes the five kinds and router.Candidate.Kind exists for exactly this

Phase 3's executor holds one adapter, so every candidate is spoken to in OpenAI's
wire format regardless of what it actually speaks. `router.Candidate` has carried
`Kind` since Phase 3 for exactly this moment.

A candidate whose kind has no registered adapter is **skipped, not failed**.
Providers live in SQLite, where a row can name `bedrock` two phases before the
adapter exists, and a chain that dies on the first such row would be far harder
to diagnose than one that records why it stepped over it. The skip reason lands
on the request row next to the router's own skips.

The 400-means-unknown-model refinement moves behind `adapter.BodyClassifier`.
Phase 3 called `openaicompat.ClassifyBody` unconditionally, which would apply
OpenAI's error-code vocabulary to Anthropic and Gemini responses — and which is
why `internal/exec` imported a specific adapter at all.

- [ ] **Step 1: Write the failing test**

Append to `internal/exec/exec_test.go`:

```go
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
```

`captureLogger` is declared in `internal/exec/exec_test.go:226` — the same file
this test goes in — and its accessor is `only(t *testing.T) *store.RequestRecord`,
which fatals unless exactly one record was logged. That is the right assertion
here: a skipped candidate must still produce exactly one request row. Add `os`,
`path/filepath`, `net/http`, `net/http/httptest`, `strings`,
`internal/adapter`, `internal/adapter/openaicompat`, `internal/config`, and
`internal/provider` to the test file's imports if any are missing.

- [ ] **Step 2: Run the test to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/exec/ -run NoRegisteredAdapter -v`
Expected: compile failure — `New` does not take a map.

- [ ] **Step 3: Hold a registry instead of one adapter**

In `internal/exec/exec.go`, replace the field and the constructor's parameter:

```go
type Executor struct {
	store    *config.Store
	src      provider.Source
	adapters map[string]adapter.Adapter
	client   *http.Client
	deps     Deps
}
```

```go
func New(store *config.Store, src provider.Source, adapters map[string]adapter.Adapter, deps Deps) *Executor {
	t := store.Current().Policy.Timeout
	return &Executor{
		store: store, src: src, adapters: adapters, deps: deps,
		client: &http.Client{
```

The rest of the `http.Client` literal is unchanged. Add the lookup below `New`:

```go
// adapterFor resolves a candidate's provider kind. A miss is a routing fact
// rather than an error: a SQLite row may name a kind whose adapter arrives in a
// later phase, and failing the chain there would be harder to diagnose than
// stepping over it with a reason on the record.
func (e *Executor) adapterFor(kind string) (adapter.Adapter, bool) {
	ad, ok := e.adapters[kind]
	return ad, ok
}
```

- [ ] **Step 4: Resolve per candidate in the loop**

In `runAttempts`, insert the lookup immediately after the live-health re-check
and before `MarkUsed`, so an unusable candidate never marks a credential used:

```go
		ad, ok := e.adapterFor(c.Kind)
		if !ok {
			rec.Skips = append(rec.Skips, traceSkipOf(c, "no_adapter"))
			i++
			continue
		}
```

and pass it into the attempt:

```go
		outcome, status, aerr := e.attempt(w, r, d, cfg, req, c, byID[c.ProviderID], bud, rec, attempts, ad)
```

- [ ] **Step 5: Take the adapter as a parameter**

`attempt` gains a trailing `ad adapter.Adapter` parameter and uses it in place of
`e.ad` throughout:

```go
func (e *Executor) attempt(w http.ResponseWriter, r *http.Request, d edge.Dialect,
	cfg *config.Config, req *ir.Request, c router.Candidate, p provider.Provider,
	bud budget, rec *store.RequestRecord, seq int, ad adapter.Adapter) (adapter.Outcome, int, *ir.Error) {
```

Its three call sites inside the function become `ad.BuildRequest`,
`ad.ParseResponse`, and `e.classify(ad, r.Context(), ctx, resp, doErr)`. The
streaming hand-off becomes:

```go
	if req.Stream {
		return e.attemptStream(w, d, cfg, c, resp, statusCode, rec, seq, timer, warns, ad)
	}
```

`attemptStream` gains a trailing `ad adapter.Adapter` and calls
`ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes)`.

`classify` takes the adapter first:

```go
func (e *Executor) classify(ad adapter.Adapter, inbound, upstream context.Context,
	resp *http.Response, err error) adapter.Outcome {

	if err == nil {
		return ad.Classify(resp, nil)
	}
	if errors.Is(context.Cause(upstream), errDarkrouterTimeout) {
		return adapter.OutcomeRetryableProvider
	}
	if errors.Is(inbound.Err(), context.Canceled) {
		return adapter.OutcomeClientCancelled
	}
	return ad.Classify(resp, err)
}
```

- [ ] **Step 6: Refine the 400 through the optional interface**

Replace the `openaicompat.ClassifyBody` block in `attempt` with:

```go
	// Some providers report an unknown model as a 400 with an identifying error
	// code. Classifying that as Fatal would make failover die on the first
	// provider in a chain that does not carry the model. Only an adapter whose
	// upstreams distinguish the two implements BodyClassifier; the 64 KiB bound
	// matters, because reading an unbounded error body from a misbehaving
	// provider is the hazard max_body_bytes exists to prevent.
	if bc, ok := ad.(adapter.BodyClassifier); ok &&
		outcome == adapter.OutcomeFatal && resp != nil && resp.StatusCode == 400 {

		body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body = io.NopCloser(bytes.NewReader(body))
		outcome = bc.ClassifyBody(resp, body, doErr)
	}
```

Delete the `"github.com/darkraise/darkrouter/internal/adapter/openaicompat"`
import from `internal/exec/exec.go`.

- [ ] **Step 7: Update the constructor's callers**

In `internal/server/server.go`, `New` builds the registry:

```go
		ex: exec.New(cfgStore, src, map[string]adapter.Adapter{
			"openaicompat": openaicompat.New(),
		}, exec.Deps{
			Log: logw, Health: breaker, Fleet: breaker,
		}),
```

Add `"github.com/darkraise/darkrouter/internal/adapter"` to its imports. Task 25
registers the other two kinds here.

In `internal/exec/exec_test.go`, `newExecutorWith` ends with:

```go
	return New(cfgStore, provider.NewYAMLSource(cfgStore),
		map[string]adapter.Adapter{"openaicompat": openaicompat.New()}, deps)
```

Grep for other `New(` call sites in `internal/exec` tests and update them the
same way: `grep -rn "= New(cfgStore" internal/exec/`.

- [ ] **Step 8: Run the suite**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./...`
Expected: PASS, no vet output.

- [ ] **Step 9: Commit**

```bash
git add internal/exec/ internal/server/server.go
git commit -m "feat(exec): select the adapter by provider kind"
```

---

### Task 5: Per-dialect inbound proxy authentication

**Files:**
- Modify: `internal/edge/edge.go`
- Modify: `internal/edge/openai/dialect.go`
- Modify: `internal/server/server.go` (`ProxyHandler`, `withProxyAuth`)
- Test: `internal/edge/openai/dialect_test.go` (create), `internal/server/server_test.go`

**Interfaces:**
- Consumes: `edge.Dialect` from Phase 1.
- Produces: `Dialect.ProxyToken(r *http.Request) string`; `openai.ProxyToken(*http.Request) string`; `(*Server).authed(d edge.Dialect, h http.HandlerFunc) http.HandlerFunc`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 3: master design §13 assigns each dialect its own credential form

Risk is 3 because this is the authentication path; the reducible sum is 3, under
Rule S's gate.

Master design §13 requires each dialect's native credential form to be accepted
and compared against the same `server.proxy_token` in constant time. A Gemini
client that can only send `?key=` would otherwise be unable to authenticate at
all, and Claude Code sends `x-api-key` rather than a bearer token.

The extractor belongs on the dialect because it is dialect knowledge, and because
the alternative — a path-prefix table in the server — would have to be kept in
sync with the route table by hand.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/openai/dialect_test.go`:

```go
package openai

import (
	"net/http/httptest"
	"testing"
)

func TestProxyTokenReadsTheBearerHeader(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"bearer", "Bearer sk-abc", "sk-abc"},
		{"lowercase scheme is still a bearer", "bearer sk-abc", "sk-abc"},
		{"surrounding space is trimmed", "Bearer   sk-abc  ", "sk-abc"},
		{"no header", "", ""},
		{"wrong scheme", "Basic sk-abc", ""},
		{"scheme only", "Bearer", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			if got := New().ProxyToken(r); got != tc.want {
				t.Errorf("ProxyToken = %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -run ProxyToken -v`
Expected: FAIL — `New().ProxyToken undefined`.

- [ ] **Step 3: Add the interface method**

In `internal/edge/edge.go`:

```go
type Dialect interface {
	Name() string

	// ProxyToken extracts the inbound proxy credential in this dialect's own
	// form. Master design §13: an OpenAI client sends Authorization: Bearer, an
	// Anthropic client x-api-key, a Gemini client x-goog-api-key or ?key=. All
	// three are compared against the same server.proxy_token.
	ProxyToken(r *http.Request) string

	ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *Passthrough, error)
	WriteResponse(w http.ResponseWriter, resp *ir.Response) error
	WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error
	WriteError(w http.ResponseWriter, e *ir.Error) error
}
```

- [ ] **Step 4: Implement it for OpenAI**

In `internal/edge/openai/dialect.go`, add the method and move `parseBearer` over
from the server:

```go
// ProxyToken reads the bearer token. RFC 7235 auth schemes are
// case-insensitive, so a client sending "bearer" must still authenticate.
func (d *Dialect) ProxyToken(r *http.Request) string {
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}
```

Add `"strings"` to the file's imports.

- [ ] **Step 5: Wrap each route with its own dialect**

In `internal/server/server.go`, replace `ProxyHandler` and `withProxyAuth`:

```go
func (s *Server) ProxyHandler() http.Handler {
	mux := http.NewServeMux()
	oa := openaiedge.New()
	mux.HandleFunc("POST /v1/chat/completions", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, oa)
	}))
	mux.HandleFunc("GET /v1/models", s.authed(oa, s.handleModels))
	return mux
}

// authed enforces the optional proxy token in the route's own dialect. The
// token is read live because proxy_token is hot-reloadable, unlike the listen
// addresses, and a rejection is written in the dialect the client speaks so its
// existing error handling applies.
func (s *Server) authed(d edge.Dialect, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := s.store.Current().Server.ProxyToken
		if token != "" && !constantTimeEqual(d.ProxyToken(r), token) {
			_ = d.WriteError(w, &ir.Error{
				Type: ir.ErrAuthentication, Message: "invalid proxy token",
			})
			return
		}
		h(w, r)
	}
}
```

Delete `parseBearer` from `internal/server/server.go` and add
`"github.com/darkraise/darkrouter/internal/edge"` to its imports. Remove
`"strings"` if nothing else in the file uses it — `go vet` will not catch an
unused import, but the compiler will.

- [ ] **Step 6: Run the suite**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./...`
Expected: PASS. `TestProxyTokenIsEnforcedWhenConfigured`,
`TestProxyTokenIsOptional`, and `TestProxyTokenRejectsWrongToken` in
`internal/server/server_test.go` all drive `GET /v1/models`, which is still
wrapped, so they continue to pass unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/edge/ internal/server/server.go
git commit -m "feat(edge): read the proxy token per dialect"
```

---

### Task 6: `xlate` collects system content

**Files:**
- Create: `internal/adapter/xlate/system.go`
- Test: `internal/adapter/xlate/system_test.go`

**Interfaces:**
- Consumes: `ir.Request`, `ir.Warning` from Task 1.
- Produces: `xlate.CollectSystem(req *ir.Request, target string) (string, []ir.Warning)`; `xlate.CollectSystemBlocks(req *ir.Request, target string) ([]ir.ContentBlock, []ir.Warning)`; `xlate.NonSystemMessages(msgs []ir.Message) []ir.Message`.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2
**Approach:** inline - skip 3: spec §4.1 fixes the concatenate-in-order-and-warn rule

Spec §4.1: OpenAI permits several system messages including mid-conversation;
Anthropic and Gemini take one. Concatenating in order preserves the content, and
the position — which was meaningful and is being lost — becomes a warning.

This lives in `xlate` rather than in each adapter because all three outbound
kinds need it and a divergence would be invisible: the request would still
succeed, just with a differently-assembled prompt per target.

There are two collectors because there are two shapes. Gemini's
`systemInstruction` is prose, so a string is enough. Anthropic's `system` is an
**array of text blocks that can each carry `cache_control`**, and a cached
system prompt is a Phase 4 done criterion — flattening it to a string would
throw the caching away on the one request type that most needs it.

Both are defined over one unexported `systemSources` walk. Two copies of the
walk would put the misplacement and non-text warnings in two places that must
stay in lockstep forever, which is the same divergence-is-invisible problem this
package exists to solve, one level down.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/xlate/system_test.go`:

```go
package xlate

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func sys(text string) ir.ContentBlock  { return ir.ContentBlock{Type: ir.BlockText, Text: text} }
func user(text string) ir.Message {
	return ir.Message{Role: ir.RoleUser, Content: []ir.ContentBlock{sys(text)}}
}
func sysMsg(text string) ir.Message {
	return ir.Message{Role: ir.RoleSystem, Content: []ir.ContentBlock{sys(text)}}
}

func TestCollectSystemReturnsEmptyWhenThereIsNone(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{Messages: []ir.Message{user("hi")}}, "anthropic")
	if got != "" {
		t.Errorf("system = %q, want empty", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func TestCollectSystemReadsTheTopLevelField(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{
		System:   []ir.ContentBlock{sys("be terse")},
		Messages: []ir.Message{user("hi")},
	}, "anthropic")
	if got != "be terse" {
		t.Errorf("system = %q", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func TestCollectSystemPutsTheTopLevelFieldFirst(t *testing.T) {
	got, _ := CollectSystem(&ir.Request{
		System:   []ir.ContentBlock{sys("first")},
		Messages: []ir.Message{sysMsg("second"), user("hi")},
	}, "gemini")
	if got != "first\n\nsecond" {
		t.Errorf("system = %q, want %q", got, "first\n\nsecond")
	}
}

func TestCollectSystemLeadingMessagesProduceNoWarning(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{
		Messages: []ir.Message{sysMsg("a"), sysMsg("b"), user("hi")},
	}, "anthropic")
	if got != "a\n\nb" {
		t.Errorf("system = %q", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; leading system messages lose no position", warns)
	}
}

func TestCollectSystemWarnsWhenOneFollowedAUserTurn(t *testing.T) {
	got, warns := CollectSystem(&ir.Request{
		Messages: []ir.Message{sysMsg("a"), user("hi"), sysMsg("now be brief")},
	}, "anthropic")
	if got != "a\n\nnow be brief" {
		t.Errorf("system = %q", got)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warns)
	}
	if warns[0].Field != "messages[].role=system" || warns[0].Target != "anthropic" {
		t.Errorf("warning = %+v", warns[0])
	}
}

func TestCollectSystemWarnsOnceForSeveralMisplacedMessages(t *testing.T) {
	_, warns := CollectSystem(&ir.Request{
		Messages: []ir.Message{user("hi"), sysMsg("a"), user("again"), sysMsg("b")},
	}, "gemini")
	if len(warns) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warns)
	}
}

func TestCollectSystemWarnsOnNonTextSystemBlocks(t *testing.T) {
	_, warns := CollectSystem(&ir.Request{
		System: []ir.ContentBlock{
			sys("look"),
			{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
		},
	}, "anthropic")
	if len(warns) != 1 || warns[0].Field != "system[].image" {
		t.Fatalf("warnings = %+v", warns)
	}
}

func TestCollectSystemBlocksKeepsBlocksAndTheirCacheControl(t *testing.T) {
	got, warns := CollectSystemBlocks(&ir.Request{
		System: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "be terse",
				CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"}},
		},
		Messages: []ir.Message{sysMsg("also be kind"), user("hi")},
	}, "anthropic")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	if len(got) != 2 {
		t.Fatalf("blocks = %+v, want two", got)
	}
	if got[0].CacheControl == nil || got[0].CacheControl.TTL != "1h" {
		t.Errorf("block 0 = %+v; the marker must survive collection", got[0])
	}
	if got[1].Text != "also be kind" {
		t.Errorf("block 1 = %+v", got[1])
	}
}

func TestCollectSystemBlocksWarnsOnMisplacementAndNonText(t *testing.T) {
	got, warns := CollectSystemBlocks(&ir.Request{
		System: []ir.ContentBlock{
			{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
		},
		Messages: []ir.Message{user("hi"), sysMsg("now be brief")},
	}, "anthropic")
	if len(got) != 1 || got[0].Text != "now be brief" {
		t.Fatalf("blocks = %+v", got)
	}
	if len(warns) != 2 {
		t.Fatalf("warnings = %+v, want one for the image and one for the position", warns)
	}
}

func TestCollectSystemBlocksReturnsNilWhenThereIsNone(t *testing.T) {
	got, warns := CollectSystemBlocks(&ir.Request{Messages: []ir.Message{user("hi")}}, "anthropic")
	if len(got) != 0 || len(warns) != 0 {
		t.Fatalf("blocks = %+v, warnings = %+v", got, warns)
	}
}

func TestNonSystemMessagesDropsSystemTurns(t *testing.T) {
	got := NonSystemMessages([]ir.Message{sysMsg("a"), user("hi"), sysMsg("b")})
	if len(got) != 1 || got[0].Role != ir.RoleUser {
		t.Fatalf("messages = %+v", got)
	}
}

func TestNonSystemMessagesReturnsNilForAllSystem(t *testing.T) {
	if got := NonSystemMessages([]ir.Message{sysMsg("a")}); len(got) != 0 {
		t.Fatalf("messages = %+v", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/xlate/`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/xlate/system.go`:

```go
// Package xlate holds the conversions every outbound adapter shares.
//
// They live here rather than in each adapter because a divergence would be
// silent: the request still succeeds, only with a differently assembled prompt
// or a differently sized thinking budget per target. Spec §4.6 names that
// specifically for the effort-to-budget table.
package xlate

import (
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
)

// systemSources walks every source of system content exactly once: req.System
// first, then RoleSystem messages in conversation order, one group per source.
//
// Both public collectors are defined over this. The grouping is what the string
// form needs — sources join with a blank line while blocks within one source
// concatenate — and a flat block slice destroys it, which is why the split is
// here rather than at the block level.
func systemSources(req *ir.Request, target string) ([][]ir.ContentBlock, []ir.Warning) {
	var (
		groups    [][]ir.ContentBlock
		warns     []ir.Warning
		seenTurn  bool
		misplaced bool
	)
	add := func(blocks []ir.ContentBlock, field string) {
		var g []ir.ContentBlock
		for _, b := range blocks {
			if b.Type == ir.BlockText {
				g = append(g, b)
				continue
			}
			warns = append(warns, ir.Warning{
				Field:  field + "." + string(b.Type),
				Target: target,
				Reason: "system content accepts text only",
			})
		}
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}

	add(req.System, "system[]")
	for _, m := range req.Messages {
		if m.Role != ir.RoleSystem {
			seenTurn = true
			continue
		}
		// A system message that arrived after a non-system turn had a position
		// that carried meaning, and no target with one system field can express
		// it. One warning covers all of them: the client's fix is the same
		// however many there were.
		if seenTurn {
			misplaced = true
		}
		add(m.Content, "messages[].system")
	}
	if misplaced {
		warns = append(warns, ir.Warning{
			Field:  "messages[].role=system",
			Target: target,
			Reason: "a system message after a conversation turn was moved to the front",
		})
	}
	return groups, warns
}

// CollectSystem flattens to the single string Gemini's systemInstruction takes.
func CollectSystem(req *ir.Request, target string) (string, []ir.Warning) {
	groups, warns := systemSources(req, target)
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		var b strings.Builder
		for _, blk := range g {
			b.WriteString(blk.Text)
		}
		if text := b.String(); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n"), warns
}

// CollectSystemBlocks is CollectSystem for a target whose system field is an
// array. Blocks keep their cache_control, which is what lets an Anthropic
// client's cached system prompt stay cached through the gateway.
func CollectSystemBlocks(req *ir.Request, target string) ([]ir.ContentBlock, []ir.Warning) {
	groups, warns := systemSources(req, target)
	var out []ir.ContentBlock
	for _, g := range groups {
		out = append(out, g...)
	}
	return out, warns
}

// NonSystemMessages is the conversation with system turns removed, for targets
// that carry system content in a field of its own.
func NonSystemMessages(msgs []ir.Message) []ir.Message {
	out := make([]ir.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role != ir.RoleSystem {
			out = append(out, m)
		}
	}
	return out
}
```

Note the warning field for a non-text block nested in a `RoleSystem` message
reads `messages[].system.image`, and for one in `req.System` reads
`system[].image`. `TestCollectSystemWarnsOnNonTextSystemBlocks` asserts the
latter.

`CollectSystem` joins *groups* with a blank line while concatenating blocks
within a group with no separator. That is what
`TestCollectSystemPutsTheTopLevelFieldFirst` (`"first\n\nsecond"`, two sources)
and `TestCollectSystemLeadingMessagesProduceNoWarning` (`"a\n\nb"`, two
messages) pin. Flattening to blocks before joining would change both.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/xlate/ -v`
Expected: PASS, all thirteen.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/xlate/
git commit -m "feat(xlate): collect system content once"
```

`gofmt -l .` must print nothing.

---

### Task 7: `xlate` converts reasoning budgets and required caps

**Files:**
- Create: `internal/adapter/xlate/params.go`
- Test: `internal/adapter/xlate/params_test.go`

**Interfaces:**
- Consumes: `ir.Request`, `ir.Reasoning`, `ir.Warning` from Task 1.
- Produces: `xlate.EffortBudget(effort string, maxOut int) int`; `xlate.BudgetEffort(budget int) string`; `xlate.RequiredMaxTokens(req *ir.Request, target string) (int, []ir.Warning)`; `xlate.SyntheticToolCallID(turn, call int) string`; constants `xlate.DefaultMaxTokens = 4096` and `xlate.MaxCacheBreakpoints = 4`.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2
**Approach:** inline - skip 3: spec §4.6 fixes the conversion table precisely so no implementer chooses

Spec §4.6 fixes the conversion table rather than leaving it to each adapter,
because two implementers would otherwise choose differently and the same request
would reason differently depending on which provider served it. Spec §4.7 does
the same for Anthropic's mandatory `max_tokens`.

`maxOut` is the model's maximum output tokens from the catalog. Phase 6 supplies
it; until then every caller passes 0, which means unknown and disables the clamp.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/xlate/params_test.go`:

```go
package xlate

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestEffortBudgetUsesTheFixedTable(t *testing.T) {
	cases := []struct {
		effort string
		maxOut int
		want   int
	}{
		{"low", 0, 4096},
		{"medium", 0, 16384},
		{"high", 0, 32768},
		{"LOW", 0, 4096},
		{"", 0, 0},
		{"minimal", 0, 0},
		{"high", 8192, 8192},
		{"low", 65536, 4096},
	}
	for _, tc := range cases {
		if got := EffortBudget(tc.effort, tc.maxOut); got != tc.want {
			t.Errorf("EffortBudget(%q, %d) = %d, want %d", tc.effort, tc.maxOut, got, tc.want)
		}
	}
}

func TestBudgetEffortIsTheInverseBanding(t *testing.T) {
	cases := []struct {
		budget int
		want   string
	}{
		{0, ""},
		{1024, "low"},
		{4096, "low"},
		{10239, "low"},
		{10240, "medium"},
		{16384, "medium"},
		{24575, "medium"},
		{24576, "high"},
		{100000, "high"},
	}
	for _, tc := range cases {
		if got := BudgetEffort(tc.budget); got != tc.want {
			t.Errorf("BudgetEffort(%d) = %q, want %q", tc.budget, got, tc.want)
		}
	}
}

func TestRequiredMaxTokensPassesAnExplicitCapThrough(t *testing.T) {
	n := 512
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic")
	if got != 512 {
		t.Errorf("max_tokens = %d, want 512", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v, want none", warns)
	}
}

func TestRequiredMaxTokensSubstitutesAndWarns(t *testing.T) {
	got, warns := RequiredMaxTokens(&ir.Request{}, "anthropic")
	if got != DefaultMaxTokens {
		t.Errorf("max_tokens = %d, want %d", got, DefaultMaxTokens)
	}
	if len(warns) != 1 || warns[0].Field != "max_tokens" || warns[0].Target != "anthropic" {
		t.Fatalf("warnings = %+v", warns)
	}
}

func TestRequiredMaxTokensTreatsZeroAsAbsent(t *testing.T) {
	n := 0
	got, warns := RequiredMaxTokens(&ir.Request{MaxTokens: &n}, "anthropic")
	if got != DefaultMaxTokens || len(warns) != 1 {
		t.Fatalf("max_tokens = %d, warnings = %+v", got, warns)
	}
}

func TestSyntheticToolCallIDIsStableAndPositional(t *testing.T) {
	if got := SyntheticToolCallID(2, 1); got != "call_2_1" {
		t.Errorf("SyntheticToolCallID(2, 1) = %q", got)
	}
	if SyntheticToolCallID(0, 0) == SyntheticToolCallID(0, 1) {
		t.Error("ids for two calls in one turn must differ")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/xlate/ -run 'Effort|Budget|Required|Synthetic'`
Expected: FAIL — undefined identifiers.

- [ ] **Step 3: Write the implementation**

Create `internal/adapter/xlate/params.go`:

```go
package xlate

import (
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/ir"
)

// DefaultMaxTokens is the cap substituted when a target requires one and
// neither the request nor the catalog supplies it. Phase 6 replaces the
// catalog half; until then every substitution carries a warning.
const DefaultMaxTokens = 4096

// MaxCacheBreakpoints is Anthropic's hard limit. A fifth marker is a 400 the
// client cannot diagnose, because the error does not say which one was surplus.
const MaxCacheBreakpoints = 4

// Effort bands, fixed by spec §4.6 so the same request reasons identically
// against every target.
const (
	budgetLow    = 4096
	budgetMedium = 16384
	budgetHigh   = 32768
)

// EffortBudget converts a reasoning effort to a token budget, clamped to the
// model's maximum output tokens. A maxOut of 0 means the catalog does not know
// it, which disables the clamp rather than clamping to nothing.
func EffortBudget(effort string, maxOut int) int {
	var b int
	switch strings.ToLower(effort) {
	case "low":
		b = budgetLow
	case "medium":
		b = budgetMedium
	case "high":
		b = budgetHigh
	default:
		return 0
	}
	if maxOut > 0 && b > maxOut {
		return maxOut
	}
	return b
}

// BudgetEffort is the inverse banding, for targets that take an effort rather
// than a budget. The boundaries sit midway between the table's values so a
// budget written by EffortBudget maps back to the effort it came from.
func BudgetEffort(budget int) string {
	switch {
	case budget <= 0:
		return ""
	case budget < (budgetLow+budgetMedium)/2:
		return "low"
	case budget < (budgetMedium+budgetHigh)/2:
		return "medium"
	default:
		return "high"
	}
}

// RequiredMaxTokens supplies the cap a target demands. A request that carries
// none gets DefaultMaxTokens and a warning, so a truncated answer is traceable
// to the substitution rather than looking like the model stopping early.
func RequiredMaxTokens(req *ir.Request, target string) (int, []ir.Warning) {
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		return *req.MaxTokens, nil
	}
	return DefaultMaxTokens, []ir.Warning{{
		Field:  "max_tokens",
		Target: target,
		Reason: "required by the target and absent from the request; substituted " +
			strconv.Itoa(DefaultMaxTokens),
	}}
}

// SyntheticToolCallID names a tool call the upstream left unidentified.
//
// Gemini's functionCall and functionResponse ids are optional and most models
// omit them, so correlation is positional within the turn. Deriving the id from
// the same two positions keeps it stable across a re-render, which is what makes
// a retried attempt produce the same conversation.
func SyntheticToolCallID(turn, call int) string {
	return "call_" + strconv.Itoa(turn) + "_" + strconv.Itoa(call)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/xlate/ -v`
Expected: PASS.

The banding boundaries are the midpoints between the table's values —
`(4096+16384)/2 = 10240` and `(16384+32768)/2 = 24576` — which is what makes
`BudgetEffort(EffortBudget(e, 0)) == e` for every named effort. The test's
`{10239, "low"}` and `{10240, "medium"}` rows pin exactly that boundary.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/xlate/
git commit -m "feat(xlate): fix the effort and budget table"
```

---

### Task 8: openaicompat renders tool results, prefill, and the reverse split

**Files:**
- Create: `internal/adapter/openaicompat/messages.go`
- Modify: `internal/adapter/openaicompat/build.go` (delete `renderMessages` and `renderContent`, call the new ones)
- Test: `internal/adapter/openaicompat/messages_test.go`

**Interfaces:**
- Consumes: `xlate.SyntheticToolCallID` from Task 7; `ir.ToolResult.Text()` from Task 1.
- Produces: `renderMessages(req *ir.Request, target string) ([]any, []ir.Warning)`; `renderContent(blocks []ir.ContentBlock, target string) (any, []ir.Warning)`; `blocksText(blocks []ir.ContentBlock, field, target string) (string, []ir.Warning)`; `cacheWarning(b ir.ContentBlock, target string) []ir.Warning`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Phase 1's renderer handles text and images only, which is enough for
OpenAI-in, OpenAI-out. The moment an Anthropic client fails over to Groq, the
conversation carries tool results, thinking blocks, cache markers, and possibly
a prefill — and every one of them is silently discarded today.

Spec §4.1's reverse split is the part that produces a 400 rather than a
degraded answer if it is wrong: an Anthropic user turn mixing `tool_result`
blocks and text becomes one `tool` message per `tool_call_id`, placed
immediately after the assistant message carrying the matching `tool_calls`,
followed by a separate `user` message with the remaining text. Emitting the
results first within their own turn is what puts them in that position, because
the IR always carries them in the turn directly after the assistant's.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/openaicompat/messages_test.go`:

```go
package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// rendered marshals through JSON so assertions read against the wire shape
// rather than against map[string]any, which is what the provider sees.
func rendered(t *testing.T, req *ir.Request) ([]map[string]any, []ir.Warning) {
	t.Helper()
	msgs, warns := renderMessages(req, "openaicompat")
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, warns
}

func hasWarning(warns []ir.Warning, field string) bool {
	for _, w := range warns {
		if w.Field == field {
			return true
		}
	}
	return false
}

func TestRenderMessagesEmitsToolCallsOnTheAssistantTurn(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "weather?"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{
			Type:    ir.BlockToolUse,
			ToolUse: &ir.ToolUse{ID: "call_a", Name: "get_weather", Input: json.RawMessage(`{"city":"Oslo"}`)},
		}}},
	}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v, want none", warns)
	}
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2: %v", len(got), got)
	}
	calls := got[1]["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if calls[0].(map[string]any)["id"] != "call_a" || fn["name"] != "get_weather" {
		t.Errorf("tool_calls = %v", calls)
	}
	if fn["arguments"] != `{"city":"Oslo"}` {
		t.Errorf("arguments = %v; OpenAI takes a JSON string, not an object", fn["arguments"])
	}
	if _, ok := got[1]["content"]; !ok {
		t.Error("a tool-call-only assistant message must still carry a content key")
	}
}

func TestRenderMessagesSplitsAToolResultTurn(t *testing.T) {
	got, _ := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{
			Type:    ir.BlockToolUse,
			ToolUse: &ir.ToolUse{ID: "call_a", Name: "f", Input: json.RawMessage(`{}`)},
		}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}},
			}},
			{Type: ir.BlockText, Text: "and tomorrow?"},
		}},
	}})
	if len(got) != 3 {
		t.Fatalf("messages = %d, want 3: %v", len(got), got)
	}
	if got[1]["role"] != "tool" || got[1]["tool_call_id"] != "call_a" || got[1]["content"] != "17C" {
		t.Errorf("tool message = %v", got[1])
	}
	if got[2]["role"] != "user" || got[2]["content"] != "and tomorrow?" {
		t.Errorf("trailing user message = %v", got[2])
	}
}

func TestRenderMessagesKeepsParallelResultsInOrder(t *testing.T) {
	got, _ := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "one"}}}},
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_b", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "two"}}}},
		}},
	}})
	if len(got) != 2 {
		t.Fatalf("messages = %d, want 2: %v", len(got), got)
	}
	if got[0]["tool_call_id"] != "call_a" || got[1]["tool_call_id"] != "call_b" {
		t.Errorf("order = %v, %v; two calls to one function differ only by id",
			got[0]["tool_call_id"], got[1]["tool_call_id"])
	}
}

func TestRenderMessagesHoistsAnImageOutOfAToolResult(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content: []ir.ContentBlock{
					{Type: ir.BlockText, Text: "here"},
					{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
				},
			}},
		}},
	}})
	if len(got) != 2 {
		t.Fatalf("messages = %d, want a tool message and a hoisting user message: %v", len(got), got)
	}
	if got[0]["content"] != "here" {
		t.Errorf("tool content = %v; the image must not be stringified into it", got[0]["content"])
	}
	parts := got[1]["content"].([]any)
	img := parts[0].(map[string]any)
	if img["type"] != "image_url" {
		t.Errorf("hoisted part = %v", img)
	}
	if !hasWarning(warns, "messages[].tool_result.image") {
		t.Errorf("warnings = %+v; the hoist must be recorded", warns)
	}
}

func TestRenderMessagesDropsAnAssistantPrefill(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "write json"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "{"}}},
	}})
	if len(got) != 1 || got[0]["role"] != "user" {
		t.Fatalf("messages = %v, want the user turn alone", got)
	}
	if !hasWarning(warns, "messages[last].assistant_prefill") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderMessagesDropsThinkingAndWarns(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "hmm", Signature: "sig"}},
			{Type: ir.BlockText, Text: "answer"},
		}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "more"}}},
	}})
	if got[0]["content"] != "answer" {
		t.Errorf("content = %v", got[0]["content"])
	}
	if !hasWarning(warns, "messages[].assistant.thinking") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderMessagesWarnsOnCacheControl(t *testing.T) {
	_, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "long prompt",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}}},
	}})
	if !hasWarning(warns, "cache_control") {
		t.Errorf("warnings = %+v; a paid feature must never vanish quietly", warns)
	}
}

func TestRenderMessagesEmitsTopLevelSystemFirst(t *testing.T) {
	got, _ := rendered(t, &ir.Request{
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}},
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	})
	if len(got) != 2 || got[0]["role"] != "system" || got[0]["content"] != "be terse" {
		t.Fatalf("messages = %v", got)
	}
}

func TestRenderMessagesKeepsInlineSystemTurnsInPlace(t *testing.T) {
	got, warns := rendered(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}},
		{Role: ir.RoleSystem, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "now be brief"}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "again"}}},
	}})
	if len(got) != 3 || got[1]["role"] != "system" {
		t.Fatalf("messages = %v; OpenAI permits a mid-conversation system message", got)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %+v; nothing was lost, so nothing is warned about", warns)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/openaicompat/ -run RenderMessages`
Expected: compile failure — `renderMessages` takes one argument and returns one value.

- [ ] **Step 3: Write the renderer**

Create `internal/adapter/openaicompat/messages.go`:

```go
package openaicompat

import (
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// renderMessages converts the IR conversation to OpenAI's messages array.
//
// System content arrives from two places and both are kept: req.System becomes
// a leading system message, and RoleSystem turns stay where they are, because
// OpenAI permits several and their position carries meaning. Only targets with
// a single system field need xlate.CollectSystem.
func renderMessages(req *ir.Request, target string) ([]any, []ir.Warning) {
	var (
		out   []any
		warns []ir.Warning
	)
	if len(req.System) > 0 {
		text, w := blocksText(req.System, "system[]", target)
		warns = append(warns, w...)
		if text != "" {
			out = append(out, map[string]any{"role": "system", "content": text})
		}
	}

	msgs := req.Messages
	// A conversation ending in a text-only assistant turn is Anthropic's prefill
	// idiom, which constrains the next completion. OpenAI reads it as a finished
	// turn and answers the wrong question, so it is dropped rather than
	// mistranslated. A trailing assistant turn carrying tool calls is not a
	// prefill — it is an agentic step whose results have yet to arrive — and
	// dropping it would erase the calls the next turn answers.
	if n := len(msgs); n > 0 && isTextOnlyAssistant(msgs[n-1]) {
		msgs = msgs[:n-1]
		warns = append(warns, ir.Warning{
			Field: "messages[last].assistant_prefill", Target: target,
			Reason: "assistant prefill has no equivalent; the turn was dropped",
		})
	}

	for i, m := range msgs {
		rendered, w := renderMessage(i, m, target)
		out = append(out, rendered...)
		warns = append(warns, w...)
	}
	return out, warns
}

// isTextOnlyAssistant identifies the prefill idiom. Text and nothing else is
// what makes a trailing assistant turn a constraint on the next completion
// rather than a completed step.
func isTextOnlyAssistant(m ir.Message) bool {
	if m.Role != ir.RoleAssistant {
		return false
	}
	for _, b := range m.Content {
		if b.Type != ir.BlockText {
			return false
		}
	}
	return true
}

// renderMessage emits every OpenAI message one IR turn becomes.
//
// A turn holding tool results becomes one `tool` message per result followed by
// a `user` message carrying whatever else it held. OpenAI requires each result
// in its own message keyed by tool_call_id, immediately after the assistant
// message that made the calls, and rejects a mixed turn with a 400.
func renderMessage(turn int, m ir.Message, target string) ([]any, []ir.Warning) {
	var (
		out   []any
		warns []ir.Warning
	)
	for _, b := range m.Content {
		warns = append(warns, cacheWarning(b, target)...)
	}

	switch m.Role {
	case ir.RoleSystem:
		text, w := blocksText(m.Content, "messages[].system", target)
		warns = append(warns, w...)
		if text != "" {
			out = append(out, map[string]any{"role": "system", "content": text})
		}
		return out, warns

	case ir.RoleAssistant:
		var (
			text  strings.Builder
			calls []any
		)
		for _, b := range m.Content {
			switch b.Type {
			case ir.BlockText:
				text.WriteString(b.Text)
			case ir.BlockToolUse:
				if b.ToolUse == nil {
					continue
				}
				id := b.ToolUse.ID
				if id == "" {
					id = xlate.SyntheticToolCallID(turn, len(calls))
				}
				args := string(b.ToolUse.Input)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, map[string]any{
					"id": id, "type": "function",
					"function": map[string]any{"name": b.ToolUse.Name, "arguments": args},
				})
			default:
				warns = append(warns, ir.Warning{
					Field: "messages[].assistant." + string(b.Type), Target: target,
					Reason: "assistant turns carry text and tool calls only",
				})
			}
		}
		msg := map[string]any{"role": "assistant"}
		// The key is always present, even as null: several compatible providers
		// reject an assistant message without one.
		if text.Len() > 0 {
			msg["content"] = text.String()
		} else {
			msg["content"] = nil
		}
		if len(calls) > 0 {
			msg["tool_calls"] = calls
		}
		return append(out, msg), warns
	}

	// user and tool turns share a shape: results first, then everything else.
	var rest, hoisted []ir.ContentBlock
	for _, b := range m.Content {
		if b.Type != ir.BlockToolResult || b.ToolResult == nil {
			rest = append(rest, b)
			continue
		}
		tr := b.ToolResult
		out = append(out, map[string]any{
			"role": "tool", "tool_call_id": tr.ToolUseID, "content": tr.Text(),
		})
		for _, inner := range tr.Content {
			if inner.Type == ir.BlockText {
				continue
			}
			// Spec §7: an OpenAI tool message is text-only, so the media moves
			// into the user turn that follows rather than being discarded.
			hoisted = append(hoisted, inner)
			warns = append(warns, ir.Warning{
				Field: "messages[].tool_result." + string(inner.Type), Target: target,
				Reason: "tool messages are text-only; moved into a following user message",
			})
		}
	}

	blocks := make([]ir.ContentBlock, 0, len(hoisted)+len(rest))
	blocks = append(blocks, hoisted...)
	blocks = append(blocks, rest...)
	if len(blocks) == 0 {
		return out, warns
	}
	content, w := renderContent(blocks, target)
	warns = append(warns, w...)
	return append(out, map[string]any{"role": "user", "content": content}), warns
}

// blocksText flattens to a plain string, warning about anything that is not
// text. It serves the two places OpenAI takes a bare string: system messages
// and tool results.
func blocksText(blocks []ir.ContentBlock, field, target string) (string, []ir.Warning) {
	var (
		b     strings.Builder
		warns []ir.Warning
	)
	for _, blk := range blocks {
		if blk.Type == ir.BlockText {
			b.WriteString(blk.Text)
			continue
		}
		warns = append(warns, ir.Warning{
			Field: field + "." + string(blk.Type), Target: target,
			Reason: "this position accepts text only",
		})
	}
	return b.String(), warns
}

// cacheWarning records a cache-control marker the target cannot express. A
// user whose paid caching stops working on failover has to see it in the trace
// rather than infer it from a bill.
func cacheWarning(b ir.ContentBlock, target string) []ir.Warning {
	if b.CacheControl == nil {
		return nil
	}
	return []ir.Warning{{
		Field: "cache_control", Target: target,
		Reason: "no explicit prompt-caching control in this dialect",
	}}
}
```

- [ ] **Step 4: Move `renderContent` and rewire `BuildRequest`**

Cut `renderContent` out of `internal/adapter/openaicompat/build.go` and paste it
into `messages.go`, changing its signature to collect warnings. Task 9 adds the
document and audio arms; here it keeps its text and image behavior:

```go
// renderContent emits a plain string when every block is text and the
// multi-part form otherwise. Some compatible providers reject the multi-part
// form for text-only messages.
func renderContent(blocks []ir.ContentBlock, target string) (any, []ir.Warning) {
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
		return b.String(), nil
	}

	var warns []ir.Warning
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
			if url == "" {
				warns = append(warns, ir.Warning{
					Field: "messages[].image", Target: target,
					Reason: "image carried neither data nor a URL",
				})
				continue
			}
			parts = append(parts, map[string]any{
				"type": "image_url", "image_url": map[string]any{"url": url},
			})
		default:
			warns = append(warns, ir.Warning{
				Field: "messages[]." + string(blk.Type), Target: target,
				Reason: "unsupported content block",
			})
		}
	}
	return parts, warns
}
```

In `build.go`, the call site becomes:

`BuildRequest` is a package function with no receiver, so the target label is a
constant. Add it at the top of `build.go`:

```go
// targetName labels the warnings this kind produces.
const targetName = "openaicompat"
```

and open the body with:

```go
	msgs, mwarns := renderMessages(req, targetName)
	warns = append(warns, mwarns...)
	body := map[string]any{
		"model":    t.Model,
		"messages": msgs,
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/openaicompat/ -v`
Expected: PASS, including the Phase 1 tests already in `build_test.go`.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/openaicompat/
git commit -m "feat(openaicompat): render tool results and prefill"
```

---

### Task 9: openaicompat renders documents, audio, and the remaining parameters

**Files:**
- Modify: `internal/adapter/openaicompat/messages.go` (`renderContent`)
- Modify: `internal/adapter/openaicompat/build.go`
- Test: `internal/adapter/openaicompat/build_test.go`

**Interfaces:**
- Consumes: `renderContent` and `targetName` from Task 8; `xlate.BudgetEffort` from Task 7.
- Produces: nothing new; `BuildRequest` now populates `response_format`, `parallel_tool_calls`, `reasoning_effort`, and `metadata`, and warns for `top_k`, `safety`, and unmappable media.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Spec §4.2 records that OpenAI Chat Completions **does** support documents via
`file` parts — review finding F11 corrected an earlier draft that claimed a
protocol-level absence. Most `openaicompat` providers do not implement them, but
that is a per-provider capability Phase 6 will know about, not something to
hard-code as impossible here.

`top_k` and `safety` genuinely have no OpenAI equivalent. A Gemini client
sending safety settings that vanish on failover to Groq must see that in the
trace.

- [ ] **Step 1: Write the failing tests**

Append to `internal/adapter/openaicompat/build_test.go`:

```go
func built(t *testing.T, req *ir.Request) (map[string]any, []ir.Warning) {
	t.Helper()
	hr, warns, err := BuildRequest(context.Background(),
		&adapter.Target{BaseURL: "https://x.example/v1", Model: "m"}, req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out, warns
}

func TestBuildRequestRendersADocumentPart(t *testing.T) {
	got, warns := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "summarize"},
			{Type: ir.BlockDocument, Media: &ir.Media{MIME: "application/pdf", Data: "JVBER"}},
		},
	}}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v, want none", warns)
	}
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	file := parts[1].(map[string]any)
	if file["type"] != "file" {
		t.Fatalf("part = %v", file)
	}
	f := file["file"].(map[string]any)
	if f["filename"] != "document.pdf" {
		t.Errorf("filename = %v", f["filename"])
	}
	if f["file_data"] != "data:application/pdf;base64,JVBER" {
		t.Errorf("file_data = %v", f["file_data"])
	}
}

func TestBuildRequestRendersADocumentByFileID(t *testing.T) {
	got, _ := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "read it"},
			{Type: ir.BlockDocument, Media: &ir.Media{MIME: "application/pdf", FileID: "file-123"}},
		},
	}}})
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	f := parts[1].(map[string]any)["file"].(map[string]any)
	if f["file_id"] != "file-123" {
		t.Errorf("file = %v", f)
	}
	if _, ok := f["file_data"]; ok {
		t.Error("a file id and inline data must not both be sent")
	}
}

func TestBuildRequestRendersInputAudio(t *testing.T) {
	got, warns := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "transcribe"},
			{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/wav", Data: "UklGR"}},
		},
	}}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	parts := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	a := parts[1].(map[string]any)
	if a["type"] != "input_audio" {
		t.Fatalf("part = %v", a)
	}
	in := a["input_audio"].(map[string]any)
	if in["format"] != "wav" || in["data"] != "UklGR" {
		t.Errorf("input_audio = %v", in)
	}
}

func TestBuildRequestWarnsOnAnUnsupportedAudioFormat(t *testing.T) {
	_, warns := built(t, &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "hi"},
			{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/ogg", Data: "T2dn"}},
		},
	}}})
	if !hasWarning(warns, "messages[].audio") {
		t.Errorf("warnings = %+v; OpenAI accepts wav and mp3 only", warns)
	}
}

func TestBuildRequestRendersAJSONSchemaResponseFormat(t *testing.T) {
	got, warns := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		ResponseFormat: &ir.ResponseFormat{
			Type:   "json_schema",
			Schema: json.RawMessage(`{"type":"object","properties":{"a":{"type":"string"}}}`),
		},
	})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	rf := got["response_format"].(map[string]any)
	if rf["type"] != "json_schema" {
		t.Fatalf("response_format = %v", rf)
	}
	js := rf["json_schema"].(map[string]any)
	if js["name"] != "response" {
		t.Errorf("json_schema = %v; OpenAI requires a name", js)
	}
	if _, ok := js["schema"]; !ok {
		t.Errorf("json_schema = %v; the schema must be nested under `schema`", js)
	}
}

func TestBuildRequestBandsAReasoningBudgetToAnEffort(t *testing.T) {
	got, warns := built(t, &ir.Request{
		Messages:  []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Reasoning: &ir.Reasoning{Budget: 30000},
	})
	if got["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", got["reasoning_effort"])
	}
	if !hasWarning(warns, "reasoning.budget") {
		t.Errorf("warnings = %+v; banding a budget loses precision and must say so", warns)
	}
}

func TestBuildRequestWarnsOnTopKAndSafety(t *testing.T) {
	k := 40
	_, warns := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		TopK:     &k,
		Safety:   []ir.SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
	})
	if !hasWarning(warns, "top_k") || !hasWarning(warns, "safety") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestPassesParallelToolCallsThrough(t *testing.T) {
	no := false
	got, _ := built(t, &ir.Request{
		Messages:          []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Tools:             []ir.Tool{{Name: "f", Schema: json.RawMessage(`{"type":"object"}`)}},
		ParallelToolCalls: &no,
	})
	if got["parallel_tool_calls"] != false {
		t.Errorf("parallel_tool_calls = %v", got["parallel_tool_calls"])
	}
}

func TestBuildRequestDoesNotLeakAnthropicTransportMetadata(t *testing.T) {
	got, _ := built(t, &ir.Request{
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
		Metadata: map[string]string{
			"anthropic_version":       "2023-06-01",
			"anthropic_thinking_type": "adaptive",
			"user_id":                 "u1",
		},
	})
	md, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("metadata = %v", got["metadata"])
	}
	for _, k := range []string{"anthropic_version", "anthropic_thinking_type"} {
		if _, leaked := md[k]; leaked {
			t.Errorf("%s must not reach an OpenAI upstream as metadata", k)
		}
	}
	if md["user_id"] != "u1" {
		t.Errorf("metadata = %v", md)
	}
}
```

The test file needs `context`, `encoding/json`, `io`, and the `adapter` and `ir`
packages imported; `hasWarning` comes from `messages_test.go` in the same package.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/openaicompat/ -run 'BuildRequestRenders|BuildRequestWarns|BuildRequestBands|BuildRequestPasses|BuildRequestDoes'`
Expected: FAIL — the parts and fields do not exist.

- [ ] **Step 3: Extend `renderContent`**

In `internal/adapter/openaicompat/messages.go`, replace the `default:` arm with
document and audio arms, keeping `default:` for everything else:

```go
		case ir.BlockDocument:
			if blk.Media == nil {
				continue
			}
			file := map[string]any{}
			switch {
			case blk.Media.FileID != "":
				file["file_id"] = blk.Media.FileID
			case blk.Media.Data != "":
				file["filename"] = documentFilename(blk.Media.MIME)
				file["file_data"] = "data:" + blk.Media.MIME + ";base64," + blk.Media.Data
			default:
				warns = append(warns, ir.Warning{
					Field: "messages[].document", Target: target,
					Reason: "document carried neither data nor a file id",
				})
				continue
			}
			parts = append(parts, map[string]any{"type": "file", "file": file})

		case ir.BlockAudio:
			if blk.Media == nil {
				continue
			}
			format, ok := audioFormat(blk.Media.MIME)
			if !ok || blk.Media.Data == "" {
				warns = append(warns, ir.Warning{
					Field: "messages[].audio", Target: target,
					Reason: "OpenAI input_audio accepts inline wav or mp3 only",
				})
				continue
			}
			parts = append(parts, map[string]any{
				"type": "input_audio",
				"input_audio": map[string]any{"data": blk.Media.Data, "format": format},
			})
```

and add both helpers to the same file:

```go
// audioFormat maps a MIME type onto OpenAI's short format name. The API takes
// only these two, so anything else is a drop rather than a guess.
func audioFormat(mime string) (string, bool) {
	switch mime {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav", true
	case "audio/mp3", "audio/mpeg":
		return "mp3", true
	default:
		return "", false
	}
}

// documentFilename supplies the name OpenAI requires alongside inline file
// data. The extension is what several providers sniff to decide how to parse it.
func documentFilename(mime string) string {
	switch mime {
	case "application/pdf":
		return "document.pdf"
	case "text/plain":
		return "document.txt"
	case "text/markdown":
		return "document.md"
	case "text/csv":
		return "document.csv"
	default:
		return "document"
	}
}
```

- [ ] **Step 4: Render the remaining parameters**

In `internal/adapter/openaicompat/build.go`, replace the reasoning block and add
the rest, immediately before the `if req.Stream` block:

```go
	if req.TopK != nil {
		warns = append(warns, ir.Warning{
			Field: "top_k", Target: targetName, Reason: "no equivalent parameter",
		})
	}
	if len(req.Safety) > 0 {
		warns = append(warns, ir.Warning{
			Field: "safety", Target: targetName, Reason: "safety settings are Gemini-only",
		})
	}
	if req.Reasoning != nil {
		switch {
		case req.Reasoning.Effort != "":
			body["reasoning_effort"] = req.Reasoning.Effort
		case req.Reasoning.Budget > 0:
			// A budget is finer-grained than an effort, so the conversion is
			// lossy in one direction only and the loss is worth recording.
			body["reasoning_effort"] = xlate.BudgetEffort(req.Reasoning.Budget)
			warns = append(warns, ir.Warning{
				Field: "reasoning.budget", Target: targetName,
				Reason: "converted to the nearest reasoning_effort band",
			})
		}
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		body["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"strict": true,
				"schema": req.ResponseFormat.Schema,
			},
		}
	}
	if req.ParallelToolCalls != nil {
		body["parallel_tool_calls"] = *req.ParallelToolCalls
	}
	if md := forwardableMetadata(req.Metadata); len(md) > 0 {
		body["metadata"] = md
	}
```

Delete the old `if req.Reasoning != nil && req.Reasoning.Effort != ""` block it
replaces. Add the helper at the bottom of `build.go`:

```go
// forwardableMetadata strips the keys Darkrouter uses internally. The
// anthropic_ prefix is transport state the Anthropic edge parks in Metadata so
// the Anthropic adapter can act on it — the version header and the thinking
// mode; forwarding either to an OpenAI upstream would be nonsense at best and a
// rejected request at worst.
func forwardableMetadata(md map[string]string) map[string]string {
	if len(md) == 0 {
		return nil
	}
	out := make(map[string]string, len(md))
	for k, v := range md {
		if strings.HasPrefix(k, "anthropic_") {
			continue
		}
		out[k] = v
	}
	return out
}
```

Add `"github.com/darkraise/darkrouter/internal/adapter/xlate"` to the imports of
`build.go`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/openaicompat/ -v`
Expected: PASS.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/openaicompat/
git commit -m "feat(openaicompat): map media and the tail parameters"
```

---

### Task 10: The OpenAI edge parses the whole request

**Files:**
- Modify: `internal/edge/openai/parse.go`
- Test: `internal/edge/openai/parse_test.go`

**Interfaces:**
- Consumes: `ir.ToolResult`, `ir.Media.FileID`, `ir.Request.ParallelToolCalls` from Task 1; `xlate.SyntheticToolCallID` from Task 7.
- Produces: nothing new; `ParseRequest` now populates `Tools` from legacy `functions`, tool results, tool calls, `ResponseFormat`, `ParallelToolCalls`, `Metadata`, and both max-token spellings.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Phase 1's parser reads a text-and-images chat request. An OpenAI client running
an agentic loop sends assistant turns with `tool_calls` and `tool` messages with
`tool_call_id` on every turn after the first, and today all of it is dropped
before the router ever sees it — so an Anthropic target receives a conversation
with the tool calls missing and answers as if nothing had happened.

`stop` is the one field with a shape bug rather than a gap: OpenAI accepts a
bare string as well as an array, and `[]string` fails to unmarshal the string
form, which fails the whole request with "invalid JSON body".

Legacy `functions`, `function_call`, and the `function` role are accepted per
spec §4.1 and review finding F16. They are translated to the tool equivalents on
the way in, so nothing downstream has to know they exist.

- [ ] **Step 1: Write the failing tests**

Append to `internal/edge/openai/parse_test.go`:

```go
func parsed(t *testing.T, body string) *ir.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req, _, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestParseRequestReadsAssistantToolCalls(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[
		{"role":"user","content":"weather?"},
		{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_a","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}}]}
	]}`)
	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	blocks := req.Messages[1].Content
	if len(blocks) != 1 || blocks[0].Type != ir.BlockToolUse {
		t.Fatalf("assistant blocks = %+v", blocks)
	}
	tu := blocks[0].ToolUse
	if tu.ID != "call_a" || tu.Name != "get_weather" || string(tu.Input) != `{"city":"Oslo"}` {
		t.Errorf("tool_use = %+v", tu)
	}
}

func TestParseRequestReadsAToolMessage(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[
		{"role":"tool","tool_call_id":"call_a","content":"17C"}]}`)
	blocks := req.Messages[0].Content
	if req.Messages[0].Role != ir.RoleTool {
		t.Errorf("role = %q", req.Messages[0].Role)
	}
	if len(blocks) != 1 || blocks[0].Type != ir.BlockToolResult {
		t.Fatalf("blocks = %+v", blocks)
	}
	tr := blocks[0].ToolResult
	if tr.ToolUseID != "call_a" || tr.Text() != "17C" {
		t.Errorf("tool_result = %+v", tr)
	}
}

func TestParseRequestAcceptsALegacyFunctionMessage(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[
		{"role":"function","name":"get_weather","content":"17C"}]}`)
	tr := req.Messages[0].Content[0].ToolResult
	if tr == nil || tr.Text() != "17C" {
		t.Fatalf("tool_result = %+v", tr)
	}
	if tr.ToolUseID == "" {
		t.Error("a legacy function message carries no id; one must be synthesized")
	}
}

func TestParseRequestAcceptsLegacyFunctionDeclarations(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[{"role":"user","content":"hi"}],
		"functions":[{"name":"f","description":"d","parameters":{"type":"object"}}],
		"function_call":{"name":"f"}}`)
	if len(req.Tools) != 1 || req.Tools[0].Name != "f" {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "tool" || req.ToolChoice.Name != "f" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}

func TestParseRequestAcceptsStopAsAStringOrArray(t *testing.T) {
	if got := parsed(t, `{"model":"m","messages":[],"stop":"END"}`).StopSequences; len(got) != 1 || got[0] != "END" {
		t.Errorf("stop as string = %v", got)
	}
	if got := parsed(t, `{"model":"m","messages":[],"stop":["A","B"]}`).StopSequences; len(got) != 2 {
		t.Errorf("stop as array = %v", got)
	}
	if got := parsed(t, `{"model":"m","messages":[]}`).StopSequences; got != nil {
		t.Errorf("stop absent = %v, want nil", got)
	}
}

func TestParseRequestPrefersMaxCompletionTokens(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[],"max_tokens":100,"max_completion_tokens":200}`)
	if req.MaxTokens == nil || *req.MaxTokens != 200 {
		t.Errorf("max tokens = %v; max_completion_tokens is the current spelling", req.MaxTokens)
	}
}

func TestParseRequestReadsMultiPartMediaParts(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[{"role":"user","content":[
		{"type":"text","text":"look"},
		{"type":"image_url","image_url":{"url":"https://x.example/a.png"}},
		{"type":"input_audio","input_audio":{"data":"UklGR","format":"wav"}},
		{"type":"file","file":{"file_id":"file-1"}}]}]}`)
	blocks := req.Messages[0].Content
	if len(blocks) != 4 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[1].Type != ir.BlockImage || blocks[1].Media.URL != "https://x.example/a.png" {
		t.Errorf("image = %+v", blocks[1])
	}
	if blocks[2].Type != ir.BlockAudio || blocks[2].Media.MIME != "audio/wav" || blocks[2].Media.Data != "UklGR" {
		t.Errorf("audio = %+v", blocks[2])
	}
	if blocks[3].Type != ir.BlockDocument || blocks[3].Media.FileID != "file-1" {
		t.Errorf("file = %+v", blocks[3])
	}
}

func TestParseRequestReadsResponseFormatAndFlags(t *testing.T) {
	req := parsed(t, `{"model":"m","messages":[],"parallel_tool_calls":false,
		"metadata":{"user_id":"u1"},
		"response_format":{"type":"json_schema","json_schema":{"name":"r","schema":{"type":"object"}}}}`)
	if req.ResponseFormat == nil || req.ResponseFormat.Type != "json_schema" {
		t.Fatalf("response_format = %+v", req.ResponseFormat)
	}
	if string(req.ResponseFormat.Schema) != `{"type":"object"}` {
		t.Errorf("schema = %s; the inner schema is what the IR carries", req.ResponseFormat.Schema)
	}
	if req.ParallelToolCalls == nil || *req.ParallelToolCalls {
		t.Errorf("parallel_tool_calls = %v", req.ParallelToolCalls)
	}
	if req.Metadata["user_id"] != "u1" {
		t.Errorf("metadata = %v", req.Metadata)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -run ParseRequest`
Expected: FAIL — tool calls, tool results, legacy fields, and the string `stop` are all unhandled; several panic on nil.

- [ ] **Step 3: Widen the wire types**

In `internal/edge/openai/parse.go`, replace the three wire structs and add three:

```go
type wireRequest struct {
	Model               string              `json:"model"`
	Messages            []wireMessage       `json:"messages"`
	Tools               []wireTool          `json:"tools"`
	Functions           []wireFunction      `json:"functions"`
	ToolChoice          json.RawMessage     `json:"tool_choice"`
	FunctionCall        json.RawMessage     `json:"function_call"`
	MaxTokens           *int                `json:"max_tokens"`
	MaxCompletionTokens *int                `json:"max_completion_tokens"`
	Temperature         *float64            `json:"temperature"`
	TopP                *float64            `json:"top_p"`
	Stop                json.RawMessage     `json:"stop"`
	Stream              bool                `json:"stream"`
	Reasoning           *string             `json:"reasoning_effort"`
	ResponseFormat      *wireResponseFormat `json:"response_format"`
	ParallelToolCalls   *bool               `json:"parallel_tool_calls"`
	Metadata            map[string]string   `json:"metadata"`
}

type wireMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Name       string          `json:"name"`
	ToolCallID string          `json:"tool_call_id"`
	ToolCalls  []wireToolCall  `json:"tool_calls"`
	// FunctionCall is the pre-2023 single-call form. Some SDK versions still
	// replay it from stored history.
	FunctionCall *wireFunctionCall `json:"function_call"`
}

type wireToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireResponseFormat struct {
	Type       string `json:"type"`
	JSONSchema *struct {
		Name   string          `json:"name"`
		Schema json.RawMessage `json:"schema"`
	} `json:"json_schema"`
}

type wirePart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
	InputAudio *struct {
		Data   string `json:"data"`
		Format string `json:"format"`
	} `json:"input_audio"`
	File *struct {
		FileID   string `json:"file_id"`
		FileData string `json:"file_data"`
		Filename string `json:"filename"`
	} `json:"file"`
}
```

- [ ] **Step 4: Fill the IR request**

Replace the body of `ParseRequest` between the unmarshal and the return:

```go
	req := &ir.Request{
		Model:             w.Model,
		MaxTokens:         w.MaxTokens,
		Temperature:       w.Temperature,
		TopP:              w.TopP,
		StopSequences:     parseStop(w.Stop),
		Stream:            w.Stream,
		ParallelToolCalls: w.ParallelToolCalls,
		Metadata:          w.Metadata,
	}
	// max_completion_tokens is the current spelling and wins where a client
	// sends both, which SDKs do while they migrate.
	if w.MaxCompletionTokens != nil {
		req.MaxTokens = w.MaxCompletionTokens
	}
	if w.Reasoning != nil {
		req.Reasoning = &ir.Reasoning{Effort: *w.Reasoning}
	}
	if w.ResponseFormat != nil && w.ResponseFormat.Type == "json_schema" && w.ResponseFormat.JSONSchema != nil {
		req.ResponseFormat = &ir.ResponseFormat{
			Type: "json_schema", Schema: w.ResponseFormat.JSONSchema.Schema,
		}
	}
	for i, m := range w.Messages {
		msg, err := parseMessage(i, m)
		if err != nil {
			return nil, nil, err
		}
		req.Messages = append(req.Messages, msg)
	}
	for _, t := range w.Tools {
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Function.Name, Description: t.Function.Description, Schema: t.Function.Parameters,
		})
	}
	// Legacy declarations are translated rather than rejected: nothing
	// downstream should have to know the old spelling existed.
	for _, f := range w.Functions {
		req.Tools = append(req.Tools, ir.Tool{
			Name: f.Name, Description: f.Description, Schema: f.Parameters,
		})
	}
	req.ToolChoice = parseToolChoice(w.ToolChoice)
	if req.ToolChoice == nil {
		req.ToolChoice = parseToolChoice(w.FunctionCall)
	}
```

- [ ] **Step 5: Add the message and stop parsers**

```go
// parseMessage converts one wire message. The turn index is needed because a
// legacy function message carries no call id and one has to be synthesized
// from its position.
func parseMessage(turn int, m wireMessage) (ir.Message, error) {
	role := mapRole(m.Role)
	blocks, err := parseContent(m.Content)
	if err != nil {
		return ir.Message{}, err
	}

	if role == ir.RoleTool {
		id := m.ToolCallID
		if id == "" {
			id = xlate.SyntheticToolCallID(turn, 0)
		}
		return ir.Message{Role: role, Content: []ir.ContentBlock{{
			Type:       ir.BlockToolResult,
			ToolResult: &ir.ToolResult{ToolUseID: id, Content: blocks},
		}}}, nil
	}

	for i, tc := range m.ToolCalls {
		id := tc.ID
		if id == "" {
			id = xlate.SyntheticToolCallID(turn, i)
		}
		blocks = append(blocks, ir.ContentBlock{
			Type: ir.BlockToolUse,
			ToolUse: &ir.ToolUse{
				ID: id, Name: tc.Function.Name,
				Input: json.RawMessage(argumentsOrEmpty(tc.Function.Arguments)),
			},
		})
	}
	if m.FunctionCall != nil {
		blocks = append(blocks, ir.ContentBlock{
			Type: ir.BlockToolUse,
			ToolUse: &ir.ToolUse{
				ID: xlate.SyntheticToolCallID(turn, len(m.ToolCalls)), Name: m.FunctionCall.Name,
				Input: json.RawMessage(argumentsOrEmpty(m.FunctionCall.Arguments)),
			},
		})
	}
	return ir.Message{Role: role, Content: blocks}, nil
}

// argumentsOrEmpty guards the IR against an empty Input, which would serialize
// as bare nothing rather than an empty object and be rejected by every target.
func argumentsOrEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

// parseStop accepts both shapes OpenAI documents. A bare string is common
// enough that rejecting it fails real requests with a JSON error.
func parseStop(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many
	}
	return nil
}
```

`parseContent` also needs a null guard. `{"content": null}` is what every SDK
sends on an assistant turn that is only tool calls, and unmarshalling JSON null
into a string succeeds with an empty result — so today it yields one empty text
block, and the tool-call block would arrive as the *second* entry of a turn the
IR should describe as holding one. Replace its opening:

```go
func parseContent(raw json.RawMessage) ([]ir.ContentBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, nil
		}
		return []ir.ContentBlock{{Type: ir.BlockText, Text: s}}, nil
	}
```

Extend `parseContent`'s switch with the media arms:

```go
		case "input_audio":
			if p.InputAudio == nil {
				continue
			}
			out = append(out, ir.ContentBlock{Type: ir.BlockAudio, Media: &ir.Media{
				MIME: "audio/" + p.InputAudio.Format, Data: p.InputAudio.Data,
			}})
		case "file":
			if p.File == nil {
				continue
			}
			mime, data := splitDataURI(p.File.FileData)
			out = append(out, ir.ContentBlock{Type: ir.BlockDocument, Media: &ir.Media{
				MIME: mime, Data: data, FileID: p.File.FileID,
			}})
```

and add the data-URI splitter:

```go
// splitDataURI pulls the MIME type and payload out of a data URI. A value that
// is not one is returned as opaque data, since some clients send bare base64.
func splitDataURI(s string) (mime, data string) {
	if !strings.HasPrefix(s, "data:") {
		return "", s
	}
	rest := strings.TrimPrefix(s, "data:")
	head, payload, found := strings.Cut(rest, ",")
	if !found {
		return "", s
	}
	return strings.TrimSuffix(head, ";base64"), payload
}
```

Add `"strings"` and
`"github.com/darkraise/darkrouter/internal/adapter/xlate"` to the imports of
`parse.go`. Extend `parseToolChoice` to accept the legacy named form, which
carries the name at the top level rather than under `function`:

```go
	var named struct {
		Name     string `json:"name"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &named); err == nil {
		if named.Function.Name != "" {
			return &ir.ToolChoice{Mode: "tool", Name: named.Function.Name}
		}
		if named.Name != "" {
			return &ir.ToolChoice{Mode: "tool", Name: named.Name}
		}
	}
	return nil
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -v`
Expected: PASS.

- [ ] **Step 7: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/openai/
git commit -m "feat(edge/openai): parse tools, media, and legacy fields"
```

---

### Task 11: The OpenAI edge writes tool calls and reasoning

**Files:**
- Modify: `internal/edge/openai/write.go`
- Test: `internal/edge/openai/write_test.go`

**Interfaces:**
- Consumes: `ir.Response` from Phase 1.
- Produces: `var now = time.Now` in package `openai`, the clock seam the golden suite overrides.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Phase 1's writer flattens the response to its text blocks. An OpenAI client
whose request was served by Anthropic therefore receives an empty `content` and
no `tool_calls` at all whenever the model chose to call a tool — the agentic
loop stops with the client believing the model had nothing to say.

`created` becomes a package variable so a golden fixture can be byte-stable.
Everything else in the response is a pure function of the IR.

- [ ] **Step 1: Write the failing tests**

Append to `internal/edge/openai/write_test.go`:

```go
func written(t *testing.T, resp *ir.Response) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := WriteResponse(rec, resp); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWriteResponseEmitsToolCalls(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "req-1", Model: "m", StopReason: ir.StopToolUse,
		Content: []ir.ContentBlock{{
			Type:    ir.BlockToolUse,
			ToolUse: &ir.ToolUse{ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)},
		}},
	})
	choice := got["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v", choice["finish_reason"])
	}
	msg := choice["message"].(map[string]any)
	if msg["content"] != nil {
		t.Errorf("content = %v; a tool-call-only reply sends null", msg["content"])
	}
	calls := msg["tool_calls"].([]any)
	fn := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["arguments"] != `{"x":1}` {
		t.Errorf("arguments = %v; OpenAI takes a JSON string", fn["arguments"])
	}
}

func TestWriteResponseEmitsReasoningContent(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "req-1", Model: "m", StopReason: ir.StopEndTurn,
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "let me see", Signature: "sig"}},
			{Type: ir.BlockText, Text: "42"},
		},
	})
	msg := got["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "42" {
		t.Errorf("content = %v", msg["content"])
	}
	if msg["reasoning_content"] != "let me see" {
		t.Errorf("reasoning_content = %v", msg["reasoning_content"])
	}
}

func TestWriteResponseReportsCachedAndReasoningTokens(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "req-1", Model: "m", StopReason: ir.StopEndTurn,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
		Usage: ir.Usage{
			InputTokens: 10, OutputTokens: 5, CacheReadTokens: 8, ReasoningTokens: 3,
		},
	})
	usage := got["usage"].(map[string]any)
	pd := usage["prompt_tokens_details"].(map[string]any)
	if pd["cached_tokens"].(float64) != 8 {
		t.Errorf("cached_tokens = %v", pd["cached_tokens"])
	}
	cd := usage["completion_tokens_details"].(map[string]any)
	if cd["reasoning_tokens"].(float64) != 3 {
		t.Errorf("reasoning_tokens = %v", cd["reasoning_tokens"])
	}
}

func TestWriteResponseMapsStopSequenceAndPauseTurn(t *testing.T) {
	for _, sr := range []ir.StopReason{ir.StopStopSequence, ir.StopPauseTurn} {
		got := written(t, &ir.Response{
			ID: "r", Model: "m", StopReason: sr,
			Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
		})
		fr := got["choices"].([]any)[0].(map[string]any)["finish_reason"]
		if fr != "stop" {
			t.Errorf("finish_reason for %q = %v, want stop", sr, fr)
		}
	}
}

func TestWriteResponseUsesTheClockSeam(t *testing.T) {
	orig := now
	now = func() time.Time { return time.Unix(1700000000, 0) }
	defer func() { now = orig }()

	got := written(t, &ir.Response{
		ID: "r", Model: "m", StopReason: ir.StopEndTurn,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
	})
	if got["created"].(float64) != 1700000000 {
		t.Errorf("created = %v", got["created"])
	}
}
```

The test file needs `encoding/json`, `net/http/httptest`, `testing`, and `time`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -run WriteResponse`
Expected: FAIL — no `tool_calls`, no `reasoning_content`, no token details, and `now` is undefined.

- [ ] **Step 3: Rewrite `WriteResponse`**

Replace it in `internal/edge/openai/write.go`:

```go
// now is a seam so the golden suite can pin `created`, which is the only
// non-deterministic field in an OpenAI response.
var now = time.Now

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	var text, reasoning strings.Builder
	var calls []any
	for _, b := range resp.Content {
		switch b.Type {
		case ir.BlockText:
			text.WriteString(b.Text)
		case ir.BlockThinking:
			if b.Thinking != nil {
				reasoning.WriteString(b.Thinking.Text)
			}
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			args := string(b.ToolUse.Input)
			if args == "" {
				args = "{}"
			}
			calls = append(calls, map[string]any{
				"id": b.ToolUse.ID, "type": "function",
				"function": map[string]any{"name": b.ToolUse.Name, "arguments": args},
			})
		}
	}

	msg := map[string]any{"role": "assistant"}
	// null rather than "" when there is no text: an OpenAI client reads an
	// empty string as a real empty answer and stops its loop.
	if text.Len() > 0 {
		msg["content"] = text.String()
	} else {
		msg["content"] = nil
	}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}

	out := map[string]any{
		"id":      resp.ID,
		"object":  "chat.completion",
		"created": now().Unix(),
		"model":   resp.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       msg,
			"finish_reason": finishReason(resp.StopReason),
		}},
		"usage": usageBody(resp.Usage),
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// usageBody reports cache and reasoning tokens under OpenAI's nested details
// objects, and omits each object when its counter is zero — sending zeros would
// claim the provider reported them.
func usageBody(u ir.Usage) map[string]any {
	body := map[string]any{
		"prompt_tokens":     u.InputTokens,
		"completion_tokens": u.OutputTokens,
		"total_tokens":      u.InputTokens + u.OutputTokens,
	}
	if u.CacheReadTokens > 0 {
		body["prompt_tokens_details"] = map[string]any{"cached_tokens": u.CacheReadTokens}
	}
	if u.ReasoningTokens > 0 {
		body["completion_tokens_details"] = map[string]any{"reasoning_tokens": u.ReasoningTokens}
	}
	return body
}
```

Add `"strings"` to the imports if the file lost it, and keep `"time"`.

`finishReason` already returns `"stop"` for every unlisted reason, which covers
`stop_sequence`, `pause_turn`, and `error` — OpenAI has no wire value for any of
them, and `stop` is the only one a client handles without breaking.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -v`
Expected: PASS, including the Phase 1 assertions in `TestWriteResponseProducesOpenAIShape`.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/openai/
git commit -m "feat(edge/openai): write tool calls and reasoning"
```

---

### Task 12: The OpenAI edge streams tool calls

**Files:**
- Modify: `internal/edge/openai/stream.go`
- Test: `internal/edge/openai/stream_test.go`

**Interfaces:**
- Consumes: `now` from Task 11; `ir.StreamEvent`, `ir.Delta` from Task 1.
- Produces: nothing new; `WriteStream` now emits `tool_calls` and `reasoning_content` deltas.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Spec §4.8: OpenAI streams tool arguments as JSON fragments across many chunks,
indexed by `tool_calls[].index`, and that index is **dense and per stream** — it
is not the IR block index. `internal/adapter/openaicompat` deliberately offsets
tool blocks by `toolBlockBase = 1000` to keep them out of the text block's index
space, so writing the IR index straight into the wire chunk would send
`"index": 1000` and every client that pre-allocates by index would break.

The mapping is assigned at `content_block_start`, in the order blocks open,
which is the order OpenAI itself numbers them.

- [ ] **Step 1: Write the failing test**

Append to `internal/edge/openai/stream_test.go`:

```go
// chunks collects the JSON payload of every SSE data line except the sentinel.
func chunks(t *testing.T, events []ir.StreamEvent) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	err := WriteStream(rec, func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("chunk %q: %v", data, err)
		}
		out = append(out, m)
	}
	return out
}

func delta(t *testing.T, chunk map[string]any) map[string]any {
	t.Helper()
	return chunk["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
}

func TestWriteStreamEmitsDenseToolCallIndices(t *testing.T) {
	got := chunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_a", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"x":`}},
		{Type: ir.EventBlockStart, Index: 1001, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_b", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `1}`}},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	})
	// role chunk, two starts, two argument fragments, one finish
	if len(got) != 6 {
		t.Fatalf("chunks = %d: %v", len(got), got)
	}
	first := delta(t, got[1])["tool_calls"].([]any)[0].(map[string]any)
	if first["index"].(float64) != 0 || first["id"] != "call_a" {
		t.Errorf("first tool call = %v", first)
	}
	if first["function"].(map[string]any)["name"] != "f" {
		t.Errorf("first tool call = %v", first)
	}
	second := delta(t, got[3])["tool_calls"].([]any)[0].(map[string]any)
	if second["index"].(float64) != 1 {
		t.Errorf("second tool call index = %v; the wire index is dense, not the IR block index",
			second["index"])
	}
	frag := delta(t, got[4])["tool_calls"].([]any)[0].(map[string]any)
	if frag["index"].(float64) != 0 {
		t.Errorf("continuation index = %v; it must return to the first call", frag["index"])
	}
	if frag["function"].(map[string]any)["arguments"] != "1}" {
		t.Errorf("continuation = %v", frag)
	}
	if _, ok := frag["id"]; ok {
		t.Error("a continuation must not repeat the id")
	}
}

func TestWriteStreamEmitsReasoningDeltas(t *testing.T) {
	got := chunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "hmm"}},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockText, Text: "42"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	})
	if delta(t, got[1])["reasoning_content"] != "hmm" {
		t.Errorf("chunk 1 = %v", got[1])
	}
	if delta(t, got[2])["content"] != "42" {
		t.Errorf("chunk 2 = %v", got[2])
	}
}
```

The test file needs `encoding/json`, `net/http/httptest`, `strings`, and `testing`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -run WriteStream`
Expected: FAIL — block starts and tool deltas are skipped, so only three chunks appear.

- [ ] **Step 3: Track the wire index and emit the new deltas**

In `internal/edge/openai/stream.go`, replace the loop body's switch. `chunk` also
takes its timestamp from the seam now:

```go
func chunk(id, model string, delta map[string]any, finish any) map[string]any {
	if id == "" {
		id = "chatcmpl-darkrouter"
	}
	return map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": now().Unix(),
		"model":   model,
		"choices": []any{map[string]any{
			"index":         0,
			"delta":         delta,
			"finish_reason": finish,
		}},
	}
}
```

Inside `WriteStream`, declare the mapping beside `id` and `model`:

```go
	// OpenAI numbers tool calls densely from zero in the order they open. The
	// IR block index is not that number — the openaicompat adapter offsets tool
	// blocks by 1000 to keep them clear of the text block — so the two are
	// mapped rather than passed through.
	toolIndex := map[int]int{}
```

and replace the switch arms:

```go
		switch ev.Type {
		case ir.EventPing, ir.EventBlockStop:
			continue
		case ir.EventMessageStart:
			id, model = ev.ID, ev.Model
			sendErr = send(chunk(id, model, map[string]any{"role": "assistant"}, nil))
		case ir.EventBlockStart:
			if ev.Delta == nil || ev.Delta.Type != ir.BlockToolUse {
				continue
			}
			idx := len(toolIndex)
			toolIndex[ev.Index] = idx
			sendErr = send(chunk(id, model, map[string]any{
				"tool_calls": []any{map[string]any{
					"index": idx, "id": ev.Delta.ToolID, "type": "function",
					"function": map[string]any{"name": ev.Delta.ToolName, "arguments": ""},
				}},
			}, nil))
		case ir.EventContentDelta:
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case ir.BlockText:
				sendErr = send(chunk(id, model, map[string]any{"content": ev.Delta.Text}, nil))
			case ir.BlockThinking:
				if ev.Delta.Thinking == "" {
					continue
				}
				sendErr = send(chunk(id, model, map[string]any{"reasoning_content": ev.Delta.Thinking}, nil))
			case ir.BlockToolUse:
				if ev.Delta.ToolInput == "" {
					continue
				}
				idx, ok := toolIndex[ev.Index]
				if !ok {
					// A provider that streams arguments without ever opening the
					// block still has to reach the client, so the block is
					// opened here rather than dropping the call.
					idx = len(toolIndex)
					toolIndex[ev.Index] = idx
					if err := send(chunk(id, model, map[string]any{
						"tool_calls": []any{map[string]any{
							"index": idx, "id": ev.Delta.ToolID, "type": "function",
							"function": map[string]any{"name": ev.Delta.ToolName, "arguments": ""},
						}},
					}, nil)); err != nil {
						return err
					}
				}
				// No id on a continuation: repeating it makes some clients open
				// a second call.
				sendErr = send(chunk(id, model, map[string]any{
					"tool_calls": []any{map[string]any{
						"index":    idx,
						"function": map[string]any{"arguments": ev.Delta.ToolInput},
					}},
				}, nil))
			default:
				continue
			}
		case ir.EventMessageDelta:
```

The `EventMessageDelta` and `EventMessageStop` arms are unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/openai/ -v`
Expected: PASS. The Phase 1 stream tests still hold: `EventBlockStop` and pings
remain skipped, and the in-stream error shape — `data: {"error":{...}}` followed
by `data: [DONE]` — is already what spec §4.9 prescribes for this dialect.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/openai/
git commit -m "feat(edge/openai): stream tool calls and reasoning"
```

---

### Task 13: Anthropic content blocks

**Files:**
- Create: `internal/adapter/anthropic/content.go`
- Test: `internal/adapter/anthropic/content_test.go`

**Interfaces:**
- Consumes: `ir.ContentBlock`, `ir.ToolResult`, `ir.Media` from Task 1; `xlate.MaxCacheBreakpoints` from Task 7.
- Produces: `type cacheBudget struct{...}` with `func (c *cacheBudget) take() bool`; `renderBlocks(blocks []ir.ContentBlock, cb *cacheBudget) ([]any, []ir.Warning)`; `const targetName = "anthropic"`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

This is where spec §4.2's invariants are either honored or quietly broken.
Thinking blocks must be emitted **unmodified and in order, with their
signatures**, or the next turn loses the model's reasoning state — and the
failure is invisible, because the request still succeeds and just answers worse.

The four-breakpoint cap is enforced here rather than at the API, because
Anthropic's 400 does not say which marker was surplus and the client has no way
to find out.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/anthropic/content_test.go`:

```go
package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func blocks(t *testing.T, in []ir.ContentBlock, cb *cacheBudget) ([]map[string]any, []ir.Warning) {
	t.Helper()
	if cb == nil {
		cb = &cacheBudget{}
	}
	raw, warns := renderBlocks(in, cb)
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, warns
}

func hasWarning(warns []ir.Warning, field string) bool {
	for _, w := range warns {
		if w.Field == field {
			return true
		}
	}
	return false
}

func TestRenderBlocksPreservesThinkingVerbatim(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "step one", Signature: "sig-abc"}},
		{Type: ir.BlockText, Text: "42"},
	}, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v; nothing is lost round-tripping to Anthropic", warns)
	}
	if got[0]["type"] != "thinking" || got[0]["thinking"] != "step one" || got[0]["signature"] != "sig-abc" {
		t.Fatalf("thinking block = %v", got[0])
	}
	if got[1]["type"] != "text" {
		t.Errorf("order was not preserved: %v", got)
	}
}

func TestRenderBlocksEmitsRedactedThinking(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: "encrypted"}},
	}, nil)
	if got[0]["type"] != "redacted_thinking" || got[0]["data"] != "encrypted" {
		t.Errorf("block = %v", got[0])
	}
}

func TestRenderBlocksEmitsImageSources(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
		{Type: ir.BlockImage, Media: &ir.Media{URL: "https://x.example/a.png"}},
		{Type: ir.BlockImage, Media: &ir.Media{FileID: "file_1"}},
	}, nil)
	if s := got[0]["source"].(map[string]any); s["type"] != "base64" ||
		s["media_type"] != "image/png" || s["data"] != "AAAA" {
		t.Errorf("base64 source = %v", s)
	}
	if s := got[1]["source"].(map[string]any); s["type"] != "url" || s["url"] != "https://x.example/a.png" {
		t.Errorf("url source = %v", s)
	}
	if s := got[2]["source"].(map[string]any); s["type"] != "file" || s["file_id"] != "file_1" {
		t.Errorf("file source = %v", s)
	}
}

func TestRenderBlocksDropsAudioWithAWarning(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{
		{Type: ir.BlockText, Text: "listen"},
		{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/wav", Data: "UklGR"}},
	}, nil)
	if len(got) != 1 {
		t.Fatalf("blocks = %v; Anthropic has no audio content block", got)
	}
	if !hasWarning(warns, "messages[].audio") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderBlocksNestsToolResultContent(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{{
		Type: ir.BlockToolResult,
		ToolResult: &ir.ToolResult{
			ToolUseID: "call_a", IsError: true,
			Content: []ir.ContentBlock{
				{Type: ir.BlockText, Text: "boom"},
				{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
			},
		},
	}}, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v; Anthropic tool results carry images natively", warns)
	}
	if got[0]["type"] != "tool_result" || got[0]["tool_use_id"] != "call_a" || got[0]["is_error"] != true {
		t.Fatalf("tool_result = %v", got[0])
	}
	inner := got[0]["content"].([]any)
	if len(inner) != 2 || inner[1].(map[string]any)["type"] != "image" {
		t.Errorf("nested content = %v", inner)
	}
}

func TestRenderBlocksEmitsToolUseInputAsAnObject(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{{
		Type:    ir.BlockToolUse,
		ToolUse: &ir.ToolUse{ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)},
	}}, nil)
	in, ok := got[0]["input"].(map[string]any)
	if !ok {
		t.Fatalf("input = %#v; Anthropic takes an object, not a JSON string", got[0]["input"])
	}
	if in["x"].(float64) != 1 {
		t.Errorf("input = %v", in)
	}
}

func TestRenderBlocksEmitsToolUseEmptyInputAsAnEmptyObject(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{{
		Type:    ir.BlockToolUse,
		ToolUse: &ir.ToolUse{ID: "call_a", Name: "f"},
	}}, nil)
	if in, ok := got[0]["input"].(map[string]any); !ok || len(in) != 0 {
		t.Errorf("input = %#v, want {}", got[0]["input"])
	}
}

func TestRenderBlocksCarriesCacheControlWithTTL(t *testing.T) {
	got, warns := blocks(t, []ir.ContentBlock{{
		Type: ir.BlockText, Text: "long",
		CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
	}}, nil)
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	cc := got[0]["cache_control"].(map[string]any)
	if cc["type"] != "ephemeral" || cc["ttl"] != "1h" {
		t.Errorf("cache_control = %v; the TTL is a paid feature and must survive", cc)
	}
}

func TestRenderBlocksOmitsAnEmptyTTL(t *testing.T) {
	got, _ := blocks(t, []ir.ContentBlock{{
		Type: ir.BlockText, Text: "long",
		CacheControl: &ir.CacheControl{Type: "ephemeral"},
	}}, nil)
	cc := got[0]["cache_control"].(map[string]any)
	if _, ok := cc["ttl"]; ok {
		t.Errorf("cache_control = %v; an absent TTL means the default, not \"\"", cc)
	}
}

func TestRenderBlocksDropsTheFifthCacheBreakpoint(t *testing.T) {
	cb := &cacheBudget{}
	marked := func(text string) ir.ContentBlock {
		return ir.ContentBlock{
			Type: ir.BlockText, Text: text,
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "5m"},
		}
	}
	got, warns := blocks(t, []ir.ContentBlock{
		marked("a"), marked("b"), marked("c"), marked("d"), marked("e"),
	}, cb)
	if len(got) != 5 {
		t.Fatalf("blocks = %d; the content stays, only the marker is dropped", len(got))
	}
	for i := 0; i < 4; i++ {
		if _, ok := got[i]["cache_control"]; !ok {
			t.Errorf("block %d lost its marker", i)
		}
	}
	if _, ok := got[4]["cache_control"]; ok {
		t.Error("the fifth marker must be dropped; a fifth is a 400")
	}
	if !hasWarning(warns, "cache_control") {
		t.Errorf("warnings = %+v", warns)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the renderer**

Create `internal/adapter/anthropic/content.go`:

```go
// Package anthropic speaks the Anthropic Messages wire format to an upstream.
package anthropic

import (
	"encoding/json"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// targetName labels the warnings this kind produces.
const targetName = "anthropic"

// cacheBudget tracks Anthropic's four-breakpoint limit across a whole request.
// A fifth marker is a 400 whose message does not name the surplus one, so
// Darkrouter drops it here and says which rather than letting the upstream fail.
type cacheBudget struct{ used int }

func (c *cacheBudget) take() bool {
	if c.used >= xlate.MaxCacheBreakpoints {
		return false
	}
	c.used++
	return true
}

// renderBlocks converts IR content to Anthropic blocks.
//
// Thinking and redacted-thinking blocks pass through with their payloads and
// their order intact. Anthropic requires them returned byte-identical; a
// re-encoded signature silently loses the model's reasoning state on the turn
// after next, which is far harder to diagnose than an error would be.
func renderBlocks(blocks []ir.ContentBlock, cb *cacheBudget) ([]any, []ir.Warning) {
	var (
		out   []any
		warns []ir.Warning
	)
	for _, b := range blocks {
		rendered, w := renderBlock(b, cb)
		warns = append(warns, w...)
		if rendered == nil {
			continue
		}
		if b.CacheControl != nil {
			if cb.take() {
				rendered["cache_control"] = cacheControl(b.CacheControl)
			} else {
				warns = append(warns, ir.Warning{
					Field: "cache_control", Target: targetName,
					Reason: "more than four breakpoints; the surplus marker was dropped",
				})
			}
		}
		out = append(out, rendered)
	}
	return out, warns
}

func cacheControl(cc *ir.CacheControl) map[string]any {
	t := cc.Type
	if t == "" {
		t = "ephemeral"
	}
	m := map[string]any{"type": t}
	// An absent TTL means Anthropic's default. Sending "" is a validation error.
	if cc.TTL != "" {
		m["ttl"] = cc.TTL
	}
	return m
}

func renderBlock(b ir.ContentBlock, cb *cacheBudget) (map[string]any, []ir.Warning) {
	switch b.Type {
	case ir.BlockText:
		return map[string]any{"type": "text", "text": b.Text}, nil

	case ir.BlockThinking:
		if b.Thinking == nil {
			return nil, nil
		}
		m := map[string]any{"type": "thinking", "thinking": b.Thinking.Text}
		if b.Thinking.Signature != "" {
			m["signature"] = b.Thinking.Signature
		}
		return m, nil

	case ir.BlockRedactedThinking:
		if b.Thinking == nil {
			return nil, nil
		}
		return map[string]any{"type": "redacted_thinking", "data": b.Thinking.Data}, nil

	case ir.BlockImage:
		src, w := mediaSource(b.Media, "image")
		if src == nil {
			return nil, w
		}
		return map[string]any{"type": "image", "source": src}, w

	case ir.BlockDocument:
		src, w := mediaSource(b.Media, "document")
		if src == nil {
			return nil, w
		}
		return map[string]any{"type": "document", "source": src}, w

	case ir.BlockAudio:
		return nil, []ir.Warning{{
			Field: "messages[].audio", Target: targetName,
			Reason: "Anthropic has no audio content block",
		}}

	case ir.BlockToolUse:
		if b.ToolUse == nil {
			return nil, nil
		}
		// Anthropic takes the arguments as an object, unlike OpenAI's JSON
		// string. An empty input must still be {} or the call is rejected.
		input := b.ToolUse.Input
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		return map[string]any{
			"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": input,
		}, nil

	case ir.BlockToolResult:
		if b.ToolResult == nil {
			return nil, nil
		}
		inner, w := renderBlocks(b.ToolResult.Content, cb)
		m := map[string]any{
			"type": "tool_result", "tool_use_id": b.ToolResult.ToolUseID, "content": inner,
		}
		if b.ToolResult.IsError {
			m["is_error"] = true
		}
		return m, w

	default:
		return nil, []ir.Warning{{
			Field: "messages[]." + string(b.Type), Target: targetName,
			Reason: "unsupported content block",
		}}
	}
}

// mediaSource builds Anthropic's source object. The three source types are not
// interchangeable: a file id is a workspace handle, a URL is fetched by
// Anthropic, and base64 is inline.
func mediaSource(m *ir.Media, field string) (map[string]any, []ir.Warning) {
	if m == nil {
		return nil, nil
	}
	switch {
	case m.FileID != "":
		return map[string]any{"type": "file", "file_id": m.FileID}, nil
	case m.Data != "":
		return map[string]any{"type": "base64", "media_type": m.MIME, "data": m.Data}, nil
	case m.URL != "":
		return map[string]any{"type": "url", "url": m.URL}, nil
	}
	return nil, []ir.Warning{{
		Field: "messages[]." + field, Target: targetName,
		Reason: "carried neither data, a URL, nor a file id",
	}}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/ -v`
Expected: PASS.

`TestRenderBlocksEmitsToolUseInputAsAnObject` passes because `json.RawMessage`
marshals as embedded JSON rather than as a string, which is exactly the
difference between Anthropic's shape and OpenAI's.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/anthropic/
git commit -m "feat(anthropic): render content blocks"
```

---

### Task 14: Anthropic `BuildRequest`

**Files:**
- Create: `internal/adapter/anthropic/build.go`
- Test: `internal/adapter/anthropic/build_test.go`

**Interfaces:**
- Consumes: `renderBlocks`, `cacheBudget`, `targetName` from Task 13; `xlate.CollectSystemBlocks`, `xlate.NonSystemMessages`, `xlate.RequiredMaxTokens`, `xlate.EffortBudget` from Tasks 6 and 7.
- Produces: `anthropic.BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error)`; `const DefaultVersion = "2023-06-01"`; `modelTraits` with `traitsFor(model string) modelTraits`; `thinkingChoice` with `thinkingMode(req *ir.Request, traits modelTraits) thinkingChoice`; `adaptiveEffort(req *ir.Request) string`; `droppedPrefill(req *ir.Request) bool`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Seven API constraints decide whether this works against the real service, and
none of them produce an error a client can act on. All were checked against the
live documentation on 2026-08-22, because spec §4.6 and §4.7 explicitly asked for
that and two of the spec's assumptions had gone stale.

1. `max_tokens` is mandatory. Spec §4.7 substitutes 4096 and warns.
2. **Thinking splits by model generation, and the two modes are mutually
   exclusive.** `thinking: {type:"enabled", budget_tokens}` is deprecated on the
   4.6 generation and **returns a 400 on Claude 4.7 and later**;
   `thinking: {type:"adaptive"}` **returns a 400 on Claude 4.5 and earlier**,
   where manual is the only mode. Emitting one shape everywhere fails every
   reasoning request against half the fleet.
3. `budget_tokens` must be at least **1024** and **strictly less** than
   `max_tokens`. The clamp for the second can land under the first.
4. Sampling parameters split the same way. On Fable 5, Mythos 5, Mythos Preview,
   Opus 5, Opus 4.8, Opus 4.7, and Sonnet 5, a non-default `temperature`,
   `top_p`, or `top_k` is a 400 on **every** request, thinking or not. On older
   models the restriction applies only while thinking is on, and there
   `temperature` and `top_k` are rejected while **`top_p` is allowed between
   0.95 and 1** — the spec's "all three are rejected" was an over-generalization.
5. Forced tool use (`tool_choice` `any` or `tool`) is incompatible with manual
   thinking, though fine with adaptive.
6. A response prefill is rejected while thinking is on.
7. Messages must alternate roles. Two consecutive user turns — which the IR
   produces whenever a tool-result turn follows a user turn — is a 400.

**The generation is read off the model name**, because there is no catalog until
Phase 6. That is a heuristic and it says so: an unrecognized name gets the
permissive traits and a `model` warning, and the request is then shaped by what
the client asked for rather than by a guess. `internal/tokenize` picks a
vocabulary the same way, so the technique is already established here.

Structured output is **generally available** — no beta header, and the schema
lives under `output_config.format`. Spec §4.6 assumed a beta and told the
implementer to re-check; it was right to.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/anthropic/build_test.go`:

```go
package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// built renders against a manual-budget generation, which is what most of these
// cases are about. builtFor names a different one where the generation is the
// thing under test.
func built(t *testing.T, req *ir.Request) (*http_Request, map[string]any, []ir.Warning) {
	t.Helper()
	return builtFor(t, "claude-sonnet-4-5", req)
}

func builtFor(t *testing.T, model string, req *ir.Request) (*http_Request, map[string]any, []ir.Warning) {
	t.Helper()
	hr, warns, err := BuildRequest(context.Background(),
		&adapter.Target{BaseURL: "https://api.anthropic.com/v1", APIKey: "sk-ant", Model: model}, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return hr, body, warns
}

func userMsg(text string) ir.Message {
	return ir.Message{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: text}}}
}

func TestBuildRequestSetsURLAndHeaders(t *testing.T) {
	hr, _, _ := built(t, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if hr.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Errorf("url = %s", hr.URL)
	}
	if hr.Header.Get("x-api-key") != "sk-ant" {
		t.Errorf("x-api-key = %q", hr.Header.Get("x-api-key"))
	}
	if hr.Header.Get("anthropic-version") != DefaultVersion {
		t.Errorf("anthropic-version = %q", hr.Header.Get("anthropic-version"))
	}
	if hr.Header.Get("Authorization") != "" {
		t.Error("Anthropic authenticates with x-api-key; a bearer header confuses some proxies")
	}
}

func TestBuildRequestForwardsAnInboundVersion(t *testing.T) {
	hr, _, _ := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic_version": "2024-10-22"},
	})
	if hr.Header.Get("anthropic-version") != "2024-10-22" {
		t.Errorf("anthropic-version = %q; an inbound version is forwarded, not overridden",
			hr.Header.Get("anthropic-version"))
	}
}

func TestBuildRequestSubstitutesMaxTokens(t *testing.T) {
	_, body, warns := built(t, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if body["max_tokens"].(float64) != 4096 {
		t.Errorf("max_tokens = %v", body["max_tokens"])
	}
	if !hasWarning(warns, "max_tokens") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestMergesConsecutiveSameRoleTurns(t *testing.T) {
	_, body, _ := built(t, &ir.Request{Messages: []ir.Message{
		userMsg("first"),
		{Role: ir.RoleTool, Content: []ir.ContentBlock{{
			Type:       ir.BlockToolResult,
			ToolResult: &ir.ToolResult{ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}}},
		}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "ok"}}},
	}})
	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2; Anthropic rejects consecutive same-role turns", len(msgs))
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "user" || len(first["content"].([]any)) != 2 {
		t.Errorf("merged turn = %v", first)
	}
	if msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("second turn = %v", msgs[1])
	}
}

func TestBuildRequestKeepsSystemAsBlocksWithCacheControl(t *testing.T) {
	_, body, _ := built(t, &ir.Request{
		System: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "you are terse",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}},
		Messages: []ir.Message{userMsg("hi")},
	})
	sys := body["system"].([]any)
	block := sys[0].(map[string]any)
	if block["text"] != "you are terse" {
		t.Fatalf("system = %v", sys)
	}
	cc := block["cache_control"].(map[string]any)
	if cc["ttl"] != "1h" {
		t.Errorf("cache_control = %v; a cached system prompt is the point of the array form", cc)
	}
}

func TestBuildRequestClampsThinkingBelowMaxTokens(t *testing.T) {
	n := 2000
	_, body, warns := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		MaxTokens: &n,
		Reasoning: &ir.Reasoning{Budget: 30000},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "enabled" {
		t.Fatalf("thinking = %v", th)
	}
	if th["budget_tokens"].(float64) != 1999 {
		t.Errorf("budget_tokens = %v; Anthropic requires max_tokens > budget_tokens", th["budget_tokens"])
	}
	if !hasWarning(warns, "reasoning.budget") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestDisablesThinkingBelowTheMinimumBudget(t *testing.T) {
	n := 900
	_, body, warns := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		MaxTokens: &n,
		Reasoning: &ir.Reasoning{Budget: 30000},
	})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking = %v; the clamp landed under the 1024 floor, which is a 400", body["thinking"])
	}
	if !hasWarning(warns, "reasoning") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestUsesAdaptiveThinkingOnNewerModels(t *testing.T) {
	_, body, _ := builtFor(t, "claude-opus-4-7", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Budget: 16000},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "adaptive" {
		t.Fatalf("thinking = %v; type enabled is a 400 on Claude 4.7 and later", th)
	}
	if _, ok := th["budget_tokens"]; ok {
		t.Error("adaptive thinking takes no budget")
	}
	if body["output_config"].(map[string]any)["effort"] != "medium" {
		t.Errorf("output_config = %v; a budget bands back to an effort", body["output_config"])
	}
}

func TestBuildRequestUsesManualThinkingOnOlderModels(t *testing.T) {
	n := 32000
	_, body, _ := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		MaxTokens: &n,
		Reasoning: &ir.Reasoning{Effort: "medium"},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "enabled" {
		t.Fatalf("thinking = %v; type adaptive is a 400 on Claude 4.5 and earlier", th)
	}
	if th["budget_tokens"].(float64) != 16384 {
		t.Errorf("budget_tokens = %v; medium is 16384 by the fixed table", th["budget_tokens"])
	}
}

func TestBuildRequestRoundTripsAnInboundThinkingType(t *testing.T) {
	_, body, _ := builtFor(t, "claude-opus-4-7", &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic_thinking_type": "disabled"},
	})
	th := body["thinking"].(map[string]any)
	if th["type"] != "disabled" {
		t.Errorf("thinking = %v; an explicit off switch must survive", th)
	}
}

func TestBuildRequestOmitsThinkingWhenDisabledOnAnOlderModel(t *testing.T) {
	_, body, _ := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Metadata: map[string]string{"anthropic_thinking_type": "disabled"},
	})
	if _, ok := body["thinking"]; ok {
		t.Errorf("thinking = %v; older models have no disabled type, they just omit it", body["thinking"])
	}
}

func TestBuildRequestDropsSamplingWhenThinkingIsOn(t *testing.T) {
	temp, wide, narrow := 0.7, 0.5, 0.97
	k := 40
	_, body, warns := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages:    []ir.Message{userMsg("hi")},
		Temperature: &temp,
		TopK:        &k,
		TopP:        &wide,
		Reasoning:   &ir.Reasoning{Budget: 1024},
	})
	if _, ok := body["temperature"]; ok {
		t.Error("temperature is rejected alongside thinking")
	}
	if _, ok := body["top_k"]; ok {
		t.Error("top_k is rejected alongside thinking")
	}
	if _, ok := body["top_p"]; ok {
		t.Error("top_p below 0.95 is rejected alongside thinking")
	}
	if !hasWarning(warns, "temperature") || !hasWarning(warns, "top_k") || !hasWarning(warns, "top_p") {
		t.Errorf("warnings = %+v", warns)
	}

	_, body2, _ := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		TopP:      &narrow,
		Reasoning: &ir.Reasoning{Budget: 1024},
	})
	if body2["top_p"].(float64) != 0.97 {
		t.Errorf("top_p = %v; Anthropic accepts 0.95 to 1 with thinking on", body2["top_p"])
	}
}

func TestBuildRequestDropsAllSamplingOnTheNewestGeneration(t *testing.T) {
	temp, top := 0.7, 0.97
	k := 40
	_, body, warns := builtFor(t, "claude-opus-5", &ir.Request{
		Messages:    []ir.Message{userMsg("hi")},
		Temperature: &temp, TopP: &top, TopK: &k,
	})
	for _, f := range []string{"temperature", "top_p", "top_k"} {
		if _, ok := body[f]; ok {
			t.Errorf("%s survived; this generation rejects any non-default sampling value", f)
		}
		if !hasWarning(warns, f) {
			t.Errorf("warnings = %+v, missing %s", warns, f)
		}
	}
}

func TestBuildRequestDropsThinkingForAForcedToolChoice(t *testing.T) {
	_, body, warns := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages:   []ir.Message{userMsg("hi")},
		Tools:      []ir.Tool{{Name: "f", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: &ir.ToolChoice{Mode: "any"},
		Reasoning:  &ir.Reasoning{Budget: 2048},
	})
	if _, ok := body["thinking"]; ok {
		t.Error("manual thinking is incompatible with a forced tool choice")
	}
	if body["tool_choice"].(map[string]any)["type"] != "any" {
		t.Errorf("tool_choice = %v; the client's explicit instruction is the one that survives",
			body["tool_choice"])
	}
	if !hasWarning(warns, "reasoning") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestDropsAPrefillWhenThinkingIsOn(t *testing.T) {
	_, body, warns := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages: []ir.Message{
			userMsg("return JSON"),
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "{"}}},
		},
		Reasoning: &ir.Reasoning{Budget: 2048},
	})
	if len(body["messages"].([]any)) != 1 {
		t.Errorf("messages = %v; a prefill is rejected while thinking is on", body["messages"])
	}
	if !hasWarning(warns, "messages[last].assistant_prefill") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestKeepsAPrefillWithoutThinking(t *testing.T) {
	_, body, warns := builtFor(t, "claude-sonnet-4-5", &ir.Request{
		Messages: []ir.Message{
			userMsg("return JSON"),
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "{"}}},
		},
	})
	if len(body["messages"].([]any)) != 2 {
		t.Errorf("messages = %v; prefill is Anthropic's own idiom", body["messages"])
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestBuildRequestWarnsOnAnUnknownModelName(t *testing.T) {
	_, _, warns := builtFor(t, "some-proxy/mystery-model", &ir.Request{
		Messages:  []ir.Message{userMsg("hi")},
		Reasoning: &ir.Reasoning{Budget: 2048},
	})
	if !hasWarning(warns, "model") {
		t.Errorf("warnings = %+v; the generation was guessed and that must be visible", warns)
	}
}

func TestBuildRequestRendersToolsAndChoice(t *testing.T) {
	no := false
	_, body, _ := built(t, &ir.Request{
		Messages:          []ir.Message{userMsg("hi")},
		Tools:             []ir.Tool{{Name: "f", Description: "d", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice:        &ir.ToolChoice{Mode: "any"},
		ParallelToolCalls: &no,
	})
	tool := body["tools"].([]any)[0].(map[string]any)
	if tool["name"] != "f" {
		t.Fatalf("tool = %v", tool)
	}
	if _, ok := tool["input_schema"]; !ok {
		t.Errorf("tool = %v; Anthropic names the schema input_schema", tool)
	}
	tc := body["tool_choice"].(map[string]any)
	if tc["type"] != "any" {
		t.Errorf("tool_choice = %v; OpenAI required maps to Anthropic any", tc)
	}
	if tc["disable_parallel_tool_use"] != true {
		t.Errorf("tool_choice = %v; parallel_tool_calls inverts", tc)
	}
}

func TestBuildRequestEmitsStructuredOutputAndWarnsOnSafety(t *testing.T) {
	_, body, warns := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		ResponseFormat: &ir.ResponseFormat{
			Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`),
		},
		Safety: []ir.SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
	})
	format := body["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("output_config = %v; structured output is GA under output_config.format",
			body["output_config"])
	}
	if _, ok := format["schema"]; !ok {
		t.Errorf("format = %v", format)
	}
	if hasWarning(warns, "response_format") {
		t.Error("structured output is no longer dropped, so it must not warn")
	}
	if !hasWarning(warns, "safety") {
		t.Errorf("warnings = %+v", warns)
	}
}
```

Replace the placeholder type in the helper's signature with the real one:
`func built(t *testing.T, req *ir.Request) (*http.Request, map[string]any, []ir.Warning)`,
importing `net/http`. It is written as `*http_Request` above only to make the
substitution impossible to miss.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/ -run BuildRequest`
Expected: FAIL — `BuildRequest` is undefined.

- [ ] **Step 3: Write the builder**

Create `internal/adapter/anthropic/build.go`:

```go
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// DefaultVersion is the anthropic-version Darkrouter pins when the client did
// not send one. Anthropic requires the header on every request.
const DefaultVersion = "2023-06-01"

func BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	cb := &cacheBudget{}
	var warns []ir.Warning

	body := map[string]any{"model": t.Model}

	sysBlocks, w := xlate.CollectSystemBlocks(req, targetName)
	warns = append(warns, w...)
	if len(sysBlocks) > 0 {
		rendered, w := renderBlocks(sysBlocks, cb)
		warns = append(warns, w...)
		body["system"] = rendered
	}

	maxTok, w := xlate.RequiredMaxTokens(req, targetName)
	warns = append(warns, w...)
	body["max_tokens"] = maxTok

	outputConfig := map[string]any{}
	traits := traitsFor(t.Model)

	// Thinking splits by model generation, and the two modes are mutually
	// exclusive per generation: type "enabled" is a 400 on Claude 4.7 and
	// later, and type "adaptive" is a 400 on Claude 4.5 and earlier. Getting
	// this wrong makes every reasoning request against that generation fail.
	thinking, manual := false, false
	mode := thinkingMode(req, traits)
	if mode != modeNone && !traits.known {
		// Warned here rather than unconditionally: an unrecognized name only
		// costs something once the guess decides a wire shape, and warning on
		// every request to a self-hosted Anthropic-compatible endpoint would be
		// noise that trains people to ignore warnings.
		warns = append(warns, ir.Warning{
			Field: "model", Target: targetName,
			Reason: "unrecognized Anthropic model name; the thinking mode was guessed from the request",
		})
	}
	switch mode {
	case modeAdaptive:
		body["thinking"] = map[string]any{"type": "adaptive"}
		if e := adaptiveEffort(req); e != "" {
			outputConfig["effort"] = e
		}
		thinking = true

	case modeManual:
		budget := req.Reasoning.Budget
		if budget == 0 {
			budget = xlate.EffortBudget(req.Reasoning.Effort, 0)
		}
		// max_tokens must be strictly greater than the budget. Clamping keeps a
		// servable request servable; raising max_tokens instead would silently
		// multiply the bill on the one control the client actually set.
		if budget >= maxTok {
			budget = maxTok - 1
			warns = append(warns, ir.Warning{
				Field: "reasoning.budget", Target: targetName,
				Reason: "clamped below max_tokens, which Anthropic requires to be larger",
			})
		}
		// The clamp can land under Anthropic's 1024 floor, which is a
		// guaranteed 400. Below it there is no budget worth asking for.
		if budget < 1024 {
			warns = append(warns, ir.Warning{
				Field: "reasoning", Target: targetName,
				Reason: "budget below Anthropic's 1024-token minimum; thinking disabled",
			})
			break
		}
		// Forced tool use is incompatible with manual thinking, though not with
		// adaptive. The forced tool is the client's explicit instruction and an
		// agentic loop depends on it; the reasoning depth is the softer ask.
		if req.ToolChoice != nil && (req.ToolChoice.Mode == "any" || req.ToolChoice.Mode == "tool") {
			warns = append(warns, ir.Warning{
				Field: "reasoning", Target: targetName,
				Reason: "manual thinking is incompatible with a forced tool choice; thinking disabled",
			})
			break
		}
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		thinking, manual = true, true

	case modeDisabled:
		// Only adaptive-capable models have an explicit off switch; on older
		// ones, omitting the field is what "no thinking" means.
		if traits.adaptive {
			body["thinking"] = map[string]any{"type": "disabled"}
		}
	}

	// Sampling parameters. On the newest generation any non-default value is a
	// 400 on every request, thinking or not. On older models the restriction
	// applies only while thinking is on, and top_p survives inside a narrow band.
	drop := func(field, reason string) {
		warns = append(warns, ir.Warning{Field: field, Target: targetName, Reason: reason})
	}
	const sealed = "this model rejects any non-default sampling parameter"
	const withThinking = "rejected by Anthropic alongside thinking"
	if req.Temperature != nil {
		switch {
		case !traits.freeSampling:
			drop("temperature", sealed)
		case thinking:
			drop("temperature", withThinking)
		default:
			body["temperature"] = *req.Temperature
		}
	}
	if req.TopP != nil {
		switch {
		case !traits.freeSampling:
			drop("top_p", sealed)
		case thinking && (*req.TopP < 0.95 || *req.TopP > 1):
			drop("top_p", "with thinking on, Anthropic accepts top_p only between 0.95 and 1")
		default:
			body["top_p"] = *req.TopP
		}
	}
	if req.TopK != nil {
		switch {
		case !traits.freeSampling:
			drop("top_k", sealed)
		case thinking:
			drop("top_k", withThinking)
		default:
			body["top_k"] = *req.TopK
		}
	}

	if len(req.StopSequences) > 0 {
		body["stop_sequences"] = req.StopSequences
	}
	if req.Stream {
		body["stream"] = true
	}
	if len(req.Tools) > 0 {
		body["tools"] = renderTools(req.Tools)
	}
	if tc := renderToolChoice(req.ToolChoice, req.ParallelToolCalls); tc != nil {
		body["tool_choice"] = tc
	}
	// Structured output is generally available: no beta header, and the schema
	// lives under output_config.format.
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		outputConfig["format"] = map[string]any{
			"type": "json_schema", "schema": req.ResponseFormat.Schema,
		}
	}
	if len(outputConfig) > 0 {
		body["output_config"] = outputConfig
	}
	if len(req.Safety) > 0 {
		warns = append(warns, ir.Warning{
			Field: "safety", Target: targetName, Reason: "safety settings are Gemini-only",
		})
	}
	if uid := req.Metadata["user_id"]; uid != "" {
		body["metadata"] = map[string]any{"user_id": uid}
	}

	// Rendered last, because the assistant-prefill rule depends on whether
	// thinking ended up enabled: a prefill is rejected while it is.
	msgs, w := renderMessages(req, cb, thinking)
	warns = append(warns, w...)
	if thinking && manual && droppedPrefill(req) {
		warns = append(warns, ir.Warning{
			Field: "messages[last].assistant_prefill", Target: targetName,
			Reason: "response prefill is rejected while thinking is on; the turn was dropped",
		})
	}
	body["messages"] = msgs

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/messages"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("x-api-key", t.APIKey)
	}
	version := DefaultVersion
	if v := req.Metadata["anthropic_version"]; v != "" {
		version = v
	}
	hr.Header.Set("anthropic-version", version)
	return hr, warns, nil
}

// renderMessages merges consecutive same-role turns. Anthropic requires
// alternating roles, and the IR routinely produces two user turns in a row —
// a tool-result turn follows a user turn in every agentic loop.
//
// A trailing text-only assistant turn is Anthropic's prefill idiom and is
// normally preserved. It is dropped when thinking is on, which Anthropic
// rejects outright.
func renderMessages(req *ir.Request, cb *cacheBudget, thinking bool) ([]any, []ir.Warning) {
	var (
		out     []any
		warns   []ir.Warning
		curRole string
		content []any
	)
	flush := func() {
		if curRole == "" {
			return
		}
		out = append(out, map[string]any{"role": curRole, "content": content})
		curRole, content = "", nil
	}

	msgs := xlate.NonSystemMessages(req.Messages)
	if thinking && droppedPrefill(req) {
		msgs = msgs[:len(msgs)-1]
	}
	for _, m := range msgs {
		role := "user"
		if m.Role == ir.RoleAssistant {
			role = "assistant"
		}
		blocks, w := renderBlocks(m.Content, cb)
		warns = append(warns, w...)
		if len(blocks) == 0 {
			continue
		}
		if role != curRole {
			flush()
			curRole = role
		}
		content = append(content, blocks...)
	}
	flush()
	return out, warns
}

// droppedPrefill reports whether the conversation ends in the prefill idiom —
// a trailing assistant turn holding only text. Anthropic rejects a prefill
// while thinking is on, so that combination has to give one of them up.
func droppedPrefill(req *ir.Request) bool {
	msgs := xlate.NonSystemMessages(req.Messages)
	if len(msgs) == 0 {
		return false
	}
	last := msgs[len(msgs)-1]
	if last.Role != ir.RoleAssistant || len(last.Content) == 0 {
		return false
	}
	for _, b := range last.Content {
		if b.Type != ir.BlockText {
			return false
		}
	}
	return true
}

// modelTraits records what one Anthropic model generation accepts.
type modelTraits struct {
	adaptive     bool // accepts thinking: {"type": "adaptive"}
	manualBudget bool // accepts thinking: {"type": "enabled", budget_tokens}
	freeSampling bool // accepts non-default temperature, top_p, or top_k
	known        bool
}

// generations maps a model-name fragment to its traits, most specific first.
//
// Reading the generation off the name is a heuristic, and it is here because
// there is no catalog until Phase 6 — the same technique internal/tokenize uses
// to pick a vocabulary. It is checked longest-first because "opus-4-5" and
// "opus-4" would otherwise both match the same name.
var generations = []struct {
	fragment string
	traits   modelTraits
}{
	{"mythos-preview", modelTraits{adaptive: true, manualBudget: true, known: true}},
	{"opus-4-7", modelTraits{adaptive: true, known: true}},
	{"opus-4-8", modelTraits{adaptive: true, known: true}},
	{"opus-5", modelTraits{adaptive: true, known: true}},
	{"sonnet-5", modelTraits{adaptive: true, known: true}},
	{"fable-5", modelTraits{adaptive: true, known: true}},
	{"mythos-5", modelTraits{adaptive: true, known: true}},
	{"opus-4-6", modelTraits{adaptive: true, manualBudget: true, freeSampling: true, known: true}},
	{"sonnet-4-6", modelTraits{adaptive: true, manualBudget: true, freeSampling: true, known: true}},
	{"opus-4-5", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"sonnet-4-5", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"haiku-4-5", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"opus-4-1", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"opus-4", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"sonnet-4", modelTraits{manualBudget: true, freeSampling: true, known: true}},
	{"claude-3", modelTraits{manualBudget: true, freeSampling: true, known: true}},
}

// traitsFor reads a model name. Dots become dashes first, because proxies spell
// the same model "claude-sonnet-4.5" where Anthropic spells it
// "claude-sonnet-4-5-20250929". An unrecognized name gets the permissive set,
// so the request is shaped by what the client asked for rather than by a guess.
func traitsFor(model string) modelTraits {
	name := strings.ReplaceAll(strings.ToLower(model), ".", "-")
	for _, g := range generations {
		if strings.Contains(name, g.fragment) {
			return g.traits
		}
	}
	return modelTraits{adaptive: true, manualBudget: true, freeSampling: true}
}

type thinkingChoice int

const (
	modeNone thinkingChoice = iota
	modeAdaptive
	modeManual
	modeDisabled
)

// thinkingMode picks the wire shape. The client's own spelling decides what it
// wants; the model's traits decide what it can have, and the conversion runs
// through xlate so a request reasons the same depth either way.
func thinkingMode(req *ir.Request, traits modelTraits) thinkingChoice {
	inbound := req.Metadata["anthropic_thinking_type"]
	if inbound == "disabled" {
		return modeDisabled
	}
	if req.Reasoning == nil && inbound == "" {
		return modeNone
	}
	// An explicit token budget is evidence the client targets a budget-taking
	// model; effort or an adaptive config is evidence of the opposite. Where the
	// target cannot honor the client's shape, convert rather than fail.
	wantsManual := inbound == "enabled" || (req.Reasoning != nil && req.Reasoning.Budget > 0)
	if wantsManual && traits.manualBudget && req.Reasoning != nil {
		return modeManual
	}
	if traits.adaptive {
		return modeAdaptive
	}
	// Manual mode needs a budget to state. A client that asked for thinking
	// without one, against a model with no adaptive mode, gets none.
	if traits.manualBudget && req.Reasoning != nil {
		return modeManual
	}
	return modeNone
}

// adaptiveEffort is the depth control adaptive thinking takes in place of a
// budget. A client that sent a budget has it banded back to an effort here.
func adaptiveEffort(req *ir.Request) string {
	if req.Reasoning == nil {
		return ""
	}
	if req.Reasoning.Effort != "" {
		return strings.ToLower(req.Reasoning.Effort)
	}
	return xlate.BudgetEffort(req.Reasoning.Budget)
}

func renderTools(tools []ir.Tool) []any {
	out := make([]any, 0, len(tools))
	for _, t := range tools {
		schema := t.Schema
		// A tool with no schema still needs one: Anthropic rejects a null
		// input_schema outright.
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name": t.Name, "description": t.Description, "input_schema": schema,
		})
	}
	return out
}

// renderToolChoice maps the IR's four modes. disable_parallel_tool_use lives
// inside tool_choice rather than at the top level, so a request that only sets
// parallel_tool_calls still needs a choice object to hang it on.
func renderToolChoice(tc *ir.ToolChoice, parallel *bool) map[string]any {
	if tc == nil && parallel == nil {
		return nil
	}
	m := map[string]any{"type": "auto"}
	if tc != nil {
		switch tc.Mode {
		case "none":
			m = map[string]any{"type": "none"}
		case "any":
			m = map[string]any{"type": "any"}
		case "tool":
			m = map[string]any{"type": "tool", "name": tc.Name}
		}
	}
	if parallel != nil && !*parallel {
		m["disable_parallel_tool_use"] = true
	}
	return m
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/ -v`
Expected: PASS.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/anthropic/
git commit -m "feat(anthropic): build messages requests"
```

---

### Task 15: One status ladder for every adapter

**Files:**
- Create: `internal/adapter/classify.go`
- Modify: `internal/adapter/openaicompat/classify.go` (`Classify` delegates)
- Test: `internal/adapter/classify_test.go`

**Interfaces:**
- Consumes: `adapter.Outcome` from Phase 1.
- Produces: `adapter.ClassifyStatus(resp *http.Response, err error) Outcome`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Master design §8.1 is authoritative for outcome classification and says nothing
dialect-specific: the ladder is about HTTP, not about payload shape. Three
adapters each carrying their own copy is three places for the default buckets to
drift, and the default buckets are the part that matters — an unlisted transport
error is `RetryableProvider`, an unlisted 5xx is `RetryableProvider`, any other
unlisted 4xx is `Fatal`.

Anthropic's `529 overloaded_error` and Gemini's `503 UNAVAILABLE` both fall out
of the ≥500 rule without either adapter naming them.

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/classify_test.go`:

```go
package adapter

import (
	"errors"
	"net/http"
	"testing"
)

func TestClassifyStatusBuckets(t *testing.T) {
	cases := []struct {
		code int
		want Outcome
	}{
		{200, OutcomeSuccess},
		{204, OutcomeSuccess},
		{302, OutcomeRetryableProvider},
		{400, OutcomeFatal},
		{401, OutcomeRetryableCredential},
		{402, OutcomeRetryableCredential},
		{403, OutcomeRetryableCredential},
		{404, OutcomeRetryableModel},
		{408, OutcomeRetryableProvider},
		{413, OutcomeFatal},
		{422, OutcomeFatal},
		{429, OutcomeRetryableProvider},
		{500, OutcomeRetryableProvider},
		{503, OutcomeRetryableProvider},
		{529, OutcomeRetryableProvider},
	}
	for _, tc := range cases {
		got := ClassifyStatus(&http.Response{StatusCode: tc.code}, nil)
		if got != tc.want {
			t.Errorf("ClassifyStatus(%d) = %q, want %q", tc.code, got, tc.want)
		}
	}
}

func TestClassifyStatusTreatsTransportErrorsAsRetryable(t *testing.T) {
	if got := ClassifyStatus(nil, errors.New("dial tcp: no such host")); got != OutcomeRetryableProvider {
		t.Errorf("transport error = %q", got)
	}
	if got := ClassifyStatus(nil, nil); got != OutcomeRetryableProvider {
		t.Errorf("no response and no error = %q; there is nothing to succeed with", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/ -run ClassifyStatus`
Expected: FAIL — `ClassifyStatus` is undefined.

- [ ] **Step 3: Write the ladder**

Create `internal/adapter/classify.go`, moving the body of
`openaicompat.Classify` verbatim including its comments:

```go
package adapter

import "net/http"

// ClassifyStatus buckets an upstream result by its status line. Master design
// §8.1 is authoritative, and the default buckets matter as much as the listed
// codes: without them, DNS failures and TLS errors get classified differently
// by different adapters.
//
// It is dialect-independent on purpose. Anthropic's 529 and Gemini's 503 both
// land on the ≥500 rule without either adapter naming them.
func ClassifyStatus(resp *http.Response, err error) Outcome {
	if err != nil || resp == nil {
		return OutcomeRetryableProvider
	}
	switch code := resp.StatusCode; {
	case code >= 200 && code < 300:
		return OutcomeSuccess
	case code == 401, code == 402, code == 403:
		return OutcomeRetryableCredential
	case code == 404:
		return OutcomeRetryableModel
	case code == 408, code == 429:
		return OutcomeRetryableProvider
	case code >= 300 && code < 400:
		// Redirects are never followed: Go's client converts a redirected POST
		// into a body-less GET.
		return OutcomeRetryableProvider
	case code >= 500:
		return OutcomeRetryableProvider
	default:
		return OutcomeFatal
	}
}
```

- [ ] **Step 4: Delegate from openaicompat**

Replace the body of `Classify` in `internal/adapter/openaicompat/classify.go`:

```go
// Classify buckets an upstream result. The ladder is shared, because it is
// about HTTP rather than about payload shape; ClassifyBody below is the part
// that is genuinely OpenAI-specific.
func Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}
```

`internal/adapter/openaicompat/classify_test.go` exercises the same table and
must keep passing unchanged — if a case fails, the ladder was transcribed wrong.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/
git commit -m "refactor(adapter): share the status ladder"
```

---

### Task 16: Anthropic `ParseResponse` and `Classify`

**Files:**
- Create: `internal/adapter/anthropic/parse.go`
- Test: `internal/adapter/anthropic/parse_test.go`

**Interfaces:**
- Consumes: `adapter.ClassifyStatus` from Task 15; `targetName` from Task 13.
- Produces: `anthropic.ParseResponse(resp *http.Response) (*ir.Response, error)`; `anthropic.Classify(resp *http.Response, err error) adapter.Outcome`; `anthropic.stopReason(s string) (ir.StopReason, bool)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

Spec §4.5 and review finding F6: Anthropic's stop-reason set is larger than an
OpenAI-shaped adapter expects. `refusal` is its content filter, `pause_turn`
signals a server-tool loop, and `model_context_window_exceeded` is a second
spelling of hitting the cap. An earlier draft of the spec left the
content-filter cell empty.

An unmapped value becomes `end_turn` **with a warning** rather than an error,
because Anthropic ships new ones without notice and a gateway that 500s on a new
enum value is worse than one that reports a slightly wrong reason.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/anthropic/parse_test.go`:

```go
package anthropic

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func parseBody(t *testing.T, body string) *ir.Response {
	t.Helper()
	resp := &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}
	out, err := ParseResponse(resp)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseResponseReadsBlocksAndUsage(t *testing.T) {
	got := parseBody(t, `{"id":"msg_1","model":"claude-x","stop_reason":"tool_use",
		"content":[
			{"type":"thinking","thinking":"hmm","signature":"sig-1"},
			{"type":"text","text":"calling"},
			{"type":"tool_use","id":"call_a","name":"f","input":{"x":1}}],
		"usage":{"input_tokens":10,"output_tokens":4,
			"cache_creation_input_tokens":7,"cache_read_input_tokens":3}}`)

	if len(got.Content) != 3 {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Thinking == nil || got.Content[0].Thinking.Signature != "sig-1" {
		t.Errorf("thinking = %+v; the signature must survive", got.Content[0].Thinking)
	}
	if got.Content[2].ToolUse == nil || string(got.Content[2].ToolUse.Input) != `{"x":1}` {
		t.Errorf("tool_use = %+v", got.Content[2].ToolUse)
	}
	if got.StopReason != ir.StopToolUse {
		t.Errorf("stop_reason = %q", got.StopReason)
	}
	if got.Usage.CacheWriteTokens != 7 || got.Usage.CacheReadTokens != 3 {
		t.Errorf("usage = %+v; creation is a write and read is a read", got.Usage)
	}
}

func TestParseResponseReadsRedactedThinking(t *testing.T) {
	got := parseBody(t, `{"id":"m","model":"c","stop_reason":"end_turn",
		"content":[{"type":"redacted_thinking","data":"enc"}],"usage":{}}`)
	if got.Content[0].Type != ir.BlockRedactedThinking || got.Content[0].Thinking.Data != "enc" {
		t.Errorf("block = %+v", got.Content[0])
	}
}

func TestStopReasonTable(t *testing.T) {
	cases := []struct {
		in   string
		want ir.StopReason
		ok   bool
	}{
		{"end_turn", ir.StopEndTurn, true},
		{"max_tokens", ir.StopMaxTokens, true},
		{"model_context_window_exceeded", ir.StopMaxTokens, true},
		{"stop_sequence", ir.StopStopSequence, true},
		{"tool_use", ir.StopToolUse, true},
		{"refusal", ir.StopContentFilter, true},
		{"pause_turn", ir.StopPauseTurn, true},
		{"", ir.StopEndTurn, true},
		{"something_new", ir.StopEndTurn, false},
	}
	for _, tc := range cases {
		got, ok := stopReason(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("stopReason(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseResponseWarnsOnAnUnknownStopReason(t *testing.T) {
	got := parseBody(t, `{"id":"m","model":"c","stop_reason":"something_new",
		"content":[{"type":"text","text":"hi"}],"usage":{}}`)
	if got.StopReason != ir.StopEndTurn {
		t.Errorf("stop_reason = %q; an unknown value degrades rather than failing", got.StopReason)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Field != "stop_reason" {
		t.Errorf("warnings = %+v", got.Warnings)
	}
}

func TestClassifyUsesTheSharedLadder(t *testing.T) {
	if got := Classify(&http.Response{StatusCode: 529}, nil); got != adapter.OutcomeRetryableProvider {
		t.Errorf("529 = %q; overloaded_error is retryable", got)
	}
	if got := Classify(&http.Response{StatusCode: 404}, nil); got != adapter.OutcomeRetryableModel {
		t.Errorf("404 = %q", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/ -run 'ParseResponse|StopReason|Classify'`
Expected: FAIL — undefined.

- [ ] **Step 3: Write the parser**

Create `internal/adapter/anthropic/parse.go`:

```go
package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

func (u *wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheWriteTokens: u.CacheCreationInputTokens,
		CacheReadTokens:  u.CacheReadInputTokens,
	}
}

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	Data      string          `json:"data"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

// stopReason maps Anthropic's set. The bool reports whether the value was
// recognized; an unknown one degrades to end_turn and is warned about rather
// than failing the response, because Anthropic adds values without notice.
func stopReason(s string) (ir.StopReason, bool) {
	switch s {
	case "", "end_turn":
		return ir.StopEndTurn, true
	case "max_tokens", "model_context_window_exceeded":
		return ir.StopMaxTokens, true
	case "stop_sequence":
		return ir.StopStopSequence, true
	case "tool_use":
		return ir.StopToolUse, true
	case "refusal":
		return ir.StopContentFilter, true
	case "pause_turn":
		return ir.StopPauseTurn, true
	default:
		return ir.StopEndTurn, false
	}
}

func blockToIR(b wireBlock) (ir.ContentBlock, bool) {
	switch b.Type {
	case "text":
		return ir.ContentBlock{Type: ir.BlockText, Text: b.Text}, true
	case "thinking":
		return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
			Text: b.Thinking, Signature: b.Signature,
		}}, true
	case "redacted_thinking":
		return ir.ContentBlock{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: b.Data}}, true
	case "tool_use":
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: b.ID, Name: b.Name, Input: b.Input,
		}}, true
	default:
		return ir.ContentBlock{}, false
	}
}

func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer resp.Body.Close()
	var w struct {
		ID         string      `json:"id"`
		Model      string      `json:"model"`
		Content    []wireBlock `json:"content"`
		StopReason string      `json:"stop_reason"`
		Usage      wireUsage   `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, err
	}

	out := &ir.Response{ID: w.ID, Model: w.Model, Usage: w.Usage.toIR()}
	sr, known := stopReason(w.StopReason)
	out.StopReason = sr
	if !known {
		out.Warnings = append(out.Warnings, ir.Warning{
			Field: "stop_reason", Target: targetName,
			Reason: "unrecognized value " + w.StopReason + "; reported as end_turn",
		})
	}
	for _, b := range w.Content {
		blk, ok := blockToIR(b)
		if !ok {
			out.Warnings = append(out.Warnings, ir.Warning{
				Field: "content[]." + b.Type, Target: targetName,
				Reason: "unrecognized content block",
			})
			continue
		}
		out.Content = append(out.Content, blk)
	}
	return out, nil
}

func Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/ -v`
Expected: PASS.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/anthropic/
git commit -m "feat(anthropic): parse responses and stop reasons"
```

---

### Task 17: Anthropic `ParseStream` and the adapter type

**Files:**
- Create: `internal/adapter/anthropic/stream.go`, `internal/adapter/anthropic/adapter.go`
- Test: `internal/adapter/anthropic/stream_test.go`

**Interfaces:**
- Consumes: `sse.NewReader` from Phase 1; `stopReason`, `wireUsage` from Task 16.
- Produces: `anthropic.ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]`; `anthropic.New() *Adapter` satisfying `adapter.Adapter`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Anthropic's event model **is** the IR's, so this mapping is close to identity —
which makes the two places it is not identity the whole risk.

The first is review finding F13: **usage arrives in two events.** `message_start`
carries `input_tokens` and the cache counters; `message_delta` carries
`output_tokens` and nothing else. `exec.applyUsage` assigns rather than
accumulates, so yielding `message_delta`'s usage verbatim would overwrite the
input count with zero and every Anthropic request would be logged as having cost
nothing to prompt. The adapter accumulates and yields the merged total.

The second is that unknown event types must be **ignored, not errors**.
Anthropic explicitly tells clients new ones will appear.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/anthropic/stream_test.go`:

```go
package anthropic

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func collect(t *testing.T, body string) ([]ir.StreamEvent, error) {
	t.Helper()
	var (
		evs  []ir.StreamEvent
		last error
	)
	for ev, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			last = err
			break
		}
		evs = append(evs, ev)
	}
	return evs, last
}

func sseEvent(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }

func TestParseStreamMapsTheEventModel(t *testing.T) {
	body := sseEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","model":"claude-x","usage":{"input_tokens":10,"cache_read_input_tokens":3}}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`) +
		sseEvent("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`) +
		sseEvent("message_stop", `{"type":"message_stop"}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	want := []ir.EventType{
		ir.EventMessageStart, ir.EventMessageDelta, ir.EventBlockStart,
		ir.EventContentDelta, ir.EventBlockStop, ir.EventMessageDelta, ir.EventMessageStop,
	}
	if len(evs) != len(want) {
		t.Fatalf("events = %d, want %d: %+v", len(evs), len(want), evs)
	}
	for i, w := range want {
		if evs[i].Type != w {
			t.Fatalf("event %d = %q, want %q", i, evs[i].Type, w)
		}
	}
	if evs[0].ID != "msg_1" || evs[0].Model != "claude-x" {
		t.Errorf("message_start = %+v", evs[0])
	}
	if evs[len(evs)-1].StopReason != ir.StopEndTurn {
		t.Errorf("message_stop = %+v", evs[len(evs)-1])
	}
}

func TestParseStreamAccumulatesUsageAcrossBothEvents(t *testing.T) {
	body := sseEvent("message_start", `{"type":"message_start","message":{"id":"m","model":"c","usage":{"input_tokens":10,"cache_read_input_tokens":3,"cache_creation_input_tokens":7}}}`) +
		sseEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Usage == nil {
		t.Fatal("message_delta carried no usage")
	}
	if last.Usage.InputTokens != 10 || last.Usage.OutputTokens != 4 {
		t.Errorf("usage = %+v; input arrives in message_start and must not be overwritten", last.Usage)
	}
	if last.Usage.CacheReadTokens != 3 || last.Usage.CacheWriteTokens != 7 {
		t.Errorf("usage = %+v", last.Usage)
	}
}

func TestParseStreamReadsToolAndThinkingDeltas(t *testing.T) {
	body := sseEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weighing"}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`) +
		sseEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call_a","name":"f","input":{}}}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"x\":1}"}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Delta.Type != ir.BlockThinking {
		t.Errorf("block start = %+v", evs[0].Delta)
	}
	if evs[1].Delta.Thinking != "weighing" {
		t.Errorf("thinking delta = %+v", evs[1].Delta)
	}
	if evs[2].Delta.Signature != "sig-1" || evs[2].Delta.Thinking != "" {
		t.Errorf("signature delta = %+v; a signature is not thinking text", evs[2].Delta)
	}
	if evs[3].Delta.ToolID != "call_a" || evs[3].Delta.ToolName != "f" {
		t.Errorf("tool block start = %+v", evs[3].Delta)
	}
	if evs[4].Delta.ToolInput != `{"x":1}` || evs[4].Index != 1 {
		t.Errorf("tool delta = %+v", evs[4])
	}
}

func TestParseStreamIgnoresUnknownEventTypes(t *testing.T) {
	body := sseEvent("ping", `{"type":"ping"}`) +
		sseEvent("future_event", `{"type":"future_event","whatever":1}`) +
		sseEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatalf("an unknown event type must not fail the stream: %v", err)
	}
	if len(evs) != 2 || evs[0].Type != ir.EventPing || evs[1].Type != ir.EventContentDelta {
		t.Errorf("events = %+v", evs)
	}
}

func TestParseStreamYieldsAnInStreamError(t *testing.T) {
	body := sseEvent("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`)
	_, err := collect(t, body)
	var e *ir.Error
	if !errorsAs(err, &e) {
		t.Fatalf("err = %v, want *ir.Error", err)
	}
	if e.Type != ir.ErrOverloaded || e.Message != "Overloaded" {
		t.Errorf("error = %+v; Anthropic sends overloaded_error under a 200", e)
	}
}
```

Add `errorsAs` as a one-line wrapper in the test file so the import list stays
short: `func errorsAs(err error, t **ir.Error) bool { return errors.As(err, t) }`,
importing `errors`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/ -run ParseStream`
Expected: FAIL — undefined.

- [ ] **Step 3: Write the stream parser**

Create `internal/adapter/anthropic/stream.go`:

```go
package anthropic

import (
	"encoding/json"
	"errors"
	"io"
	"iter"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

type wireStreamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		ID    string    `json:"id"`
		Model string    `json:"model"`
		Usage wireUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *wireBlock `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *wireUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// blockKind maps a content_block's type onto the IR's. An unrecognized kind
// becomes text so its deltas still reach the client rather than vanishing.
func blockKind(t string) ir.BlockType {
	switch t {
	case "thinking":
		return ir.BlockThinking
	case "redacted_thinking":
		return ir.BlockRedactedThinking
	case "tool_use":
		return ir.BlockToolUse
	default:
		return ir.BlockText
	}
}

// errorType maps Anthropic's error taxonomy. It matters most for
// overloaded_error, which arrives in-stream under a 200 and would otherwise be
// reported as a generic API failure the exec loop cannot reason about.
func errorType(t string) ir.ErrorType {
	switch t {
	case "invalid_request_error":
		return ir.ErrInvalidRequest
	case "authentication_error":
		return ir.ErrAuthentication
	case "permission_error":
		return ir.ErrPermission
	case "not_found_error":
		return ir.ErrNotFound
	case "rate_limit_error":
		return ir.ErrRateLimit
	case "overloaded_error":
		return ir.ErrOverloaded
	default:
		return ir.ErrAPI
	}
}

// ParseStream converts Anthropic's SSE events to the IR's. The event model is
// the same, so the interesting parts are the two that are not: usage arrives
// split across message_start and message_delta and has to be accumulated, and
// unknown event types are ignored rather than treated as failures.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		reader := sse.NewReader(r, maxLine)
		var (
			usage ir.Usage
			stop  = ir.StopEndTurn
		)
		for {
			raw, err := reader.Next()
			if errors.Is(err, io.EOF) {
				return
			}
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			if raw.Data == "" {
				continue
			}
			var ev wireStreamEvent
			if json.Unmarshal([]byte(raw.Data), &ev) != nil {
				continue // an unparseable event is not a reason to kill the stream
			}

			switch ev.Type {
			case "message_start":
				if ev.Message == nil {
					continue
				}
				usage = ev.Message.Usage.toIR()
				if !yield(ir.StreamEvent{
					Type: ir.EventMessageStart, ID: ev.Message.ID, Model: ev.Message.Model,
				}, nil) {
					return
				}
				u := usage
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &u}, nil) {
					return
				}

			case "content_block_start":
				d := &ir.Delta{Type: ir.BlockText}
				if ev.ContentBlock != nil {
					d.Type = blockKind(ev.ContentBlock.Type)
					d.ToolID = ev.ContentBlock.ID
					d.ToolName = ev.ContentBlock.Name
				}
				if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: ev.Index, Delta: d}, nil) {
					return
				}

			case "content_block_delta":
				if ev.Delta == nil {
					continue
				}
				d := &ir.Delta{}
				switch ev.Delta.Type {
				case "text_delta":
					d.Type, d.Text = ir.BlockText, ev.Delta.Text
				case "thinking_delta":
					d.Type, d.Thinking = ir.BlockThinking, ev.Delta.Thinking
				case "signature_delta":
					// Carries no text: a signature must never look like content,
					// or an empty thinking block would commit the response.
					d.Type, d.Signature = ir.BlockThinking, ev.Delta.Signature
				case "input_json_delta":
					d.Type, d.ToolInput = ir.BlockToolUse, ev.Delta.PartialJSON
				default:
					continue
				}
				if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: ev.Index, Delta: d}, nil) {
					return
				}

			case "content_block_stop":
				if !yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: ev.Index}, nil) {
					return
				}

			case "message_delta":
				if ev.Delta != nil && ev.Delta.StopReason != "" {
					stop, _ = stopReason(ev.Delta.StopReason)
				}
				if ev.Usage != nil {
					// Merged, not replaced: message_delta reports output tokens
					// only, and assigning it would erase the input count.
					u := ev.Usage.toIR()
					if u.OutputTokens > 0 {
						usage.OutputTokens = u.OutputTokens
					}
					if u.InputTokens > 0 {
						usage.InputTokens = u.InputTokens
					}
					if u.CacheReadTokens > 0 {
						usage.CacheReadTokens = u.CacheReadTokens
					}
					if u.CacheWriteTokens > 0 {
						usage.CacheWriteTokens = u.CacheWriteTokens
					}
				}
				merged := usage
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &merged}, nil) {
					return
				}

			case "message_stop":
				if !yield(ir.StreamEvent{Type: ir.EventMessageStop, StopReason: stop}, nil) {
					return
				}

			case "ping":
				if !yield(ir.StreamEvent{Type: ir.EventPing}, nil) {
					return
				}

			case "error":
				e := &ir.Error{Type: ir.ErrAPI, Message: "upstream stream error"}
				if ev.Error != nil {
					e = &ir.Error{Type: errorType(ev.Error.Type), Message: ev.Error.Message, Code: ev.Error.Type}
				}
				yield(ir.StreamEvent{}, e)
				return

			default:
				// Anthropic warns that new event types will appear. Ignoring
				// them is the documented client behavior.
				continue
			}
		}
	}
}
```

- [ ] **Step 4: Add the adapter type**

Create `internal/adapter/anthropic/adapter.go`:

```go
package anthropic

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) Kind() string { return "anthropic" }

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
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

Anthropic reports an unknown model as a 404, which the shared ladder already
buckets as `RetryableModel`, so this kind implements no `BodyClassifier`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/anthropic/ -v`
Expected: PASS.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/anthropic/
git commit -m "feat(anthropic): parse the message stream"
```

---

### Task 18: The Anthropic edge parses `/v1/messages`

**Files:**
- Create: `internal/edge/anthropic/parse.go`
- Test: `internal/edge/anthropic/parse_test.go`

**Interfaces:**
- Consumes: `ir.Request`, `edge.Passthrough` from Tasks 1 and 2.
- Produces: `anthropic.ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

Two shapes matter here more than the field list.

**The thinking mode round-trips, not just the budget.** Anthropic now has three
`thinking.type` values — `enabled` with a budget, `adaptive`, and `disabled` —
and only the first carries a number. Reading `budget_tokens` alone would discard
a Claude Code client's `adaptive` config and, worse, silently ignore an explicit
`disabled`. The type is parked in `Metadata` alongside `anthropic_version` and
Task 14 turns it back into whichever shape the target generation accepts.

**Tool results stay in their user turn.** Anthropic carries them as blocks
inside a `user` message, and the IR keeps them exactly that way — role
`RoleUser`, block `BlockToolResult`. Converting them to `RoleTool` would split a
turn that Anthropic deliberately keeps whole, and the openaicompat renderer
already treats user and tool turns identically, so nothing downstream benefits.

**`anthropic-version` is transport state, not content.** The header is parked in
`Metadata["anthropic_version"]` so the Anthropic adapter can echo it back
upstream, and `openaicompat.forwardableMetadata` strips it so it never reaches a
target that would not understand it.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/anthropic/parse_test.go`:

```go
package anthropic

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func parsed(t *testing.T, body string, headers map[string]string) *ir.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	req, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt == nil || pt.Surface != ir.SurfaceLLM || pt.ModelField != "model" {
		t.Fatalf("passthrough = %+v", pt)
	}
	return req
}

func TestParseRequestReadsSystemAsStringOrBlocks(t *testing.T) {
	one := parsed(t, `{"model":"claude-x","max_tokens":10,"system":"be terse","messages":[]}`, nil)
	if len(one.System) != 1 || one.System[0].Text != "be terse" {
		t.Fatalf("system = %+v", one.System)
	}
	many := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[],
		"system":[{"type":"text","text":"be terse","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`, nil)
	if len(many.System) != 1 || many.System[0].CacheControl == nil || many.System[0].CacheControl.TTL != "1h" {
		t.Fatalf("system = %+v; the TTL is a paid feature", many.System)
	}
}

func TestParseRequestKeepsToolResultsInTheUserTurn(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[
		{"role":"assistant","content":[{"type":"tool_use","id":"call_a","name":"f","input":{"x":1}}]},
		{"role":"user","content":[
			{"type":"tool_result","tool_use_id":"call_a","is_error":true,
			 "content":[{"type":"text","text":"boom"},
			            {"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}]},
			{"type":"text","text":"what now?"}]}]}`, nil)

	if len(req.Messages) != 2 {
		t.Fatalf("messages = %d", len(req.Messages))
	}
	if req.Messages[0].Content[0].ToolUse.ID != "call_a" ||
		string(req.Messages[0].Content[0].ToolUse.Input) != `{"x":1}` {
		t.Errorf("tool_use = %+v", req.Messages[0].Content[0].ToolUse)
	}
	turn := req.Messages[1]
	if turn.Role != ir.RoleUser {
		t.Errorf("role = %q; Anthropic carries results inside a user turn", turn.Role)
	}
	if len(turn.Content) != 2 {
		t.Fatalf("blocks = %+v", turn.Content)
	}
	tr := turn.Content[0].ToolResult
	if tr == nil || !tr.IsError || len(tr.Content) != 2 {
		t.Fatalf("tool_result = %+v", tr)
	}
	if tr.Content[1].Type != ir.BlockImage || tr.Content[1].Media.Data != "AAAA" {
		t.Errorf("nested image = %+v", tr.Content[1])
	}
}

func TestParseRequestReadsThinkingBlocksBack(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[
		{"role":"assistant","content":[
			{"type":"thinking","thinking":"step one","signature":"sig-1"},
			{"type":"redacted_thinking","data":"enc"}]}]}`, nil)
	blocks := req.Messages[0].Content
	if blocks[0].Thinking.Signature != "sig-1" {
		t.Errorf("thinking = %+v; the signature must round-trip byte for byte", blocks[0].Thinking)
	}
	if blocks[1].Type != ir.BlockRedactedThinking || blocks[1].Thinking.Data != "enc" {
		t.Errorf("redacted = %+v", blocks[1])
	}
}

func TestParseRequestReadsToolChoiceAndParallelFlag(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[],
		"tools":[{"name":"f","description":"d","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"tool","name":"f","disable_parallel_tool_use":true}}`, nil)
	if len(req.Tools) != 1 || string(req.Tools[0].Schema) != `{"type":"object"}` {
		t.Fatalf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "tool" || req.ToolChoice.Name != "f" {
		t.Fatalf("tool_choice = %+v", req.ToolChoice)
	}
	if req.ParallelToolCalls == nil || *req.ParallelToolCalls {
		t.Errorf("parallel = %v; disable_parallel_tool_use inverts", req.ParallelToolCalls)
	}
}

func TestParseRequestRoundTripsTheThinkingType(t *testing.T) {
	for _, mode := range []string{"adaptive", "disabled", "enabled"} {
		req := parsed(t, `{"model":"claude-x","max_tokens":10,"messages":[],
			"thinking":{"type":"`+mode+`"}}`, nil)
		if req.Metadata["anthropic_thinking_type"] != mode {
			t.Errorf("%s: metadata = %v; the mode is transport state the adapter needs",
				mode, req.Metadata)
		}
	}
}

func TestParseRequestReadsThinkingBudgetAndVersion(t *testing.T) {
	req := parsed(t, `{"model":"claude-x","max_tokens":10000,"messages":[],
		"thinking":{"type":"enabled","budget_tokens":8000},"metadata":{"user_id":"u1"}}`,
		map[string]string{"anthropic-version": "2024-10-22"})
	if req.Reasoning == nil || req.Reasoning.Budget != 8000 {
		t.Errorf("reasoning = %+v", req.Reasoning)
	}
	if req.Metadata["anthropic_version"] != "2024-10-22" {
		t.Errorf("metadata = %v; the inbound version is forwarded upstream", req.Metadata)
	}
	if req.Metadata["user_id"] != "u1" {
		t.Errorf("metadata = %v", req.Metadata)
	}
}

func TestParseRequestRejectsAnOversizedBody(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(strings.Repeat("x", 100)))
	if _, _, err := ParseRequest(r, 10); err == nil {
		t.Fatal("want an error for a body over the cap")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/anthropic/`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the parser**

Create `internal/edge/anthropic/parse.go`:

```go
// Package anthropic implements the Anthropic Messages inbound dialect.
package anthropic

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wireRequest struct {
	Model         string          `json:"model"`
	System        json.RawMessage `json:"system"`
	Messages      []wireMessage   `json:"messages"`
	Tools         []wireTool      `json:"tools"`
	ToolChoice    *wireToolChoice `json:"tool_choice"`
	MaxTokens     *int            `json:"max_tokens"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	TopK          *int            `json:"top_k"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
	Thinking      *struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	} `json:"thinking"`
	Metadata map[string]string `json:"metadata"`
}

type wireMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireToolChoice struct {
	Type                   string `json:"type"`
	Name                   string `json:"name"`
	DisableParallelToolUse *bool  `json:"disable_parallel_tool_use"`
}

type wireSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
	URL       string `json:"url"`
	FileID    string `json:"file_id"`
}

type wireBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	Thinking     string          `json:"thinking"`
	Signature    string          `json:"signature"`
	Data         string          `json:"data"`
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Input        json.RawMessage `json:"input"`
	ToolUseID    string          `json:"tool_use_id"`
	IsError      bool            `json:"is_error"`
	Content      json.RawMessage `json:"content"`
	Source       *wireSource     `json:"source"`
	CacheControl *struct {
		Type string `json:"type"`
		TTL  string `json:"ttl"`
	} `json:"cache_control"`
}

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
		TopK:          w.TopK,
		StopSequences: w.StopSequences,
		Stream:        w.Stream,
		Metadata:      w.Metadata,
	}
	// The inbound version is transport state the Anthropic adapter echoes
	// upstream. Every other adapter strips it.
	if v := r.Header.Get("anthropic-version"); v != "" {
		if req.Metadata == nil {
			req.Metadata = map[string]string{}
		}
		req.Metadata["anthropic_version"] = v
	}
	if w.Thinking != nil {
		if w.Thinking.BudgetTokens > 0 {
			req.Reasoning = &ir.Reasoning{Budget: w.Thinking.BudgetTokens}
		}
		// The mode itself is transport state, like the version header: the
		// Anthropic adapter needs it to choose between the manual and adaptive
		// shapes, and a client that explicitly disabled thinking must not have
		// that instruction silently discarded.
		if w.Thinking.Type != "" {
			if req.Metadata == nil {
				req.Metadata = map[string]string{}
			}
			req.Metadata["anthropic_thinking_type"] = w.Thinking.Type
		}
	}

	sys, err := parseContent(w.System)
	if err != nil {
		return nil, nil, err
	}
	req.System = sys

	for _, m := range w.Messages {
		blocks, err := parseContent(m.Content)
		if err != nil {
			return nil, nil, err
		}
		role := ir.RoleUser
		if m.Role == "assistant" {
			role = ir.RoleAssistant
		}
		req.Messages = append(req.Messages, ir.Message{Role: role, Content: blocks})
	}

	for _, t := range w.Tools {
		req.Tools = append(req.Tools, ir.Tool{
			Name: t.Name, Description: t.Description, Schema: t.InputSchema,
		})
	}
	if w.ToolChoice != nil {
		switch w.ToolChoice.Type {
		case "none":
			req.ToolChoice = &ir.ToolChoice{Mode: "none"}
		case "any":
			req.ToolChoice = &ir.ToolChoice{Mode: "any"}
		case "tool":
			req.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: w.ToolChoice.Name}
		default:
			req.ToolChoice = &ir.ToolChoice{Mode: "auto"}
		}
		if d := w.ToolChoice.DisableParallelToolUse; d != nil {
			parallel := !*d
			req.ParallelToolCalls = &parallel
		}
	}

	return req, &edge.Passthrough{Body: body, ModelField: "model", Surface: ir.SurfaceLLM}, nil
}

// parseContent accepts both the plain-string and block-array forms. Anthropic
// permits the string form for system and for any message.
func parseContent(raw json.RawMessage) ([]ir.ContentBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, nil
		}
		return []ir.ContentBlock{{Type: ir.BlockText, Text: s}}, nil
	}
	var blocks []wireBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, fmt.Errorf("unsupported content: %w", err)
	}
	out := make([]ir.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		blk, err := parseBlock(b)
		if err != nil {
			return nil, err
		}
		if blk.Type == "" {
			continue
		}
		if b.CacheControl != nil {
			blk.CacheControl = &ir.CacheControl{Type: b.CacheControl.Type, TTL: b.CacheControl.TTL}
		}
		out = append(out, blk)
	}
	return out, nil
}

func parseBlock(b wireBlock) (ir.ContentBlock, error) {
	switch b.Type {
	case "text":
		return ir.ContentBlock{Type: ir.BlockText, Text: b.Text}, nil
	case "thinking":
		return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
			Text: b.Thinking, Signature: b.Signature,
		}}, nil
	case "redacted_thinking":
		return ir.ContentBlock{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: b.Data}}, nil
	case "image":
		return ir.ContentBlock{Type: ir.BlockImage, Media: sourceToMedia(b.Source)}, nil
	case "document":
		return ir.ContentBlock{Type: ir.BlockDocument, Media: sourceToMedia(b.Source)}, nil
	case "tool_use":
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: b.ID, Name: b.Name, Input: b.Input,
		}}, nil
	case "tool_result":
		inner, err := parseContent(b.Content)
		if err != nil {
			return ir.ContentBlock{}, err
		}
		return ir.ContentBlock{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
			ToolUseID: b.ToolUseID, IsError: b.IsError, Content: inner,
		}}, nil
	default:
		// An unrecognized block is skipped rather than rejected: Anthropic adds
		// block types, and a gateway that 400s on a new one is worse than one
		// that forwards what it understands.
		return ir.ContentBlock{}, nil
	}
}

func sourceToMedia(s *wireSource) *ir.Media {
	if s == nil {
		return nil
	}
	return &ir.Media{MIME: s.MediaType, Data: s.Data, URL: s.URL, FileID: s.FileID}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/anthropic/ -v`
Expected: PASS.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/anthropic/
git commit -m "feat(edge/anthropic): parse messages requests"
```

---

### Task 19: The Anthropic edge writes responses and errors

**Files:**
- Create: `internal/edge/anthropic/write.go`, `internal/edge/anthropic/dialect.go`
- Test: `internal/edge/anthropic/write_test.go`

**Interfaces:**
- Consumes: `edge.Dialect` including `ProxyToken` from Task 5.
- Produces: `anthropic.New() *Dialect` satisfying `edge.Dialect`; `anthropic.WriteResponse`, `anthropic.WriteError`; `anthropic.responseBlocks(blocks []ir.ContentBlock) []any`; `anthropic.stopReasonWire(ir.StopReason) any`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Master design §14: errors are normalized into the **inbound** dialect's shape, so
Claude Code handles a gateway failure with the code it already has for an
Anthropic outage. The status codes matter as much as the body — Claude Code
retries a 529 and gives up on a 400.

The response block renderer here is deliberately not shared with
`internal/adapter/anthropic`. A response carries text, thinking, redacted
thinking, and tool uses; it never carries images, documents, tool results, or
cache-control markers, and the request-side renderer's budget threading would be
dead weight.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/anthropic/write_test.go`:

```go
package anthropic

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func written(t *testing.T, resp *ir.Response) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := WriteResponse(rec, resp); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWriteResponseProducesTheMessageShape(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "msg_1", Model: "claude-x", StopReason: ir.StopToolUse,
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "hmm", Signature: "sig-1"}},
			{Type: ir.BlockText, Text: "calling"},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)}},
		},
		Usage: ir.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, CacheWriteTokens: 7},
	})
	if got["type"] != "message" || got["role"] != "assistant" {
		t.Fatalf("envelope = %v", got)
	}
	if got["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v", got["stop_reason"])
	}
	blocks := got["content"].([]any)
	if len(blocks) != 3 {
		t.Fatalf("content = %v", blocks)
	}
	if blocks[0].(map[string]any)["signature"] != "sig-1" {
		t.Errorf("thinking = %v", blocks[0])
	}
	if _, ok := blocks[2].(map[string]any)["input"].(map[string]any); !ok {
		t.Errorf("tool_use input = %#v; Anthropic takes an object", blocks[2].(map[string]any)["input"])
	}
	usage := got["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 10 || usage["cache_read_input_tokens"].(float64) != 3 ||
		usage["cache_creation_input_tokens"].(float64) != 7 {
		t.Errorf("usage = %v", usage)
	}
}

func TestWriteResponseEmitsAnEmptyContentArray(t *testing.T) {
	got := written(t, &ir.Response{ID: "m", Model: "c", StopReason: ir.StopEndTurn})
	blocks, ok := got["content"].([]any)
	if !ok || blocks == nil {
		t.Fatalf("content = %#v; Anthropic clients index it unconditionally", got["content"])
	}
	if len(blocks) != 0 {
		t.Errorf("content = %v", blocks)
	}
}

func TestWriteResponseMapsContentFilterToRefusal(t *testing.T) {
	got := written(t, &ir.Response{ID: "m", Model: "c", StopReason: ir.StopContentFilter})
	if got["stop_reason"] != "refusal" {
		t.Errorf("stop_reason = %v; refusal is Anthropic's content filter", got["stop_reason"])
	}
}

func TestWriteErrorUsesTheAnthropicShapeAndStatus(t *testing.T) {
	cases := []struct {
		in     ir.ErrorType
		status int
		typ    string
	}{
		{ir.ErrInvalidRequest, 400, "invalid_request_error"},
		{ir.ErrAuthentication, 401, "authentication_error"},
		{ir.ErrPermission, 403, "permission_error"},
		{ir.ErrNotFound, 404, "not_found_error"},
		{ir.ErrRateLimit, 429, "rate_limit_error"},
		{ir.ErrOverloaded, 529, "overloaded_error"},
		{ir.ErrContentFilter, 400, "invalid_request_error"},
		{ir.ErrDarkrouter, 502, "api_error"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		if err := WriteError(rec, &ir.Error{Type: tc.in, Message: "nope"}); err != nil {
			t.Fatal(err)
		}
		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.in, rec.Code, tc.status)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["type"] != "error" {
			t.Errorf("%s: envelope = %v", tc.in, got)
		}
		if got["error"].(map[string]any)["type"] != tc.typ {
			t.Errorf("%s: error type = %v, want %s", tc.in, got["error"], tc.typ)
		}
	}
}

func TestProxyTokenAcceptsBothForms(t *testing.T) {
	d := New()
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	r.Header.Set("x-api-key", "sk-ant")
	if got := d.ProxyToken(r); got != "sk-ant" {
		t.Errorf("x-api-key = %q", got)
	}

	r2 := httptest.NewRequest("POST", "/v1/messages", nil)
	r2.Header.Set("Authorization", "Bearer sk-bearer")
	if got := d.ProxyToken(r2); got != "sk-bearer" {
		t.Errorf("bearer = %q", got)
	}

	r3 := httptest.NewRequest("POST", "/v1/messages", nil)
	r3.Header.Set("x-api-key", "sk-ant")
	r3.Header.Set("Authorization", "Bearer sk-bearer")
	if got := d.ProxyToken(r3); got != "sk-ant" {
		t.Errorf("both = %q; x-api-key is Anthropic's own form and wins", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/anthropic/ -run 'Write|ProxyToken'`
Expected: FAIL — undefined.

- [ ] **Step 3: Write the response and error writers**

Create `internal/edge/anthropic/write.go`:

```go
package anthropic

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
)

// stopReasonWire maps the IR onto Anthropic's set. `error` has no Anthropic
// spelling and reports as end_turn, which is what a client can act on.
func stopReasonWire(s ir.StopReason) any {
	switch s {
	case ir.StopMaxTokens:
		return "max_tokens"
	case ir.StopToolUse:
		return "tool_use"
	case ir.StopStopSequence:
		return "stop_sequence"
	case ir.StopContentFilter:
		return "refusal"
	case ir.StopPauseTurn:
		return "pause_turn"
	default:
		return "end_turn"
	}
}

// responseBlocks renders assistant content. A response carries text, thinking,
// and tool uses only, so this is deliberately narrower than the adapter's
// request-side renderer.
func responseBlocks(blocks []ir.ContentBlock) []any {
	// Never nil: Anthropic clients index content without checking.
	out := []any{}
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			m := map[string]any{"type": "thinking", "thinking": b.Thinking.Text}
			if b.Thinking.Signature != "" {
				m["signature"] = b.Thinking.Signature
			}
			out = append(out, m)
		case ir.BlockRedactedThinking:
			if b.Thinking == nil {
				continue
			}
			out = append(out, map[string]any{"type": "redacted_thinking", "data": b.Thinking.Data})
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			input := b.ToolUse.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out = append(out, map[string]any{
				"type": "tool_use", "id": b.ToolUse.ID, "name": b.ToolUse.Name, "input": input,
			})
		}
	}
	return out
}

func usageBody(u ir.Usage) map[string]any {
	return map[string]any{
		"input_tokens":                u.InputTokens,
		"output_tokens":               u.OutputTokens,
		"cache_read_input_tokens":     u.CacheReadTokens,
		"cache_creation_input_tokens": u.CacheWriteTokens,
	}
}

func messageID(id string) string {
	if id == "" {
		return "msg_darkrouter"
	}
	return id
}

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	out := map[string]any{
		"id":            messageID(resp.ID),
		"type":          "message",
		"role":          "assistant",
		"model":         resp.Model,
		"content":       responseBlocks(resp.Content),
		"stop_reason":   stopReasonWire(resp.StopReason),
		"stop_sequence": nil,
		"usage":         usageBody(resp.Usage),
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// errorShape maps a canonical error onto Anthropic's type name and status.
// The status is what Claude Code's retry logic reads: 529 is retried, 400 is not.
func errorShape(t ir.ErrorType) (string, int) {
	switch t {
	case ir.ErrInvalidRequest, ir.ErrContentFilter:
		return "invalid_request_error", http.StatusBadRequest
	case ir.ErrAuthentication:
		return "authentication_error", http.StatusUnauthorized
	case ir.ErrPermission:
		return "permission_error", http.StatusForbidden
	case ir.ErrNotFound:
		return "not_found_error", http.StatusNotFound
	case ir.ErrRateLimit:
		return "rate_limit_error", http.StatusTooManyRequests
	case ir.ErrOverloaded:
		return "overloaded_error", 529
	default:
		return "api_error", http.StatusBadGateway
	}
}

func WriteError(w http.ResponseWriter, e *ir.Error) error {
	name, status := errorShape(e.Type)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": name, "message": e.Message},
	})
}
```

- [ ] **Step 4: Add the dialect type**

Create `internal/edge/anthropic/dialect.go`:

```go
package anthropic

import (
	"iter"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

type Dialect struct{}

func New() *Dialect { return &Dialect{} }

func (d *Dialect) Name() string { return "anthropic" }

// ProxyToken accepts both forms master design §13 lists. x-api-key is
// Anthropic's own and wins when a client sends both, which some SDKs do while
// they migrate between credential styles.
func (d *Dialect) ProxyToken(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("x-api-key")); k != "" {
		return k
	}
	scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

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

`WriteStream` arrives in Task 20, so this file does not compile until then. Add
a temporary stub at the bottom of `write.go` to keep the task green, and delete
it in Task 20:

```go
// WriteStream is implemented in Task 20.
func WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	return WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: "streaming not implemented"})
}
```

Add `"iter"` to `write.go`'s imports for the stub.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/anthropic/ -v`
Expected: PASS.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/anthropic/
git commit -m "feat(edge/anthropic): write messages and errors"
```

---

### Task 20: The Anthropic edge streams the event model

**Files:**
- Create: `internal/edge/anthropic/stream.go`
- Modify: `internal/edge/anthropic/write.go` (delete the Task 19 stub)
- Test: `internal/edge/anthropic/stream_test.go`

**Interfaces:**
- Consumes: `sse.NewWriter` from Phase 1; `messageID`, `usageBody`, `stopReasonWire` from Task 19.
- Produces: `anthropic.WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Three things make this more than a transcription.

**Indices must be dense.** `internal/adapter/openaicompat` offsets tool blocks by
1000 so they cannot collide with the text block, and an Anthropic client reading
`"index": 1000` allocates a thousand-slot array or crashes. The writer keeps its
own dense numbering in open order, exactly as it does on the OpenAI side.

**A delta may arrive with no block start.** `openaicompat.ParseStream` emits
`reasoning_content` deltas without ever opening a block, because OpenAI's flat
delta model has nothing to open. Anthropic clients reject a
`content_block_delta` for an index they never saw start, so the writer opens one.

**`message_start` waits.** The IR carries `id` and `model` on
`EventMessageStart` but usage on a later `EventMessageDelta` — and Anthropic
puts `input_tokens` inside `message_start`. Holding the event until the first
one that is not a usage update lets an Anthropic-to-Anthropic route report real
input tokens, which is what a client's cost accounting reads. An
openaicompat-served route reports zero there and the true total in
`message_delta`, because that is genuinely all the upstream said in time.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/anthropic/stream_test.go`:

```go
package anthropic

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

type wireEvent struct {
	name string
	body map[string]any
}

func streamed(t *testing.T, events []ir.StreamEvent, final error) []wireEvent {
	t.Helper()
	rec := httptest.NewRecorder()
	err := WriteStream(rec, func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	var out []wireEvent
	for _, block := range strings.Split(rec.Body.String(), "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		var ev wireEvent
		for _, line := range strings.Split(block, "\n") {
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				ev.name = name
			}
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				if err := json.Unmarshal([]byte(data), &ev.body); err != nil {
					t.Fatalf("data %q: %v", data, err)
				}
			}
		}
		out = append(out, ev)
	}
	return out
}

func names(evs []wireEvent) []string {
	out := make([]string, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.name)
	}
	return out
}

func TestWriteStreamEmitsTheAnthropicSequence(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "msg_1", Model: "claude-x"},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 10, CacheReadTokens: 3}},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "Hi"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 10, OutputTokens: 4}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)

	want := []string{"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v", names(got), want)
	}
	msg := got[0].body["message"].(map[string]any)
	if msg["id"] != "msg_1" || msg["model"] != "claude-x" {
		t.Errorf("message_start = %v", msg)
	}
	if msg["usage"].(map[string]any)["input_tokens"].(float64) != 10 {
		t.Errorf("message_start usage = %v; it must carry the input count", msg["usage"])
	}
	if got[4].body["delta"].(map[string]any)["stop_reason"] != "end_turn" {
		t.Errorf("message_delta = %v", got[4].body)
	}
	if got[4].body["usage"].(map[string]any)["output_tokens"].(float64) != 4 {
		t.Errorf("message_delta usage = %v", got[4].body["usage"])
	}
}

func TestWriteStreamRenumbersToolBlocksDensely(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_a", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"x":1}`}},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil)

	var toolStart, toolDelta map[string]any
	for _, e := range got {
		if e.name == "content_block_start" &&
			e.body["content_block"].(map[string]any)["type"] == "tool_use" {
			toolStart = e.body
		}
		if e.name == "content_block_delta" &&
			e.body["delta"].(map[string]any)["type"] == "input_json_delta" {
			toolDelta = e.body
		}
	}
	if toolStart == nil || toolDelta == nil {
		t.Fatalf("events = %v", names(got))
	}
	if toolStart["index"].(float64) != 1 {
		t.Errorf("tool block index = %v; the wire index is dense, not the IR's 1000",
			toolStart["index"])
	}
	if toolDelta["index"].(float64) != 1 {
		t.Errorf("tool delta index = %v", toolDelta["index"])
	}
	cb := toolStart["content_block"].(map[string]any)
	if cb["id"] != "call_a" || cb["name"] != "f" {
		t.Errorf("content_block = %v", cb)
	}
}

func TestWriteStreamOpensABlockForAnOrphanDelta(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "hmm"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)
	want := []string{"message_start", "content_block_start", "content_block_delta",
		"content_block_stop", "message_delta", "message_stop"}
	if strings.Join(names(got), ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v, want %v; a delta with no start is invalid to a client",
			names(got), want)
	}
	if got[1].body["content_block"].(map[string]any)["type"] != "thinking" {
		t.Errorf("synthesized block = %v", got[1].body)
	}
	if got[2].body["delta"].(map[string]any)["type"] != "thinking_delta" {
		t.Errorf("delta = %v", got[2].body)
	}
}

func TestWriteStreamClosesOpenBlocksBeforeTheEnd(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "hi"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)
	if names(got)[3] != "content_block_stop" {
		t.Fatalf("events = %v; an unclosed block leaves the client waiting", names(got))
	}
}

func TestWriteStreamEmitsARealErrorEvent(t *testing.T) {
	got := streamed(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "m", Model: "c"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "partial"}},
	}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream gave up"})

	last := got[len(got)-1]
	if last.name != "error" {
		t.Fatalf("events = %v; spec §4.9 gives Anthropic a real error event", names(got))
	}
	e := last.body["error"].(map[string]any)
	if e["type"] != "overloaded_error" || e["message"] != "upstream gave up" {
		t.Errorf("error = %v", e)
	}
	if strings.Contains(strings.Join(names(got), ","), "message_stop") {
		t.Error("a stream that errored must not also claim to have stopped normally")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/anthropic/ -run WriteStream`
Expected: FAIL — the stub writes a JSON error rather than an event stream.

- [ ] **Step 3: Write the stream writer**

Delete the `WriteStream` stub from `write.go` (and `"iter"` from its imports),
then create `internal/edge/anthropic/stream.go`:

```go
package anthropic

import (
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"sort"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// blockStartBody describes an opening block. Anthropic requires the shape to
// match the kind: a tool_use block carries id, name, and an empty input object.
func blockStartBody(d *ir.Delta) map[string]any {
	if d == nil {
		return map[string]any{"type": "text", "text": ""}
	}
	switch d.Type {
	case ir.BlockThinking:
		return map[string]any{"type": "thinking", "thinking": "", "signature": ""}
	case ir.BlockRedactedThinking:
		return map[string]any{"type": "redacted_thinking", "data": ""}
	case ir.BlockToolUse:
		return map[string]any{
			"type": "tool_use", "id": d.ToolID, "name": d.ToolName, "input": map[string]any{},
		}
	default:
		return map[string]any{"type": "text", "text": ""}
	}
}

// deltaBody names the delta kind Anthropic uses for each block kind. A
// signature arrives on its own event and carries no text.
func deltaBody(d *ir.Delta) map[string]any {
	switch d.Type {
	case ir.BlockThinking:
		if d.Signature != "" {
			return map[string]any{"type": "signature_delta", "signature": d.Signature}
		}
		return map[string]any{"type": "thinking_delta", "thinking": d.Thinking}
	case ir.BlockToolUse:
		return map[string]any{"type": "input_json_delta", "partial_json": d.ToolInput}
	default:
		return map[string]any{"type": "text_delta", "text": d.Text}
	}
}

// WriteStream renders IR events as Anthropic's SSE event model. Anthropic
// sends no [DONE] sentinel: message_stop ends the stream.
func WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	s := sse.NewWriter(w)
	send := func(typ string, body map[string]any) error {
		body["type"] = typ
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		return s.Send(typ, string(b))
	}

	var (
		id, model string
		usage     ir.Usage
		started   bool
		wireOf    = map[int]int{}
		stop      = ir.StopEndTurn
	)

	// start is deferred until the first event that is not a usage update, so an
	// Anthropic-served route can report real input tokens inside message_start.
	start := func() error {
		if started {
			return nil
		}
		started = true
		return send("message_start", map[string]any{"message": map[string]any{
			"id": messageID(id), "type": "message", "role": "assistant", "model": model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": usageBody(usage),
		}})
	}

	openBlock := func(irIdx int, d *ir.Delta) (int, error) {
		if wire, ok := wireOf[irIdx]; ok {
			return wire, nil
		}
		if err := start(); err != nil {
			return 0, err
		}
		wire := len(wireOf)
		wireOf[irIdx] = wire
		body := blockStartBody(d)
		return wire, send("content_block_start", map[string]any{
			"index": wire, "content_block": body,
		})
	}

	closeAll := func() error {
		// Ascending wire order, so the sequence is deterministic; map iteration
		// order is not.
		wires := make([]int, 0, len(wireOf))
		for _, wire := range wireOf {
			wires = append(wires, wire)
		}
		sort.Ints(wires)
		for _, wire := range wires {
			if err := send("content_block_stop", map[string]any{"index": wire}); err != nil {
				return err
			}
		}
		wireOf = map[int]int{}
		return nil
	}

	for ev, err := range events {
		if err != nil {
			if serr := start(); serr != nil {
				return serr
			}
			var e *ir.Error
			if !errors.As(err, &e) {
				e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
			}
			name, _ := errorShape(e.Type)
			// Spec §4.9: Anthropic's shape is a real error event, and the
			// stream ends there — no message_stop, which would claim success.
			return send("error", map[string]any{
				"error": map[string]any{"type": name, "message": e.Message},
			})
		}

		switch ev.Type {
		case ir.EventMessageStart:
			id, model = ev.ID, ev.Model

		case ir.EventPing:
			if err := start(); err != nil {
				return err
			}
			if err := send("ping", map[string]any{}); err != nil {
				return err
			}

		case ir.EventBlockStart:
			if _, err := openBlock(ev.Index, ev.Delta); err != nil {
				return err
			}

		case ir.EventContentDelta:
			if ev.Delta == nil {
				continue
			}
			// An orphan delta opens its own block: OpenAI's flat model has
			// nothing to open, and a client rejects a delta for an index it
			// never saw start.
			wire, err := openBlock(ev.Index, ev.Delta)
			if err != nil {
				return err
			}
			if err := send("content_block_delta", map[string]any{
				"index": wire, "delta": deltaBody(ev.Delta),
			}); err != nil {
				return err
			}

		case ir.EventBlockStop:
			wire, ok := wireOf[ev.Index]
			if !ok {
				continue
			}
			delete(wireOf, ev.Index)
			if err := send("content_block_stop", map[string]any{"index": wire}); err != nil {
				return err
			}

		case ir.EventMessageDelta:
			if ev.Usage != nil {
				usage = *ev.Usage
			}
			if ev.StopReason != "" {
				stop = ev.StopReason
			}

		case ir.EventMessageStop:
			if ev.StopReason != "" {
				stop = ev.StopReason
			}
			if err := start(); err != nil {
				return err
			}
			if err := closeAll(); err != nil {
				return err
			}
			if err := send("message_delta", map[string]any{
				"delta": map[string]any{"stop_reason": stopReasonWire(stop), "stop_sequence": nil},
				"usage": map[string]any{"output_tokens": usage.OutputTokens},
			}); err != nil {
				return err
			}
			return send("message_stop", map[string]any{})
		}
	}

	// The sequence ended without a message_stop. Close what is open and end the
	// message anyway, or the client waits forever.
	if err := start(); err != nil {
		return err
	}
	if err := closeAll(); err != nil {
		return err
	}
	if err := send("message_delta", map[string]any{
		"delta": map[string]any{"stop_reason": stopReasonWire(stop), "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": usage.OutputTokens},
	}); err != nil {
		return err
	}
	return send("message_stop", map[string]any{})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/anthropic/ -v`
Expected: PASS.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/anthropic/
git commit -m "feat(edge/anthropic): stream the event model"
```

---

### Task 21: Gemini media, including the URL inlining rule

**Files:**
- Create: `internal/adapter/gemini/media.go`
- Test: `internal/adapter/gemini/media_test.go`

**Interfaces:**
- Consumes: `ir.Media`, `ir.Warning` from Task 1.
- Produces: `gemini.Fetcher` with `NewFetcher() *Fetcher` and `func (f *Fetcher) part(ctx context.Context, m *ir.Media, field string) (map[string]any, []ir.Warning)`; `gemini.passthroughURI(u string) bool`; `const targetName = "gemini"`; `const DefaultMaxInlineBytes = 20 << 20`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5
**Approach:** inline - skip 3: review finding F9 rules out fileData for a public URL, leaving fetch-and-inline

Review finding F9: **`fileData.fileUri` accepts Files API URIs and YouTube URLs,
not arbitrary HTTP image URLs.** Emitting `fileData` for a public image URL is
rejected by the API, and an adapter that does it turns every OpenAI-inbound
request carrying a hosted image into a 400 against Gemini specifically.

So Darkrouter fetches and inlines. That makes the gateway issue outbound
requests to client-supplied addresses, which is server-side request forgery in
the general case, and the mitigations are the reason risk is 3:

- **http and https only.** A `file://` or `gopher://` URL never reaches the client.
- **No redirects.** A redirect to an internal address is the standard bypass.
- **A byte cap**, enforced with a `LimitReader` rather than by trusting
  `Content-Length`, which a hostile server controls.
- **A short timeout**, so a slow URL cannot hold an attempt's budget open.

A fetch that fails for any reason drops the block with a warning rather than
failing the request: the model can still answer about the rest of the prompt,
and the trace says the image did not make it.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/gemini/media_test.go`:

```go
package gemini

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func hasWarning(warns []ir.Warning, field string) bool {
	for _, w := range warns {
		if w.Field == field {
			return true
		}
	}
	return false
}

func TestPassthroughURIRecognizesOnlyTheAcceptedForms(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://generativelanguage.googleapis.com/v1beta/files/abc123", true},
		{"gs://bucket/object.png", true},
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc", true},
		{"https://x.example/a.png", false},
		{"http://x.example/a.png", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := passthroughURI(tc.in); got != tc.want {
			t.Errorf("passthroughURI(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestPartEmitsInlineDataForBase64(t *testing.T) {
	got, warns := NewFetcher().part(context.Background(),
		&ir.Media{MIME: "image/png", Data: "AAAA"}, "image")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	in := got["inlineData"].(map[string]any)
	if in["mimeType"] != "image/png" || in["data"] != "AAAA" {
		t.Errorf("part = %v", got)
	}
}

func TestPartPassesAFilesAPIURIThrough(t *testing.T) {
	got, warns := NewFetcher().part(context.Background(),
		&ir.Media{MIME: "image/png", URL: "https://generativelanguage.googleapis.com/v1beta/files/abc"}, "image")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	fd := got["fileData"].(map[string]any)
	if fd["fileUri"] != "https://generativelanguage.googleapis.com/v1beta/files/abc" {
		t.Errorf("part = %v", got)
	}
}

func TestPartInlinesAPublicURL(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer up.Close()

	got, warns := NewFetcher().part(context.Background(), &ir.Media{URL: up.URL + "/a.png"}, "image")
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	in, ok := got["inlineData"].(map[string]any)
	if !ok {
		t.Fatalf("part = %v; a public URL must be inlined, not sent as fileData", got)
	}
	if in["mimeType"] != "image/png" {
		t.Errorf("mimeType = %v; it comes from the response when the IR has none", in["mimeType"])
	}
	if in["data"] != "iVBORw==" {
		t.Errorf("data = %v", in["data"])
	}
}

func TestPartDropsAnOversizedURL(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer up.Close()

	f := NewFetcher()
	f.MaxBytes = 10
	got, warns := f.part(context.Background(), &ir.Media{URL: up.URL + "/big.png"}, "image")
	if got != nil {
		t.Fatalf("part = %v, want nil", got)
	}
	if !hasWarning(warns, "messages[].image") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestPartDropsAFailedFetch(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer up.Close()

	got, warns := NewFetcher().part(context.Background(), &ir.Media{URL: up.URL + "/gone.png"}, "image")
	if got != nil || !hasWarning(warns, "messages[].image") {
		t.Fatalf("part = %v, warnings = %+v", got, warns)
	}
}

func TestPartRefusesANonHTTPScheme(t *testing.T) {
	got, warns := NewFetcher().part(context.Background(),
		&ir.Media{URL: "file:///etc/passwd"}, "document")
	if got != nil {
		t.Fatalf("part = %v; only http and https are fetched", got)
	}
	if !hasWarning(warns, "messages[].document") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestPartDropsAnEmptyMedia(t *testing.T) {
	got, warns := NewFetcher().part(context.Background(), &ir.Media{}, "image")
	if got != nil || len(warns) != 1 {
		t.Fatalf("part = %v, warnings = %+v", got, warns)
	}
}
```

`"iVBORw=="` is the standard base64 encoding of the four bytes
`0x89 0x50 0x4e 0x47`. Confirm with
`printf '\x89\x50\x4e\x47' | base64` before trusting the assertion; if the tool
disagrees, the tool is right and the literal in the test is wrong.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the fetcher**

Create `internal/adapter/gemini/media.go`:

```go
// Package gemini speaks Google's generateContent wire format to an upstream.
package gemini

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/ir"
)

// targetName labels the warnings this kind produces.
const targetName = "gemini"

// DefaultMaxInlineBytes bounds what one URL may contribute to a request.
const DefaultMaxInlineBytes int64 = 20 << 20

// Fetcher downloads a public URL so it can be inlined as base64.
//
// Gemini's fileData.fileUri accepts Files API URIs and YouTube URLs only, so a
// public image address has to be fetched by the gateway. That makes Darkrouter
// issue requests to client-supplied addresses, and the constraints here are
// what keep that from being server-side request forgery: http and https only,
// no redirects, a byte cap enforced on the reader rather than read off
// Content-Length, and a short timeout.
type Fetcher struct {
	Client   *http.Client
	MaxBytes int64
}

func NewFetcher() *Fetcher {
	return &Fetcher{
		Client: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				// A redirect to an internal address is the standard bypass.
				return http.ErrUseLastResponse
			},
		},
		MaxBytes: DefaultMaxInlineBytes,
	}
}

// passthroughURI reports whether Gemini accepts this URI as fileData. Anything
// else has to be inlined, because the API rejects a fileUri it cannot resolve.
func passthroughURI(u string) bool {
	if strings.HasPrefix(u, "gs://") {
		return true
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return false
	}
	switch parsed.Host {
	case "generativelanguage.googleapis.com",
		"www.youtube.com", "youtube.com", "m.youtube.com", "youtu.be":
		return true
	}
	return false
}

// part renders one media block. It returns nil when the block cannot be
// expressed, always with a warning: the model can still answer about the rest
// of the prompt, and the trace records what did not arrive.
func (f *Fetcher) part(ctx context.Context, m *ir.Media, field string) (map[string]any, []ir.Warning) {
	drop := func(reason string) (map[string]any, []ir.Warning) {
		return nil, []ir.Warning{{
			Field: "messages[]." + field, Target: targetName, Reason: reason,
		}}
	}
	if m == nil {
		return drop("media block carried nothing")
	}
	switch {
	case m.Data != "":
		return map[string]any{"inlineData": map[string]any{
			"mimeType": m.MIME, "data": m.Data,
		}}, nil

	case m.FileID != "":
		return map[string]any{"fileData": map[string]any{
			"mimeType": m.MIME, "fileUri": m.FileID,
		}}, nil

	case m.URL == "":
		return drop("media block carried neither data nor a URL")

	case passthroughURI(m.URL):
		return map[string]any{"fileData": map[string]any{
			"mimeType": m.MIME, "fileUri": m.URL,
		}}, nil
	}

	mime, data, err := f.inline(ctx, m.URL)
	if err != nil {
		return drop("could not inline the URL: " + err.Error())
	}
	if m.MIME != "" {
		mime = m.MIME
	}
	return map[string]any{"inlineData": map[string]any{"mimeType": mime, "data": data}}, nil
}

func (f *Fetcher) inline(ctx context.Context, raw string) (mime, data string, err error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", errUnsupportedScheme
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", errFetchStatus
	}

	// Read one byte past the cap so an oversized body is detected rather than
	// silently truncated into a corrupt image.
	body, err := io.ReadAll(io.LimitReader(resp.Body, f.MaxBytes+1))
	if err != nil {
		return "", "", err
	}
	if int64(len(body)) > f.MaxBytes {
		return "", "", errTooLarge
	}

	mime = resp.Header.Get("Content-Type")
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return mime, base64.StdEncoding.EncodeToString(body), nil
}
```

Declare the three sentinels at the top of the file, below the constants:

```go
var (
	errUnsupportedScheme = errors.New("gemini: only http and https URLs are inlined")
	errFetchStatus       = errors.New("gemini: the URL did not return 2xx")
	errTooLarge          = errors.New("gemini: the URL exceeded the inline cap")
)
```

and add `"errors"` to the imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ -v`
Expected: PASS.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/gemini/
git commit -m "feat(gemini): inline media the API cannot fetch"
```

---

### Task 22: Gemini content parts and tool correlation

**Files:**
- Create: `internal/adapter/gemini/content.go`
- Test: `internal/adapter/gemini/content_test.go`

**Interfaces:**
- Consumes: `Fetcher.part`, `targetName` from Task 21; `xlate.NonSystemMessages`, `xlate.SyntheticToolCallID` from Tasks 6 and 7.
- Produces: `func (f *Fetcher) renderContents(ctx context.Context, req *ir.Request) ([]any, []ir.Warning)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Three rules from the spec decide whether an agentic loop works against Gemini.

**The assistant role is `model`.** Sending `assistant` is an API error.

**`functionResponse` needs a `name`,** and Gemini's own ids are optional and
usually absent — so the name has to come from the call it answers. Spec §4.4:
that match is **positional within the turn**, never by name, because parallel
calls to the same function are exactly what agentic loops produce and name
matching pairs them arbitrarily.

**`functionResponse.response` is a JSON object.** A tool that returned prose
gets wrapped; a tool that returned an image has the image hoisted into a
following user turn with a warning, per spec §7, rather than stringified into
the struct where the model cannot see it.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/gemini/content_test.go`:

```go
package gemini

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func contents(t *testing.T, req *ir.Request) ([]map[string]any, []ir.Warning) {
	t.Helper()
	raw, warns := NewFetcher().renderContents(context.Background(), req)
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out, warns
}

func parts(t *testing.T, c map[string]any) []map[string]any {
	t.Helper()
	raw := c["parts"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, p := range raw {
		out = append(out, p.(map[string]any))
	}
	return out
}

func TestRenderContentsNamesTheAssistantRoleModel(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}},
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hello"}}},
	}})
	if got[0]["role"] != "user" || got[1]["role"] != "model" {
		t.Fatalf("roles = %v, %v; Gemini rejects \"assistant\"", got[0]["role"], got[1]["role"])
	}
}

func TestRenderContentsCarriesThoughtSignatures(t *testing.T) {
	got, warns := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "weighing", Signature: "sig-1"}},
			{Type: ir.BlockText, Text: "42"},
		}},
	}})
	if len(warns) != 0 {
		t.Fatalf("warnings = %+v", warns)
	}
	ps := parts(t, got[0])
	if ps[0]["thought"] != true || ps[0]["thoughtSignature"] != "sig-1" {
		t.Errorf("thought part = %v; thought:true alone does not restore reasoning state", ps[0])
	}
	if ps[0]["text"] != "weighing" {
		t.Errorf("thought part = %v", ps[0])
	}
	if _, ok := ps[1]["thought"]; ok {
		t.Errorf("plain text part = %v", ps[1])
	}
}

func TestRenderContentsMatchesParallelCallsPositionally(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_a", Name: "lookup", Input: json.RawMessage(`{"city":"Oslo"}`)}},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_b", Name: "lookup", Input: json.RawMessage(`{"city":"Bergen"}`)}},
		}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_b", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "rain"}}}},
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "clear"}}}},
		}},
	}})
	calls := parts(t, got[0])
	if calls[0]["functionCall"].(map[string]any)["name"] != "lookup" {
		t.Fatalf("functionCall = %v", calls[0])
	}
	resps := parts(t, got[1])
	first := resps[0]["functionResponse"].(map[string]any)
	if first["name"] != "lookup" {
		t.Errorf("functionResponse = %v", first)
	}
	if first["id"] != "call_b" {
		t.Errorf("functionResponse id = %v; an id the IR knows is preserved", first["id"])
	}
}

func TestRenderContentsWrapsToolResultText(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "f"}}}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a", Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "17C"}}}}}},
	}})
	fr := parts(t, got[1])[0]["functionResponse"].(map[string]any)
	resp := fr["response"].(map[string]any)
	if resp["result"] != "17C" {
		t.Errorf("response = %v; a struct is required, so prose is wrapped", resp)
	}
}

func TestRenderContentsKeepsAJSONToolResultAsAStruct(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "f"}}}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: `{"tempC":17}`}}}}}},
	}})
	resp := parts(t, got[1])[0]["functionResponse"].(map[string]any)["response"].(map[string]any)
	if resp["tempC"].(float64) != 17 {
		t.Errorf("response = %v; an object result is passed through rather than re-wrapped", resp)
	}
}

func TestRenderContentsHoistsAToolResultImage(t *testing.T) {
	got, warns := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{ID: "call_a", Name: "screenshot"}}}},
		{Role: ir.RoleTool, Content: []ir.ContentBlock{
			{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: "call_a",
				Content: []ir.ContentBlock{
					{Type: ir.BlockText, Text: "captured"},
					{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: "AAAA"}},
				}}}}},
	}})
	ps := parts(t, got[1])
	if len(ps) != 2 {
		t.Fatalf("parts = %v; the image is preserved alongside the response", ps)
	}
	if _, ok := ps[1]["inlineData"]; !ok {
		t.Errorf("hoisted part = %v", ps[1])
	}
	if !hasWarning(warns, "messages[].tool_result.image") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderContentsDropsRedactedThinkingAndCacheControl(t *testing.T) {
	_, warns := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
			{Type: ir.BlockRedactedThinking, Thinking: &ir.Thinking{Data: "enc"}},
		}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "long",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}}},
	}})
	if !hasWarning(warns, "messages[].redacted_thinking") || !hasWarning(warns, "cache_control") {
		t.Errorf("warnings = %+v", warns)
	}
}

func TestRenderContentsMergesConsecutiveSameRoleTurns(t *testing.T) {
	got, _ := contents(t, &ir.Request{Messages: []ir.Message{
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "one"}}},
		{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "two"}}},
	}})
	if len(got) != 1 || len(parts(t, got[0])) != 2 {
		t.Fatalf("contents = %v", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ -run RenderContents`
Expected: FAIL — `renderContents` is undefined.

- [ ] **Step 3: Write the renderer**

Create `internal/adapter/gemini/content.go`:

```go
package gemini

import (
	"context"
	"encoding/json"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

// pendingCall remembers a function call so the response answering it can carry
// the name Gemini requires. Ids are optional in this dialect and usually
// absent, so the ordered slice — not the map — is the authority.
type pendingCall struct {
	id   string
	name string
}

// renderContents converts the IR conversation to Gemini's contents array.
func (f *Fetcher) renderContents(ctx context.Context, req *ir.Request) ([]any, []ir.Warning) {
	var (
		out     []any
		warns   []ir.Warning
		curRole string
		curPart []any
		pending []pendingCall
	)
	flush := func() {
		if curRole == "" {
			return
		}
		out = append(out, map[string]any{"role": curRole, "parts": curPart})
		curRole, curPart = "", nil
	}

	for turn, m := range xlate.NonSystemMessages(req.Messages) {
		role := "user"
		if m.Role == ir.RoleAssistant {
			role = "model"
		}
		ps, calls, w := f.renderParts(ctx, turn, m.Content, pending)
		warns = append(warns, w...)
		if len(calls) > 0 {
			pending = calls
		}
		if len(ps) == 0 {
			continue
		}
		if role != curRole {
			flush()
			curRole = role
		}
		curPart = append(curPart, ps...)
	}
	flush()
	return out, warns
}

// renderParts converts one turn. It returns the parts, and the function calls
// this turn made so the next turn's responses can be matched to them.
func (f *Fetcher) renderParts(ctx context.Context, turn int, blocks []ir.ContentBlock,
	pending []pendingCall) ([]any, []pendingCall, []ir.Warning) {

	var (
		out     []any
		calls   []pendingCall
		warns   []ir.Warning
		results int
	)
	for _, b := range blocks {
		if b.CacheControl != nil {
			warns = append(warns, ir.Warning{
				Field: "cache_control", Target: targetName,
				Reason: "Gemini caches explicitly through cachedContent, not per block",
			})
		}

		switch b.Type {
		case ir.BlockText:
			out = append(out, map[string]any{"text": b.Text})

		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			// thought:true alone does not restore reasoning state on the next
			// turn — the signature is what does. Review finding F10.
			p := map[string]any{"text": b.Thinking.Text, "thought": true}
			if b.Thinking.Signature != "" {
				p["thoughtSignature"] = b.Thinking.Signature
			}
			out = append(out, p)

		case ir.BlockRedactedThinking:
			warns = append(warns, ir.Warning{
				Field: "messages[].redacted_thinking", Target: targetName,
				Reason: "no equivalent part; the block was dropped",
			})

		case ir.BlockImage, ir.BlockDocument, ir.BlockAudio:
			p, w := f.part(ctx, b.Media, string(b.Type))
			warns = append(warns, w...)
			if p != nil {
				out = append(out, p)
			}

		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			id := b.ToolUse.ID
			if id == "" {
				id = xlate.SyntheticToolCallID(turn, len(calls))
			}
			calls = append(calls, pendingCall{id: id, name: b.ToolUse.Name})
			args := b.ToolUse.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			call := map[string]any{"name": b.ToolUse.Name, "args": args}
			if b.ToolUse.ID != "" {
				call["id"] = b.ToolUse.ID
			}
			out = append(out, map[string]any{"functionCall": call})

		case ir.BlockToolResult:
			if b.ToolResult == nil {
				continue
			}
			// Positional within the turn: parallel calls to one function are
			// indistinguishable by name, and that is exactly what an agentic
			// loop produces.
			name := ""
			if results < len(pending) {
				name = pending[results].name
			}
			results++

			resp := map[string]any{"name": name, "response": responseStruct(b.ToolResult.Text())}
			if b.ToolResult.ToolUseID != "" {
				resp["id"] = b.ToolResult.ToolUseID
			}
			out = append(out, map[string]any{"functionResponse": resp})

			for _, inner := range b.ToolResult.Content {
				if inner.Type == ir.BlockText {
					continue
				}
				// Spec §7: functionResponse.response is a struct, so media is
				// hoisted into the same turn as its own part rather than lost.
				p, w := f.part(ctx, inner.Media, "tool_result."+string(inner.Type))
				warns = append(warns, w...)
				if p != nil {
					out = append(out, p)
					warns = append(warns, ir.Warning{
						Field: "messages[].tool_result." + string(inner.Type), Target: targetName,
						Reason: "moved out of the function response, which accepts a JSON struct only",
					})
				}
			}

		default:
			warns = append(warns, ir.Warning{
				Field: "messages[]." + string(b.Type), Target: targetName,
				Reason: "unsupported content block",
			})
		}
	}
	return out, calls, warns
}

// responseStruct meets Gemini's requirement that a function response be an
// object. A tool that already returned one is passed through; prose is wrapped
// under "result" rather than being sent as a bare string, which is rejected.
func responseStruct(text string) any {
	var obj map[string]any
	if json.Unmarshal([]byte(text), &obj) == nil && obj != nil {
		return obj
	}
	return map[string]any{"result": text}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ -v`
Expected: PASS.

`TestRenderContentsHoistsAToolResultImage` expects one warning field,
`messages[].tool_result.image`, and `f.part` is called with the field string
`tool_result.image`, so a failed inline would also warn under that name. Both
paths therefore carry the same field, which is intended: the client's question
is "did my image reach the model", not "which code path dropped it".

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/gemini/
git commit -m "feat(gemini): render contents and tool parts"
```

---

### Task 23: Gemini `BuildRequest`

**Files:**
- Create: `internal/adapter/gemini/build.go`
- Test: `internal/adapter/gemini/build_test.go`

**Interfaces:**
- Consumes: `Fetcher.renderContents` from Task 22; `xlate.CollectSystem`, `xlate.EffortBudget` from Tasks 6 and 7.
- Produces: `func (f *Fetcher) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

The trap spec §4.3 names explicitly: **every function declaration goes inside a
single `tools[0].functionDeclarations` array.** Separate `tools` entries are
reserved for built-in tools, and emitting one entry per function silently
disables function calling — no error, the model just never calls anything.

The model lives in the URL rather than the body, and streaming is a different
method (`:streamGenerateContent`) rather than a body flag. `?alt=sse` is
requested unconditionally on the streaming path, because the JSON-array form is
harder to consume incrementally and Darkrouter is the client here.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/gemini/build_test.go`:

```go
package gemini

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func built(t *testing.T, req *ir.Request) (*http.Request, map[string]any, []ir.Warning) {
	t.Helper()
	hr, warns, err := NewFetcher().BuildRequest(context.Background(),
		&adapter.Target{
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			APIKey:  "AIza", Model: "gemini-2.0-flash",
		}, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return hr, body, warns
}

func userMsg(text string) ir.Message {
	return ir.Message{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: text}}}
}

func TestBuildRequestPutsTheModelAndMethodInTheURL(t *testing.T) {
	hr, _, _ := built(t, &ir.Request{Messages: []ir.Message{userMsg("hi")}})
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent"
	if hr.URL.String() != want {
		t.Errorf("url = %s, want %s", hr.URL, want)
	}
	if hr.Header.Get("x-goog-api-key") != "AIza" {
		t.Errorf("x-goog-api-key = %q", hr.Header.Get("x-goog-api-key"))
	}
	if hr.URL.Query().Get("key") != "" {
		t.Error("the key belongs in a header, not the query string, where it lands in logs")
	}
}

func TestBuildRequestStreamsWithAltSSE(t *testing.T) {
	hr, body, _ := built(t, &ir.Request{Stream: true, Messages: []ir.Message{userMsg("hi")}})
	if hr.URL.Path != "/v1beta/models/gemini-2.0-flash:streamGenerateContent" {
		t.Errorf("path = %s", hr.URL.Path)
	}
	if hr.URL.Query().Get("alt") != "sse" {
		t.Errorf("query = %s; the JSON-array form is far harder to read incrementally", hr.URL.RawQuery)
	}
	if _, ok := body["stream"]; ok {
		t.Error("Gemini selects streaming by method, not by a body flag")
	}
}

func TestBuildRequestDeclaresEveryFunctionInOneToolsEntry(t *testing.T) {
	_, body, _ := built(t, &ir.Request{
		Messages: []ir.Message{userMsg("hi")},
		Tools: []ir.Tool{
			{Name: "a", Description: "da", Schema: json.RawMessage(`{"type":"object"}`)},
			{Name: "b", Description: "db", Schema: json.RawMessage(`{"type":"object"}`)},
		},
	})
	tools := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %d entries; one entry per function silently disables calling", len(tools))
	}
	decls := tools[0].(map[string]any)["functionDeclarations"].([]any)
	if len(decls) != 2 {
		t.Fatalf("functionDeclarations = %v", decls)
	}
	if decls[0].(map[string]any)["name"] != "a" {
		t.Errorf("declaration = %v", decls[0])
	}
	if _, ok := decls[0].(map[string]any)["parameters"]; !ok {
		t.Errorf("declaration = %v; Gemini names the schema parameters", decls[0])
	}
}

func TestBuildRequestMapsToolChoiceModes(t *testing.T) {
	cases := []struct {
		mode string
		name string
		want string
	}{
		{"auto", "", "AUTO"},
		{"none", "", "NONE"},
		{"any", "", "ANY"},
		{"tool", "f", "ANY"},
	}
	for _, tc := range cases {
		_, body, _ := built(t, &ir.Request{
			Messages:   []ir.Message{userMsg("hi")},
			ToolChoice: &ir.ToolChoice{Mode: tc.mode, Name: tc.name},
		})
		cfg := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
		if cfg["mode"] != tc.want {
			t.Errorf("mode %q = %v, want %v", tc.mode, cfg["mode"], tc.want)
		}
		if tc.mode == "tool" {
			names := cfg["allowedFunctionNames"].([]any)
			if len(names) != 1 || names[0] != "f" {
				t.Errorf("allowedFunctionNames = %v; forcing one tool needs the allow list", names)
			}
		}
	}
}

func TestBuildRequestFillsGenerationConfig(t *testing.T) {
	temp, top := 0.7, 0.9
	k, max := 40, 512
	_, body, _ := built(t, &ir.Request{
		Messages:       []ir.Message{userMsg("hi")},
		Temperature:    &temp,
		TopP:           &top,
		TopK:           &k,
		MaxTokens:      &max,
		StopSequences:  []string{"END"},
		Reasoning:      &ir.Reasoning{Effort: "medium"},
		ResponseFormat: &ir.ResponseFormat{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)},
	})
	cfg := body["generationConfig"].(map[string]any)
	if cfg["temperature"].(float64) != 0.7 || cfg["topP"].(float64) != 0.9 ||
		cfg["topK"].(float64) != 40 || cfg["maxOutputTokens"].(float64) != 512 {
		t.Errorf("generationConfig = %v", cfg)
	}
	if cfg["stopSequences"].([]any)[0] != "END" {
		t.Errorf("stopSequences = %v", cfg["stopSequences"])
	}
	if cfg["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType = %v; a schema without it is ignored", cfg["responseMimeType"])
	}
	if _, ok := cfg["responseSchema"]; !ok {
		t.Errorf("generationConfig = %v", cfg)
	}
	th := cfg["thinkingConfig"].(map[string]any)
	if th["thinkingBudget"].(float64) != 16384 || th["includeThoughts"] != true {
		t.Errorf("thinkingConfig = %v; medium is 16384 by the fixed table", th)
	}
}

func TestBuildRequestSendsSystemInstructionAndSafety(t *testing.T) {
	_, body, _ := built(t, &ir.Request{
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}},
		Messages: []ir.Message{userMsg("hi")},
		Safety: []ir.SafetySetting{
			{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"},
		},
	})
	si := body["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if si["text"] != "be terse" {
		t.Errorf("systemInstruction = %v", body["systemInstruction"])
	}
	s := body["safetySettings"].([]any)[0].(map[string]any)
	if s["category"] != "HARM_CATEGORY_HARASSMENT" || s["threshold"] != "BLOCK_NONE" {
		t.Errorf("safetySettings = %v", body["safetySettings"])
	}
}

func TestBuildRequestWarnsOnParallelToolCallsAndMetadata(t *testing.T) {
	no := false
	_, _, warns := built(t, &ir.Request{
		Messages:          []ir.Message{userMsg("hi")},
		ParallelToolCalls: &no,
		Metadata:          map[string]string{"user_id": "u1"},
	})
	if !hasWarning(warns, "parallel_tool_calls") || !hasWarning(warns, "metadata") {
		t.Errorf("warnings = %+v", warns)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ -run BuildRequest`
Expected: FAIL — undefined.

- [ ] **Step 3: Write the builder**

Create `internal/adapter/gemini/build.go`:

```go
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/ir"
)

func (f *Fetcher) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	var warns []ir.Warning
	body := map[string]any{}

	contents, w := f.renderContents(ctx, req)
	warns = append(warns, w...)
	body["contents"] = contents

	sys, w := xlate.CollectSystem(req, targetName)
	warns = append(warns, w...)
	if sys != "" {
		body["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": sys}}}
	}

	if len(req.Tools) > 0 {
		decls := make([]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			schema := tool.Schema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			decls = append(decls, map[string]any{
				"name": tool.Name, "description": tool.Description, "parameters": schema,
			})
		}
		// One entry holding every declaration. Separate entries are reserved
		// for built-in tools, and splitting them here disables function calling
		// without an error.
		body["tools"] = []any{map[string]any{"functionDeclarations": decls}}
	}
	if cfg := functionCallingConfig(req.ToolChoice); cfg != nil {
		body["toolConfig"] = map[string]any{"functionCallingConfig": cfg}
	}
	if req.ParallelToolCalls != nil {
		warns = append(warns, ir.Warning{
			Field: "parallel_tool_calls", Target: targetName, Reason: "no equivalent setting",
		})
	}
	if len(req.Metadata) > 0 {
		for k := range req.Metadata {
			if strings.HasPrefix(k, "anthropic_") {
				continue
			}
			warns = append(warns, ir.Warning{
				Field: "metadata", Target: targetName, Reason: "no request metadata field",
			})
			break
		}
	}
	if len(req.Safety) > 0 {
		settings := make([]any, 0, len(req.Safety))
		for _, s := range req.Safety {
			settings = append(settings, map[string]any{
				"category": s.Category, "threshold": s.Threshold,
			})
		}
		body["safetySettings"] = settings
	}

	cfg := map[string]any{}
	if req.Temperature != nil {
		cfg["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		cfg["topP"] = *req.TopP
	}
	if req.TopK != nil {
		cfg["topK"] = *req.TopK
	}
	if req.MaxTokens != nil {
		cfg["maxOutputTokens"] = *req.MaxTokens
	}
	if len(req.StopSequences) > 0 {
		cfg["stopSequences"] = req.StopSequences
	}
	if req.ResponseFormat != nil && req.ResponseFormat.Type == "json_schema" {
		// A responseSchema without the MIME type is ignored outright.
		cfg["responseMimeType"] = "application/json"
		cfg["responseSchema"] = req.ResponseFormat.Schema
	}
	if req.Reasoning != nil {
		budget := req.Reasoning.Budget
		if budget == 0 {
			budget = xlate.EffortBudget(req.Reasoning.Effort, 0)
		}
		if budget > 0 {
			cfg["thinkingConfig"] = map[string]any{
				"thinkingBudget": budget, "includeThoughts": true,
			}
		}
	}
	if len(cfg) > 0 {
		body["generationConfig"] = cfg
	}

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, warns, err
	}

	method := ":generateContent"
	if req.Stream {
		method = ":streamGenerateContent"
	}
	// url.PathEscape on the model keeps a provider/model name from opening
	// extra path segments the API would not match.
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/models/" + url.PathEscape(t.Model) + method
	if req.Stream {
		endpoint += "?alt=sse"
	}

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, warns, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		// The header rather than ?key=: a query parameter lands in access logs
		// and proxy traces.
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, warns, nil
}

// functionCallingConfig maps the IR's tool choice. Forcing one tool is ANY plus
// an allow list, since Gemini has no single-tool mode.
func functionCallingConfig(tc *ir.ToolChoice) map[string]any {
	if tc == nil {
		return nil
	}
	switch tc.Mode {
	case "none":
		return map[string]any{"mode": "NONE"}
	case "any":
		return map[string]any{"mode": "ANY"}
	case "tool":
		return map[string]any{"mode": "ANY", "allowedFunctionNames": []string{tc.Name}}
	default:
		return map[string]any{"mode": "AUTO"}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ -v`
Expected: PASS.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/gemini/
git commit -m "feat(gemini): build generateContent requests"
```

---

### Task 24: Gemini `ParseResponse`, blocked prompts, and `Classify`

**Files:**
- Create: `internal/adapter/gemini/parse.go`
- Modify: `internal/exec/exec.go` (the `ParseResponse` error path)
- Test: `internal/adapter/gemini/parse_test.go`, `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `adapter.ClassifyStatus` from Task 15; `targetName` from Task 21.
- Produces: `gemini.ParseResponse(resp *http.Response) (*ir.Response, error)`; `gemini.Classify(resp *http.Response, err error) adapter.Outcome`; `gemini.finishReason(s string, hasCall bool) (ir.StopReason, bool)`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: spec §9's done criterion requires an error rather than an empty success

Two facts from review finding F12 shape this.

**A blocked prompt returns zero candidates.** `promptFeedback.blockReason` is
set and there is no `finishReason` anywhere; an adapter reading
`candidates[0].finishReason` either nil-dereferences or reports an empty
success. It becomes a content-filter error — which is a Phase 4 done criterion.

That error must not look like a provider fault. `exec` currently classifies any
`ParseResponse` failure as `RetryableProvider`, which would trip the breaker on
a healthy provider and burn the rest of the chain re-asking a question every
model will refuse. An `*ir.Error` carrying `ErrContentFilter` is classified
`Fatal` instead and no health signal is recorded.

**Gemini does not signal tool use in its finish reason.** `STOP` with a
`functionCall` part present is the only evidence, and getting it wrong makes
agentic clients terminate mid-task.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/gemini/parse_test.go`:

```go
package gemini

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func parseBody(t *testing.T, body string) (*ir.Response, error) {
	t.Helper()
	return ParseResponse(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)),
	})
}

func TestParseResponseReadsPartsAndUsage(t *testing.T) {
	got, err := parseBody(t, `{"modelVersion":"gemini-2.0-flash","candidates":[{
		"content":{"role":"model","parts":[
			{"text":"weighing","thought":true,"thoughtSignature":"sig-1"},
			{"text":"calling"},
			{"functionCall":{"id":"call_a","name":"f","args":{"x":1}}}]},
		"finishReason":"STOP"}],
		"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":4,
			"cachedContentTokenCount":3,"thoughtsTokenCount":6}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Content) != 3 {
		t.Fatalf("content = %+v", got.Content)
	}
	if got.Content[0].Type != ir.BlockThinking || got.Content[0].Thinking.Signature != "sig-1" {
		t.Errorf("thought part = %+v", got.Content[0])
	}
	if got.Content[2].ToolUse == nil || string(got.Content[2].ToolUse.Input) != `{"x":1}` {
		t.Errorf("functionCall = %+v", got.Content[2].ToolUse)
	}
	if got.StopReason != ir.StopToolUse {
		t.Errorf("stop = %q; STOP with a functionCall present means tool use", got.StopReason)
	}
	if got.Model != "gemini-2.0-flash" {
		t.Errorf("model = %q", got.Model)
	}
	if got.Usage.InputTokens != 10 || got.Usage.OutputTokens != 4 ||
		got.Usage.CacheReadTokens != 3 || got.Usage.ReasoningTokens != 6 {
		t.Errorf("usage = %+v", got.Usage)
	}
}

func TestParseResponseStopWithoutACallIsEndTurn(t *testing.T) {
	got, err := parseBody(t, `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.StopReason != ir.StopEndTurn {
		t.Errorf("stop = %q", got.StopReason)
	}
}

func TestParseResponseBlockedPromptIsAContentFilterError(t *testing.T) {
	_, err := parseBody(t, `{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[]}`)
	var e *ir.Error
	if !errors.As(err, &e) {
		t.Fatalf("err = %v, want *ir.Error", err)
	}
	if e.Type != ir.ErrContentFilter {
		t.Errorf("error = %+v; an empty success is the failure this guards against", e)
	}
	if !strings.Contains(e.Message, "SAFETY") {
		t.Errorf("message = %q; the block reason is the only actionable detail", e.Message)
	}
}

func TestParseResponseNoCandidatesAndNoBlockIsAnEmptyTurn(t *testing.T) {
	got, err := parseBody(t, `{"candidates":[]}`)
	if err != nil {
		t.Fatalf("err = %v; a model that simply said nothing is not an error", err)
	}
	if len(got.Content) != 0 || got.StopReason != ir.StopEndTurn {
		t.Errorf("response = %+v", got)
	}
}

func TestFinishReasonTable(t *testing.T) {
	cases := []struct {
		in      string
		hasCall bool
		want    ir.StopReason
		known   bool
	}{
		{"STOP", false, ir.StopEndTurn, true},
		{"STOP", true, ir.StopToolUse, true},
		{"", false, ir.StopEndTurn, true},
		{"MAX_TOKENS", false, ir.StopMaxTokens, true},
		{"SAFETY", false, ir.StopContentFilter, true},
		{"BLOCKLIST", false, ir.StopContentFilter, true},
		{"PROHIBITED_CONTENT", false, ir.StopContentFilter, true},
		{"SPII", false, ir.StopContentFilter, true},
		{"RECITATION", false, ir.StopContentFilter, true},
		{"IMAGE_SAFETY", false, ir.StopContentFilter, true},
		{"MALFORMED_FUNCTION_CALL", false, ir.StopError, true},
		{"OTHER", false, ir.StopError, true},
		{"LANGUAGE", false, ir.StopError, true},
		{"SOMETHING_NEW", false, ir.StopEndTurn, false},
	}
	for _, tc := range cases {
		got, known := finishReason(tc.in, tc.hasCall)
		if got != tc.want || known != tc.known {
			t.Errorf("finishReason(%q, %v) = %q, %v; want %q, %v",
				tc.in, tc.hasCall, got, known, tc.want, tc.known)
		}
	}
}

func TestParseResponseWarnsOnAnUnknownFinishReason(t *testing.T) {
	got, err := parseBody(t, `{"candidates":[{"content":{"parts":[{"text":"hi"}]},
		"finishReason":"SOMETHING_NEW"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].Field != "finishReason" {
		t.Errorf("warnings = %+v", got.Warnings)
	}
}

func TestClassifyUsesTheSharedLadder(t *testing.T) {
	if got := Classify(&http.Response{StatusCode: 503}, nil); got != adapter.OutcomeRetryableProvider {
		t.Errorf("503 = %q", got)
	}
}
```

Append to `internal/exec/exec_test.go`:

```go
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
```

`internal/exec/exec_test.go` needs `errors` in its import block for this.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ ./internal/exec/ -run 'ParseResponse|FinishReason|ContentFilter'`
Expected: FAIL — undefined in both packages.

- [ ] **Step 3: Write the parser**

Create `internal/adapter/gemini/parse.go`:

```go
package gemini

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

type wirePart struct {
	Text             string `json:"text"`
	Thought          bool   `json:"thought"`
	ThoughtSignature string `json:"thoughtSignature"`
	InlineData       *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
}

type wireCandidate struct {
	Content struct {
		Role  string     `json:"role"`
		Parts []wirePart `json:"parts"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

type wireUsage struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	ThoughtsTokenCount      int `json:"thoughtsTokenCount"`
	TotalTokenCount         int `json:"totalTokenCount"`
}

func (u *wireUsage) toIR() ir.Usage {
	return ir.Usage{
		InputTokens:     u.PromptTokenCount,
		OutputTokens:    u.CandidatesTokenCount,
		CacheReadTokens: u.CachedContentTokenCount,
		ReasoningTokens: u.ThoughtsTokenCount,
	}
}

type wireResponse struct {
	ResponseID     string          `json:"responseId"`
	ModelVersion   string          `json:"modelVersion"`
	Candidates     []wireCandidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata wireUsage `json:"usageMetadata"`
}

// finishReason maps Gemini's enum. hasCall carries the one thing the enum does
// not say: Gemini reports STOP for a turn that called a tool, and an agentic
// client that reads that as end_turn stops mid-task.
//
// The bool reports whether the value was recognized. An unknown one degrades to
// end_turn with a warning rather than failing, because the enum grows.
func finishReason(s string, hasCall bool) (ir.StopReason, bool) {
	switch s {
	case "", "FINISH_REASON_UNSPECIFIED", "STOP":
		if hasCall {
			return ir.StopToolUse, true
		}
		return ir.StopEndTurn, true
	case "MAX_TOKENS":
		return ir.StopMaxTokens, true
	case "SAFETY", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "RECITATION", "IMAGE_SAFETY":
		return ir.StopContentFilter, true
	case "MALFORMED_FUNCTION_CALL", "OTHER", "LANGUAGE":
		return ir.StopError, true
	default:
		return ir.StopEndTurn, false
	}
}

func partToIR(p wirePart) (ir.ContentBlock, bool) {
	switch {
	case p.FunctionCall != nil:
		args := p.FunctionCall.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		return ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
			ID: p.FunctionCall.ID, Name: p.FunctionCall.Name, Input: args,
		}}, true
	case p.Thought:
		return ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
			Text: p.Text, Signature: p.ThoughtSignature,
		}}, true
	case p.InlineData != nil:
		return ir.ContentBlock{Type: ir.BlockImage, Media: &ir.Media{
			MIME: p.InlineData.MimeType, Data: p.InlineData.Data,
		}}, true
	case p.Text != "":
		return ir.ContentBlock{Type: ir.BlockText, Text: p.Text}, true
	default:
		return ir.ContentBlock{}, false
	}
}

func ParseResponse(resp *http.Response) (*ir.Response, error) {
	defer resp.Body.Close()
	var w wireResponse
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return nil, err
	}

	// A blocked prompt carries no candidates and no finish reason at all.
	// Reporting it as an empty success is the failure review finding F12 names.
	if len(w.Candidates) == 0 && w.PromptFeedback != nil && w.PromptFeedback.BlockReason != "" {
		return nil, &ir.Error{
			Type:    ir.ErrContentFilter,
			Message: "the prompt was blocked: " + w.PromptFeedback.BlockReason,
			Code:    w.PromptFeedback.BlockReason,
		}
	}

	out := &ir.Response{
		ID: w.ResponseID, Model: w.ModelVersion,
		Usage: w.UsageMetadata.toIR(), StopReason: ir.StopEndTurn,
	}
	if len(w.Candidates) == 0 {
		return out, nil
	}

	c := w.Candidates[0]
	hasCall := false
	for _, p := range c.Content.Parts {
		blk, ok := partToIR(p)
		if !ok {
			continue
		}
		if blk.Type == ir.BlockToolUse {
			hasCall = true
		}
		out.Content = append(out.Content, blk)
	}

	sr, known := finishReason(c.FinishReason, hasCall)
	out.StopReason = sr
	if !known {
		out.Warnings = append(out.Warnings, ir.Warning{
			Field: "finishReason", Target: targetName,
			Reason: "unrecognized value " + c.FinishReason + "; reported as end_turn",
		})
	}
	return out, nil
}

func Classify(resp *http.Response, err error) adapter.Outcome {
	return adapter.ClassifyStatus(resp, err)
}
```

- [ ] **Step 4: Teach exec that a refusal is not a provider fault**

In `internal/exec/exec.go`, replace the `ParseResponse` error block inside
`attempt`:

```go
	out, perr := ad.ParseResponse(resp)
	if perr != nil {
		outcome := outcomeForParseError(perr)
		last := len(rec.Attempts) - 1
		rec.Attempts[last].Outcome = string(outcome)
		rec.Attempts[last].Error = perr.Error()
		if outcome != adapter.OutcomeFatal {
			// A 2xx that cannot be read is a provider fault, so it rejoins the
			// outcome path. A refusal is not: recording it would trip the
			// breaker on a healthy provider, and failing over would re-ask a
			// question every model in the chain will refuse.
			e.recordHealthFor(c, outcome, resp)
		}
		var ie *ir.Error
		if errors.As(perr, &ie) {
			return outcome, statusCode, ie
		}
		return outcome, statusCode, errorFor(outcome, perr)
	}
```

and add the classifier beside `errorFor`:

```go
// outcomeForParseError separates "this provider is broken" from "this provider
// answered, and the answer was a refusal". Only the first is a health signal.
func outcomeForParseError(err error) adapter.Outcome {
	var e *ir.Error
	if errors.As(err, &e) && e.Type == ir.ErrContentFilter {
		return adapter.OutcomeFatal
	}
	return adapter.OutcomeRetryableProvider
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ ./internal/exec/ -v`
Expected: PASS.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/gemini/ internal/exec/
git commit -m "feat(gemini): parse responses and blocked prompts"
```

---

### Task 25: Gemini `ParseStream` and the adapter type

**Files:**
- Create: `internal/adapter/gemini/stream.go`, `internal/adapter/gemini/adapter.go`
- Test: `internal/adapter/gemini/stream_test.go`

**Interfaces:**
- Consumes: `wireResponse`, `wirePart`, `finishReason` from Task 24; `sse.NewReader` from Phase 1.
- Produces: `gemini.ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error]`; `gemini.New() *Adapter` and `gemini.NewWithFetcher(f *Fetcher) *Adapter`, satisfying `adapter.Adapter`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Review finding F1, and the single most damaging thing to get wrong in this
phase: **Gemini chunks are incremental, not cumulative.** Each chunk carries new
content to append. Text parts are fragments the client concatenates; structural
parts like `functionCall` arrive whole in one chunk. An earlier spec draft
described them as whole-part snapshots needing a diff against the previous
chunk, and implementing that against append semantics produces garbage — every
fragment emitted as though it were the entire message so far.

So: iterate each chunk's parts and emit deltas directly. A `functionCall`
becomes a block start, one full-input delta, and a block stop, all from the one
chunk it arrived in.

`usageMetadata` appears on interim chunks as well as the last, and the last is
authoritative — which it becomes automatically, because `exec.applyUsage`
assigns.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/gemini/stream_test.go`:

```go
package gemini

import (
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func collect(t *testing.T, body string) ([]ir.StreamEvent, error) {
	t.Helper()
	var (
		evs  []ir.StreamEvent
		last error
	)
	for ev, err := range ParseStream(strings.NewReader(body), 1<<20) {
		if err != nil {
			last = err
			break
		}
		evs = append(evs, ev)
	}
	return evs, last
}

func data(chunk string) string { return "data: " + chunk + "\n\n" }

func TestParseStreamAppendsTextFragments(t *testing.T) {
	body := data(`{"responseId":"r1","modelVersion":"gemini-2.0-flash","candidates":[{"content":{"parts":[{"text":"Hel"}]}}]}`) +
		data(`{"candidates":[{"content":{"parts":[{"text":"lo"}]}}]}`) +
		data(`{"candidates":[{"content":{"parts":[]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}`)

	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].Type != ir.EventMessageStart || evs[0].ID != "r1" || evs[0].Model != "gemini-2.0-flash" {
		t.Fatalf("first event = %+v", evs[0])
	}

	var text strings.Builder
	for _, ev := range evs {
		if ev.Type == ir.EventContentDelta && ev.Delta.Type == ir.BlockText {
			text.WriteString(ev.Delta.Text)
		}
	}
	if text.String() != "Hello" {
		t.Errorf("text = %q; fragments append, they do not replace", text.String())
	}
	last := evs[len(evs)-1]
	if last.Type != ir.EventMessageStop || last.StopReason != ir.StopEndTurn {
		t.Errorf("last event = %+v", last)
	}
}

func TestParseStreamOpensOneTextBlockOnly(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[{"text":"a"}]}}]}`) +
		data(`{"candidates":[{"content":{"parts":[{"text":"b"}]}}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	starts := 0
	for _, ev := range evs {
		if ev.Type == ir.EventBlockStart && ev.Delta.Type == ir.BlockText {
			starts++
		}
	}
	if starts != 1 {
		t.Errorf("text block starts = %d, want 1; the fragments belong to one block", starts)
	}
}

func TestParseStreamEmitsAFunctionCallWhole(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[
		{"functionCall":{"id":"call_a","name":"f","args":{"x":1}}}]},"finishReason":"STOP"}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	var start, delta, stop bool
	for _, ev := range evs {
		switch {
		case ev.Type == ir.EventBlockStart && ev.Delta.Type == ir.BlockToolUse:
			start = true
			if ev.Delta.ToolID != "call_a" || ev.Delta.ToolName != "f" {
				t.Errorf("block start = %+v", ev.Delta)
			}
		case ev.Type == ir.EventContentDelta && ev.Delta.Type == ir.BlockToolUse:
			delta = true
			if ev.Delta.ToolInput != `{"x":1}` {
				t.Errorf("tool input = %q; it arrives whole, not fragmented", ev.Delta.ToolInput)
			}
		case ev.Type == ir.EventBlockStop:
			stop = true
		}
	}
	if !start || !delta || !stop {
		t.Fatalf("events = %+v", evs)
	}
	last := evs[len(evs)-1]
	if last.StopReason != ir.StopToolUse {
		t.Errorf("stop = %q; STOP with a functionCall means tool use", last.StopReason)
	}
}

func TestParseStreamCarriesThoughtsAndSignatures(t *testing.T) {
	body := data(`{"candidates":[{"content":{"parts":[
		{"text":"weighing","thought":true},
		{"text":"","thought":true,"thoughtSignature":"sig-1"}]}}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatal(err)
	}
	var sawText, sawSig bool
	for _, ev := range evs {
		if ev.Type != ir.EventContentDelta || ev.Delta.Type != ir.BlockThinking {
			continue
		}
		if ev.Delta.Thinking == "weighing" {
			sawText = true
		}
		if ev.Delta.Signature == "sig-1" {
			sawSig = true
		}
	}
	if !sawText || !sawSig {
		t.Fatalf("events = %+v", evs)
	}
}

func TestParseStreamReportsABlockedPrompt(t *testing.T) {
	_, err := collect(t, data(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[]}`))
	var e *ir.Error
	if !errors.As(err, &e) || e.Type != ir.ErrContentFilter {
		t.Fatalf("err = %v, want a content-filter *ir.Error", err)
	}
}

func TestParseStreamWarnsOnAnUnknownFinishReason(t *testing.T) {
	evs, err := collect(t, data(`{"candidates":[{"content":{"parts":[{"text":"hi"}]},
		"finishReason":"SOMETHING_NEW"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	last := evs[len(evs)-1]
	if last.Type != ir.EventMessageStop || last.StopReason != ir.StopEndTurn {
		t.Fatalf("last event = %+v", last)
	}
	if len(last.Warnings) != 1 || last.Warnings[0].Field != "finishReason" {
		t.Errorf("warnings = %+v; a stream must not lose what the unary path records",
			last.Warnings)
	}
}

func TestParseStreamIgnoresAnUnparseableChunk(t *testing.T) {
	body := "data: {not json\n\n" + data(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	evs, err := collect(t, body)
	if err != nil {
		t.Fatalf("a bad chunk must not kill the stream: %v", err)
	}
	if len(evs) == 0 {
		t.Fatal("the good chunk was lost")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ -run ParseStream`
Expected: FAIL — undefined.

- [ ] **Step 3: Write the stream parser**

Create `internal/adapter/gemini/stream.go`:

```go
package gemini

import (
	"encoding/json"
	"errors"
	"io"
	"iter"
	"sort"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// ParseStream reads Gemini's alt=sse stream.
//
// Chunks are incremental: every chunk carries new content to append. Text parts
// are fragments and structural parts arrive whole, so the parts are walked once
// and emitted as deltas. Diffing successive chunks — which an earlier draft of
// the spec described — produces garbage against these semantics.
func ParseStream(r io.Reader, maxLine int) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		reader := sse.NewReader(r, maxLine)
		var (
			started    bool
			textIdx    = -1
			thoughtIdx = -1
			nextIdx    int
			open       = map[int]ir.BlockType{}
		)

		closeAll := func() bool {
			idxs := make([]int, 0, len(open))
			for idx := range open {
				idxs = append(idxs, idx)
			}
			// Ascending, because map iteration order is not deterministic and
			// the event sequence has to be.
			sort.Ints(idxs)
			for _, idx := range idxs {
				if !yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: idx}, nil) {
					return false
				}
			}
			open = map[int]ir.BlockType{}
			textIdx, thoughtIdx = -1, -1
			return true
		}

		// openBlock returns the index for a persistent kind, opening it once.
		openBlock := func(slot *int, kind ir.BlockType) (int, bool) {
			if *slot >= 0 {
				return *slot, true
			}
			*slot = nextIdx
			nextIdx++
			open[*slot] = kind
			return *slot, yield(ir.StreamEvent{
				Type: ir.EventBlockStart, Index: *slot, Delta: &ir.Delta{Type: kind},
			}, nil)
		}

		for {
			raw, err := reader.Next()
			if errors.Is(err, io.EOF) {
				closeAll()
				return
			}
			if err != nil {
				yield(ir.StreamEvent{}, err)
				return
			}
			if raw.Data == "" || raw.Data == sse.Done {
				continue
			}

			var chunk wireResponse
			if json.Unmarshal([]byte(raw.Data), &chunk) != nil {
				continue // a chunk we cannot parse is not a reason to kill the stream
			}

			if len(chunk.Candidates) == 0 && chunk.PromptFeedback != nil &&
				chunk.PromptFeedback.BlockReason != "" {
				yield(ir.StreamEvent{}, &ir.Error{
					Type:    ir.ErrContentFilter,
					Message: "the prompt was blocked: " + chunk.PromptFeedback.BlockReason,
					Code:    chunk.PromptFeedback.BlockReason,
				})
				return
			}

			if !started {
				started = true
				if !yield(ir.StreamEvent{
					Type: ir.EventMessageStart, ID: chunk.ResponseID, Model: chunk.ModelVersion,
				}, nil) {
					return
				}
			}

			if chunk.UsageMetadata.TotalTokenCount > 0 || chunk.UsageMetadata.PromptTokenCount > 0 {
				u := chunk.UsageMetadata.toIR()
				if !yield(ir.StreamEvent{Type: ir.EventMessageDelta, Usage: &u}, nil) {
					return
				}
			}
			if len(chunk.Candidates) == 0 {
				continue
			}

			c := chunk.Candidates[0]
			hasCall := false
			for _, p := range c.Content.Parts {
				switch {
				case p.FunctionCall != nil:
					hasCall = true
					args := p.FunctionCall.Args
					if len(args) == 0 {
						args = json.RawMessage(`{}`)
					}
					idx := nextIdx
					nextIdx++
					d := &ir.Delta{
						Type: ir.BlockToolUse,
						ToolID: p.FunctionCall.ID, ToolName: p.FunctionCall.Name,
					}
					if !yield(ir.StreamEvent{Type: ir.EventBlockStart, Index: idx, Delta: d}, nil) {
						return
					}
					full := &ir.Delta{
						Type: ir.BlockToolUse, ToolID: d.ToolID, ToolName: d.ToolName,
						ToolInput: string(args),
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx, Delta: full}, nil) {
						return
					}
					if !yield(ir.StreamEvent{Type: ir.EventBlockStop, Index: idx}, nil) {
						return
					}

				case p.Thought:
					idx, ok := openBlock(&thoughtIdx, ir.BlockThinking)
					if !ok {
						return
					}
					d := &ir.Delta{Type: ir.BlockThinking, Thinking: p.Text}
					if p.ThoughtSignature != "" {
						// A signature arrives on its own delta and carries no
						// text, so an empty thought block never commits the
						// response on a signature alone.
						d = &ir.Delta{Type: ir.BlockThinking, Signature: p.ThoughtSignature}
					}
					if !yield(ir.StreamEvent{Type: ir.EventContentDelta, Index: idx, Delta: d}, nil) {
						return
					}

				case p.Text != "":
					idx, ok := openBlock(&textIdx, ir.BlockText)
					if !ok {
						return
					}
					if !yield(ir.StreamEvent{
						Type: ir.EventContentDelta, Index: idx,
						Delta: &ir.Delta{Type: ir.BlockText, Text: p.Text},
					}, nil) {
						return
					}
				}
			}

			if c.FinishReason != "" {
				if !closeAll() {
					return
				}
				sr, known := finishReason(c.FinishReason, hasCall)
				stop := ir.StreamEvent{Type: ir.EventMessageStop, StopReason: sr}
				if !known {
					// Spec §4.5: degrade, but never silently. The unary path
					// puts this on ir.Response.Warnings; streaming has its own
					// channel for exactly this reason.
					stop.Warnings = append(stop.Warnings, ir.Warning{
						Field: "finishReason", Target: targetName,
						Reason: "unrecognized value " + c.FinishReason + "; reported as end_turn",
					})
				}
				if !yield(stop, nil) {
					return
				}
				return
			}
		}
	}
}
```

A `functionCall` that arrives in the same chunk as the finish reason sets
`hasCall` before `finishReason` is consulted, which is what makes
`TestParseStreamEmitsAFunctionCallWhole` report `tool_use` rather than
`end_turn`.

- [ ] **Step 4: Add the adapter type**

Create `internal/adapter/gemini/adapter.go`:

```go
package gemini

import (
	"context"
	"io"
	"iter"
	"net/http"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Adapter wraps a Fetcher, which BuildRequest needs in order to inline media
// the Gemini API will not fetch for itself.
type Adapter struct{ f *Fetcher }

func New() *Adapter { return &Adapter{f: NewFetcher()} }

// NewWithFetcher lets a test supply a bounded or stubbed fetcher.
func NewWithFetcher(f *Fetcher) *Adapter { return &Adapter{f: f} }

func (a *Adapter) Kind() string { return "gemini" }

func (a *Adapter) BuildRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, []ir.Warning, error) {
	return a.f.BuildRequest(ctx, t, req)
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

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/gemini/ -v`
Expected: PASS.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/gemini/
git commit -m "feat(gemini): parse the incremental stream"
```

---

### Task 26: The Gemini edge parses `/v1beta` requests

**Files:**
- Create: `internal/edge/gemini/parse.go`
- Test: `internal/edge/gemini/parse_test.go`

**Interfaces:**
- Consumes: `edge.Passthrough` from Task 2; `xlate.SyntheticToolCallID` from Task 7.
- Produces: `gemini.ExtractModel(segment string) (model, method string)`; `gemini.ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error)`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

Spec §3.1 gives three rules for pulling the model out of the path, and one of
them has already been done for you by the router. This was verified against Go
1.26 before writing the plan:

```
POST /v1beta/models/{model}
  /v1beta/models/gemini-2.0-flash:generateContent
    → PathValue("model") == "gemini-2.0-flash:generateContent"
  /v1beta/models/openrouter%2Fanthropic%2Fclaude-sonnet-4.5:generateContent
    → PathValue("model") == "openrouter/anthropic/claude-sonnet-4.5:generateContent"
```

`net/http`'s wildcard **already percent-decodes**, and an encoded slash does not
split the segment. Decoding again in the handler would corrupt any name
containing a literal `%`. What remains is: split on the **last** colon to
separate the method suffix, then strip a leading `models/` resource prefix.

The pattern must be `{model}` alone. A wildcard has to occupy a whole path
segment, so `{model}:generateContent` is not a legal pattern and the method is
dispatched inside the handler.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/gemini/parse_test.go`:

```go
package gemini

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestExtractModelSplitsOnTheLastColon(t *testing.T) {
	cases := []struct {
		in     string
		model  string
		method string
	}{
		{"gemini-2.0-flash:generateContent", "gemini-2.0-flash", "generateContent"},
		{"models/gemini-2.0-flash:streamGenerateContent", "gemini-2.0-flash", "streamGenerateContent"},
		{"openrouter/anthropic/claude-sonnet-4.5:generateContent",
			"openrouter/anthropic/claude-sonnet-4.5", "generateContent"},
		{"fast:coding:countTokens", "fast:coding", "countTokens"},
		{"gemini-2.0-flash", "gemini-2.0-flash", ""},
		{"models/fast", "fast", ""},
	}
	for _, tc := range cases {
		model, method := ExtractModel(tc.in)
		if model != tc.model || method != tc.method {
			t.Errorf("ExtractModel(%q) = %q, %q; want %q, %q", tc.in, model, method, tc.model, tc.method)
		}
	}
}

// request builds a request whose PathValue is set the way the ServeMux pattern
// "POST /v1beta/models/{model}" sets it.
func request(t *testing.T, segment, query, body string) *http.Request {
	t.Helper()
	target := "/v1beta/models/" + segment
	if query != "" {
		target += "?" + query
	}
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	r.SetPathValue("model", segment)
	return r
}

func parsed(t *testing.T, segment, query, body string) *ir.Request {
	t.Helper()
	req, pt, err := ParseRequest(request(t, segment, query, body), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt == nil || pt.Surface != ir.SurfaceLLM {
		t.Fatalf("passthrough = %+v", pt)
	}
	if pt.ModelField != "" {
		t.Errorf("ModelField = %q; the Gemini model lives in the URL", pt.ModelField)
	}
	return req
}

func TestParseRequestTakesTheModelFromThePath(t *testing.T) {
	req := parsed(t, "models/gemini-2.0-flash:generateContent", "",
		`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	if req.Model != "gemini-2.0-flash" {
		t.Errorf("model = %q", req.Model)
	}
	if req.Stream {
		t.Error("generateContent is not a streaming method")
	}
}

func TestParseRequestMarksStreamGenerateContent(t *testing.T) {
	req := parsed(t, "gemini-2.0-flash:streamGenerateContent", "alt=sse", `{"contents":[]}`)
	if !req.Stream {
		t.Error("streamGenerateContent must set Stream")
	}
}

func TestParseRequestMapsRolesAndParts(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[
		{"role":"user","parts":[
			{"text":"look"},
			{"inlineData":{"mimeType":"image/png","data":"AAAA"}},
			{"fileData":{"mimeType":"video/mp4","fileUri":"https://youtu.be/abc"}}]},
		{"role":"model","parts":[
			{"text":"weighing","thought":true,"thoughtSignature":"sig-1"},
			{"functionCall":{"name":"f","args":{"x":1}}}]}]}`)

	if req.Messages[0].Role != ir.RoleUser || req.Messages[1].Role != ir.RoleAssistant {
		t.Fatalf("roles = %q, %q; model maps to assistant", req.Messages[0].Role, req.Messages[1].Role)
	}
	u := req.Messages[0].Content
	if u[1].Type != ir.BlockImage || u[1].Media.Data != "AAAA" {
		t.Errorf("inlineData = %+v", u[1])
	}
	if u[2].Media.URL != "https://youtu.be/abc" {
		t.Errorf("fileData = %+v", u[2])
	}
	m := req.Messages[1].Content
	if m[0].Type != ir.BlockThinking || m[0].Thinking.Signature != "sig-1" {
		t.Errorf("thought = %+v", m[0])
	}
	if m[1].ToolUse == nil || m[1].ToolUse.ID == "" {
		t.Errorf("functionCall = %+v; an absent id must be synthesized", m[1].ToolUse)
	}
}

func TestParseRequestMatchesFunctionResponsesPositionally(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[
		{"role":"model","parts":[
			{"functionCall":{"name":"lookup","args":{"city":"Oslo"}}},
			{"functionCall":{"name":"lookup","args":{"city":"Bergen"}}}]},
		{"role":"user","parts":[
			{"functionResponse":{"name":"lookup","response":{"result":"clear"}}},
			{"functionResponse":{"name":"lookup","response":{"result":"rain"}}}]}]}`)

	calls := req.Messages[0].Content
	resps := req.Messages[1].Content
	if resps[0].ToolResult.ToolUseID != calls[0].ToolUse.ID {
		t.Errorf("first response paired with %q, want %q; matching is positional",
			resps[0].ToolResult.ToolUseID, calls[0].ToolUse.ID)
	}
	if resps[1].ToolResult.ToolUseID != calls[1].ToolUse.ID {
		t.Errorf("second response paired with %q, want %q",
			resps[1].ToolResult.ToolUseID, calls[1].ToolUse.ID)
	}
	if resps[0].ToolResult.Text() != `{"result":"clear"}` {
		t.Errorf("response body = %q", resps[0].ToolResult.Text())
	}
}

func TestParseRequestReadsConfigAndTools(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[],
		"systemInstruction":{"parts":[{"text":"be terse"}]},
		"tools":[{"functionDeclarations":[
			{"name":"f","description":"d","parameters":{"type":"object"}}]}],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["f"]}},
		"safetySettings":[{"category":"HARM_CATEGORY_HARASSMENT","threshold":"BLOCK_NONE"}],
		"generationConfig":{"temperature":0.7,"topP":0.9,"topK":40,"maxOutputTokens":512,
			"stopSequences":["END"],"responseMimeType":"application/json",
			"responseSchema":{"type":"object"},
			"thinkingConfig":{"thinkingBudget":8000,"includeThoughts":true}}}`)

	if len(req.System) != 1 || req.System[0].Text != "be terse" {
		t.Errorf("system = %+v", req.System)
	}
	if len(req.Tools) != 1 || string(req.Tools[0].Schema) != `{"type":"object"}` {
		t.Errorf("tools = %+v", req.Tools)
	}
	if req.ToolChoice == nil || req.ToolChoice.Mode != "tool" || req.ToolChoice.Name != "f" {
		t.Errorf("tool_choice = %+v; ANY with one allowed name is forcing that tool", req.ToolChoice)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 || req.TopK == nil || *req.TopK != 40 {
		t.Errorf("sampling = %v %v", req.Temperature, req.TopK)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 512 {
		t.Errorf("max tokens = %v", req.MaxTokens)
	}
	if req.ResponseFormat == nil || string(req.ResponseFormat.Schema) != `{"type":"object"}` {
		t.Errorf("response format = %+v", req.ResponseFormat)
	}
	if req.Reasoning == nil || req.Reasoning.Budget != 8000 {
		t.Errorf("reasoning = %+v", req.Reasoning)
	}
	if len(req.Safety) != 1 || req.Safety[0].Threshold != "BLOCK_NONE" {
		t.Errorf("safety = %+v", req.Safety)
	}
}

func TestParseRequestModeAnyWithoutNamesIsAny(t *testing.T) {
	req := parsed(t, "m:generateContent", "", `{"contents":[],
		"toolConfig":{"functionCallingConfig":{"mode":"ANY"}}}`)
	if req.ToolChoice == nil || req.ToolChoice.Mode != "any" {
		t.Errorf("tool_choice = %+v", req.ToolChoice)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/`
Expected: FAIL — the package does not exist.

- [ ] **Step 3: Write the parser**

Create `internal/edge/gemini/parse.go`:

```go
// Package gemini implements the Google Gemini inbound dialect.
package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter/xlate"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

// ExtractModel splits the single path segment Gemini puts the model in.
//
// net/http's {model} wildcard already percent-decodes, and an encoded slash
// does not split the segment — so `openrouter%2Fanthropic%2Fclaude-sonnet-4.5`
// arrives here with real slashes and needs no further decoding. Decoding again
// would corrupt a name containing a literal percent sign.
//
// The split is on the LAST colon: an alias may contain one, the method suffix
// is always final.
func ExtractModel(segment string) (model, method string) {
	model = strings.TrimPrefix(segment, "models/")
	if i := strings.LastIndexByte(model, ':'); i >= 0 {
		return model[:i], model[i+1:]
	}
	return model, ""
}

type wirePart struct {
	Text             string `json:"text"`
	Thought          bool   `json:"thought"`
	ThoughtSignature string `json:"thoughtSignature"`
	InlineData       *struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"`
	} `json:"inlineData"`
	FileData *struct {
		MimeType string `json:"mimeType"`
		FileURI  string `json:"fileUri"`
	} `json:"fileData"`
	FunctionCall *struct {
		ID   string          `json:"id"`
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	} `json:"functionCall"`
	FunctionResponse *struct {
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Response json.RawMessage `json:"response"`
	} `json:"functionResponse"`
}

type wireContent struct {
	Role  string     `json:"role"`
	Parts []wirePart `json:"parts"`
}

type wireRequest struct {
	Contents          []wireContent `json:"contents"`
	SystemInstruction *wireContent  `json:"systemInstruction"`
	Tools             []struct {
		FunctionDeclarations []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"functionDeclarations"`
	} `json:"tools"`
	ToolConfig *struct {
		FunctionCallingConfig *struct {
			Mode                 string   `json:"mode"`
			AllowedFunctionNames []string `json:"allowedFunctionNames"`
		} `json:"functionCallingConfig"`
	} `json:"toolConfig"`
	SafetySettings []struct {
		Category  string `json:"category"`
		Threshold string `json:"threshold"`
	} `json:"safetySettings"`
	GenerationConfig *struct {
		Temperature      *float64        `json:"temperature"`
		TopP             *float64        `json:"topP"`
		TopK             *int            `json:"topK"`
		MaxOutputTokens  *int            `json:"maxOutputTokens"`
		StopSequences    []string        `json:"stopSequences"`
		ResponseMimeType string          `json:"responseMimeType"`
		ResponseSchema   json.RawMessage `json:"responseSchema"`
		ThinkingConfig   *struct {
			ThinkingBudget  int  `json:"thinkingBudget"`
			IncludeThoughts bool `json:"includeThoughts"`
		} `json:"thinkingConfig"`
	} `json:"generationConfig"`
}

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

	model, method := ExtractModel(r.PathValue("model"))
	req := &ir.Request{Model: model, Stream: method == "streamGenerateContent"}

	if w.SystemInstruction != nil {
		for _, p := range w.SystemInstruction.Parts {
			if p.Text != "" {
				req.System = append(req.System, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
			}
		}
	}

	// The ids of the previous model turn's calls, in order. A functionResponse
	// is matched to one of them by position, never by name: parallel calls to
	// one function are otherwise indistinguishable.
	var pending []string
	for turn, c := range w.Contents {
		role := ir.RoleUser
		if c.Role == "model" {
			role = ir.RoleAssistant
		}
		blocks, calls := parseParts(turn, c.Parts, pending)
		if len(calls) > 0 {
			pending = calls
		}
		req.Messages = append(req.Messages, ir.Message{Role: role, Content: blocks})
	}

	for _, t := range w.Tools {
		for _, d := range t.FunctionDeclarations {
			req.Tools = append(req.Tools, ir.Tool{
				Name: d.Name, Description: d.Description, Schema: d.Parameters,
			})
		}
	}
	if w.ToolConfig != nil && w.ToolConfig.FunctionCallingConfig != nil {
		cfg := w.ToolConfig.FunctionCallingConfig
		switch cfg.Mode {
		case "NONE":
			req.ToolChoice = &ir.ToolChoice{Mode: "none"}
		case "ANY":
			// ANY plus exactly one allowed name is how Gemini spells "use this
			// tool", which the IR models as the distinct mode "tool".
			if len(cfg.AllowedFunctionNames) == 1 {
				req.ToolChoice = &ir.ToolChoice{Mode: "tool", Name: cfg.AllowedFunctionNames[0]}
			} else {
				req.ToolChoice = &ir.ToolChoice{Mode: "any"}
			}
		default:
			req.ToolChoice = &ir.ToolChoice{Mode: "auto"}
		}
	}
	for _, s := range w.SafetySettings {
		req.Safety = append(req.Safety, ir.SafetySetting{Category: s.Category, Threshold: s.Threshold})
	}

	if g := w.GenerationConfig; g != nil {
		req.Temperature = g.Temperature
		req.TopP = g.TopP
		req.TopK = g.TopK
		req.MaxTokens = g.MaxOutputTokens
		req.StopSequences = g.StopSequences
		if len(g.ResponseSchema) > 0 {
			req.ResponseFormat = &ir.ResponseFormat{Type: "json_schema", Schema: g.ResponseSchema}
		}
		if g.ThinkingConfig != nil && g.ThinkingConfig.ThinkingBudget > 0 {
			req.Reasoning = &ir.Reasoning{Budget: g.ThinkingConfig.ThinkingBudget}
		}
	}

	// ModelField is empty: the Gemini model lives in the URL, which is why
	// Phase 9's passthrough rewrites the path rather than the body.
	return req, &edge.Passthrough{Body: body, ModelField: "", Surface: ir.SurfaceLLM}, nil
}

// parseParts converts one content entry, returning its blocks and the ids of
// any function calls it made.
func parseParts(turn int, parts []wirePart, pending []string) ([]ir.ContentBlock, []string) {
	var (
		out     []ir.ContentBlock
		calls   []string
		results int
	)
	for _, p := range parts {
		switch {
		case p.FunctionCall != nil:
			id := p.FunctionCall.ID
			if id == "" {
				id = xlate.SyntheticToolCallID(turn, len(calls))
			}
			calls = append(calls, id)
			args := p.FunctionCall.Args
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out = append(out, ir.ContentBlock{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: id, Name: p.FunctionCall.Name, Input: args,
			}})

		case p.FunctionResponse != nil:
			id := p.FunctionResponse.ID
			if id == "" && results < len(pending) {
				id = pending[results]
			}
			results++
			out = append(out, ir.ContentBlock{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
				ToolUseID: id,
				Content: []ir.ContentBlock{{
					Type: ir.BlockText, Text: string(p.FunctionResponse.Response),
				}},
			}})

		case p.Thought:
			out = append(out, ir.ContentBlock{Type: ir.BlockThinking, Thinking: &ir.Thinking{
				Text: p.Text, Signature: p.ThoughtSignature,
			}})

		case p.InlineData != nil:
			out = append(out, ir.ContentBlock{Type: mediaKind(p.InlineData.MimeType),
				Media: &ir.Media{MIME: p.InlineData.MimeType, Data: p.InlineData.Data}})

		case p.FileData != nil:
			out = append(out, ir.ContentBlock{Type: mediaKind(p.FileData.MimeType),
				Media: &ir.Media{MIME: p.FileData.MimeType, URL: p.FileData.FileURI}})

		case p.Text != "":
			out = append(out, ir.ContentBlock{Type: ir.BlockText, Text: p.Text})
		}
	}
	return out, calls
}

// mediaKind picks the IR block type from a MIME type. Gemini has one part shape
// for every medium, so the type is the only signal, and the router's vision
// capability check reads it.
func mediaKind(mime string) ir.BlockType {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return ir.BlockImage
	case strings.HasPrefix(mime, "audio/"):
		return ir.BlockAudio
	default:
		return ir.BlockDocument
	}
}
```

`video/mp4` therefore parses as a document block, which is what
`TestParseRequestMapsRolesAndParts` asserts by checking only the media URL.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/ -v`
Expected: PASS.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/gemini/
git commit -m "feat(edge/gemini): parse generateContent requests"
```

---

### Task 27: The Gemini edge writes responses and errors

**Files:**
- Create: `internal/edge/gemini/write.go`, `internal/edge/gemini/dialect.go`
- Test: `internal/edge/gemini/write_test.go`

**Interfaces:**
- Consumes: `edge.Dialect` including `ProxyToken` from Task 5.
- Produces: `gemini.New() *Dialect` and `gemini.NewFor(r *http.Request) *Dialect` satisfying `edge.Dialect`; `gemini.WriteResponse`, `gemini.WriteError`, `gemini.responseParts([]ir.ContentBlock) []any`, `gemini.finishReasonWire(ir.StopReason) string`, `gemini.usageBody(ir.Usage) map[string]any`, `gemini.errorShape(ir.ErrorType) (string, int)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: a Dialect is already constructed per use, so a constructor that reads the request is the same pattern

`NewFor` exists because spec §3.2 makes the streaming wire form a property of
the **request** — `?alt=sse` versus the chunked JSON array — while
`edge.Dialect.WriteStream` sees only the writer and the events. The dialect is a
two-field value, so constructing one per request costs nothing and keeps the
alternative, a request-scoped flag threaded through `exec`, off the table.

Google's error vocabulary is a status string alongside the code, and Gemini CLI
switches on the string. `UNAVAILABLE` and `RESOURCE_EXHAUSTED` are the two it
retries.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/gemini/write_test.go`:

```go
package gemini

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func written(t *testing.T, resp *ir.Response) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := WriteResponse(rec, resp); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWriteResponseProducesTheCandidateShape(t *testing.T) {
	got := written(t, &ir.Response{
		ID: "r1", Model: "gemini-2.0-flash", StopReason: ir.StopToolUse,
		Content: []ir.ContentBlock{
			{Type: ir.BlockThinking, Thinking: &ir.Thinking{Text: "weighing", Signature: "sig-1"}},
			{Type: ir.BlockText, Text: "calling"},
			{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
				ID: "call_a", Name: "f", Input: json.RawMessage(`{"x":1}`)}},
		},
		Usage: ir.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, ReasoningTokens: 6},
	})
	cands := got["candidates"].([]any)
	c := cands[0].(map[string]any)
	if c["finishReason"] != "STOP" {
		t.Errorf("finishReason = %v; Gemini has no tool-use reason", c["finishReason"])
	}
	content := c["content"].(map[string]any)
	if content["role"] != "model" {
		t.Errorf("role = %v", content["role"])
	}
	ps := content["parts"].([]any)
	if len(ps) != 3 {
		t.Fatalf("parts = %v", ps)
	}
	if ps[0].(map[string]any)["thought"] != true ||
		ps[0].(map[string]any)["thoughtSignature"] != "sig-1" {
		t.Errorf("thought part = %v", ps[0])
	}
	fc := ps[2].(map[string]any)["functionCall"].(map[string]any)
	if fc["name"] != "f" || fc["id"] != "call_a" {
		t.Errorf("functionCall = %v", fc)
	}
	if _, ok := fc["args"].(map[string]any); !ok {
		t.Errorf("args = %#v; Gemini takes an object", fc["args"])
	}
	u := got["usageMetadata"].(map[string]any)
	if u["promptTokenCount"].(float64) != 10 || u["candidatesTokenCount"].(float64) != 4 ||
		u["cachedContentTokenCount"].(float64) != 3 || u["thoughtsTokenCount"].(float64) != 6 ||
		u["totalTokenCount"].(float64) != 14 {
		t.Errorf("usageMetadata = %v", u)
	}
	if got["modelVersion"] != "gemini-2.0-flash" || got["responseId"] != "r1" {
		t.Errorf("envelope = %v", got)
	}
}

func TestWriteResponseMapsFinishReasons(t *testing.T) {
	cases := map[ir.StopReason]string{
		ir.StopEndTurn:       "STOP",
		ir.StopToolUse:       "STOP",
		ir.StopStopSequence:  "STOP",
		ir.StopPauseTurn:     "STOP",
		ir.StopMaxTokens:     "MAX_TOKENS",
		ir.StopContentFilter: "SAFETY",
		ir.StopError:         "OTHER",
	}
	for in, want := range cases {
		got := written(t, &ir.Response{ID: "r", Model: "m", StopReason: in})
		c := got["candidates"].([]any)[0].(map[string]any)
		if c["finishReason"] != want {
			t.Errorf("%q -> %v, want %s", in, c["finishReason"], want)
		}
	}
}

func TestWriteErrorUsesGoogleStatusStrings(t *testing.T) {
	cases := []struct {
		in     ir.ErrorType
		status int
		name   string
	}{
		{ir.ErrInvalidRequest, 400, "INVALID_ARGUMENT"},
		{ir.ErrContentFilter, 400, "INVALID_ARGUMENT"},
		{ir.ErrAuthentication, 401, "UNAUTHENTICATED"},
		{ir.ErrPermission, 403, "PERMISSION_DENIED"},
		{ir.ErrNotFound, 404, "NOT_FOUND"},
		{ir.ErrRateLimit, 429, "RESOURCE_EXHAUSTED"},
		{ir.ErrOverloaded, 503, "UNAVAILABLE"},
		{ir.ErrAPI, 500, "INTERNAL"},
		{ir.ErrDarkrouter, 500, "INTERNAL"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		if err := WriteError(rec, &ir.Error{Type: tc.in, Message: "nope"}); err != nil {
			t.Fatal(err)
		}
		if rec.Code != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.in, rec.Code, tc.status)
		}
		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		e := got["error"].(map[string]any)
		if e["status"] != tc.name || int(e["code"].(float64)) != tc.status {
			t.Errorf("%s: error = %v", tc.in, e)
		}
	}
}

func TestProxyTokenAcceptsHeaderOrQuery(t *testing.T) {
	d := New()

	r := httptest.NewRequest("POST", "/v1beta/models/m:generateContent", nil)
	r.Header.Set("x-goog-api-key", "AIza-header")
	if got := d.ProxyToken(r); got != "AIza-header" {
		t.Errorf("header = %q", got)
	}

	r2 := httptest.NewRequest("POST", "/v1beta/models/m:generateContent?key=AIza-query", nil)
	if got := d.ProxyToken(r2); got != "AIza-query" {
		t.Errorf("query = %q", got)
	}

	r3 := httptest.NewRequest("POST", "/v1beta/models/m:generateContent?key=AIza-query", nil)
	r3.Header.Set("x-goog-api-key", "AIza-header")
	if got := d.ProxyToken(r3); got != "AIza-header" {
		t.Errorf("both = %q; the header wins", got)
	}
}

func TestNewForReadsTheAltParameter(t *testing.T) {
	plain := httptest.NewRequest("POST", "/v1beta/models/m:streamGenerateContent", nil)
	if NewFor(plain).SSE {
		t.Error("no alt parameter means the JSON-array form")
	}
	sse := httptest.NewRequest("POST", "/v1beta/models/m:streamGenerateContent?alt=sse", nil)
	if !NewFor(sse).SSE {
		t.Error("alt=sse selects the event-stream form")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/ -run 'Write|ProxyToken|NewFor'`
Expected: FAIL — undefined.

- [ ] **Step 3: Write the response and error writers**

Create `internal/edge/gemini/write.go`:

```go
package gemini

import (
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/ir"
)

// finishReasonWire maps the IR onto Gemini's enum. Gemini has no tool-use
// reason: a turn that called a tool finishes with STOP, and the functionCall
// part is the only signal. That asymmetry is why the adapter has to infer it
// coming back the other way.
func finishReasonWire(s ir.StopReason) string {
	switch s {
	case ir.StopMaxTokens:
		return "MAX_TOKENS"
	case ir.StopContentFilter:
		return "SAFETY"
	case ir.StopError:
		return "OTHER"
	default:
		return "STOP"
	}
}

// responseParts renders assistant content. A response never carries tool
// results, images, or cache markers, so the set is text, thoughts, and calls.
func responseParts(blocks []ir.ContentBlock) []any {
	out := []any{}
	for _, b := range blocks {
		switch b.Type {
		case ir.BlockText:
			out = append(out, map[string]any{"text": b.Text})
		case ir.BlockThinking:
			if b.Thinking == nil {
				continue
			}
			p := map[string]any{"text": b.Thinking.Text, "thought": true}
			if b.Thinking.Signature != "" {
				p["thoughtSignature"] = b.Thinking.Signature
			}
			out = append(out, p)
		case ir.BlockToolUse:
			if b.ToolUse == nil {
				continue
			}
			args := b.ToolUse.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			call := map[string]any{"name": b.ToolUse.Name, "args": args}
			if b.ToolUse.ID != "" {
				call["id"] = b.ToolUse.ID
			}
			out = append(out, map[string]any{"functionCall": call})
		}
	}
	return out
}

func usageBody(u ir.Usage) map[string]any {
	return map[string]any{
		"promptTokenCount":        u.InputTokens,
		"candidatesTokenCount":    u.OutputTokens,
		"cachedContentTokenCount": u.CacheReadTokens,
		"thoughtsTokenCount":      u.ReasoningTokens,
		"totalTokenCount":         u.InputTokens + u.OutputTokens,
	}
}

// candidate builds the single candidate Darkrouter ever returns. Routing picks
// one target and one completion; there is no n>1 concept to surface.
func candidate(blocks []ir.ContentBlock, stop ir.StopReason) map[string]any {
	return map[string]any{
		"content":      map[string]any{"role": "model", "parts": responseParts(blocks)},
		"finishReason": finishReasonWire(stop),
		"index":        0,
	}
}

func WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	out := map[string]any{
		"candidates":    []any{candidate(resp.Content, resp.StopReason)},
		"usageMetadata": usageBody(resp.Usage),
		"modelVersion":  resp.Model,
		"responseId":    resp.ID,
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(out)
}

// errorShape maps a canonical error onto Google's status string and code.
// Gemini CLI switches on the string, and UNAVAILABLE and RESOURCE_EXHAUSTED are
// the two it retries.
func errorShape(t ir.ErrorType) (string, int) {
	switch t {
	case ir.ErrInvalidRequest, ir.ErrContentFilter:
		return "INVALID_ARGUMENT", http.StatusBadRequest
	case ir.ErrAuthentication:
		return "UNAUTHENTICATED", http.StatusUnauthorized
	case ir.ErrPermission:
		return "PERMISSION_DENIED", http.StatusForbidden
	case ir.ErrNotFound:
		return "NOT_FOUND", http.StatusNotFound
	case ir.ErrRateLimit:
		return "RESOURCE_EXHAUSTED", http.StatusTooManyRequests
	case ir.ErrOverloaded:
		return "UNAVAILABLE", http.StatusServiceUnavailable
	default:
		// 500 rather than the 502 the other dialects use: Google's vocabulary
		// has no gateway status, and a Gemini client would not recognize one.
		return "INTERNAL", http.StatusInternalServerError
	}
}

func WriteError(w http.ResponseWriter, e *ir.Error) error {
	name, status := errorShape(e.Type)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": status, "message": e.Message, "status": name},
	})
}
```

- [ ] **Step 4: Add the dialect type**

Create `internal/edge/gemini/dialect.go`:

```go
package gemini

import (
	"iter"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
)

// Dialect carries the one request-scoped decision this edge has to make:
// spec §3.2 lets a client ask for streaming as SSE or as a chunked JSON array,
// and WriteStream cannot see the request that chose.
type Dialect struct{ SSE bool }

func New() *Dialect { return &Dialect{} }

// NewFor reads ?alt=sse. Both client styles exist, so the absence of the
// parameter selects the JSON-array form rather than defaulting to SSE.
func NewFor(r *http.Request) *Dialect {
	return &Dialect{SSE: r.URL.Query().Get("alt") == "sse"}
}

func (d *Dialect) Name() string { return "gemini" }

// ProxyToken accepts both forms master design §13 lists. The header wins,
// because a key in the query string ends up in access logs.
func (d *Dialect) ProxyToken(r *http.Request) string {
	if k := strings.TrimSpace(r.Header.Get("x-goog-api-key")); k != "" {
		return k
	}
	return strings.TrimSpace(r.URL.Query().Get("key"))
}

func (d *Dialect) ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error) {
	return ParseRequest(r, maxBody)
}

func (d *Dialect) WriteResponse(w http.ResponseWriter, resp *ir.Response) error {
	return WriteResponse(w, resp)
}

func (d *Dialect) WriteStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error]) error {
	return writeStream(w, events, d.SSE)
}

func (d *Dialect) WriteError(w http.ResponseWriter, e *ir.Error) error {
	return WriteError(w, e)
}

var _ edge.Dialect = (*Dialect)(nil)
```

`writeStream` arrives in Task 28. Add a temporary stub at the bottom of
`write.go` to keep this task green, and delete it there:

```go
// writeStream is implemented in Task 28.
func writeStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error], asSSE bool) error {
	return WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: "streaming not implemented"})
}
```

Add `"iter"` to `write.go`'s imports for the stub.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/ -v`
Expected: PASS.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/gemini/
git commit -m "feat(edge/gemini): write candidates and errors"
```

---

### Task 28: The Gemini edge streams in both wire forms

**Files:**
- Create: `internal/edge/gemini/stream.go`
- Modify: `internal/edge/gemini/write.go` (delete the Task 27 stub)
- Test: `internal/edge/gemini/stream_test.go`

**Interfaces:**
- Consumes: `candidate`, `usageBody`, `finishReasonWire`, `responseParts` from Task 27; `sse.NewWriter` from Phase 1.
- Produces: `gemini.writeStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error], asSSE bool) error`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Two shapes, one event loop. `?alt=sse` frames each chunk as `data: {...}`;
without it the response is one JSON array written incrementally — `[`, then
chunks separated by commas, then `]`. Both are flushed per chunk, or the array
form buffers into a single write and time-to-first-token becomes
time-to-completion.

**Tool calls have to be reassembled.** Gemini sends a `functionCall` whole in
one chunk, while the IR carries arguments as fragments whenever the upstream was
OpenAI-compatible. The writer accumulates per block index and emits the call
when the block closes.

**Spec §4.9 gives Gemini the awkward error case.** Its SSE has no error event
type at all, so a failure after commit becomes a final chunk carrying a
`promptFeedback`-shaped object and a terminal `finishReason`. A content filter
reports `SAFETY`; anything else reports `OTHER`, because claiming a safety block
for an upstream timeout would send the client chasing its own prompt.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/gemini/stream_test.go`:

```go
package gemini

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func seq(events []ir.StreamEvent, final error) func(func(ir.StreamEvent, error) bool) {
	return func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	}
}

func sseChunks(t *testing.T, events []ir.StreamEvent, final error) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(events, final), true); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			t.Fatalf("chunk %q: %v", payload, err)
		}
		out = append(out, m)
	}
	return out
}

func arrayChunks(t *testing.T, events []ir.StreamEvent, final error) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(events, final), false); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}
	return out
}

func textEvents() []ir.StreamEvent {
	return []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r1", Model: "gemini-2.0-flash"},
		{Type: ir.EventBlockStart, Index: 0, Delta: &ir.Delta{Type: ir.BlockText}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "Hel"}},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "lo"}},
		{Type: ir.EventBlockStop, Index: 0},
		{Type: ir.EventMessageDelta, Usage: &ir.Usage{InputTokens: 3, OutputTokens: 2}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}
}

func TestWriteStreamSSEEmitsOneChunkPerFragment(t *testing.T) {
	got := sseChunks(t, textEvents(), nil)
	if len(got) != 3 {
		t.Fatalf("chunks = %d, want two fragments and a terminal chunk: %v", len(got), got)
	}
	first := got[0]["candidates"].([]any)[0].(map[string]any)
	part := first["content"].(map[string]any)["parts"].([]any)[0].(map[string]any)
	if part["text"] != "Hel" {
		t.Errorf("first chunk = %v; chunks are incremental", got[0])
	}
	if _, ok := first["finishReason"]; ok {
		t.Errorf("first chunk = %v; only the terminal chunk finishes", first)
	}
	last := got[2]["candidates"].([]any)[0].(map[string]any)
	if last["finishReason"] != "STOP" {
		t.Errorf("terminal chunk = %v", last)
	}
	u := got[2]["usageMetadata"].(map[string]any)
	if u["candidatesTokenCount"].(float64) != 2 {
		t.Errorf("usage = %v; the last chunk is authoritative", u)
	}
	if strings.Contains(sseBody(t, textEvents()), "[DONE]") {
		t.Error("Gemini sends no DONE sentinel")
	}
}

// sseBody re-runs the SSE writer and returns the raw body, for assertions about
// framing rather than payload.
func sseBody(t *testing.T, events []ir.StreamEvent) string {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(events, nil), true); err != nil {
		t.Fatal(err)
	}
	return rec.Body.String()
}

func TestWriteStreamArrayFormIsValidJSON(t *testing.T) {
	got := arrayChunks(t, textEvents(), nil)
	if len(got) != 3 {
		t.Fatalf("chunks = %d: %v", len(got), got)
	}
	rec := httptest.NewRecorder()
	if err := writeStream(rec, seq(textEvents(), nil), false); err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "[") || !strings.HasSuffix(body, "]") {
		t.Errorf("body = %q; the array form must be bracketed", body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestWriteStreamEmptyStreamIsStillAValidArray(t *testing.T) {
	got := arrayChunks(t, nil, nil)
	if got == nil {
		t.Fatal("an empty stream must still produce []")
	}
}

func TestWriteStreamReassemblesAFunctionCall(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventBlockStart, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolID: "call_a", ToolName: "f"}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `{"x":`}},
		{Type: ir.EventContentDelta, Index: 1000, Delta: &ir.Delta{
			Type: ir.BlockToolUse, ToolInput: `1}`}},
		{Type: ir.EventBlockStop, Index: 1000},
		{Type: ir.EventMessageStop, StopReason: ir.StopToolUse},
	}, nil)

	var call map[string]any
	for _, c := range got {
		cands, ok := c["candidates"].([]any)
		if !ok || len(cands) == 0 {
			continue
		}
		parts, ok := cands[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)
		if !ok {
			continue
		}
		for _, p := range parts {
			if fc, ok := p.(map[string]any)["functionCall"]; ok {
				call = fc.(map[string]any)
			}
		}
	}
	if call == nil {
		t.Fatalf("no functionCall part: %v", got)
	}
	if call["name"] != "f" || call["id"] != "call_a" {
		t.Errorf("functionCall = %v", call)
	}
	args, ok := call["args"].(map[string]any)
	if !ok || args["x"].(float64) != 1 {
		t.Errorf("args = %#v; fragments must be reassembled into one object", call["args"])
	}
}

func TestWriteStreamCarriesThoughtSignatures(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Thinking: "weighing"}},
		{Type: ir.EventContentDelta, Delta: &ir.Delta{Type: ir.BlockThinking, Signature: "sig-1"}},
		{Type: ir.EventMessageStop, StopReason: ir.StopEndTurn},
	}, nil)

	var sawThought, sawSig bool
	for _, c := range got {
		cands, _ := c["candidates"].([]any)
		if len(cands) == 0 {
			continue
		}
		parts, _ := cands[0].(map[string]any)["content"].(map[string]any)["parts"].([]any)
		for _, p := range parts {
			m := p.(map[string]any)
			if m["thought"] == true && m["text"] == "weighing" {
				sawThought = true
			}
			if m["thoughtSignature"] == "sig-1" {
				sawSig = true
			}
		}
	}
	if !sawThought || !sawSig {
		t.Fatalf("chunks = %v", got)
	}
}

func TestWriteStreamEndsAnErroredStreamWithAFeedbackChunk(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "partial"}},
	}, &ir.Error{Type: ir.ErrOverloaded, Message: "upstream gave up"})

	last := got[len(got)-1]
	fb, ok := last["promptFeedback"].(map[string]any)
	if !ok {
		t.Fatalf("last chunk = %v; Gemini SSE has no error event, so the shape is a chunk", last)
	}
	if fb["blockReason"] != "OTHER" {
		t.Errorf("blockReason = %v; SAFETY would send the client chasing its own prompt", fb["blockReason"])
	}
	if !strings.Contains(fb["blockReasonMessage"].(string), "upstream gave up") {
		t.Errorf("blockReasonMessage = %v", fb["blockReasonMessage"])
	}
	c := last["candidates"].([]any)[0].(map[string]any)
	if c["finishReason"] != "OTHER" {
		t.Errorf("finishReason = %v", c["finishReason"])
	}
}

func TestWriteStreamContentFilterErrorReportsSafety(t *testing.T) {
	got := sseChunks(t, []ir.StreamEvent{
		{Type: ir.EventMessageStart, ID: "r", Model: "m"},
		{Type: ir.EventContentDelta, Index: 0, Delta: &ir.Delta{Type: ir.BlockText, Text: "partial"}},
	}, &ir.Error{Type: ir.ErrContentFilter, Message: "blocked"})

	last := got[len(got)-1]
	if last["promptFeedback"].(map[string]any)["blockReason"] != "SAFETY" {
		t.Errorf("last chunk = %v", last)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/ -run WriteStream`
Expected: FAIL — the stub writes a JSON error object.

- [ ] **Step 3: Write the stream writer**

Delete the `writeStream` stub from `write.go` (and `"iter"` from its imports),
then create `internal/edge/gemini/stream.go`:

```go
package gemini

import (
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"sort"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// chunkWriter frames chunks in whichever form the client asked for. Both flush
// per chunk: buffering the array form turns time-to-first-token into
// time-to-completion.
type chunkWriter struct {
	sse   *sse.Writer
	w     http.ResponseWriter
	rc    *http.ResponseController
	first bool
}

func newChunkWriter(w http.ResponseWriter, asSSE bool) *chunkWriter {
	if asSSE {
		return &chunkWriter{sse: sse.NewWriter(w), first: true}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Accel-Buffering", "no")
	return &chunkWriter{w: w, rc: http.NewResponseController(w), first: true}
}

func (c *chunkWriter) send(v map[string]any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if c.sse != nil {
		c.first = false
		return c.sse.Send("", string(b))
	}
	prefix := ","
	if c.first {
		prefix = "["
		c.first = false
	}
	if _, err := io.WriteString(c.w, prefix); err != nil {
		return err
	}
	if _, err := c.w.Write(b); err != nil {
		return err
	}
	_ = c.rc.Flush()
	return nil
}

// close finishes the array form. Gemini's SSE has no terminator at all — no
// [DONE], no final event — so the SSE path closes with nothing.
func (c *chunkWriter) close() error {
	if c.sse != nil {
		return nil
	}
	body := "]"
	if c.first {
		body = "[]"
	}
	_, err := io.WriteString(c.w, body)
	_ = c.rc.Flush()
	return err
}

// pendingCall accumulates a tool call. Gemini sends functionCall whole in one
// chunk, while the IR carries its arguments as fragments whenever the upstream
// was OpenAI-compatible.
type pendingCall struct {
	id   string
	name string
	args string
}

func writeStream(w http.ResponseWriter, events iter.Seq2[ir.StreamEvent, error], asSSE bool) error {
	cw := newChunkWriter(w, asSSE)

	var (
		model   string
		usage   ir.Usage
		stop    = ir.StopEndTurn
		calls   = map[int]*pendingCall{}
		sendErr error
	)

	partChunk := func(parts []any) error {
		return cw.send(map[string]any{
			"candidates": []any{map[string]any{
				"content": map[string]any{"role": "model", "parts": parts},
				"index":   0,
			}},
			"modelVersion": model,
		})
	}

	flushCall := func(idx int) error {
		pc, ok := calls[idx]
		if !ok {
			return nil
		}
		delete(calls, idx)
		args := json.RawMessage(pc.args)
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		call := map[string]any{"name": pc.name, "args": args}
		if pc.id != "" {
			call["id"] = pc.id
		}
		return partChunk([]any{map[string]any{"functionCall": call}})
	}

	flushAllCalls := func() error {
		idxs := make([]int, 0, len(calls))
		for idx := range calls {
			idxs = append(idxs, idx)
		}
		sort.Ints(idxs)
		for _, idx := range idxs {
			if err := flushCall(idx); err != nil {
				return err
			}
		}
		return nil
	}

	terminal := func(reason string) error {
		return cw.send(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"role": "model", "parts": []any{}},
				"finishReason": reason,
				"index":        0,
			}},
			"usageMetadata": usageBody(usage),
			"modelVersion":  model,
		})
	}

	for ev, err := range events {
		if err != nil {
			var e *ir.Error
			if !errors.As(err, &e) {
				e = &ir.Error{Type: ir.ErrAPI, Message: err.Error()}
			}
			// Spec §4.9: Gemini's SSE defines no error event, so a post-commit
			// failure becomes a terminal chunk carrying a promptFeedback-shaped
			// object. Only a real content filter reports SAFETY.
			reason := "OTHER"
			if e.Type == ir.ErrContentFilter {
				reason = "SAFETY"
			}
			if serr := cw.send(map[string]any{
				"candidates": []any{map[string]any{
					"content":      map[string]any{"role": "model", "parts": []any{}},
					"finishReason": reason,
					"index":        0,
				}},
				"promptFeedback": map[string]any{
					"blockReason":        reason,
					"blockReasonMessage": e.Message,
				},
				"usageMetadata": usageBody(usage),
				"modelVersion":  model,
			}); serr != nil {
				return serr
			}
			return cw.close()
		}

		switch ev.Type {
		case ir.EventMessageStart:
			model = ev.Model

		case ir.EventBlockStart:
			if ev.Delta != nil && ev.Delta.Type == ir.BlockToolUse {
				calls[ev.Index] = &pendingCall{id: ev.Delta.ToolID, name: ev.Delta.ToolName}
			}

		case ir.EventContentDelta:
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case ir.BlockText:
				if ev.Delta.Text == "" {
					continue
				}
				sendErr = partChunk([]any{map[string]any{"text": ev.Delta.Text}})
			case ir.BlockThinking:
				p := map[string]any{"text": ev.Delta.Thinking, "thought": true}
				if ev.Delta.Signature != "" {
					p["thoughtSignature"] = ev.Delta.Signature
				}
				sendErr = partChunk([]any{p})
			case ir.BlockToolUse:
				pc, ok := calls[ev.Index]
				if !ok {
					// A provider that streams arguments without opening a block
					// still has to reach the client.
					pc = &pendingCall{id: ev.Delta.ToolID, name: ev.Delta.ToolName}
					calls[ev.Index] = pc
				}
				if pc.name == "" {
					pc.name = ev.Delta.ToolName
				}
				if pc.id == "" {
					pc.id = ev.Delta.ToolID
				}
				pc.args += ev.Delta.ToolInput
			}

		case ir.EventBlockStop:
			sendErr = flushCall(ev.Index)

		case ir.EventMessageDelta:
			if ev.Usage != nil {
				usage = *ev.Usage
			}
			if ev.StopReason != "" {
				stop = ev.StopReason
			}

		case ir.EventMessageStop:
			if ev.StopReason != "" {
				stop = ev.StopReason
			}
			if err := flushAllCalls(); err != nil {
				return err
			}
			if err := terminal(finishReasonWire(stop)); err != nil {
				return err
			}
			return cw.close()
		}

		if sendErr != nil {
			return sendErr
		}
	}

	// The sequence ended without a message_stop. Flush and terminate anyway, or
	// the array form is never closed and the client sees truncated JSON.
	if err := flushAllCalls(); err != nil {
		return err
	}
	if err := terminal(finishReasonWire(stop)); err != nil {
		return err
	}
	return cw.close()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/ -v`
Expected: PASS.

`TestWriteStreamEmptyStreamIsStillAValidArray` exercises the fall-through path:
no events at all still produces a terminal chunk, so `close()` writes `]` after
it rather than `[]`. The assertion only requires valid JSON, which both give.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/gemini/
git commit -m "feat(edge/gemini): stream SSE and JSON array"
```

---

### Task 29: Routes, per-dialect auth, and the Gemini model listing

**Files:**
- Create: `internal/edge/gemini/list.go`
- Modify: `internal/server/server.go` (`New`, `ProxyHandler`)
- Test: `internal/edge/gemini/list_test.go`, `internal/server/server_test.go`

**Interfaces:**
- Consumes: every dialect and adapter from Tasks 5, 17, 19, 20, 25, 27, 28; `(*Server).authed` from Task 5.
- Produces: `gemini.ListModels(models []string) map[string]any`; the six routes spec §3 names, less the two counting ones.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The pattern must be `POST /v1beta/models/{model}`, with the method dispatched
inside the handler. A `net/http` wildcard has to occupy a whole path segment, so
`{model}:generateContent` is not a legal pattern — and `{model}` swallows the
method suffix along with the name, which is exactly why `ExtractModel` splits on
the last colon.

`GET /v1beta/models` returns Gemini's own listing shape because some clients
filter on `supportedGenerationMethods` and will show no models at all if the
field is missing. `inputTokenLimit` and `outputTokenLimit` are **omitted** rather
than sent as zero: the catalog does not know them until Phase 6, and a client
that reads a zero limit refuses to send anything.

- [ ] **Step 1: Write the failing tests**

Create `internal/edge/gemini/list_test.go`:

```go
package gemini

import (
	"encoding/json"
	"testing"
)

func TestListModelsUsesGeminiResourceNames(t *testing.T) {
	raw, err := json.Marshal(ListModels([]string{"gemini-2.0-flash", "groq/llama-3.3-70b"}))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Models []struct {
			Name                       string   `json:"name"`
			BaseModelID                string   `json:"baseModelId"`
			DisplayName                string   `json:"displayName"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
			InputTokenLimit            *int     `json:"inputTokenLimit"`
		} `json:"models"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models = %+v", got.Models)
	}
	if got.Models[0].Name != "models/gemini-2.0-flash" {
		t.Errorf("name = %q; Gemini names are resource paths", got.Models[0].Name)
	}
	if got.Models[0].BaseModelID != "gemini-2.0-flash" {
		t.Errorf("baseModelId = %q", got.Models[0].BaseModelID)
	}
	if len(got.Models[0].SupportedGenerationMethods) == 0 {
		t.Error("clients filter on supportedGenerationMethods and hide models without it")
	}
	if got.Models[0].InputTokenLimit != nil {
		t.Error("an unknown limit is omitted; a zero limit makes clients refuse to send")
	}
	if got.Models[1].Name != "models/groq/llama-3.3-70b" {
		t.Errorf("name = %q", got.Models[1].Name)
	}
}

func TestListModelsEmitsAnEmptyArray(t *testing.T) {
	raw, err := json.Marshal(ListModels(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == `{"models":null}` || !json.Valid(raw) {
		t.Fatalf("body = %s; clients range over models unconditionally", raw)
	}
}
```

Append to `internal/server/server_test.go`:

```go
func TestProxyHandlerRoutesEveryDialect(t *testing.T) {
	s := newTestServer(t)
	h := s.ProxyHandler()

	cases := []struct {
		name   string
		method string
		target string
		body   string
	}{
		{"openai chat", "POST", "/v1/chat/completions", `{"model":"nope","messages":[]}`},
		{"anthropic messages", "POST", "/v1/messages", `{"model":"nope","max_tokens":1,"messages":[]}`},
		{"gemini generate", "POST", "/v1beta/models/nope:generateContent", `{"contents":[]}`},
		{"gemini stream", "POST", "/v1beta/models/nope:streamGenerateContent?alt=sse", `{"contents":[]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body)))
			if rec.Code == http.StatusNotFound && !strings.Contains(rec.Body.String(), "model") {
				t.Fatalf("route is unregistered: %d %s", rec.Code, rec.Body.String())
			}
			if rec.Header().Get("X-Darkrouter-Request") == "" {
				t.Errorf("no request id: the handler did not reach exec (%d %s)",
					rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGeminiListingIsServed(t *testing.T) {
	s := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/v1beta/models", nil))
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "supportedGenerationMethods") {
		t.Errorf("body = %s", rec.Body.String())
	}
}

func TestGeminiAuthUsesItsOwnCredentialForm(t *testing.T) {
	s := newTestServerWithToken(t, "secret")
	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/v1beta/models?key=secret", nil)
	s.ProxyHandler().ServeHTTP(rec, r)
	if rec.Code == 401 {
		t.Fatalf("?key= must authenticate a Gemini client: %s", rec.Body.String())
	}

	rec2 := httptest.NewRecorder()
	s.ProxyHandler().ServeHTTP(rec2, httptest.NewRequest("GET", "/v1beta/models?key=wrong", nil))
	if rec2.Code != 401 {
		t.Errorf("wrong key = %d, want 401", rec2.Code)
	}
	if !strings.Contains(rec2.Body.String(), "UNAUTHENTICATED") {
		t.Errorf("body = %s; the rejection is written in Google's vocabulary", rec2.Body.String())
	}
}
```

Read the existing helpers at the top of `internal/server/server_test.go` before
writing these: the file already builds a `*Server` for
`TestProxyTokenIsEnforcedWhenConfigured`. Reuse that construction, naming the
two helpers `newTestServer` and `newTestServerWithToken`, and refactor the
existing tests onto them rather than adding a third way to build a server.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/ ./internal/server/ -run 'ListModels|ProxyHandlerRoutes|GeminiListing|GeminiAuth'`
Expected: FAIL — `ListModels` is undefined and the routes 404.

- [ ] **Step 3: Write the listing**

Create `internal/edge/gemini/list.go`:

```go
package gemini

// generationMethods is what Darkrouter serves for every chat model. Clients
// filter on this list, and one that omits it shows no models at all.
var generationMethods = []string{"generateContent", "streamGenerateContent", "countTokens"}

// ListModels renders Gemini's listing shape.
//
// inputTokenLimit and outputTokenLimit are omitted rather than zeroed: the
// catalog does not know them until Phase 6, and a client reading a zero limit
// refuses to send anything at all.
func ListModels(models []string) map[string]any {
	out := []any{}
	for _, m := range models {
		out = append(out, map[string]any{
			"name":                       "models/" + m,
			"baseModelId":                m,
			"displayName":                m,
			"supportedGenerationMethods": generationMethods,
		})
	}
	return map[string]any{"models": out}
}
```

- [ ] **Step 4: Register the adapters and the routes**

In `internal/server/server.go`, extend the registry built in Task 4:

```go
		ex: exec.New(cfgStore, src, map[string]adapter.Adapter{
			"openaicompat": openaicompat.New(),
			"anthropic":    anthropicadapter.New(),
			"gemini":       geminiadapter.New(),
		}, exec.Deps{
			Log: logw, Health: breaker, Fleet: breaker,
		}),
```

with imports:

```go
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
```

Replace `ProxyHandler`:

```go
func (s *Server) ProxyHandler() http.Handler {
	mux := http.NewServeMux()

	oa := openaiedge.New()
	mux.HandleFunc("POST /v1/chat/completions", s.authed(oa, func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, oa)
	}))
	mux.HandleFunc("GET /v1/models", s.authed(oa, s.handleModels))

	an := anthropicedge.New()
	mux.HandleFunc("POST /v1/messages", s.authed(an, func(w http.ResponseWriter, r *http.Request) {
		s.ex.Handle(w, r, an)
	}))

	// One pattern for every Gemini method. A net/http wildcard occupies a whole
	// path segment, so "{model}:generateContent" is not a legal pattern and the
	// suffix is dispatched here instead.
	gm := geminiedge.New()
	mux.HandleFunc("POST /v1beta/models/{model}", s.authed(gm, s.handleGemini))
	mux.HandleFunc("GET /v1beta/models", s.authed(gm, s.handleGeminiModels))

	return mux
}

// handleGemini dispatches on the method suffix the path segment carries.
func (s *Server) handleGemini(w http.ResponseWriter, r *http.Request) {
	_, method := geminiedge.ExtractModel(r.PathValue("model"))
	switch method {
	case "generateContent", "streamGenerateContent":
		// NewFor rather than New: ?alt=sse selects the streaming wire form, and
		// WriteStream cannot see the request that chose it.
		s.ex.Handle(w, r, geminiedge.NewFor(r))
	default:
		_ = geminiedge.New().WriteError(w, &ir.Error{
			Type: ir.ErrNotFound, Message: "unsupported method: " + method,
		})
	}
}

func (s *Server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	ps, err := s.src.Providers(r.Context())
	if err != nil {
		_ = geminiedge.New().WriteError(w, &ir.Error{
			Type: ir.ErrDarkrouter, Message: "could not list providers",
		})
		return
	}
	seen := map[string]bool{}
	var models []string
	for _, p := range ps {
		for _, m := range p.Models {
			if seen[m] {
				continue
			}
			seen[m] = true
			models = append(models, m)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(geminiedge.ListModels(models))
}
```

Task 32 adds `countTokens` to the switch and the `/v1/messages/count_tokens`
route.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/edge/gemini/ ./internal/server/ -v`
Expected: PASS.

- [ ] **Step 6: Manual smoke check**

Start the gateway on a high port — 8080 and 8081 are occupied by an unrelated
application on this machine — and confirm each route answers in its own dialect.
Use a config with one provider whose base URL points nowhere, so every request
fails at the provider and the *shape* of the failure is what is being checked.

`cmd/darkrouter/main.go` takes `-config` and `-db`; the database defaults to
sitting beside the config, so a scratch directory keeps both out of the repo.

```bash
export PATH=$PATH:/usr/local/go/bin
SMOKE=$(mktemp -d)
cat > "$SMOKE/darkrouter.yaml" <<'YAML'
server:
  proxy_listen: 127.0.0.1:18080
  admin_listen: 127.0.0.1:18081
providers:
  - id: dead
    kind: openaicompat
    base_url: http://127.0.0.1:9/v1
    api_key: sk-none
    models: [m]
YAML
go build -o "$SMOKE/darkrouter" ./cmd/darkrouter
"$SMOKE/darkrouter" -config "$SMOKE/darkrouter.yaml" -db "$SMOKE/darkrouter.db" &
DR_PID=$!
sleep 2

curl -sS -X POST localhost:18080/v1/messages -H 'content-type: application/json' \
  -d '{"model":"m","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}'; echo
curl -sS -X POST 'localhost:18080/v1beta/models/m:generateContent' -H 'content-type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}'; echo
curl -sS localhost:18080/v1beta/models; echo

kill "$DR_PID"; wait "$DR_PID" 2>/dev/null
ps -p "$DR_PID" >/dev/null && echo "STILL RUNNING" || echo "stopped"
rm -rf "$SMOKE"
```

Expected: the Anthropic route answers `{"type":"error","error":{"type":"api_error",...}}`,
the Gemini route answers `{"error":{"code":500,"status":"INTERNAL",...}}`, and the
listing returns one model carrying `supportedGenerationMethods`. Ports 18080 and
18081 are used deliberately: 8080 and 8081 belong to an unrelated application on
this machine. The last line must print `stopped`.

- [ ] **Step 7: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/edge/gemini/ internal/server/
git commit -m "feat(server): route the anthropic and gemini surfaces"
```

---

### Task 30: The bundled token estimator

**Files:**
- Create: `internal/tokenize/tokenize.go`
- Modify: `go.mod`, `go.sum`
- Test: `internal/tokenize/tokenize_test.go`

**Interfaces:**
- Consumes: `ir.Request` from Task 1.
- Produces: `tokenize.Encoding` with `tokenize.O200k`, `tokenize.Cl100k`, `tokenize.Heuristic`; `tokenize.EncodingFor(model string) Encoding`; `tokenize.Count(req *ir.Request, model string) int`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 3: the human partner chose to bundle the tokenizer after seeing the measured binary cost

Spec §6 wants a `tiktoken`-compatible BPE where the target's family is known and
characters-divided-by-four otherwise, because "clients budget context windows
against this number, and estimates differ by a factor of two between plausible
approaches".

`github.com/tiktoken-go/tokenizer` embeds its vocabularies rather than
downloading them, which is what a `CGO_ENABLED=0` static binary in a container
with no network needs. It costs roughly 14 MB of binary — measured, not
estimated: the current binary is 16.9 MB and the dependency's own smoke build is
14.8 MB, nearly all of it vocabulary. The user chose that trade explicitly.

The estimate deliberately does **not** count media. An image's token cost
depends on the model's tiling rules, and a wrong number is worse than an
obviously incomplete one; the `X-Darkrouter-Estimated` header Task 32 sets is
what tells the client not to trust it to the token.

- [ ] **Step 1: Add the dependency**

```bash
export PATH=$PATH:/usr/local/go/bin
go get github.com/tiktoken-go/tokenizer@v0.8.1
go mod tidy
```

Expected: `go.mod` gains the requirement and `github.com/dlclark/regexp2/v2` as
an indirect one.

- [ ] **Step 2: Write the failing tests**

Create `internal/tokenize/tokenize_test.go`:

```go
package tokenize

import (
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestEncodingForKnownFamilies(t *testing.T) {
	cases := []struct {
		model string
		want  Encoding
	}{
		{"gpt-5", O200k},
		{"gpt-4.1-mini", O200k},
		{"gpt-4o", O200k},
		{"o3-mini", O200k},
		{"openai/gpt-oss-120b", O200k},
		{"gpt-4-turbo", Cl100k},
		{"gpt-3.5-turbo", Cl100k},
		{"text-embedding-3-small", Cl100k},
		{"claude-sonnet-4-5", Heuristic},
		{"gemini-2.0-flash", Heuristic},
		{"llama-3.3-70b", Heuristic},
		{"", Heuristic},
	}
	for _, tc := range cases {
		if got := EncodingFor(tc.model); got != tc.want {
			t.Errorf("EncodingFor(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}

func TestCountUsesTheBPEForAKnownFamily(t *testing.T) {
	req := &ir.Request{Messages: []ir.Message{{
		Role:    ir.RoleUser,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hello world"}},
	}}}
	got := Count(req, "gpt-4o")
	// "hello world" is two tokens under o200k, plus the per-message overhead.
	if got < 3 || got > 12 {
		t.Errorf("Count = %d; a real BPE count of a two-token message should be small", got)
	}
}

func TestCountFallsBackToCharactersOverFour(t *testing.T) {
	text := strings.Repeat("a", 400)
	req := &ir.Request{Messages: []ir.Message{{
		Role:    ir.RoleUser,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: text}},
	}}}
	got := Count(req, "claude-sonnet-4-5")
	if got < 100 || got > 110 {
		t.Errorf("Count = %d; 400 characters over four is 100 plus overhead", got)
	}
}

func TestCountIncludesSystemToolsAndToolResults(t *testing.T) {
	base := &ir.Request{Messages: []ir.Message{{
		Role:    ir.RoleUser,
		Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
	}}}
	withMore := &ir.Request{
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: strings.Repeat("s", 200)}},
		Messages: append([]ir.Message{}, base.Messages...),
		Tools: []ir.Tool{{
			Name: "lookup", Description: strings.Repeat("d", 200),
			Schema: []byte(`{"type":"object","properties":{}}`),
		}},
	}
	if Count(withMore, "claude-x") <= Count(base, "claude-x")+50 {
		t.Error("system text and tool declarations both consume context and must be counted")
	}
}

func TestCountIgnoresMedia(t *testing.T) {
	withImage := &ir.Request{Messages: []ir.Message{{
		Role: ir.RoleUser,
		Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "look"},
			{Type: ir.BlockImage, Media: &ir.Media{MIME: "image/png", Data: strings.Repeat("A", 10000)}},
		},
	}}}
	if Count(withImage, "gpt-4o") > 50 {
		t.Error("base64 payload must not be counted as text; tiling rules decide an image's cost")
	}
}

func TestCountIsNeverNegativeOnAnEmptyRequest(t *testing.T) {
	if got := Count(&ir.Request{}, "gpt-4o"); got < 0 {
		t.Errorf("Count = %d", got)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/tokenize/`
Expected: FAIL — the package does not exist.

- [ ] **Step 4: Write the estimator**

Create `internal/tokenize/tokenize.go`:

```go
// Package tokenize estimates prompt token counts for requests Darkrouter
// cannot forward to a provider's own counting endpoint.
//
// Clients budget context windows against this number, so the method is named
// rather than left to each caller: plausible approaches differ by a factor of
// two, and a silently different answer per route would be worse than a
// consistently approximate one.
package tokenize

import (
	"strings"

	"github.com/tiktoken-go/tokenizer"

	"github.com/darkraise/darkrouter/internal/ir"
)

type Encoding string

const (
	O200k  Encoding = "o200k_base"
	Cl100k Encoding = "cl100k_base"
	// Heuristic is characters divided by four, for a family whose real
	// tokenizer Darkrouter does not bundle.
	Heuristic Encoding = ""
)

// perMessageOverhead is the framing every message costs on top of its text.
// It is tiktoken's documented constant for chat formats and close enough for
// the others that a better guess would be false precision.
const perMessageOverhead = 4

// o200kPrefixes and cl100kPrefixes name the families whose tokenizer is
// bundled. Everything else falls back to the heuristic rather than borrowing a
// vocabulary that would be confidently wrong.
var (
	o200kPrefixes  = []string{"gpt-5", "gpt-4.1", "gpt-4o", "gpt-oss", "o1", "o3", "o4"}
	cl100kPrefixes = []string{"gpt-4", "gpt-3.5", "text-embedding"}
)

// EncodingFor picks a vocabulary from a model name, ignoring any
// provider/model prefix so that "openai/gpt-oss-120b" resolves like "gpt-oss".
func EncodingFor(model string) Encoding {
	name := strings.ToLower(model)
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	for _, p := range o200kPrefixes {
		if strings.HasPrefix(name, p) {
			return O200k
		}
	}
	for _, p := range cl100kPrefixes {
		if strings.HasPrefix(name, p) {
			return Cl100k
		}
	}
	return Heuristic
}

// Count estimates the prompt tokens a request will consume.
//
// Media is deliberately excluded: an image's cost depends on the model's tiling
// rules, and a number that is wrong in an unknowable direction is worse than one
// that is obviously incomplete. The response carries X-Darkrouter-Estimated so
// the client knows not to trust it to the token.
func Count(req *ir.Request, model string) int {
	count := counterFor(EncodingFor(model))

	total := 0
	total += count(blocksText(req.System))
	for _, m := range req.Messages {
		total += perMessageOverhead
		total += count(string(m.Role))
		total += count(blocksText(m.Content))
	}
	for _, t := range req.Tools {
		total += count(t.Name) + count(t.Description) + count(string(t.Schema))
	}
	for _, s := range req.StopSequences {
		total += count(s)
	}
	return total
}

// counterFor returns a function counting one string. A tokenizer that fails to
// load falls back to the heuristic rather than failing the request: an
// approximate answer beats a 500 on an endpoint whose whole purpose is advisory.
func counterFor(enc Encoding) func(string) int {
	var codec tokenizer.Codec
	switch enc {
	case O200k:
		codec, _ = tokenizer.Get(tokenizer.O200kBase)
	case Cl100k:
		codec, _ = tokenizer.Get(tokenizer.Cl100kBase)
	}
	if codec == nil {
		return heuristicCount
	}
	return func(s string) int {
		if s == "" {
			return 0
		}
		ids, _, err := codec.Encode(s)
		if err != nil {
			return heuristicCount(s)
		}
		return len(ids)
	}
}

func heuristicCount(s string) int { return (len(s) + 3) / 4 }

// blocksText flattens the text a block set carries, descending one level into
// tool results, whose content counts against the window like any other text.
func blocksText(blocks []ir.ContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		switch blk.Type {
		case ir.BlockText:
			b.WriteString(blk.Text)
		case ir.BlockThinking:
			if blk.Thinking != nil {
				b.WriteString(blk.Thinking.Text)
			}
		case ir.BlockToolUse:
			if blk.ToolUse != nil {
				b.WriteString(blk.ToolUse.Name)
				b.Write(blk.ToolUse.Input)
			}
		case ir.BlockToolResult:
			if blk.ToolResult != nil {
				b.WriteString(blk.ToolResult.Text())
			}
		}
	}
	return b.String()
}
```

The API was checked against v0.8.1 while writing this plan: `tokenizer.Get`
takes `tokenizer.Cl100kBase` or `tokenizer.O200kBase`, and the `Codec` interface
it returns has `Encode(string) ([]uint, []string, error)` — three return values,
the middle one the token strings, which is why the call above discards it.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/tokenize/ -v`
Expected: PASS.

- [ ] **Step 6: Measure what the dependency cost**

```bash
export PATH=$PATH:/usr/local/go/bin
CGO_ENABLED=0 go build -o /tmp/dr-sized ./cmd/darkrouter && ls -la /tmp/dr-sized && rm /tmp/dr-sized
```

Record the number in the Task 37 notes. It was 16.9 MB before this task; a
result near 31 MB is expected and is the trade the user accepted. Anything far
larger means a vocabulary was pulled in that nothing uses.

- [ ] **Step 7: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add go.mod go.sum internal/tokenize/
git commit -m "feat(tokenize): estimate tokens with a bundled BPE"
```

---

### Task 31: Native token counting for Anthropic and Gemini

**Files:**
- Modify: `internal/adapter/adapter.go`
- Create: `internal/adapter/anthropic/count.go`, `internal/adapter/gemini/count.go`
- Modify: `internal/edge/anthropic/write.go`, `internal/edge/gemini/write.go`
- Test: `internal/adapter/anthropic/count_test.go`, `internal/adapter/gemini/count_test.go`

**Interfaces:**
- Consumes: `BuildRequest` from Tasks 14 and 23.
- Produces: `adapter.TokenCounter` with `BuildCountRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, error)` and `ParseCountResponse(resp *http.Response) (int, error)`; `edge.CountWriter` with `WriteCount(w http.ResponseWriter, tokens int) error`, implemented by both dialects.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4

Spec §6: a counting request is forwarded natively when the resolved target
speaks the same counting dialect, and estimated otherwise. Both halves are
optional interfaces rather than methods on `Adapter` and `Dialect`, because
OpenAI has no counting endpoint at all and forcing every implementation to carry
a not-supported stub buys nothing.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/anthropic/count_test.go`:

```go
package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildCountRequestTargetsTheCountingEndpoint(t *testing.T) {
	hr, err := New().BuildCountRequest(context.Background(),
		&adapter.Target{BaseURL: "https://api.anthropic.com/v1", APIKey: "sk-ant", Model: "claude-x"},
		&ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://api.anthropic.com/v1/messages/count_tokens" {
		t.Errorf("url = %s", hr.URL)
	}
	if hr.Header.Get("anthropic-version") == "" {
		t.Error("the version header is required on every Anthropic request")
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["max_tokens"]; ok {
		t.Error("count_tokens rejects max_tokens; it counts input only")
	}
	if _, ok := body["stream"]; ok {
		t.Error("count_tokens rejects stream")
	}
	if body["model"] != "claude-x" {
		t.Errorf("model = %v", body["model"])
	}
}

func TestParseCountResponseReadsInputTokens(t *testing.T) {
	got, err := New().ParseCountResponse(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"input_tokens":2095}`)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 2095 {
		t.Errorf("tokens = %d", got)
	}
}

var _ adapter.TokenCounter = (*Adapter)(nil)
```

Create `internal/adapter/gemini/count_test.go`:

```go
package gemini

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

func TestBuildCountRequestUsesTheCountTokensMethod(t *testing.T) {
	hr, err := New().BuildCountRequest(context.Background(),
		&adapter.Target{
			BaseURL: "https://generativelanguage.googleapis.com/v1beta",
			APIKey:  "AIza", Model: "gemini-2.0-flash",
		},
		&ir.Request{Messages: []ir.Message{userMsg("hi")}})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:countTokens"
	if hr.URL.String() != want {
		t.Errorf("url = %s, want %s", hr.URL, want)
	}
	if hr.Header.Get("x-goog-api-key") != "AIza" {
		t.Errorf("key header = %q", hr.Header.Get("x-goog-api-key"))
	}
}

func TestParseCountResponseReadsTotalTokens(t *testing.T) {
	got, err := New().ParseCountResponse(&http.Response{
		StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"totalTokens":31}`)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != 31 {
		t.Errorf("tokens = %d", got)
	}
}

var _ adapter.TokenCounter = (*Adapter)(nil)
```

Append to `internal/edge/anthropic/write_test.go`:

```go
func TestWriteCountUsesInputTokens(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := New().WriteCount(rec, 2095); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["input_tokens"].(float64) != 2095 {
		t.Errorf("body = %v", got)
	}
}
```

Append to `internal/edge/gemini/write_test.go`:

```go
func TestWriteCountUsesTotalTokens(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := New().WriteCount(rec, 31); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["totalTokens"].(float64) != 31 {
		t.Errorf("body = %v", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/... ./internal/edge/... -run Count`
Expected: FAIL — undefined in all four packages.

- [ ] **Step 3: Declare the two optional interfaces**

Append to `internal/adapter/adapter.go`:

```go
// TokenCounter is implemented by an adapter whose upstream offers a native
// token count. OpenAI has no such endpoint, so this is optional rather than a
// method on Adapter that two thirds of implementations would stub out.
type TokenCounter interface {
	BuildCountRequest(ctx context.Context, t *Target, req *ir.Request) (*http.Request, error)
	// ParseCountResponse takes ownership of resp.Body and always closes it.
	ParseCountResponse(resp *http.Response) (int, error)
}
```

Append to `internal/edge/edge.go`:

```go
// CountWriter is implemented by a dialect with a token-counting endpoint.
type CountWriter interface {
	Dialect
	WriteCount(w http.ResponseWriter, tokens int) error
}
```

- [ ] **Step 4: Implement the Anthropic half**

Create `internal/adapter/anthropic/count.go`:

```go
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// BuildCountRequest reuses the message rendering and strips what the counting
// endpoint rejects. Rendering the conversation twice by different rules would
// mean counting a prompt the model never sees.
func (a *Adapter) BuildCountRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	counting := *req
	counting.Stream = false
	hr, _, err := BuildRequest(ctx, t, &counting)
	if err != nil {
		return nil, err
	}

	var body map[string]any
	raw, err := readAndRestore(hr)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	// count_tokens counts input, so an output cap is meaningless, streaming has
	// nothing to stream, and an output schema shapes a response that will never
	// be generated. Anthropic rejects the first two outright; the third is
	// stripped because an unrecognized field on this endpoint is a 400 risk for
	// no benefit.
	delete(body, "max_tokens")
	delete(body, "stream")
	delete(body, "output_config")

	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(t.BaseURL, "/") + "/messages/count_tokens"
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	out.Header = hr.Header.Clone()
	out.ContentLength = int64(len(buf))
	return out, nil
}

func (a *Adapter) ParseCountResponse(resp *http.Response) (int, error) {
	defer resp.Body.Close()
	var w struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return 0, err
	}
	return w.InputTokens, nil
}

var _ adapter.TokenCounter = (*Adapter)(nil)

// readAndRestore drains a freshly built request's body. The request is discarded
// immediately afterwards, so nothing has to be put back.
func readAndRestore(hr *http.Request) ([]byte, error) {
	if hr.Body == nil {
		return []byte("{}"), nil
	}
	defer hr.Body.Close()
	return io.ReadAll(hr.Body)
}
```

Add `"io"` to the imports. `ir` is imported for the signature.

- [ ] **Step 5: Implement the Gemini half**

Create `internal/adapter/gemini/count.go`:

```go
package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// BuildCountRequest reuses the request builder and rewrites the method. The
// body shape is identical, so counting the same rendering the model would see
// is free here — unlike Anthropic, where two fields have to come back out.
func (a *Adapter) BuildCountRequest(ctx context.Context, t *adapter.Target, req *ir.Request) (*http.Request, error) {
	counting := *req
	counting.Stream = false
	hr, _, err := a.f.BuildRequest(ctx, t, &counting)
	if err != nil {
		return nil, err
	}
	hr.URL.Path = strings.TrimSuffix(hr.URL.Path, ":generateContent") + ":countTokens"
	hr.URL.RawQuery = ""
	return hr, nil
}

func (a *Adapter) ParseCountResponse(resp *http.Response) (int, error) {
	defer resp.Body.Close()
	var w struct {
		TotalTokens int `json:"totalTokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&w); err != nil {
		return 0, err
	}
	return w.TotalTokens, nil
}

var _ adapter.TokenCounter = (*Adapter)(nil)
```

`hr.URL.Path` is used rather than rebuilding the URL so the model's escaping —
which `url.PathEscape` applied in `BuildRequest` — is preserved. `TrimSuffix`
operates on the decoded path, and the method suffix never contains an escapable
character, so the model segment is untouched.

- [ ] **Step 6: Add the two count writers**

Append to `internal/edge/anthropic/write.go`:

```go
// WriteCount answers /v1/messages/count_tokens. The body carries no marker for
// an estimate — clients parse it strictly — so the X-Darkrouter-Estimated
// header is where that goes, and exec sets it.
func (d *Dialect) WriteCount(w http.ResponseWriter, tokens int) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{"input_tokens": tokens})
}

var _ edge.CountWriter = (*Dialect)(nil)
```

Append to `internal/edge/gemini/write.go`:

```go
// WriteCount answers :countTokens. totalBillableCharacters is omitted: it is
// deprecated for text models and Darkrouter has no honest value for it.
func (d *Dialect) WriteCount(w http.ResponseWriter, tokens int) error {
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(map[string]any{"totalTokens": tokens})
}

var _ edge.CountWriter = (*Dialect)(nil)
```

Both files need `"github.com/darkraise/darkrouter/internal/edge"` imported, which
introduces no cycle: `edge` imports only `ir`.

- [ ] **Step 7: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/adapter/... ./internal/edge/... -v`
Expected: PASS.

- [ ] **Step 8: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/adapter/ internal/edge/
git commit -m "feat(adapter): count tokens natively where offered"
```

---

### Task 32: The counting routes

**Files:**
- Create: `internal/exec/count.go`
- Modify: `internal/server/server.go` (`ProxyHandler`, `handleGemini`)
- Test: `internal/exec/count_test.go`

**Interfaces:**
- Consumes: `adapter.TokenCounter` and `edge.CountWriter` from Task 31; `tokenize.Count` from Task 30; `router.Resolve` from Phase 3.
- Produces: `func (e *Executor) HandleCount(w http.ResponseWriter, r *http.Request, d edge.CountWriter, nativeKind string)`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Spec §6: forward natively when the resolved target speaks the same counting
dialect, and estimate otherwise, marking the estimate with
`X-Darkrouter-Estimated: true`. The header rather than the body, because clients
parse these responses strictly and an extra field breaks them.

A native count that fails **falls back to the estimate** rather than erroring.
This endpoint is advisory — the client is sizing a context window, not sending
the prompt — and an approximate answer is far more useful than a 502.

Counting does not run the attempt loop: there is no commit, no failover, and no
stream. It borrows the router only to learn which target would serve the
request, which is what decides whether a native count is even possible.

- [ ] **Step 1: Write the failing tests**

Create `internal/exec/count_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/exec/ -run HandleCount`
Expected: FAIL — `HandleCount` is undefined.

- [ ] **Step 3: Write the handler**

Create `internal/exec/count.go`:

```go
package exec

import (
	"context"
	"crypto/rand"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
	"github.com/darkraise/darkrouter/internal/store"
	"github.com/darkraise/darkrouter/internal/tokenize"
)

// HandleCount serves a token-counting request.
//
// It does not run the attempt loop: there is no commit, no failover, and no
// stream. The router is consulted only to learn which target would serve the
// request, because that decides whether a native count is possible at all.
//
// nativeKind is the adapter kind whose counting endpoint speaks this inbound
// dialect — "anthropic" for /v1/messages/count_tokens, "gemini" for
// :countTokens. Any other target is estimated locally.
func (e *Executor) HandleCount(w http.ResponseWriter, r *http.Request, d edge.CountWriter, nativeKind string) {
	start := time.Now()
	cfg := e.store.Current()
	reqID := ulid.MustNew(ulid.Timestamp(start), rand.Reader).String()

	rec := &store.RequestRecord{
		ID: reqID, TS: start, Dialect: d.Name(),
		Surface: string(ir.SurfaceLLM), Status: "error",
	}
	defer func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}()
	w.Header().Set("X-Darkrouter-Request", reqID)

	req, _, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	rec.RequestedModel = req.Model

	providers, perr := e.src.Providers(r.Context())
	if perr != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: perr.Error()})
		return
	}

	snap := router.Snapshot{
		At: start, Providers: providers,
		Catalog: catalog.FromProviders(providers), Config: cfg,
	}
	if e.deps.Fleet != nil {
		snap.Health = e.deps.Fleet.SnapshotAvailability(start)
		snap.LastUsed = e.deps.Fleet.LastUsedSnapshot()
	}
	needs := req.Needs()
	cands, _, rerr := router.Resolve(router.Query{
		Model: req.Model, Surface: ir.SurfaceLLM,
		NeedsTools: needs.Tools, NeedsVision: needs.Vision, NeedsReasoning: needs.Reasoning,
	}, snap)
	if rerr != nil {
		e2 := routerError(rerr)
		rec.ErrorCode = string(e2.Type)
		_ = d.WriteError(w, e2)
		return
	}

	byID := make(map[string]provider.Provider, len(providers))
	for _, p := range providers {
		byID[p.ID] = p
	}

	model := req.Model
	if len(cands) > 0 {
		model = cands[0].Model
	}
	if tokens, ok := e.nativeCount(r.Context(), req, cands, byID, nativeKind); ok {
		rec.Status = "success"
		rec.TokensIn = int64(tokens)
		_ = d.WriteCount(w, tokens)
		return
	}

	// The body cannot carry a marker: clients parse these responses strictly.
	w.Header().Set("X-Darkrouter-Estimated", "true")
	tokens := tokenize.Count(req, model)
	rec.Status = "success"
	rec.TokensIn = int64(tokens)
	rec.Warnings = []string{"count -> " + d.Name() + ": estimated locally"}
	_ = d.WriteCount(w, tokens)
}

// nativeCount tries the first candidate that speaks the inbound counting
// dialect. A failure reports false rather than an error: this endpoint is
// advisory, and an estimate beats a 502 for a client sizing a context window.
func (e *Executor) nativeCount(ctx context.Context, req *ir.Request, cands []router.Candidate,
	byID map[string]provider.Provider, nativeKind string) (int, bool) {

	for _, c := range cands {
		if c.Kind != nativeKind {
			continue
		}
		ad, ok := e.adapterFor(c.Kind)
		if !ok {
			continue
		}
		tc, ok := ad.(adapter.TokenCounter)
		if !ok {
			continue
		}
		p := byID[c.ProviderID]
		tgt := &adapter.Target{BaseURL: p.BaseURL, APIKey: secretOf(p, c.KeyID), Model: c.Model}
		hr, err := tc.BuildCountRequest(ctx, tgt, req)
		if err != nil {
			return 0, false
		}
		resp, err := e.client.Do(hr)
		if err != nil {
			return 0, false
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			return 0, false
		}
		tokens, err := tc.ParseCountResponse(resp)
		if err != nil {
			return 0, false
		}
		return tokens, true
	}
	return 0, false
}
```

- [ ] **Step 4: Register the two routes**

In `internal/server/server.go`, add the Anthropic route beside `/v1/messages`:

```go
	mux.HandleFunc("POST /v1/messages/count_tokens", s.authed(an, func(w http.ResponseWriter, r *http.Request) {
		s.ex.HandleCount(w, r, an, "anthropic")
	}))
```

`POST /v1/messages/count_tokens` is more specific than `POST /v1/messages`, so
`net/http`'s precedence rules pick it without any ordering concern.

Extend `handleGemini`'s switch:

```go
	case "countTokens":
		s.ex.HandleCount(w, r, geminiedge.New(), "gemini")
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/exec/ ./internal/server/ -v`
Expected: PASS.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/exec/ internal/server/
git commit -m "feat(exec): serve the token-counting routes"
```

---

### Task 33: The golden-file suite, request direction

**Files:**
- Create: `internal/golden/golden_test.go`
- Create: `internal/golden/testdata/golden/<dialect>/<case>/request.json` and `meta.json` for the ten cases listed below
- Create: `internal/golden/invariants_test.go`

**Interfaces:**
- Consumes: every edge dialect and outbound adapter built so far.
- Produces: the fixture layout Phase 8 extends with `bedrock` and both `vertex` publisher variants, and Phase 9's differential suite compares against.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 3: spec §7 fixes the fixture layout and the case list

Spec §7: fixtures live under `testdata/golden/<dialect>/<case>/` with
`request.json`, `ir.json`, and one rendered file per adapter kind, and **the
suite must include the awkward cases explicitly, because the easy ones never
catch anything**.

Two kinds of test share the fixtures, and the split matters. `golden_test.go`
regenerates with `-update` and catches *regressions* — it asserts that today's
output equals yesterday's, which proves nothing about correctness on the day the
file is written. `invariants_test.go` asserts the specific properties the spec
requires, by hand, from the same fixtures. Reviewing a regenerated golden file is
a judgment call; an invariant is not.

Comparison is semantic — both sides unmarshalled and compared as values — so a
key-order change in a `map[string]any` literal does not fail the suite. Phase 9's
differential suite is the byte-for-byte one, and it compares IR output against
passthrough rather than against these files.

- [ ] **Step 1: Write the harness**

Create `internal/golden/golden_test.go`:

```go
// Package golden runs every dialect and adapter over one fixture set, in both
// directions. It is a test-only package: it exists so the fixtures have a home
// that imports every edge and every adapter without any of them importing it.
package golden

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/ir"
)

var update = flag.Bool("update", false, "rewrite the golden files from current behavior")

// meta carries what a fixture needs beyond its body: the Gemini model lives in
// the URL, and the Anthropic version arrives as a header.
type meta struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

// dialects are the inbound edges, keyed by the directory name under
// testdata/golden.
func dialects() map[string]edge.Dialect {
	return map[string]edge.Dialect{
		"openai":    openaiedge.New(),
		"anthropic": anthropicedge.New(),
		"gemini":    geminiedge.New(),
	}
}

// adapters are the outbound kinds, keyed by the rendered file's base name.
func adapters() map[string]adapter.Adapter {
	return map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		// A fetcher with a zero byte cap and a transport that refuses, so a
		// fixture carrying a public image URL drops it with a warning instead
		// of reaching the network. No golden test makes an outbound request.
		"gemini": geminiadapter.NewWithFetcher(&offlineFetcher),
	}
}

const targetBase = "https://upstream.example/v1"

func target() *adapter.Target {
	return &adapter.Target{BaseURL: targetBase, APIKey: "sk-test", Model: "target-model"}
}

// caseDirs lists testdata/golden/<dialect>/<case> for one dialect.
func caseDirs(t *testing.T, dialect string) []string {
	t.Helper()
	root := filepath.Join("testdata", "golden", dialect)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no cases", root)
	}
	return out
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func readMeta(t *testing.T, dir string) meta {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if os.IsNotExist(err) {
		return meta{}
	}
	if err != nil {
		t.Fatal(err)
	}
	var m meta
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("meta %s: %v", dir, err)
	}
	return m
}

// requestFor builds the inbound HTTP request a fixture describes, including the
// path value a Gemini route would carry.
func requestFor(t *testing.T, dialect string, m meta, body []byte) *http.Request {
	t.Helper()
	target := "/v1/chat/completions"
	switch dialect {
	case "anthropic":
		target = "/v1/messages"
	case "gemini":
		target = "/v1beta/models/" + m.Path
	}
	if m.Path != "" && dialect != "gemini" {
		target = m.Path
	}
	r := httptest.NewRequest("POST", target, bytes.NewReader(body))
	for k, v := range m.Headers {
		r.Header.Set(k, v)
	}
	if dialect == "gemini" {
		r.SetPathValue("model", m.Path)
	}
	return r
}

// compareJSON asserts semantic equality, writing the file instead when -update
// is set. Values are compared rather than bytes so that a key-order change does
// not fail the suite.
func compareJSON(t *testing.T, path string, got any) {
	t.Helper()
	pretty, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(pretty, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want := readFixture(t, path)
	var gotV, wantV any
	if err := json.Unmarshal(pretty, &gotV); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantV); err != nil {
		t.Fatalf("golden %s: %v", path, err)
	}
	if !reflect.DeepEqual(gotV, wantV) {
		t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", path, pretty, want)
	}
}

func TestGoldenRequests(t *testing.T) {
	ctx := context.Background()
	for dialect, d := range dialects() {
		for _, dir := range caseDirs(t, dialect) {
			t.Run(dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				m := readMeta(t, dir)
				body := readFixture(t, filepath.Join(dir, "request.json"))

				req, _, err := d.ParseRequest(requestFor(t, dialect, m, body), 1<<20)
				if err != nil {
					t.Fatalf("parse: %v", err)
				}
				compareJSON(t, filepath.Join(dir, "ir.json"), req)

				for kind, ad := range adapters() {
					hr, warns, err := ad.BuildRequest(ctx, target(), req)
					if err != nil {
						t.Fatalf("%s build: %v", kind, err)
					}
					raw, err := io.ReadAll(hr.Body)
					if err != nil {
						t.Fatal(err)
					}
					var rendered any
					if err := json.Unmarshal(raw, &rendered); err != nil {
						t.Fatalf("%s produced invalid JSON: %v", kind, err)
					}
					compareJSON(t, filepath.Join(dir, "rendered", kind+".json"), rendered)
					compareJSON(t, filepath.Join(dir, "warnings", kind+".json"), warningStrings(warns))
				}
			})
		}
	}
}

// warningStrings renders warnings as a stable, human-readable list. A golden
// file full of struct field names is unreadable in a review, and reviewing
// these is the whole point.
func warningStrings(ws []ir.Warning) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.String())
	}
	return out
}

// offlineFetcher has a zero byte cap and a client that refuses every request,
// so a fixture carrying a public image URL exercises the drop-with-a-warning
// path rather than reaching the network.
var offlineFetcher = geminiadapter.Fetcher{
	Client:   &http.Client{Transport: refuseTransport{}},
	MaxBytes: 0,
}

type refuseTransport struct{}

func (refuseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errOffline
}

var errOffline = errors.New("golden: the suite makes no outbound requests")
```

Add `"errors"` to the import block. `strings` is used by `invariants_test.go`
in the same package, so it belongs there rather than here — drop it from this
file's imports if the compiler says it is unused.

- [ ] **Step 2: Create the ten request-direction fixtures**

Each is a directory under `internal/golden/testdata/golden/`. Create only
`request.json` (and `meta.json` where shown); the rest is generated in Step 4.

`openai/developer-role/request.json`:

```json
{"model":"target-model","messages":[
  {"role":"developer","content":"Answer in one word."},
  {"role":"user","content":"Capital of Norway?"}]}
```

`openai/multipart-two-images/request.json`:

```json
{"model":"target-model","messages":[{"role":"user","content":[
  {"type":"text","text":"Compare these."},
  {"type":"image_url","image_url":{"url":"data:image/png;base64,iVBORw=="}},
  {"type":"image_url","image_url":{"url":"https://example.invalid/second.png"}}]}]}
```

`openai/parallel-tool-calls/request.json`:

```json
{"model":"target-model","messages":[
  {"role":"user","content":"Weather in Oslo and Bergen?"},
  {"role":"assistant","content":null,"tool_calls":[
    {"id":"call_a","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Oslo\"}"}},
    {"id":"call_b","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Bergen\"}"}}]},
  {"role":"tool","tool_call_id":"call_a","content":"clear"},
  {"role":"tool","tool_call_id":"call_b","content":"rain"},
  {"role":"user","content":"And tomorrow?"}],
 "tools":[{"type":"function","function":{"name":"lookup","description":"weather",
   "parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}]}
```

`anthropic/thinking-with-signature/request.json`:

```json
{"model":"target-model","max_tokens":1024,"messages":[
  {"role":"user","content":"Is 91 prime?"},
  {"role":"assistant","content":[
    {"type":"thinking","thinking":"91 = 7 * 13","signature":"ErUBCkYIBRgCIkA="},
    {"type":"redacted_thinking","data":"EroBCkYIBRgCK"},
    {"type":"text","text":"No."}]},
  {"role":"user","content":"Show the factors."}]}
```

`anthropic/thinking-with-signature/meta.json`:

```json
{"headers":{"anthropic-version":"2023-06-01"}}
```

`anthropic/cache-control-1h/request.json`:

```json
{"model":"target-model","max_tokens":1024,
 "system":[{"type":"text","text":"You are a careful reviewer.",
   "cache_control":{"type":"ephemeral","ttl":"1h"}}],
 "messages":[{"role":"user","content":[
   {"type":"text","text":"Review this.","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}
```

`anthropic/five-cache-breakpoints/request.json`:

```json
{"model":"target-model","max_tokens":1024,"messages":[{"role":"user","content":[
  {"type":"text","text":"one","cache_control":{"type":"ephemeral","ttl":"5m"}},
  {"type":"text","text":"two","cache_control":{"type":"ephemeral","ttl":"5m"}},
  {"type":"text","text":"three","cache_control":{"type":"ephemeral","ttl":"5m"}},
  {"type":"text","text":"four","cache_control":{"type":"ephemeral","ttl":"5m"}},
  {"type":"text","text":"five","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}
```

`anthropic/tool-result-with-image/request.json`:

```json
{"model":"target-model","max_tokens":1024,"messages":[
  {"role":"assistant","content":[
    {"type":"tool_use","id":"call_a","name":"screenshot","input":{"url":"https://x.invalid"}}]},
  {"role":"user","content":[
    {"type":"tool_result","tool_use_id":"call_a","content":[
      {"type":"text","text":"captured"},
      {"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw=="}}]},
    {"type":"text","text":"What is on screen?"}]}]}
```

`anthropic/assistant-prefill/request.json`:

```json
{"model":"target-model","max_tokens":1024,"messages":[
  {"role":"user","content":"Return JSON only."},
  {"role":"assistant","content":"{"}]}
```

`anthropic/empty-assistant-turn/request.json`:

```json
{"model":"target-model","max_tokens":1024,"messages":[
  {"role":"user","content":"Say nothing."},
  {"role":"assistant","content":[]},
  {"role":"user","content":"Now say something."}]}
```

`gemini/thought-signature/request.json`:

```json
{"contents":[
  {"role":"user","parts":[{"text":"Is 91 prime?"}]},
  {"role":"model","parts":[
    {"text":"91 = 7 * 13","thought":true,"thoughtSignature":"CtEBAdHtim8="},
    {"text":"No."}]},
  {"role":"user","parts":[{"text":"Show the factors."}]}],
 "generationConfig":{"thinkingConfig":{"thinkingBudget":8000,"includeThoughts":true}}}
```

`gemini/thought-signature/meta.json`:

```json
{"path":"models/target-model:generateContent"}
```

- [ ] **Step 3: Write the invariants**

Create `internal/golden/invariants_test.go`. These assert the properties spec §7
names, from the same fixtures, so a regenerated golden file cannot quietly bless
a regression:

```go
package golden

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// renderCase parses a fixture and renders it for one adapter kind.
func renderCase(t *testing.T, dialect, name, kind string) (map[string]any, []string) {
	t.Helper()
	dir := filepath.Join("testdata", "golden", dialect, name)
	m := readMeta(t, dir)
	body := readFixture(t, filepath.Join(dir, "request.json"))

	req, _, err := dialects()[dialect].ParseRequest(requestFor(t, dialect, m, body), 1<<20)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ad := adapters()[kind]
	hr, warns, err := ad.BuildRequest(context.Background(),
		&adapter.Target{BaseURL: targetBase, APIKey: "sk-test", Model: "target-model"}, req)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	raw, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, warningStrings(warns)
}

func hasPrefixIn(list []string, prefix string) bool {
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func TestThinkingSignatureSurvivesAnthropicToAnthropic(t *testing.T) {
	body, warns := renderCase(t, "anthropic", "thinking-with-signature", "anthropic")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "ErUBCkYIBRgCIkA=") {
		t.Errorf("the thinking signature did not survive: %s", raw)
	}
	if !strings.Contains(string(raw), "EroBCkYIBRgCK") {
		t.Errorf("the redacted-thinking payload did not survive: %s", raw)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v; an Anthropic round trip loses nothing", warns)
	}
}

func TestThinkingIsDroppedWithAWarningElsewhere(t *testing.T) {
	for _, kind := range []string{"openaicompat", "gemini"} {
		_, warns := renderCase(t, "anthropic", "thinking-with-signature", kind)
		if !hasPrefixIn(warns, "messages[].assistant.redacted_thinking") &&
			!hasPrefixIn(warns, "messages[].redacted_thinking") {
			t.Errorf("%s: warnings = %v; redacted thinking cannot be expressed and must be recorded",
				kind, warns)
		}
	}
}

func TestThoughtSignatureSurvivesGeminiToGemini(t *testing.T) {
	body, warns := renderCase(t, "gemini", "thought-signature", "gemini")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "CtEBAdHtim8=") {
		t.Errorf("the thought signature did not survive: %s", raw)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
}

func TestCacheControlTTLSurvivesToAnthropicAndWarnsElsewhere(t *testing.T) {
	body, warns := renderCase(t, "anthropic", "cache-control-1h", "anthropic")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), `"ttl":"1h"`) {
		t.Errorf("the 1h TTL is a paid feature and did not survive: %s", raw)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
	for _, kind := range []string{"openaicompat", "gemini"} {
		_, w := renderCase(t, "anthropic", "cache-control-1h", kind)
		if !hasPrefixIn(w, "cache_control") {
			t.Errorf("%s: warnings = %v; a vanished paid feature must be visible", kind, w)
		}
	}
}

func TestFifthCacheBreakpointIsDroppedWithAWarning(t *testing.T) {
	body, warns := renderCase(t, "anthropic", "five-cache-breakpoints", "anthropic")
	blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(blocks) != 5 {
		t.Fatalf("blocks = %d; the content stays, only the marker is dropped", len(blocks))
	}
	marked := 0
	for _, b := range blocks {
		if _, ok := b.(map[string]any)["cache_control"]; ok {
			marked++
		}
	}
	if marked != 4 {
		t.Errorf("marked blocks = %d, want 4; a fifth breakpoint is a 400", marked)
	}
	if !hasPrefixIn(warns, "cache_control") {
		t.Errorf("warnings = %v", warns)
	}
}

func TestParallelToolCallsKeepTheirIdentities(t *testing.T) {
	body, _ := renderCase(t, "openai", "parallel-tool-calls", "openaicompat")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "call_a") || !strings.Contains(string(raw), "call_b") {
		t.Errorf("two calls to one function are only distinguishable by id: %s", raw)
	}

	gem, _ := renderCase(t, "openai", "parallel-tool-calls", "gemini")
	contents := gem["contents"].([]any)
	var responses []map[string]any
	for _, c := range contents {
		for _, p := range c.(map[string]any)["parts"].([]any) {
			if fr, ok := p.(map[string]any)["functionResponse"]; ok {
				responses = append(responses, fr.(map[string]any))
			}
		}
	}
	if len(responses) != 2 {
		t.Fatalf("functionResponses = %d", len(responses))
	}
	for i, r := range responses {
		if r["name"] != "lookup" {
			t.Errorf("response %d = %v; the name comes from the call it answers", i, r)
		}
	}
}

func TestToolResultImageIsHoistedNotDropped(t *testing.T) {
	for _, kind := range []string{"openaicompat", "gemini"} {
		body, warns := renderCase(t, "anthropic", "tool-result-with-image", kind)
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), "iVBORw==") {
			t.Errorf("%s: the image was dropped rather than hoisted: %s", kind, raw)
		}
		if !hasPrefixIn(warns, "messages[].tool_result.image") {
			t.Errorf("%s: warnings = %v; the move must be recorded", kind, warns)
		}
	}

	body, warns := renderCase(t, "anthropic", "tool-result-with-image", "anthropic")
	raw, _ := json.Marshal(body)
	if !strings.Contains(string(raw), "tool_result") || !strings.Contains(string(raw), "iVBORw==") {
		t.Errorf("Anthropic carries the image inside the result natively: %s", raw)
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}
}

func TestAssistantPrefillSurvivesOnlyForAnthropic(t *testing.T) {
	body, warns := renderCase(t, "anthropic", "assistant-prefill", "anthropic")
	msgs := body["messages"].([]any)
	if len(msgs) != 2 {
		t.Errorf("messages = %d; the prefill is Anthropic's own idiom", len(msgs))
	}
	if len(warns) != 0 {
		t.Errorf("warnings = %v", warns)
	}

	for _, kind := range []string{"openaicompat", "gemini"} {
		_, w := renderCase(t, "anthropic", "assistant-prefill", kind)
		if !hasPrefixIn(w, "messages[last].assistant_prefill") &&
			!hasPrefixIn(w, "messages[].assistant_prefill") {
			t.Errorf("%s: warnings = %v; a dropped prefill changes the answer", kind, w)
		}
	}
}

func TestDeveloperRoleBecomesSystem(t *testing.T) {
	body, _ := renderCase(t, "openai", "developer-role", "openaicompat")
	first := body["messages"].([]any)[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("role = %v; developer and system both emit as system", first["role"])
	}

	an, _ := renderCase(t, "openai", "developer-role", "anthropic")
	if _, ok := an["system"]; !ok {
		t.Errorf("Anthropic carries it in the top-level system field: %v", an)
	}
}

func TestMultipartImagesBothReachEveryTarget(t *testing.T) {
	body, _ := renderCase(t, "openai", "multipart-two-images", "openaicompat")
	parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	if len(parts) != 3 {
		t.Errorf("parts = %d, want text plus two images", len(parts))
	}

	// The Gemini fixture fetcher is offline with a zero cap, so the public URL
	// is dropped with a warning while the inline image survives. That is the
	// intended behavior, not a test artifact: review finding F9 says fileData
	// cannot carry an arbitrary HTTP URL.
	gem, warns := renderCase(t, "openai", "multipart-two-images", "gemini")
	raw, _ := json.Marshal(gem)
	if !strings.Contains(string(raw), "iVBORw==") {
		t.Errorf("the inline image did not survive: %s", raw)
	}
	if !hasPrefixIn(warns, "messages[].image") {
		t.Errorf("warnings = %v; a URL that could not be inlined must be recorded", warns)
	}
}

func TestEmptyAssistantTurnDoesNotBreakAnyTarget(t *testing.T) {
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		body, _ := renderCase(t, "anthropic", "empty-assistant-turn", kind)
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(raw) == 0 {
			t.Fatalf("%s produced nothing", kind)
		}
	}
}
```

- [ ] **Step 4: Generate the golden files and read them**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -run TestGoldenRequests -update
go test ./internal/golden/ -v
```

Expected: the second run passes with no `-update`.

**Then read every generated file.** A golden file that was never read asserts
only that behavior is stable, not that it is right. Check specifically that each
`rendered/anthropic.json` alternates roles, that each `rendered/gemini.json` has
exactly one `tools` entry when the fixture declares tools, and that no
`warnings/*.json` is unexpectedly empty. `git add -p` is a reasonable way to do
this pass.

- [ ] **Step 5: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/golden/
git commit -m "test(golden): cover the request direction"
```

---

### Task 34: The golden-file suite, response and stream directions

**Files:**
- Create: `internal/golden/response_test.go`, `internal/golden/stream_test.go`
- Create: `internal/golden/testdata/golden/responses/<kind>/<case>/response.json` and `testdata/golden/streams/<kind>/<case>/upstream.sse` for the nine cases below

**Interfaces:**
- Consumes: the harness helpers from Task 33.
- Produces: `normalize(v any) any`; `readStreamEvents(body string) []any`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Spec §7 says each case runs in both directions. The reverse direction is where
the response-shape decisions live: an Anthropic `refusal`, a Gemini blocked
prompt with zero candidates, a `stop_sequence` finish, and — the one the exec
loop depends on — a stream that errors after three content blocks.

`created` is the only non-deterministic field any writer produces, so it is
normalized to zero rather than pinned through a test-only setter in the
production package. Stream goldens are stored as a JSON array of
`{event, data}` objects rather than as raw SSE text: the same normalization
applies, and a reviewer can actually read the result.

- [ ] **Step 1: Create the response fixtures**

Under `internal/golden/testdata/golden/responses/`:

`openaicompat/stop-sequence/response.json`:

```json
{"id":"chatcmpl-1","model":"target-model","choices":[
  {"index":0,"message":{"role":"assistant","content":"Answer: 42"},"finish_reason":"stop"}],
 "usage":{"prompt_tokens":11,"completion_tokens":4,
   "prompt_tokens_details":{"cached_tokens":8},
   "completion_tokens_details":{"reasoning_tokens":3}}}
```

`openaicompat/tool-calls/response.json`:

```json
{"id":"chatcmpl-2","model":"target-model","choices":[
  {"index":0,"message":{"role":"assistant","content":null,"tool_calls":[
    {"id":"call_a","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Oslo\"}"}},
    {"id":"call_b","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Bergen\"}"}}]},
   "finish_reason":"tool_calls"}],
 "usage":{"prompt_tokens":20,"completion_tokens":15}}
```

`anthropic/refusal/response.json`:

```json
{"id":"msg_1","type":"message","role":"assistant","model":"target-model",
 "content":[{"type":"text","text":"I can't help with that."}],
 "stop_reason":"refusal","stop_sequence":null,
 "usage":{"input_tokens":14,"output_tokens":7}}
```

`anthropic/stop-sequence/response.json`:

```json
{"id":"msg_2","type":"message","role":"assistant","model":"target-model",
 "content":[{"type":"thinking","thinking":"counting","signature":"ErUBCkYIBRgCIkA="},
            {"type":"text","text":"one two three"}],
 "stop_reason":"stop_sequence","stop_sequence":"END",
 "usage":{"input_tokens":9,"output_tokens":5,
   "cache_creation_input_tokens":7,"cache_read_input_tokens":3}}
```

`anthropic/unknown-stop-reason/response.json`:

```json
{"id":"msg_3","type":"message","role":"assistant","model":"target-model",
 "content":[{"type":"text","text":"done"}],
 "stop_reason":"something_new_2027","stop_sequence":null,
 "usage":{"input_tokens":3,"output_tokens":1}}
```

`gemini/blocked-prompt/response.json`:

```json
{"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[
  {"category":"HARM_CATEGORY_DANGEROUS_CONTENT","probability":"HIGH"}]},
 "candidates":[]}
```

`gemini/tool-call-stop/response.json`:

```json
{"responseId":"r-1","modelVersion":"target-model","candidates":[
  {"index":0,"finishReason":"STOP","content":{"role":"model","parts":[
    {"text":"checking","thought":true,"thoughtSignature":"CtEBAdHtim8="},
    {"functionCall":{"name":"lookup","args":{"city":"Oslo"}}}]}}],
 "usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":6,
   "cachedContentTokenCount":4,"thoughtsTokenCount":5}}
```

- [ ] **Step 2: Write the response harness**

Create `internal/golden/response_test.go`:

```go
package golden

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// normalize strips the fields no writer can make deterministic. `created` is
// the only one: everything else in a response is a pure function of the IR.
func normalize(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, inner := range t {
			if k == "created" {
				t[k] = float64(0)
				continue
			}
			t[k] = normalize(inner)
		}
		return t
	case []any:
		for i, inner := range t {
			t[i] = normalize(inner)
		}
		return t
	default:
		return v
	}
}

// compareNormalized is compareJSON with the volatile fields flattened.
func compareNormalized(t *testing.T, path string, got any) {
	t.Helper()
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	compareJSON(t, path, normalize(v))
}

func responseCaseDirs(t *testing.T, kind string) []string {
	t.Helper()
	root := filepath.Join("testdata", "golden", "responses", kind)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

func TestGoldenResponses(t *testing.T) {
	for kind, ad := range adapters() {
		for _, dir := range responseCaseDirs(t, kind) {
			t.Run(kind+"/"+filepath.Base(dir), func(t *testing.T) {
				body := readFixture(t, filepath.Join(dir, "response.json"))
				resp := &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(strings.NewReader(string(body))),
				}
				out, err := ad.ParseResponse(resp)
				if err != nil {
					// A parse that fails is itself a result worth pinning: the
					// Gemini blocked prompt reaches the client as an error.
					var e *ir.Error
					if !errorsAs(err, &e) {
						t.Fatalf("parse: %v", err)
					}
					compareJSON(t, filepath.Join(dir, "error.json"), e)
					for dialect, d := range dialects() {
						rec := recorder()
						if werr := d.WriteError(rec, e); werr != nil {
							t.Fatalf("%s: %v", dialect, werr)
						}
						compareRecorded(t, filepath.Join(dir, "written", dialect+".json"), rec)
					}
					return
				}

				compareJSON(t, filepath.Join(dir, "ir.json"), out)
				for dialect, d := range dialects() {
					rec := recorder()
					if werr := d.WriteResponse(rec, out); werr != nil {
						t.Fatalf("%s: %v", dialect, werr)
					}
					compareRecorded(t, filepath.Join(dir, "written", dialect+".json"), rec)
				}
			})
		}
	}
}
```

Add three shared helpers to `golden_test.go`:

```go
func recorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

// compareRecorded pins the status code alongside the body, because §14 makes
// the status part of the contract — Claude Code retries a 529 and gives up on
// a 400.
func compareRecorded(t *testing.T, path string, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s: response is not JSON: %v\n%s", path, err, rec.Body.String())
	}
	compareJSON(t, path, map[string]any{
		"status": rec.Code,
		"body":   normalize(body),
	})
}

func errorsAs(err error, target **ir.Error) bool { return errors.As(err, target) }
```

- [ ] **Step 3: Create the stream fixtures**

Under `internal/golden/testdata/golden/streams/`:

`openaicompat/tool-call-fragments/upstream.sse`:

```
data: {"id":"chatcmpl-3","model":"target-model","choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"choices":[{"index":0,"delta":{"content":"Looking"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"lookup","arguments":""}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"Oslo\"}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"usage":{"prompt_tokens":18,"completion_tokens":12}}

data: [DONE]

```

`anthropic/thinking-then-text/upstream.sse`:

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_4","model":"target-model","usage":{"input_tokens":21,"cache_read_input_tokens":8}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"91 = 7 * 13"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"ErUBCkYIBRgCIkA="}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: content_block_start
data: {"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"No."}}

event: content_block_stop
data: {"type":"content_block_stop","index":1}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}

event: message_stop
data: {"type":"message_stop"}

```

`anthropic/error-after-three-blocks/upstream.sse`:

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_5","model":"target-model","usage":{"input_tokens":5}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"one "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"two "}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"three "}}

event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}

```

`gemini/incremental-text/upstream.sse`:

```
data: {"responseId":"r-2","modelVersion":"target-model","candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Hel"}]}}]}

data: {"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"lo"}]}}]}

data: {"candidates":[{"index":0,"content":{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{"city":"Oslo"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":4}}

```

Every fixture ends with a blank line, which is what terminates the last SSE
event; a file without one drops its final event.

- [ ] **Step 4: Write the stream harness**

Create `internal/golden/stream_test.go`:

```go
package golden

import (
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// readStreamEvents parses an SSE body into reviewable values. Raw text would
// carry the volatile `created` field into the golden file and make every run
// differ.
func readStreamEvents(body string) []any {
	out := []any{}
	for _, block := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(block) == "" {
			continue
		}
		entry := map[string]any{}
		for _, line := range strings.Split(block, "\n") {
			if name, ok := strings.CutPrefix(line, "event: "); ok {
				entry["event"] = name
			}
			if payload, ok := strings.CutPrefix(line, "data: "); ok {
				var v any
				if err := json.Unmarshal([]byte(payload), &v); err != nil {
					entry["data"] = payload // [DONE] and anything else non-JSON
				} else {
					entry["data"] = normalize(v)
				}
			}
		}
		out = append(out, entry)
	}
	return out
}

// collectEvents drains a stream sequence into a golden-friendly value,
// recording a terminal error as its own entry rather than losing it.
func collectEvents(seq iter.Seq2[ir.StreamEvent, error]) []any {
	out := []any{}
	for ev, err := range seq {
		if err != nil {
			out = append(out, map[string]any{"error": err.Error()})
			break
		}
		out = append(out, ev)
	}
	return out
}

// replay turns a captured event list back into a sequence, so the edge writers
// see exactly what the adapter produced.
func replay(events []ir.StreamEvent, final error) iter.Seq2[ir.StreamEvent, error] {
	return func(yield func(ir.StreamEvent, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if final != nil {
			yield(ir.StreamEvent{}, final)
		}
	}
}

func streamCaseDirs(t *testing.T, kind string) []string {
	t.Helper()
	root := filepath.Join("testdata", "golden", "streams", kind)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(root, e.Name()))
		}
	}
	return out
}

func TestGoldenStreams(t *testing.T) {
	for kind, ad := range adapters() {
		for _, dir := range streamCaseDirs(t, kind) {
			t.Run(kind+"/"+filepath.Base(dir), func(t *testing.T) {
				body := readFixture(t, filepath.Join(dir, "upstream.sse"))

				// Drain once for the event golden, and again into a replayable
				// list, so the writers see the same sequence the loop does.
				compareJSON(t, filepath.Join(dir, "events.json"),
					collectEvents(ad.ParseStream(strings.NewReader(string(body)), 1<<20)))

				var (
					events []ir.StreamEvent
					final  error
				)
				for ev, err := range ad.ParseStream(strings.NewReader(string(body)), 1<<20) {
					if err != nil {
						final = err
						break
					}
					events = append(events, ev)
				}

				for dialect, d := range dialects() {
					rec := recorder()
					if err := d.WriteStream(rec, replay(events, final)); err != nil {
						t.Fatalf("%s: %v", dialect, err)
					}
					compareJSON(t, filepath.Join(dir, "written", dialect+".json"),
						readStreamEvents(rec.Body.String()))
				}
			})
		}
	}
}

func TestStreamErrorReachesEveryDialect(t *testing.T) {
	dir := filepath.Join("testdata", "golden", "streams", "anthropic", "error-after-three-blocks")
	body := readFixture(t, filepath.Join(dir, "upstream.sse"))

	var (
		events []ir.StreamEvent
		final  error
	)
	for ev, err := range adapters()["anthropic"].ParseStream(strings.NewReader(string(body)), 1<<20) {
		if err != nil {
			final = err
			break
		}
		events = append(events, ev)
	}
	if final == nil {
		t.Fatal("the fixture ends in an error event and the adapter must surface it")
	}

	// Every dialect renders the failure in its own shape, and none of them may
	// end the stream as though it had succeeded normally.
	for dialect, d := range dialects() {
		rec := recorder()
		if err := d.WriteStream(rec, replay(events, final)); err != nil {
			t.Fatalf("%s: %v", dialect, err)
		}
		out := rec.Body.String()
		if !strings.Contains(out, "Overloaded") {
			t.Errorf("%s: the message did not reach the client: %s", dialect, out)
		}
		switch dialect {
		case "openai":
			if !strings.Contains(out, `"error"`) || !strings.HasSuffix(out, "data: [DONE]\n\n") {
				t.Errorf("openai: spec §4.9 wants an error payload then DONE: %s", out)
			}
		case "anthropic":
			if !strings.Contains(out, "event: error") {
				t.Errorf("anthropic: spec §4.9 wants a real error event: %s", out)
			}
			if strings.Contains(out, "message_stop") {
				t.Errorf("anthropic: an errored stream must not also stop normally: %s", out)
			}
		case "gemini":
			if !strings.Contains(out, "promptFeedback") {
				t.Errorf("gemini: SSE has no error event, so the shape is a final chunk: %s", out)
			}
		}
	}
}
```

- [ ] **Step 5: Generate, read, and run**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -run 'TestGoldenResponses|TestGoldenStreams' -update
go test ./internal/golden/ -v
```

Expected: PASS with no `-update`.

Read the generated files, and check three things specifically:
`responses/anthropic/refusal/written/openai.json` must carry
`finish_reason: "content_filter"`; `responses/gemini/blocked-prompt/error.json`
must be a content-filter error rather than an empty success;
`streams/openaicompat/tool-call-fragments/written/anthropic.json` must show a
`content_block_start` with index 1 for the tool call, not index 1000.

- [ ] **Step 6: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/golden/
git commit -m "test(golden): cover responses and streams"
```

---

### Task 35: Cross-dialect end-to-end through the fake fleet

**Files:**
- Create: `internal/exec/crossdialect_test.go`

**Interfaces:**
- Consumes: `exec.New` with the full registry from Task 4; every edge and adapter.
- Produces: nothing; this task adds tests only.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Spec §8 names three routes, and each one exercises a translation the unit tests
cannot: a whole request crossing the IR twice and coming back in the client's own
dialect. The third is the important one — an OpenAI client failing over from an
Anthropic provider to a Gemini one mid-chain — because it is the only test where
two different outbound renderings serve one inbound conversation and the client
must not be able to tell.

- [ ] **Step 1: Write the tests**

Create `internal/exec/crossdialect_test.go`:

```go
package exec

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
	"github.com/darkraise/darkrouter/internal/provider"
)

// fleetProvider is one upstream in a test fleet.
type fleetProvider struct {
	id      string
	kind    string
	baseURL string
	prio    int
}

func fleetExecutor(t *testing.T, ps []fleetProvider) *Executor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")

	var b strings.Builder
	b.WriteString("server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n")
	for _, p := range ps {
		b.WriteString("  - id: " + p.id + "\n    kind: " + p.kind +
			"\n    base_url: " + p.baseURL + "\n    api_key: ${K}\n    priority: " +
			itoa(p.prio) + "\n    models: [m]\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.New(),
	}, Deps{})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// serve builds an upstream returning one canned JSON body.
func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func send(t *testing.T, e *Executor, d edge.Dialect, target, body string,
	pathValue string) *httptest.ResponseRecorder {

	t.Helper()
	r := httptest.NewRequest("POST", target, strings.NewReader(body))
	if pathValue != "" {
		r.SetPathValue("model", pathValue)
	}
	rec := httptest.NewRecorder()
	e.Handle(rec, r, d)
	return rec
}

func TestAnthropicInboundServedByOpenAICompat(t *testing.T) {
	up := serve(t, `{"id":"chatcmpl-1","model":"m","choices":[{"index":0,
		"message":{"role":"assistant","content":null,"tool_calls":[
			{"id":"call_a","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Oslo\"}"}}]},
		"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":9,"completion_tokens":4}}`)

	e := fleetExecutor(t, []fleetProvider{{id: "compat", kind: "openaicompat", baseURL: up.URL}})
	rec := send(t, e, anthropicedge.New(), "/v1/messages",
		`{"model":"m","max_tokens":64,"messages":[{"role":"user","content":"weather?"}],
		  "tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`, "")

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"message"`) {
		t.Errorf("body is not an Anthropic message: %s", body)
	}
	if !strings.Contains(body, `"type":"tool_use"`) || !strings.Contains(body, "call_a") {
		t.Errorf("the tool call did not survive translation: %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Errorf("stop reason = %s", body)
	}
}

func TestGeminiInboundServedByAnthropic(t *testing.T) {
	up := serve(t, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-x",
		"content":[{"type":"text","text":"Oslo is clear."}],
		"stop_reason":"end_turn","usage":{"input_tokens":9,"output_tokens":4}}`)

	e := fleetExecutor(t, []fleetProvider{{id: "anth", kind: "anthropic", baseURL: up.URL}})
	rec := send(t, e, geminiedge.New(), "/v1beta/models/m:generateContent",
		`{"contents":[{"role":"user","parts":[{"text":"weather in Oslo?"}]}]}`,
		"m:generateContent")

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"candidates"`) || !strings.Contains(body, `"role":"model"`) {
		t.Errorf("body is not a Gemini response: %s", body)
	}
	if !strings.Contains(body, "Oslo is clear.") {
		t.Errorf("content did not survive: %s", body)
	}
}

func TestOpenAIInboundFailsOverFromAnthropicToGemini(t *testing.T) {
	var anthropicHits int
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicHits++
		w.WriteHeader(529)
	}))
	t.Cleanup(failing.Close)

	var geminiPath string
	working := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		geminiPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"responseId":"r-1","modelVersion":"gemini-x","candidates":[
			{"index":0,"finishReason":"STOP","content":{"role":"model",
			 "parts":[{"text":"42"}]}}],
			"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":1}}`))
	}))
	t.Cleanup(working.Close)

	// Priority orders the chain: the Anthropic provider is tried first and
	// fails with an overloaded status, then the Gemini one serves.
	e := fleetExecutor(t, []fleetProvider{
		{id: "anth", kind: "anthropic", baseURL: failing.URL, prio: 10},
		{id: "gem", kind: "gemini", baseURL: working.URL, prio: 1},
	})
	rec := send(t, e, openaiedge.New(), "/v1/chat/completions",
		`{"model":"m","messages":[{"role":"user","content":"what is 6*7?"}]}`, "")

	if rec.Code != 200 {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body.String())
	}
	if anthropicHits == 0 {
		t.Error("the first provider was never attempted")
	}
	if !strings.Contains(geminiPath, ":generateContent") {
		t.Errorf("the second provider was called at %q", geminiPath)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"object":"chat.completion"`) {
		t.Errorf("body is not an OpenAI completion: %s", body)
	}
	if !strings.Contains(body, "42") {
		t.Errorf("content did not survive two translations: %s", body)
	}
	if rec.Header().Get("X-Darkrouter-Provider") != "gem" {
		t.Errorf("provider header = %q", rec.Header().Get("X-Darkrouter-Provider"))
	}
	if rec.Header().Get("X-Darkrouter-Attempts") != "2" {
		t.Errorf("attempts header = %q", rec.Header().Get("X-Darkrouter-Attempts"))
	}
}

func TestAnthropicInboundStreamsThroughOpenAICompat(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"id\":\"c1\",\"model\":\"m\",\"choices\":[{\"delta\":{\"content\":\"Hi\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n"))
	}))
	t.Cleanup(up.Close)

	e := fleetExecutor(t, []fleetProvider{{id: "compat", kind: "openaicompat", baseURL: up.URL}})
	rec := send(t, e, anthropicedge.New(), "/v1/messages",
		`{"model":"m","max_tokens":32,"stream":true,
		  "messages":[{"role":"user","content":"hi"}]}`, "")

	body := rec.Body.String()
	for _, want := range []string{
		"event: message_start", "event: content_block_start",
		"event: content_block_delta", "event: message_stop",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "[DONE]") {
		t.Errorf("Anthropic sends no DONE sentinel:\n%s", body)
	}
}
```

If `internal/exec` already has a helper equivalent to `itoa`, use it and delete
this one; `strconv.Itoa` is also fine and simpler — it is written out here only
because the file otherwise needs no new import.

- [ ] **Step 2: Run the tests**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/exec/ -run 'Inbound|FailsOver' -v -race -count=1`
Expected: PASS, all four.

- [ ] **Step 3: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/exec/
git commit -m "test(exec): cover cross-dialect routes"
```

---

### Task 36: The round-trip property test

**Files:**
- Create: `internal/golden/roundtrip_test.go`

**Interfaces:**
- Consumes: `dialects()`, `adapters()`, `target()`, `requestFor` from Task 33.
- Produces: nothing; this task adds tests only.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Spec §8 asks for a property, not an example: **IR to wire to IR preserves every
field the target kind claims to support, and every dropped field produces a
warning.** `CacheControl.TTL` and `Thinking.Signature` are called out by name,
because both are silent-loss candidates that cost real money or real answer
quality.

The round trip works because each adapter's output is precisely what the
matching edge dialect parses: `anthropic.BuildRequest` writes the body
`edge/anthropic.ParseRequest` reads. That symmetry is the property being
asserted, and where it does not hold the test says which field broke it.

- [ ] **Step 1: Write the test**

Create `internal/golden/roundtrip_test.go`:

```go
package golden

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

// roundTrip renders an IR request for one kind and parses the result back with
// the edge dialect that speaks the same wire format.
func roundTrip(t *testing.T, kind string, req *ir.Request) (*ir.Request, []string) {
	t.Helper()
	hr, warns, err := adapters()[kind].BuildRequest(context.Background(), target(), req)
	if err != nil {
		t.Fatalf("%s build: %v", kind, err)
	}
	body, err := io.ReadAll(hr.Body)
	if err != nil {
		t.Fatal(err)
	}

	dialect := kind
	if kind == "openaicompat" {
		dialect = "openai"
	}
	m := meta{}
	if dialect == "gemini" {
		m.Path = "models/target-model:generateContent"
	}
	back, _, err := dialects()[dialect].ParseRequest(requestFor(t, dialect, m, body), 1<<20)
	if err != nil {
		t.Fatalf("%s reparse: %v\n%s", kind, err, body)
	}
	return back, warningStrings(warns)
}

func firstBlock(t *testing.T, req *ir.Request, kind ir.BlockType) ir.ContentBlock {
	t.Helper()
	for _, m := range req.Messages {
		for _, b := range m.Content {
			if b.Type == kind {
				return b
			}
		}
	}
	t.Fatalf("no %s block in %+v", kind, req.Messages)
	return ir.ContentBlock{}
}

func thinkingRequest() *ir.Request {
	n := 1024
	return &ir.Request{
		Model:     "target-model",
		MaxTokens: &n,
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Is 91 prime?"}}},
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.BlockThinking, Thinking: &ir.Thinking{
					Text: "91 = 7 * 13", Signature: "ErUBCkYIBRgCIkA=",
				}},
				{Type: ir.BlockText, Text: "No."},
			}},
			{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "Factors?"}}},
		},
	}
}

func cacheRequest() *ir.Request {
	n := 1024
	return &ir.Request{
		Model:     "target-model",
		MaxTokens: &n,
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{
			Type: ir.BlockText, Text: "a long shared prefix",
			CacheControl: &ir.CacheControl{Type: "ephemeral", TTL: "1h"},
		}}}},
	}
}

func TestThinkingSignatureRoundTripsOrWarns(t *testing.T) {
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, warns := roundTrip(t, kind, thinkingRequest())

			supported := kind == "anthropic" || kind == "gemini"
			if !supported {
				if !hasPrefixIn(warns, "messages[].assistant.thinking") {
					t.Errorf("thinking was dropped with no warning: %v", warns)
				}
				return
			}
			b := firstBlock(t, back, ir.BlockThinking)
			if b.Thinking == nil || b.Thinking.Signature != "ErUBCkYIBRgCIkA=" {
				t.Errorf("signature = %+v; it must return byte for byte", b.Thinking)
			}
			if b.Thinking.Text != "91 = 7 * 13" {
				t.Errorf("thinking text = %q", b.Thinking.Text)
			}
		})
	}
}

func TestCacheControlTTLRoundTripsOrWarns(t *testing.T) {
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, warns := roundTrip(t, kind, cacheRequest())

			if kind != "anthropic" {
				if !hasPrefixIn(warns, "cache_control") {
					t.Errorf("a paid feature vanished with no warning: %v", warns)
				}
				return
			}
			b := firstBlock(t, back, ir.BlockText)
			if b.CacheControl == nil || b.CacheControl.TTL != "1h" {
				t.Errorf("cache control = %+v", b.CacheControl)
			}
		})
	}
}

func TestToolCallIdentityRoundTrips(t *testing.T) {
	n := 512
	req := &ir.Request{
		Model: "target-model", MaxTokens: &n,
		Tools: []ir.Tool{{Name: "lookup", Schema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []ir.Message{
			{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "two cities"}}},
			{Role: ir.RoleAssistant, Content: []ir.ContentBlock{
				{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
					ID: "call_a", Name: "lookup", Input: json.RawMessage(`{"city":"Oslo"}`)}},
				{Type: ir.BlockToolUse, ToolUse: &ir.ToolUse{
					ID: "call_b", Name: "lookup", Input: json.RawMessage(`{"city":"Bergen"}`)}},
			}},
			{Role: ir.RoleTool, Content: []ir.ContentBlock{
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
					ToolUseID: "call_a",
					Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "clear"}}}},
				{Type: ir.BlockToolResult, ToolResult: &ir.ToolResult{
					ToolUseID: "call_b",
					Content:   []ir.ContentBlock{{Type: ir.BlockText, Text: "rain"}}}},
			}},
		},
	}

	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, _ := roundTrip(t, kind, req)

			var ids []string
			for _, m := range back.Messages {
				for _, b := range m.Content {
					if b.Type == ir.BlockToolResult && b.ToolResult != nil {
						ids = append(ids, b.ToolResult.ToolUseID)
					}
				}
			}
			if len(ids) != 2 {
				t.Fatalf("tool results = %v", ids)
			}
			if ids[0] == ids[1] {
				t.Errorf("both results carry %q; parallel calls to one function are only "+
					"distinguishable by id", ids[0])
			}
		})
	}
}

func TestEveryDroppedFieldProducesAWarning(t *testing.T) {
	k := 40
	n := 512
	req := &ir.Request{
		Model: "target-model", MaxTokens: &n, TopK: &k,
		Safety:            []ir.SafetySetting{{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "BLOCK_NONE"}},
		ResponseFormat:    &ir.ResponseFormat{Type: "json_schema", Schema: json.RawMessage(`{"type":"object"}`)},
		ParallelToolCalls: boolPtr(false),
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{
			{Type: ir.BlockText, Text: "hi"},
			{Type: ir.BlockAudio, Media: &ir.Media{MIME: "audio/wav", Data: "UklGR"}},
		}}},
	}

	// Each kind's unsupported set, from the spec's mapping tables.
	unsupported := map[string][]string{
		"openaicompat": {"top_k", "safety"},
		// Not response_format: Anthropic's structured output is GA and Task 14
		// emits it under output_config.format.
		"anthropic": {"safety", "messages[].audio"},
		"gemini":    {"parallel_tool_calls"},
	}
	for kind, fields := range unsupported {
		t.Run(kind, func(t *testing.T) {
			_, warns := roundTrip(t, kind, req)
			for _, f := range fields {
				if !hasPrefixIn(warns, f) {
					t.Errorf("%s dropped %s with no warning; warnings = %v", kind, f, warns)
				}
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }

func TestSystemContentSurvivesEveryTarget(t *testing.T) {
	n := 128
	req := &ir.Request{
		Model: "target-model", MaxTokens: &n,
		System:   []ir.ContentBlock{{Type: ir.BlockText, Text: "be terse"}},
		Messages: []ir.Message{{Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}}}},
	}
	for _, kind := range []string{"openaicompat", "anthropic", "gemini"} {
		t.Run(kind, func(t *testing.T) {
			back, _ := roundTrip(t, kind, req)
			text := strings.Join(systemTexts(back), " ")
			if !strings.Contains(text, "be terse") {
				t.Errorf("system content was lost: system=%+v messages=%+v", back.System, back.Messages)
			}
		})
	}
}

// systemTexts gathers system content from both places it can live: the
// top-level field the Anthropic and Gemini edges fill, and the inline
// RoleSystem messages the OpenAI edge keeps in place.
func systemTexts(req *ir.Request) []string {
	var out []string
	for _, b := range req.System {
		out = append(out, b.Text)
	}
	for _, m := range req.Messages {
		if m.Role != ir.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			out = append(out, b.Text)
		}
	}
	return out
}
```

- [ ] **Step 2: Run the test**

Run: `export PATH=$PATH:/usr/local/go/bin && go test ./internal/golden/ -run 'RoundTrips|DroppedField|SystemContent' -v`
Expected: PASS.

A failure here is a real finding, not a test to adjust. `TestEveryDroppedFieldProducesAWarning`
in particular is the one that catches an adapter growing a new silent drop.

- [ ] **Step 3: Run the suite and commit**

```bash
export PATH=$PATH:/usr/local/go/bin && go test ./... -race -count=1 && go vet ./... && gofmt -l .
git add internal/golden/
git commit -m "test(golden): assert the round-trip property"
```

---

### Task 37: Verification, documentation, and the phase record

**Files:**
- Modify: `README.md`
- Modify: `docs/PROGRESS.md`
- Modify: `darkrouter.example.yaml`

**Interfaces:**
- Consumes: everything.
- Produces: the handoff the next phase reads.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Spec §9's done criteria are not all mechanical. Two of them need a real provider,
and one of those — a cached system prompt — needs a real *Anthropic* provider,
which this machine has no key for. Say so in the record rather than quietly
marking it done: a criterion recorded as unverified is a fact the next phase can
act on, and a criterion recorded as passed when it was not is a lie the next
phase will build on.

- [ ] **Step 1: Run the full mechanical verification**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1
go vet ./...
gofmt -l .
CGO_ENABLED=0 go build -o /tmp/dr-phase4 ./cmd/darkrouter && file /tmp/dr-phase4 && ls -la /tmp/dr-phase4
rm -f /tmp/dr-phase4
```

Expected: all tests pass, `go vet` and `gofmt -l` print nothing, and the binary
reports `statically linked`. Record its size — it was 16.9 MB before Task 30 and
should be near 31 MB after.

- [ ] **Step 2: Run the race detector harder on the changed concurrent paths**

`internal/exec` and `internal/server` are the two packages this phase changed
that carry concurrency. The detector only observes interleavings the tests
actually schedule, so repeat them.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ ./internal/server/ -race -count=5
```

Expected: PASS. A failure here is a real race, not flakiness.

- [ ] **Step 3: Rebuild the container**

```bash
docker build -t darkrouter:phase4 . && docker images darkrouter:phase4
```

Expected: the build succeeds. Record the image size next to Phase 1's 28.8 MB;
the tokenizer's vocabulary is the difference.

- [ ] **Step 4: Verify the live done criteria against Groq**

The repository root holds a git-ignored `.env` with `GROQ_KEY`. Use high ports:
8080 and 8081 belong to an unrelated application on this machine.

```bash
export PATH=$PATH:/usr/local/go/bin
set -a; . ./.env; set +a
LIVE=$(mktemp -d)
cat > "$LIVE/darkrouter.yaml" <<'YAML'
server:
  proxy_listen: 127.0.0.1:18080
  admin_listen: 127.0.0.1:18081
providers:
  - id: groq
    kind: openaicompat
    base_url: https://api.groq.com/openai/v1
    api_key: ${GROQ_KEY}
    models: [openai/gpt-oss-120b]
YAML
go build -o "$LIVE/darkrouter" ./cmd/darkrouter
"$LIVE/darkrouter" -config "$LIVE/darkrouter.yaml" -db "$LIVE/darkrouter.db" &
DR_PID=$!
sleep 2
```

Then, one criterion at a time:

**Anthropic inbound, unary, with a system prompt:**

```bash
curl -sS -X POST localhost:18080/v1/messages \
  -H 'content-type: application/json' -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"openai/gpt-oss-120b","max_tokens":64,
       "system":"Answer in exactly one word.",
       "messages":[{"role":"user","content":"Capital of Norway?"}]}'
```

Expected: `{"id":...,"type":"message","role":"assistant","content":[{"type":"text",...}],"stop_reason":"end_turn",...}`.

**Anthropic inbound, tool use across two turns:**

```bash
curl -sS -X POST localhost:18080/v1/messages \
  -H 'content-type: application/json' -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"openai/gpt-oss-120b","max_tokens":256,
       "tools":[{"name":"lookup","description":"weather by city",
                 "input_schema":{"type":"object","properties":{"city":{"type":"string"}},
                                 "required":["city"]}}],
       "messages":[{"role":"user","content":"Use the lookup tool for Oslo."}]}'
```

Expected: a `tool_use` block and `"stop_reason":"tool_use"`. Take the `id` it
returns and send the second turn:

```bash
curl -sS -X POST localhost:18080/v1/messages \
  -H 'content-type: application/json' -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"openai/gpt-oss-120b","max_tokens":256,
       "tools":[{"name":"lookup","description":"weather by city",
                 "input_schema":{"type":"object","properties":{"city":{"type":"string"}},
                                 "required":["city"]}}],
       "messages":[
         {"role":"user","content":"Use the lookup tool for Oslo."},
         {"role":"assistant","content":[{"type":"tool_use","id":"REPLACE_ME","name":"lookup","input":{"city":"Oslo"}}]},
         {"role":"user","content":[{"type":"tool_result","tool_use_id":"REPLACE_ME","content":[{"type":"text","text":"4C and clear"}]}]}]}'
```

Expected: a text answer mentioning the result. A 400 from Groq here means the
reverse tool-result split placed the `tool` messages wrongly, which is the
failure spec §4.1 warns about.

**Anthropic inbound, streaming:**

```bash
curl -sSN -X POST localhost:18080/v1/messages \
  -H 'content-type: application/json' -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"openai/gpt-oss-120b","max_tokens":128,"stream":true,
       "messages":[{"role":"user","content":"Count to five."}]}'
```

Expected: `event: message_start`, then `content_block_start`,
`content_block_delta` arriving incrementally, `content_block_stop`,
`message_delta`, `message_stop`. No `[DONE]`.

**Gemini inbound, both wire forms:**

```bash
curl -sS -X POST 'localhost:18080/v1beta/models/openai%2Fgpt-oss-120b:generateContent' \
  -H 'content-type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"Capital of Norway?"}]}]}'

curl -sSN -X POST 'localhost:18080/v1beta/models/openai%2Fgpt-oss-120b:streamGenerateContent?alt=sse' \
  -H 'content-type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"Count to five."}]}]}'

curl -sSN -X POST 'localhost:18080/v1beta/models/openai%2Fgpt-oss-120b:streamGenerateContent' \
  -H 'content-type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"Count to five."}]}]}'
```

Expected: a `candidates` object; then an SSE stream of `data:` chunks; then a
JSON array. The percent-encoded slash in the model name is the case spec §3.1
calls out — a 404 here means `ExtractModel` or the route pattern is wrong.

**countTokens, estimated path:**

```bash
curl -sS -i -X POST 'localhost:18080/v1beta/models/openai%2Fgpt-oss-120b:countTokens' \
  -H 'content-type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"Capital of Norway?"}]}]}'

curl -sS -i -X POST localhost:18080/v1/messages/count_tokens \
  -H 'content-type: application/json' -H 'anthropic-version: 2023-06-01' \
  -d '{"model":"openai/gpt-oss-120b","messages":[{"role":"user","content":"Capital of Norway?"}]}'
```

Expected: `{"totalTokens":N}` and `{"input_tokens":N}`, both carrying
`X-Darkrouter-Estimated: true` — the target is `openaicompat`, which has no
native counting endpoint. The o200k tokenizer is used, because `gpt-oss` is in
the known-family table.

Shut the gateway down and confirm nothing is left running:

```bash
kill "$DR_PID"; wait "$DR_PID" 2>/dev/null
ps -p "$DR_PID" >/dev/null && echo "STILL RUNNING" || echo "stopped"
rm -rf "$LIVE"
```

- [ ] **Step 5: Update the README**

Add the new routes to the README's endpoint list, beside the existing
`/v1/chat/completions` and `/v1/models`:

```markdown
| Route | Dialect |
|---|---|
| `POST /v1/chat/completions` | OpenAI |
| `GET /v1/models` | OpenAI |
| `POST /v1/messages` | Anthropic |
| `POST /v1/messages/count_tokens` | Anthropic |
| `POST /v1beta/models/{model}:generateContent` | Gemini |
| `POST /v1beta/models/{model}:streamGenerateContent` | Gemini |
| `POST /v1beta/models/{model}:countTokens` | Gemini |
| `GET /v1beta/models` | Gemini |
```

and a short paragraph stating that any inbound dialect routes to any provider
kind, that a field a target cannot express is recorded as a warning on the
request rather than dropped silently, and that `X-Darkrouter-Estimated: true`
marks a locally estimated token count. Point Claude Code at
`ANTHROPIC_BASE_URL=http://<host>:<port>` and Gemini CLI at the `/v1beta` base;
both send their own credential form, which is compared against
`server.proxy_token`.

Add an `anthropic` and a `gemini` provider block to `darkrouter.example.yaml`,
commented out, so the three kinds are visible in one place.

- [ ] **Step 6: Update `docs/PROGRESS.md`**

Mark Phase 4 complete in the status table. Under "Carried forward", remove the
two items this phase closed and say so explicitly:

- `edge.Passthrough.Surface` is now `ir.Surface` (Task 2).
- The per-dialect in-stream error shape is defined and tested (Tasks 12, 20, 28).

Add a "Carried into Phase 5 and beyond" section recording, at minimum:

- **The token estimate ignores media and is not the provider's tokenizer.**
  `X-Darkrouter-Estimated` says so, but a client budgeting a context window
  around a large image will be wrong. Phase 6's catalog is what makes a better
  answer possible.
- **Two spec assumptions were stale and are now corrected in the plan**, both
  confirmed against the live documentation on 2026-08-22: Anthropic's structured
  output is generally available under `output_config.format` rather than a beta,
  and extended thinking has split into two mutually exclusive per-generation
  shapes. Spec §4.6 should be amended to match rather than left to mislead the
  next reader.
- **Gemini media inlining fetches client-supplied URLs.** It is bounded to
  http and https, no redirects, 20 MB, and a ten-second timeout, but it is
  outbound traffic the gateway initiates on a client's behalf. Phase 7's settings
  screen should be able to turn it off.
- Whatever else the implementation turned up. The point of this section is that
  the next phase does not rediscover it.

Update the "wire-format notes for phase 9" section if anything this phase built
changed what the differential suite will see.

- [ ] **Step 7: Commit the documentation**

```bash
git add README.md docs/PROGRESS.md darkrouter.example.yaml
git commit -m "docs: record phase 4 completion"
```

- [ ] **Step 8: Finish the branch**

REQUIRED SUB-SKILL: superpowers:finishing-a-development-branch.

Do not push. The repository is roughly 53 commits ahead of `origin/master` by
design, and pushing has not been asked for.

---

## Done criteria

Check each against spec §9 before calling the phase complete.

- [ ] Claude Code against `/v1/messages` works with an Anthropic provider and a Groq one, including tool use across several turns and a cached system prompt. *(Tasks 18–20, 29; verified against Groq in Task 37. The Anthropic-provider half and the cached system prompt need an Anthropic API key this machine does not have — record the gap rather than claiming it.)*
- [ ] Gemini CLI against `/v1beta` works with a Gemini provider and an Anthropic one, including `countTokens`. *(Tasks 26–29, 31, 32; the shapes are verified against Groq in Task 37, the provider halves need keys.)*
- [ ] Extended thinking with signatures survives Anthropic-to-Anthropic; Gemini thought signatures survive Gemini-to-Gemini; loss elsewhere appears as a warning. *(Tasks 13, 22, 33, 36)*
- [ ] Streaming tool calls reconstruct correctly from OpenAI's fragmented arguments and from Gemini's whole-part chunks. *(Tasks 12, 25, 28, 34)*
- [ ] A Gemini blocked prompt produces a content-filter error rather than an empty success. *(Task 24, fixture in Task 34)*
- [ ] `go test ./...` passes, golden files included. *(Task 37)*

## Carried into Phase 5 and beyond

Written before implementation; Task 37 revises it with what actually happened.

- **Capability filtering still admits everything.** Unchanged from Phase 3: every model's capabilities are `inferred` until Phase 6, and inferred capabilities pass with a warning. A request needing vision will route to a text-only model and fail at the provider.
- **Failed attempts still burn tokens invisibly.** `request_attempts` carries no usage columns, so a pre-commit failover's tokens never reach `usage_daily`.
- **The Anthropic `max_tokens` substitution is a constant.** 4096 with a warning, because the catalog cannot supply the model's real maximum until Phase 6. A model whose real cap is lower will still 400.
- **The effort-to-budget clamp is inert.** `xlate.EffortBudget` takes a `maxOut` argument every caller passes as 0. Phase 6 supplies it.
- **No Anthropic-shaped `GET /v1/models`.** That path serves the OpenAI listing. If a client turns out to need Anthropic's shape there, it needs a routing decision rather than a second handler.
- **The Anthropic model-generation table is a name heuristic.** `traitsFor` in `internal/adapter/anthropic/build.go` decides the thinking mode and the sampling rules by matching fragments of the model name, because there is no catalog until Phase 6. It is wrong for an aliased or proxied model whose name says nothing about its generation, and it needs a new entry every time Anthropic ships a generation. Phase 6 should move these three booleans onto the catalog entry and delete the table.
- **A refusal reaches the client as a hard error, not a refusal.** A Gemini blocked prompt is HTTP 200 with `promptFeedback.blockReason` natively, and 400 `INVALID_ARGUMENT` through Darkrouter; an Anthropic `refusal` is a 200 with `stop_reason: "refusal"` natively, and a 400 `invalid_request_error` through Darkrouter on the unary path. This follows from master design §8.1 classifying content filter as `Fatal` and §14 normalizing into the inbound dialect, so it is deliberate — but Gemini CLI and Claude Code will surface it as a failure rather than as the model declining, and that is worth knowing before someone files it as a bug.
- **`Adapter.Surfaces()` from master design §5.1 does not exist.** The interface still has no way to say which surfaces a kind implements, so routing cannot exclude a provider on that basis. Phase 5 introduces the auxiliary surfaces and is where it becomes load-bearing.
- **Bedrock and Vertex extend the golden suite, not replace it.** Phase 8 adds `bedrock` and both `vertex` publisher variants to `adapters()` in `internal/golden/golden_test.go`, regenerates, and reviews the new files.
