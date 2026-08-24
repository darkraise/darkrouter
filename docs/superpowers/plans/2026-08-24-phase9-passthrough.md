# Phase 9 — Passthrough Fast Path Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the inbound dialect already speaks the chosen target's wire format, stop re-rendering — rewrite the model identifier, swap auth, forward the bytes, and extract usage from the response as it streams past.

**Architecture:** Passthrough is an alternative *rendering* inside the existing attempt, not a second pipeline. The loop, the budget gate, the live health re-check, credential rotation, outcome classification, attempt rows, health signals and the request log are untouched. Two new seams carry the whole phase: `adapter.Forwarder`, an optional interface an adapter implements when its wire format matches an inbound dialect, and a per-attempt decision in `exec.attempt` that chooses between `op.Build` and a forwarded body. Eligibility is decided per attempt, so one request can pass through on attempt one and translate on attempt two.

**Tech Stack:** Go 1.26.1, standard library only. No new dependencies — the phase adds no vendor integration.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase9-passthrough.md`
**Master design:** `docs/superpowers/specs/2026-08-22-darkrouter-design.md` — §4.1 (eligibility) and §4.2 (permitted mutations) are authoritative and this plan implements them rather than restating them.

## Global Constraints

- **Module** `github.com/darkraise/darkrouter`, **Go 1.26.1**, **`CGO_ENABLED=0`** for the image. Go lives at `/usr/local/go/bin`; every task's commands assume `export PATH=$PATH:/usr/local/go/bin`.
- **No new dependencies.** Nothing in this phase needs one. A task that reaches for a library is solving the wrong problem.
- **`DARKROUTER_MASTER_KEY` must be set for any run of the binary**, including smoke tests. A throwaway value is fine.
- **English only** — code, comments, docs, commits, configs, errors, tests.
- **Commits** are `<type>(<scope>): <subject>`, subject ≤50 chars, imperative, no period. Atomic.
- **Exactly three body mutations are permitted** (master design §4.2): the model identifier, authentication, and `stream_options` injection for `openaicompat` streaming targets. Any fourth is a defect, and Task 11's differential suite is what catches it.
- **The guarantee is semantic preservation, not byte preservation** (spec §5.1) — except on the same-name path, where the body is forwarded untouched and byte fidelity is exact.
- **Routing, health and logging *semantics* do not change** (spec §2). Task 10 adds one column to `request_attempts`; that is an addition to what is recorded, not a change to what any of them decide.

## Verifying the whole tree

Every task ends with these. They are not repeated inside each task's steps beyond the specific test being written:

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l .
```

`gofmt -l .` printing a path is a failure even when the tests pass. Tasks touching
`internal/exec` also run `go test ./internal/exec/... -race -count=1`.

## File structure

| File | Responsibility |
|---|---|
| `internal/edge/edge.go` | `Passthrough` grows the URL-carried operation, the credential-stripped query, and the stream flag |
| `internal/adapter/adapter.go` | `Forwarder` and `Forward` — the optional interface that makes a kind forwardable |
| `internal/adapter/openaicompat/forward.go` | `/chat/completions`, bearer auth |
| `internal/adapter/anthropic/forward.go` | `/messages`, `x-api-key`, `anthropic-version` default |
| `internal/adapter/gemini/forward.go` | `/models/{model}:{method}`, replayed query, `x-goog-api-key` |
| `internal/exec/passthrough.go` | Eligibility, the body rewrite, the header allowlist |
| `internal/exec/rawstream.go` | The per-dialect raw SSE recognizer and the event splitter |
| `internal/exec/forward.go` | The streaming and unary forwarders |
| `internal/ir/ir.go` | `ErrUnsupportedMedia`, the 415 a compressed inbound body earns |
| `internal/store/migrations/0005_attempt_path.sql` | `request_attempts.path` |
| `internal/store/log.go`, `adminstore.go`, `internal/admin/requests.go`, `web/src/components/trace-drawer.tsx` | carrying that column from the writer to the drawer |
| `internal/golden/differential_test.go` | The corpus run through both paths, compared three ways |
| `internal/exec/bench_test.go` | Time-to-first-token and allocations, both paths |
| `internal/e2e/phase9_test.go` | Assembled server, fake upstreams, both paths |

## Two decisions this plan makes that the spec leaves open

Read these before Task 6; three tasks depend on them.

**1. The recognizer follows phase 3's content definition, not spec §6's event
list.** Spec §6 names `content_block_start`/`content_block_delta` as Anthropic's
commit trigger, then says "using phase 3's definition: text, thinking, or
tool-input content". The two disagree: `internal/adapter/anthropic/stream.go`
maps `content_block_start` to `ir.EventBlockStart`, which
`exec.IsContentBearing` rejects, and a `signature_delta` to a thinking delta
with empty text, which it also rejects. Phase 3's definition wins, because
spec §10 requires the two paths to agree and a recognizer that committed on
`content_block_start` would commit one event earlier than the IR path on every
Anthropic stream. Concretely: commit on `content_block_delta` carrying a
non-empty `text`, `thinking`, or `partial_json`, and on nothing else.

**2. The unary forwarder buffers the whole body rather than a bounded tail.**
Spec §7 rejects a size-capped *prefix* buffer because OpenAI's `usage` sits at
the end of a unary body, and offers a bounded tail buffer as the alternative.
Reading the whole body is simpler and strictly better: the IR path's
`ParseResponse` already reads the entire unary body into memory, so passthrough
buffering it changes no memory characteristic of the product, and there is no
truncation point to get wrong. A hostile provider is bounded by a cap; on breach
the remaining bytes are copied through unparsed and the row records usage as
unavailable, per §7's "losing a token count is acceptable, corrupting a response
is not".

## Consequence to record, not to fix

Setting `DisableCompression` on the shared transport (Task 9, spec §8) applies
to the IR path too, which will now receive uncompressed response bodies. That
costs bandwidth on every request in exchange for making passthrough fidelity
unconditional rather than dependent on Go's transparent-gzip preconditions. It
is the spec's choice and this plan follows it; Task 15 records it.

---

### Task 1: `edge.Passthrough` carries what a forward needs

**Files:**
- Modify: `internal/edge/edge.go:11-18`, `internal/edge/openai/parse.go:160`, `internal/edge/openai/responses.go:176-178`, `internal/edge/anthropic/parse.go:164`, `internal/edge/gemini/parse.go:178-181`
- Test: `internal/edge/gemini/parse_test.go`, `internal/edge/openai/parse_test.go`

**Interfaces:**
- Produces: `edge.Passthrough{Body []byte, ModelField string, Surface ir.Surface, Method string, Query url.Values, Stream bool}`. Tasks 3, 4, 5, 7 and 9 all read these fields.

The struct has existed since phase 1 with three fields and no consumer. Three
more are needed before anything can forward: Gemini's model lives in the URL, so
the operation suffix and the query string have to travel with the body; and the
forwarder has to know whether the request was streaming without holding the
`ir.Request`.

`Query` is the inbound query **with the credential removed**, not the raw one.
Spec §5.3: Gemini's `?key=` is stripped from the rewritten URL rather than
overridden, and the edge is the only thing that knows its own dialect spells the
credential `key`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/edge/gemini/parse_test.go`:

```go
func TestParseCarriesTheURLOperationAndQuery(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	r := httptest.NewRequest("POST",
		"/v1beta/models/gemini-2.0-flash:streamGenerateContent?alt=sse&key=secret",
		bytes.NewReader(body))
	r.SetPathValue("model", "gemini-2.0-flash:streamGenerateContent")

	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt.Method != "streamGenerateContent" {
		t.Errorf("Method = %q, want streamGenerateContent", pt.Method)
	}
	if !pt.Stream {
		t.Error("Stream = false on streamGenerateContent")
	}
	if got := pt.Query.Get("alt"); got != "sse" {
		t.Errorf("alt = %q, want sse", got)
	}
	// The inbound proxy token must not be replayed onto the upstream URL:
	// forwarding it would send Darkrouter's own proxy_token to the vendor.
	if _, present := pt.Query["key"]; present {
		t.Error("the inbound credential survived into the forwarded query")
	}
}

func TestParseLeavesModelFieldEmptyForURLCarriedModels(t *testing.T) {
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	r := httptest.NewRequest("POST", "/v1beta/models/gemini-2.0-flash:generateContent",
		bytes.NewReader(body))
	r.SetPathValue("model", "gemini-2.0-flash:generateContent")

	_, pt, err := ParseRequest(r, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if pt.ModelField != "" {
		t.Errorf("ModelField = %q, want empty", pt.ModelField)
	}
	if pt.Method != "generateContent" || pt.Stream {
		t.Errorf("Method = %q Stream = %v", pt.Method, pt.Stream)
	}
}
```

Append to `internal/edge/openai/parse_test.go`:

```go
func TestParseCarriesTheStreamFlagOnPassthrough(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"streaming", `{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`, true},
		{"unary", `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(tc.body))
			_, pt, err := ParseRequest(r, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			if pt.Stream != tc.want {
				t.Errorf("Stream = %v, want %v", pt.Stream, tc.want)
			}
			// The model is body-carried here, so there is no URL operation.
			if pt.Method != "" {
				t.Errorf("Method = %q, want empty", pt.Method)
			}
		})
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/... -run 'Passthrough|URLOperation|URLCarried|StreamFlag' -count=1
```

Expected: FAIL — `pt.Method undefined`, `pt.Query undefined`, `pt.Stream undefined`.

- [ ] **Step 3: Extend the struct**

Replace `internal/edge/edge.go:11-18` with:

```go
// Passthrough carries what the phase 9 fast path needs to forward a request
// without re-rendering it. Every dialect populates it; eligibility is decided
// per attempt in the executor, not here.
type Passthrough struct {
	Body       []byte // the raw inbound body, retained for replay across attempts
	ModelField string // top-level JSON key holding the model, or "" when in the URL
	Surface    ir.Surface

	// Method is the URL-carried operation for a dialect whose model lives in
	// the path — Gemini's generateContent or streamGenerateContent. Exactly one
	// of ModelField and Method is set; both empty means the dialect declared no
	// rewritable identifier and the request is not forwardable.
	Method string

	// Query is the inbound query string with this dialect's credential
	// parameter removed. Replayed onto the upstream URL so ?alt=sse survives a
	// forward, while Darkrouter's own proxy token never leaves the process.
	Query url.Values

	// Stream mirrors the parsed request's stream flag. The forwarder needs it
	// to decide on stream_options injection and on which response reader to
	// use, and it does not hold the ir.Request.
	Stream bool
}
```

Add `"net/url"` to the file's imports.

- [ ] **Step 4: Populate it in each dialect**

`internal/edge/openai/parse.go:160` — replace the return with:

```go
	return req, &edge.Passthrough{
		Body: body, ModelField: "model", Surface: ir.SurfaceLLM, Stream: req.Stream,
	}, nil
```

`internal/edge/anthropic/parse.go:164` — the same replacement, verbatim.

`internal/edge/openai/responses.go:176-178` — the same fields. The Responses
wire form is not `openaicompat`'s, so this value is never forwarded; Task 3's
dialect map is what excludes it, and populating the struct honestly here is
cheaper than a special case that would have to be remembered.

```go
	return req, &edge.Passthrough{
		Body: body, ModelField: "model", Surface: ir.SurfaceLLM, Stream: req.Stream,
	}, echo, nil
```

`internal/edge/gemini/parse.go` — replace lines 178-181 with:

```go
	// ModelField is empty: the Gemini model lives in the URL, which is why
	// phase 9's passthrough rewrites the path rather than the body. The
	// credential parameter is dropped here rather than overridden upstream —
	// replaying it would send Darkrouter's own proxy token to Google.
	q := r.URL.Query()
	q.Del("key")
	return req, &edge.Passthrough{
		Body: body, ModelField: "", Surface: ir.SurfaceLLM,
		Method: method, Query: q, Stream: req.Stream,
	}, nil
```

`method` is already in scope from `ExtractModel` at line 110.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/edge/... -count=1 && go build ./...
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/edge
git commit -m "feat(edge): carry the url operation on passthrough"
```

---

### Task 2: `adapter.Forwarder` and the three forward builders

**Files:**
- Modify: `internal/adapter/adapter.go`
- Create: `internal/adapter/openaicompat/forward.go`, `internal/adapter/anthropic/forward.go`, `internal/adapter/gemini/forward.go`
- Test: `internal/adapter/openaicompat/forward_test.go`, `internal/adapter/anthropic/forward_test.go`, `internal/adapter/gemini/forward_test.go`

**Interfaces:**
- Consumes: `adapter.Target` (unchanged), `edge.Passthrough.Method`/`.Query` from Task 1 — reached through `adapter.Forward`, not by importing edge.
- Produces: `adapter.Forwarder` interface with `BuildForward(ctx context.Context, t *Target, f *Forward) (*http.Request, error)`; `adapter.Forward{Body []byte, Header http.Header, Stream bool, Method string, Query url.Values}`.

This is the interface that makes master design §4.1's last two eligibility rules
mechanical rather than a list to keep in step. `bedrock` and `vertex` simply do
not implement `Forwarder`, so they are ineligible by construction: nobody has to
remember to exclude them, and a sixth kind added later is ineligible until
someone deliberately writes its forward builder.

The division of labour matches the one `BuildRequest` already draws. The
executor owns the body rewrite and the inbound header allowlist, because those
are dialect facts. The adapter owns the URL and the credential, because those
are kind facts. `Forward.Header` arrives already filtered; the builder copies it
and then overrides `Content-Type` and auth, which are the two headers a client
must never be able to dictate.

- [ ] **Step 1: Write the failing tests**

Create `internal/adapter/openaicompat/forward_test.go`:

```go
package openaicompat

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildForwardSendsTheBodyUntouched(t *testing.T) {
	body := []byte(`{"model":"target-model","messages":[{"role":"user","content":"a & b < c"}]}`)
	h := http.Header{}
	h.Set("anthropic-beta", "ignored-by-this-kind")

	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-test", Model: "target-model"},
		&adapter.Forward{Body: body, Header: h})
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://up.example/v1/chat/completions" {
		t.Errorf("URL = %s", hr.URL)
	}
	got, _ := io.ReadAll(hr.Body)
	if string(got) != string(body) {
		t.Errorf("body was rewritten\n got: %s\nwant: %s", got, body)
	}
	if hr.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength = %d, want %d", hr.ContentLength, len(body))
	}
	if got := hr.Header.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization = %q", got)
	}
	if got := hr.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}
	// An allowlisted header this kind has no opinion about still travels: the
	// whole point of the fast path is that Darkrouter is not a filter on the
	// vendor's own vocabulary.
	if got := hr.Header.Get("anthropic-beta"); got != "ignored-by-this-kind" {
		t.Errorf("forwarded header lost: %q", got)
	}
}

func TestBuildForwardWritesNoAuthWhenTheKeyIsEmpty(t *testing.T) {
	// An empty key means a non-static style, whose authorizer runs after the
	// body is materialized. A header written here would be overwritten at best
	// and would leak a placeholder at worst.
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", Model: "m"},
		&adapter.Forward{Body: []byte(`{}`), Header: http.Header{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := hr.Header["Authorization"]; present {
		t.Errorf("Authorization written for an empty key: %q", hr.Header.Get("Authorization"))
	}
}
```

Create `internal/adapter/anthropic/forward_test.go`:

```go
package anthropic

import (
	"context"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildForwardKeepsTheClientVersionHeader(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-version", "2023-01-01")
	h.Set("anthropic-beta", "context-1m-2025-08-07")

	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-ant", Model: "m"},
		&adapter.Forward{Body: []byte(`{"model":"m"}`), Header: h})
	if err != nil {
		t.Fatal(err)
	}
	if hr.URL.String() != "https://up.example/v1/messages" {
		t.Errorf("URL = %s", hr.URL)
	}
	// The client's version wins. Overwriting it with the default would defeat
	// the fidelity argument for a client pinned to an older wire contract.
	if got := hr.Header.Get("anthropic-version"); got != "2023-01-01" {
		t.Errorf("anthropic-version = %q, want the client's", got)
	}
	if got := hr.Header.Get("anthropic-beta"); got != "context-1m-2025-08-07" {
		t.Errorf("anthropic-beta = %q", got)
	}
	if got := hr.Header.Get("x-api-key"); got != "sk-ant" {
		t.Errorf("x-api-key = %q", got)
	}
}

func TestBuildForwardSuppliesTheDefaultVersionWhenTheClientSentNone(t *testing.T) {
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1", APIKey: "sk-ant", Model: "m"},
		&adapter.Forward{Body: []byte(`{"model":"m"}`), Header: http.Header{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.Header.Get("anthropic-version"); got != DefaultVersion {
		t.Errorf("anthropic-version = %q, want %q", got, DefaultVersion)
	}
}
```

Create `internal/adapter/gemini/forward_test.go`:

```go
package gemini

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func TestBuildForwardRewritesOnlyTheModelSegment(t *testing.T) {
	q := url.Values{"alt": []string{"sse"}}
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1beta", APIKey: "k", Model: "gemini-2.5-pro"},
		&adapter.Forward{
			Body: []byte(`{"contents":[]}`), Header: http.Header{},
			Stream: true, Method: "streamGenerateContent", Query: q,
		})
	if err != nil {
		t.Fatal(err)
	}
	want := "https://up.example/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse"
	if hr.URL.String() != want {
		t.Errorf("URL = %s\nwant %s", hr.URL, want)
	}
	if got := hr.Header.Get("x-goog-api-key"); got != "k" {
		t.Errorf("x-goog-api-key = %q", got)
	}
}

func TestBuildForwardEscapesTheModelSegment(t *testing.T) {
	// A slash in a model name would otherwise open a path segment the API does
	// not match, and a colon would be read as the method separator.
	hr, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1beta", Model: "tuned/abc"},
		&adapter.Forward{
			Body: []byte(`{}`), Header: http.Header{}, Method: "generateContent",
		})
	if err != nil {
		t.Fatal(err)
	}
	if got := hr.URL.EscapedPath(); got != "/v1beta/models/tuned%2Fabc:generateContent" {
		t.Errorf("EscapedPath = %s", got)
	}
}

func TestBuildForwardRejectsAnEmptyMethod(t *testing.T) {
	// Without the operation there is no URL to build, and guessing
	// generateContent would silently turn a stream into a unary call.
	_, err := New().BuildForward(context.Background(),
		&adapter.Target{BaseURL: "https://up.example/v1beta", Model: "m"},
		&adapter.Forward{Body: []byte(`{}`), Header: http.Header{}})
	if err == nil {
		t.Fatal("want an error for a missing URL operation")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... -run BuildForward -count=1
```

Expected: FAIL — `adapter.Forward undefined`, `BuildForward undefined`.

- [ ] **Step 3: Declare the interface**

Append to `internal/adapter/adapter.go`:

```go
// Forward is one passthrough attempt's outbound request, already rewritten.
//
// Body is final: the executor has done the model rewrite and any permitted
// injection, and a builder that re-encodes it defeats the phase. Header is the
// inbound allowlist from spec §5.3, filtered before it arrives, and a builder
// overrides only what the client must not be able to dictate — the credential
// and the content type.
type Forward struct {
	Body   []byte
	Header http.Header
	Stream bool

	// Method and Query serve the kinds whose model lives in the URL. Method is
	// the operation suffix; Query is the inbound query with the inbound
	// credential already removed. Both are empty for a body-carried kind.
	Method string
	Query  url.Values
}

// Forwarder is implemented by an adapter whose wire format is close enough to
// an inbound dialect that a body can be forwarded rather than re-rendered.
//
// Optional, like TokenCounter and Embedder above, and for a stronger reason:
// master design §4.1 excludes bedrock because SigV4 signs a payload hash, and
// vertex because its URL encodes both publisher and model. Neither implements
// this interface, so neither can be made eligible by an oversight in a
// predicate somewhere else, and a sixth kind is ineligible until someone
// deliberately writes its builder.
type Forwarder interface {
	BuildForward(ctx context.Context, t *Target, f *Forward) (*http.Request, error)
}
```

Add `"net/url"` to the file's imports.

- [ ] **Step 4: Write the three builders**

Create `internal/adapter/openaicompat/forward.go`:

```go
package openaicompat

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func (a *Adapter) BuildForward(ctx context.Context, t *adapter.Target, f *adapter.Forward) (*http.Request, error) {
	url := strings.TrimRight(t.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	hr.ContentLength = int64(len(f.Body))
	copyForwardHeader(hr, f.Header)
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+t.APIKey)
	}
	return hr, nil
}

// copyForwardHeader replays the allowlisted inbound headers. It is written once
// per kind rather than shared, because sharing it would need a package the
// three adapters could all import and there is nothing else to put there.
func copyForwardHeader(hr *http.Request, h http.Header) {
	for k, vs := range h {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
}

var _ adapter.Forwarder = (*Adapter)(nil)
```

Create `internal/adapter/anthropic/forward.go`:

```go
package anthropic

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func (a *Adapter) BuildForward(ctx context.Context, t *adapter.Target, f *adapter.Forward) (*http.Request, error) {
	url := strings.TrimRight(t.BaseURL, "/") + "/messages"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	hr.ContentLength = int64(len(f.Body))
	for k, vs := range f.Header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		hr.Header.Set("x-api-key", t.APIKey)
	}
	// The client's own version, when it sent one, survives. A client pinned to
	// an older wire contract is exactly who the fast path exists for, and
	// overwriting the header would change the response shape underneath it.
	if hr.Header.Get("anthropic-version") == "" {
		hr.Header.Set("anthropic-version", DefaultVersion)
	}
	return hr, nil
}

var _ adapter.Forwarder = (*Adapter)(nil)
```

Create `internal/adapter/gemini/forward.go`:

```go
package gemini

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func (a *Adapter) BuildForward(ctx context.Context, t *adapter.Target, f *adapter.Forward) (*http.Request, error) {
	if f.Method == "" {
		// The model and the operation share one path segment, so without the
		// operation there is nothing to build. Defaulting to generateContent
		// would turn a client's stream into a unary call and hang it.
		return nil, errors.New("gemini: forward carries no URL operation")
	}
	endpoint := strings.TrimRight(t.BaseURL, "/") + "/models/" +
		url.PathEscape(t.Model) + ":" + f.Method
	if q := f.Query.Encode(); q != "" {
		endpoint += "?" + q
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(f.Body))
	if err != nil {
		return nil, err
	}
	hr.ContentLength = int64(len(f.Body))
	for k, vs := range f.Header {
		for _, v := range vs {
			hr.Header.Add(k, v)
		}
	}
	hr.Header.Set("Content-Type", "application/json")
	if t.APIKey != "" {
		// The header rather than ?key=: a query parameter lands in access logs.
		hr.Header.Set("x-goog-api-key", t.APIKey)
	}
	return hr, nil
}

var _ adapter.Forwarder = (*Adapter)(nil)
```

If the concrete adapter type in any of the three packages is not named
`Adapter`, use whatever `New()` returns — check before writing the receiver.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Pin the exclusions**

Append to `internal/adapter/adapter_test.go`:

```go
func TestOnlyThreeKindsAreForwardable(t *testing.T) {
	// Master design §4.1: bedrock signs a payload hash and vertex encodes the
	// publisher in its URL, so neither can forward an inbound body. This is a
	// property of the interface set, not of a predicate somewhere else, and
	// this test is what says so.
	for name, ad := range map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.New(),
	} {
		if _, ok := ad.(adapter.Forwarder); !ok {
			t.Errorf("%s must implement Forwarder", name)
		}
	}
	for name, ad := range map[string]adapter.Adapter{
		"bedrock": bedrockadapter.New(),
		"vertex":  vertexadapter.New(),
	} {
		if _, ok := ad.(adapter.Forwarder); ok {
			t.Errorf("%s must not implement Forwarder", name)
		}
	}
}
```

Add the five adapter imports if `adapter_test.go` does not already carry them.
If that file is in package `adapter` rather than `adapter_test`, the imports
would be a cycle — put this test in `internal/golden/invariants_test.go`
instead, which already imports every kind.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... ./internal/golden/... -count=1
```

- [ ] **Step 7: Commit**

```bash
git add internal/adapter
git commit -m "feat(adapter): add the forwarder seam for three kinds"
```

---

### Task 3: The eligibility predicate

**Files:**
- Create: `internal/exec/passthrough.go`
- Test: `internal/exec/passthrough_test.go`

**Interfaces:**
- Consumes: `edge.Passthrough` (Task 1), `adapter.Forwarder` (Task 2), `router.Candidate`, `provider.Provider`.
- Produces: `exec.forwardable(dialect string, pt *edge.Passthrough, c router.Candidate, p provider.Provider, ad adapter.Adapter) (adapter.Forwarder, bool)`.

Master design §4.1 is the authority; this is its mechanics. Five conditions,
each of which a test names.

**`openai-responses` is deliberately absent from the dialect map.** Its
`Passthrough` is populated and its `ModelField` is `model`, so nothing else
would stop it — but the Responses wire form is not `/chat/completions`, and
forwarding a Responses body to an `openaicompat` target would send `input` where
the provider expects `messages`. The map is the only thing standing between that
and a 400 on every Responses request that happens to route to a matching kind.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/passthrough_test.go`:

```go
package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicadapter "github.com/darkraise/darkrouter/internal/adapter/anthropic"
	bedrockadapter "github.com/darkraise/darkrouter/internal/adapter/bedrock"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/openaicompat"
	vertexadapter "github.com/darkraise/darkrouter/internal/adapter/vertex"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
)

func chatPassthrough() *edge.Passthrough {
	return &edge.Passthrough{
		Body: []byte(`{"model":"m","messages":[]}`), ModelField: "model",
		Surface: ir.SurfaceLLM,
	}
}

func TestEligibility(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dialect string
		pt      *edge.Passthrough
		cand    router.Candidate
		prov    provider.Provider
		ad      adapter.Adapter
		want    bool
	}{
		{
			name: "openai to openaicompat", dialect: "openai", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: true,
		},
		{
			name: "anthropic to anthropic", dialect: "anthropic", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "anthropic"}, ad: anthropicadapter.New(), want: true,
		},
		{
			name:    "gemini to gemini",
			dialect: "gemini",
			pt: &edge.Passthrough{
				Body: []byte(`{"contents":[]}`), Method: "generateContent", Surface: ir.SurfaceLLM,
			},
			cand: router.Candidate{Kind: "gemini"}, ad: geminiadapter.New(), want: true,
		},
		{
			// Cross-dialect is the IR path's entire reason for existing.
			name: "anthropic to openaicompat", dialect: "anthropic", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			// SigV4 signs a payload hash: the body must be materialized and
			// signed, which forwarding it cannot be.
			name: "bedrock is never eligible", dialect: "anthropic", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "bedrock"}, ad: bedrockadapter.New(), want: false,
		},
		{
			// The Vertex URL encodes publisher and model together.
			name: "vertex is never eligible", dialect: "gemini",
			pt:   &edge.Passthrough{Body: []byte(`{}`), Method: "generateContent", Surface: ir.SurfaceLLM},
			cand: router.Candidate{Kind: "vertex", Publisher: "google"}, ad: vertexadapter.New(), want: false,
		},
		{
			// The Responses body is not a chat-completions body, whatever its
			// model field is called.
			name: "openai-responses is never eligible", dialect: "openai-responses", pt: chatPassthrough(),
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			name: "an auxiliary surface is never eligible", dialect: "openai",
			pt:   &edge.Passthrough{Body: []byte(`{}`), ModelField: "model", Surface: ir.SurfaceEmbedding},
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			name: "no passthrough at all", dialect: "openai", pt: nil,
			cand: router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
		{
			name:    "no rewritable identifier",
			dialect: "openai",
			pt:      &edge.Passthrough{Body: []byte(`{}`), Surface: ir.SurfaceLLM},
			cand:    router.Candidate{Kind: "openaicompat"}, ad: openaicompat.New(), want: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := forwardable(tc.dialect, tc.pt, tc.cand, tc.prov, tc.ad)
			if ok != tc.want {
				t.Errorf("forwardable = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestAQuirkDeclaringPresetIsIneligibleForStreamingOnly(t *testing.T) {
	// spec §5.2: injecting stream_options into a provider that rejects it turns
	// a working request into a 400. Its unary requests are unaffected, and
	// excluding those too would give up fidelity for nothing.
	p := provider.Provider{Preset: quirkPresetName(t)}
	c := router.Candidate{Kind: "openaicompat"}

	streaming := chatPassthrough()
	streaming.Stream = true
	if _, ok := forwardable("openai", streaming, c, p, openaicompat.New()); ok {
		t.Error("a rejects-stream-options preset must not stream through the fast path")
	}
	if _, ok := forwardable("openai", chatPassthrough(), c, p, openaicompat.New()); !ok {
		t.Error("its unary requests are still eligible")
	}
}
```

Add the helper at the bottom of the same file. **No shipped preset declares
`rejects-stream-options` today** — phase 6 defined the vocabulary ahead of its
consumer — so the test registers one rather than hard-coding a name that does
not exist:

```go
// quirkPresetName registers a preset declaring rejects-stream-options and
// returns its name.
//
// catalog.Embedded() returns the package-level map itself rather than a copy,
// so an entry added here is visible to presetRejectsStreamOptions. It is
// removed on cleanup: leaving it behind would give every later test in this
// package a preset that does not ship.
func quirkPresetName(t *testing.T) string {
	t.Helper()
	const name = "test-rejects-stream-options"
	catalog.Embedded()[name] = catalog.Preset{
		Name: "Strict", Kind: "openaicompat",
		Quirks: []string{"rejects-stream-options"},
	}
	t.Cleanup(func() { delete(catalog.Embedded(), name) })
	return name
}
```

Do not run this test with `t.Parallel()` — it mutates a process-wide map, and a
concurrent test reading presets would see the injected entry. If a later phase
needs parallel preset tests, the fix is a seam in `catalog`, not a copy of this
helper.

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'Eligibility|QuirkDeclaring' -count=1
```

Expected: FAIL — `forwardable undefined`.

- [ ] **Step 3: Write the predicate**

Create `internal/exec/passthrough.go`:

```go
package exec

import (
	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/edge"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/router"
)

// forwardKinds maps an inbound dialect onto the one adapter kind whose wire
// format it already speaks. Master design §4.1.
//
// openai-responses is deliberately absent. Its passthrough is populated and its
// model field is called model, so nothing else here would stop it — but a
// Responses body carries input where chat/completions expects messages, and
// forwarding one to an openaicompat target is a 400 rather than a fast path.
var forwardKinds = map[string]string{
	"openai":    "openaicompat",
	"anthropic": "anthropic",
	"gemini":    "gemini",
}

// forwardable reports the forwarder for this attempt, and whether the candidate
// is eligible at all.
//
// It is called once per attempt rather than once per request: master design
// §4.1 decides eligibility per candidate, so an Anthropic-inbound request can
// forward to Anthropic and translate to openaicompat on the next attempt.
func forwardable(dialect string, pt *edge.Passthrough, c router.Candidate,
	p provider.Provider, ad adapter.Adapter) (adapter.Forwarder, bool) {

	if pt == nil || pt.Surface != ir.SurfaceLLM {
		return nil, false
	}
	if forwardKinds[dialect] != c.Kind {
		return nil, false
	}
	// Exactly one identifier form. Neither means the dialect declared nothing
	// rewritable; both would mean two places to rewrite and no rule for which
	// wins.
	if (pt.ModelField == "") == (pt.Method == "") {
		return nil, false
	}
	fw, ok := ad.(adapter.Forwarder)
	if !ok {
		// bedrock and vertex land here, by not implementing the interface.
		return nil, false
	}
	if pt.Stream && c.Kind == "openaicompat" && presetRejectsStreamOptions(p.Preset) {
		// The injection spec §5.2 requires would be rejected by this upstream,
		// so its streaming requests take the IR path. Its unary requests are
		// unaffected.
		return nil, false
	}
	return fw, true
}

// presetRejectsStreamOptions mirrors rerankPath and presetStyle in exec.go:
// preset data is reached from here because the adapter is handed a target and
// knows nothing about presets.
func presetRejectsStreamOptions(preset string) bool {
	if preset == "" {
		return false
	}
	return catalog.Embedded()[preset].HasQuirk("rejects-stream-options")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'Eligibility|QuirkDeclaring' -count=1 -v
```

Expected: PASS, ten eligibility subtests plus the quirk pair.

- [ ] **Step 5: Commit**

```bash
git add internal/exec internal/catalog
git commit -m "feat(exec): decide passthrough eligibility per attempt"
```

---

### Task 4: The body rewrite

**Files:**
- Modify: `internal/exec/passthrough.go`
- Test: `internal/exec/passthrough_test.go`

**Interfaces:**
- Produces: `exec.rewriteForward(pt *edge.Passthrough, requested, target, kind string) (body []byte, injected bool, err error)`, `exec.ErrNoModelField`.

Master design §4.2's three mutations, and no fourth. `injected` is the return
that matters downstream: it tells Task 7's forwarder to strip the extra final
usage chunk, and it is false whenever the client asked for usage itself, because
then the chunk is the client's and stripping it would be a fourth mutation.

Three properties the tests pin, each of which spec §5.1 calls out by name:

- **`<`, `>` and `&` survive.** `json.Marshal` HTML-escapes them inside a
  `json.RawMessage` by default, silently rewriting prompt text. The encoder is
  configured `SetEscapeHTML(false)` for exactly this.
- **A same-name model with nothing to inject forwards the original bytes.**
  Not a re-encoding that happens to be equivalent — the identical slice. This
  is the only path with true byte fidelity and it is the most travelled one.
- **A missing top-level model field is an error, not a guess.** Task 9 turns it
  into a fall back to the IR path, whose parser produces a proper dialect-shaped
  error if the body really is invalid.

- [ ] **Step 1: Write the failing test**

Append to `internal/exec/passthrough_test.go`:

```go
func TestRewriteSwapsTheModelAndNothingElse(t *testing.T) {
	pt := &edge.Passthrough{
		Body: []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}],` +
			`"some_parameter_shipped_last_week":{"nested":true}}`),
		ModelField: "model", Surface: ir.SurfaceLLM,
	}
	body, injected, err := rewriteForward(pt, "claude-sonnet-4-5", "claude-opus-4-5", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if injected {
		t.Error("stream_options injected on a non-openaicompat kind")
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "claude-opus-4-5" {
		t.Errorf("model = %v", got["model"])
	}
	// The unmodelled parameter is the whole point of the phase.
	if _, ok := got["some_parameter_shipped_last_week"]; !ok {
		t.Error("an unmodelled top-level field was dropped")
	}
	if _, ok := got["stream_options"]; ok {
		t.Error("a fourth mutation appeared")
	}
}

func TestRewriteDoesNotEscapeHTMLSignificantCharacters(t *testing.T) {
	// json.Marshal escapes <, > and & inside a RawMessage by default, which
	// would silently rewrite prompt text on every forwarded request.
	pt := &edge.Passthrough{
		Body:       []byte(`{"model":"a","messages":[{"role":"user","content":"if x < y && y > z"}]}`),
		ModelField: "model", Surface: ir.SurfaceLLM,
	}
	body, _, err := rewriteForward(pt, "a", "b", "openaicompat")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`if x < y && y > z`)) {
		t.Errorf("prompt text was escaped: %s", body)
	}
}

func TestRewriteForwardsTheOriginalBytesWhenNothingChanges(t *testing.T) {
	// The Claude Code case: the client already asked for the name the target
	// serves, and a unary request needs no injection.
	raw := []byte(`{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`)
	pt := &edge.Passthrough{Body: raw, ModelField: "model", Surface: ir.SurfaceLLM}

	body, injected, err := rewriteForward(pt, "claude-opus-4-5", "claude-opus-4-5", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	if injected {
		t.Error("injected on a unary request")
	}
	if &body[0] != &raw[0] {
		t.Errorf("the body was re-encoded rather than forwarded\n got: %s\nwant: %s", body, raw)
	}
}

func TestRewriteInjectsStreamOptionsOnlyWhenAbsent(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		kind         string
		stream       bool
		wantInjected bool
	}{
		{"absent on a streaming openaicompat request",
			`{"model":"a","stream":true}`, "openaicompat", true, true},
		{"already present, so the chunk is the client's",
			`{"model":"a","stream":true,"stream_options":{"include_usage":true}}`,
			"openaicompat", true, false},
		{"present but disabled is still the client's choice",
			`{"model":"a","stream":true,"stream_options":{"include_usage":false}}`,
			"openaicompat", true, false},
		{"unary needs no usage chunk", `{"model":"a"}`, "openaicompat", false, false},
		{"anthropic has no such parameter", `{"model":"a","stream":true}`, "anthropic", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pt := &edge.Passthrough{
				Body: []byte(tc.body), ModelField: "model",
				Surface: ir.SurfaceLLM, Stream: tc.stream,
			}
			body, injected, err := rewriteForward(pt, "a", "a", tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if injected != tc.wantInjected {
				t.Errorf("injected = %v, want %v", injected, tc.wantInjected)
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			_, present := got["stream_options"]
			if tc.kind == "openaicompat" && tc.stream && !present {
				t.Error("a streaming openaicompat request must carry stream_options")
			}
		})
	}
}

func TestRewriteLeavesAURLCarriedBodyUntouched(t *testing.T) {
	raw := []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	pt := &edge.Passthrough{Body: raw, Method: "generateContent", Surface: ir.SurfaceLLM, Stream: true}

	body, injected, err := rewriteForward(pt, "gemini-2.0-flash", "gemini-2.5-pro", "gemini")
	if err != nil {
		t.Fatal(err)
	}
	if injected {
		t.Error("Gemini has no stream_options analogue")
	}
	if &body[0] != &raw[0] {
		t.Errorf("a URL-carried body was rewritten: %s", body)
	}
}

func TestRewriteReportsAMissingModelField(t *testing.T) {
	pt := &edge.Passthrough{
		Body: []byte(`{"messages":[]}`), ModelField: "model", Surface: ir.SurfaceLLM,
	}
	if _, _, err := rewriteForward(pt, "a", "b", "openaicompat"); !errors.Is(err, ErrNoModelField) {
		t.Errorf("err = %v, want ErrNoModelField", err)
	}
}

func TestRewriteReportsMalformedJSON(t *testing.T) {
	pt := &edge.Passthrough{Body: []byte(`{"model":`), ModelField: "model", Surface: ir.SurfaceLLM}
	if _, _, err := rewriteForward(pt, "a", "b", "openaicompat"); err == nil {
		t.Fatal("want an error for a malformed body")
	}
}
```

Add `bytes`, `encoding/json` and `errors` to the test file's imports.

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run Rewrite -count=1
```

Expected: FAIL — `rewriteForward undefined`.

- [ ] **Step 3: Write the rewrite**

Append to `internal/exec/passthrough.go`:

```go
// ErrNoModelField means a body-carried dialect's body has no top-level model
// key to rewrite. Task 9 turns it into a fall back to the IR path rather than a
// client error: the IR parser produces a proper dialect-shaped message if the
// body is genuinely invalid, and this function cannot tell the difference.
var ErrNoModelField = errors.New("exec: passthrough body carries no model field")

// rewriteForward applies master design §4.2's permitted body mutations and
// returns the bytes to forward. injected reports whether stream_options was
// added, which is what tells the response forwarder to strip the extra final
// usage chunk the client never asked for.
//
// The guarantee is semantic preservation, not byte preservation: re-encoding
// compacts whitespace, sorts top-level keys and collapses duplicate top-level
// keys to the last. HTML escaping is the one consequential difference and it is
// disabled. When nothing needs changing the original slice is returned
// unmodified, which is the only path with exact byte fidelity — and the most
// travelled one, because a client usually asks for the name the target serves.
func rewriteForward(pt *edge.Passthrough, requested, target, kind string) ([]byte, bool, error) {
	if pt.ModelField == "" {
		// URL-carried. The model is not in the body, and no dialect in this
		// group has a stream_options analogue, so the body is forwarded exactly
		// as it arrived.
		return pt.Body, false, nil
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(pt.Body, &top); err != nil {
		return nil, false, err
	}
	if _, ok := top[pt.ModelField]; !ok {
		return nil, false, ErrNoModelField
	}

	changed, injected := false, false
	if requested != target {
		name, err := json.Marshal(target)
		if err != nil {
			return nil, false, err
		}
		top[pt.ModelField] = name
		changed = true
	}
	if kind == "openaicompat" && pt.Stream {
		if _, ok := top["stream_options"]; !ok {
			// Compatible providers report no stream usage unless asked, and
			// without this token accounting is blind on the most-travelled
			// route. Present-but-false is the client's own choice and is left
			// alone, which also leaves the resulting chunks alone.
			top["stream_options"] = json.RawMessage(`{"include_usage":true}`)
			changed, injected = true, true
		}
	}
	if !changed {
		return pt.Body, false, nil
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// Without this, <, > and & inside every RawMessage value are escaped —
	// silently rewriting prompt text on every forwarded request.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(top); err != nil {
		return nil, false, err
	}
	// Encode appends a newline that no provider wants in a JSON body.
	return bytes.TrimRight(buf.Bytes(), "\n"), injected, nil
}
```

Add `bytes`, `encoding/json` and `errors` to the file's imports.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run Rewrite -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/exec
git commit -m "feat(exec): rewrite the forwarded body in place"
```

---

### Task 5: The header allowlist and the compressed-body refusal

**Files:**
- Modify: `internal/exec/passthrough.go`, `internal/exec/exec.go:Handle`, `internal/ir/ir.go:89-102`, `internal/edge/openai/write.go:106`, `internal/edge/anthropic/write.go:107`, `internal/edge/gemini/write.go:101`
- Test: `internal/exec/passthrough_test.go`, `internal/exec/exec_test.go`

**Interfaces:**
- Produces: `exec.forwardHeaders(r *http.Request) http.Header`, `ir.ErrUnsupportedMedia`.

Spec §5.3's allowlist, and §5.4's refusal.

**Both `content-type` and `openai-beta` are in the list because an earlier draft
omitted them.** Without `content-type` every upstream call goes out untyped and
many providers reject it; without `openai-beta` the fidelity argument fails for
half this phase's clients. They are named here so a future edit that trims the
list has to argue with a test.

The refusal is a **415**, which is a new `ir.ErrorType`. `ir.ErrPayloadTooLarge`
is the precedent: it was added rather than folded into `ErrInvalidRequest`
because the two tell a client different things. A compressed body today reaches
`json.Unmarshal` and comes back as "invalid JSON", which sends the client
looking for a syntax error that is not there.

- [ ] **Step 1: Write the failing tests**

Append to `internal/exec/passthrough_test.go`:

```go
func TestForwardHeadersKeepsOnlyTheAllowlist(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
	for k, v := range map[string]string{
		"Content-Type":      "application/json",
		"Accept":            "text/event-stream",
		"User-Agent":        "claude-cli/2.0.0",
		"anthropic-version": "2023-06-01",
		"anthropic-beta":    "context-1m-2025-08-07",
		"openai-beta":       "assistants=v2",

		// Dropped: the inbound credential in all three dialect spellings,
		// hop-by-hop headers, and anything a client invented.
		"Authorization":     "Bearer proxy-token",
		"x-api-key":         "proxy-token",
		"x-goog-api-key":    "proxy-token",
		"Connection":        "keep-alive",
		"Keep-Alive":        "timeout=5",
		"Transfer-Encoding": "chunked",
		"Te":                "trailers",
		"Upgrade":           "h2c",
		"Proxy-Authorization": "Basic abc",
		"Host":              "evil.example",
		"X-Forwarded-For":   "10.0.0.1",
		"Accept-Encoding":   "gzip",
		"Cookie":            "session=abc",
		"X-Darkrouter-Provider": "spoofed",
	} {
		r.Header.Set(k, v)
	}

	h := forwardHeaders(r)
	for _, want := range []string{
		"Content-Type", "Accept", "User-Agent", "anthropic-version",
		"anthropic-beta", "openai-beta",
	} {
		if h.Get(want) == "" {
			t.Errorf("%s was dropped", want)
		}
	}
	if len(h) != 6 {
		t.Errorf("forwarded %d headers, want 6: %v", len(h), h)
	}
}

func TestCompressedInboundBodiesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		enc  string
		want bool
	}{
		{"", false},
		{"identity", false},
		{"gzip", true},
		{"br", true},
		{"gzip, identity", true},
	} {
		r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader("{}"))
		if tc.enc != "" {
			r.Header.Set("Content-Encoding", tc.enc)
		}
		if got := compressedBody(r); got != tc.want {
			t.Errorf("Content-Encoding %q: compressedBody = %v, want %v", tc.enc, got, tc.want)
		}
	}
}
```

Append to `internal/exec/exec_test.go`:

```go
func TestHandleRefusesACompressedBodyWith415(t *testing.T) {
	e := newTestExecutor(t) // reuse whatever this file already builds
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
```

If `exec_test.go` has no `newTestExecutor`, use the constructor the neighbouring
tests in that file already use; do not add a second one.

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'ForwardHeaders|Compressed' -count=1
```

Expected: FAIL — `forwardHeaders undefined`, `compressedBody undefined`.

- [ ] **Step 3: Add the error type and its three status mappings**

In `internal/ir/ir.go`, beside `ErrPayloadTooLarge`:

```go
	// ErrUnsupportedMedia is an inbound body Darkrouter will not decode —
	// today, one carrying a Content-Encoding other than identity. Distinct
	// from ErrInvalidRequest because a compressed body reaching a JSON parser
	// comes back as "invalid JSON", which sends the client looking for a
	// syntax error that is not there.
	ErrUnsupportedMedia ErrorType = "unsupported_media"
```

In each of the three writers' `statusFor` (or its per-dialect equivalent), add
the case beside `ErrPayloadTooLarge`:

- `internal/edge/openai/write.go` — `case ir.ErrUnsupportedMedia: return http.StatusUnsupportedMediaType`
- `internal/edge/anthropic/write.go` — `case ir.ErrUnsupportedMedia: return "invalid_request_error", http.StatusUnsupportedMediaType`
- `internal/edge/gemini/write.go` — `case ir.ErrUnsupportedMedia: return "INVALID_ARGUMENT", http.StatusUnsupportedMediaType`

Anthropic and Gemini reuse their invalid-request code strings: neither vendor
defines a distinct one for 415, and inventing a code an SDK does not know would
be worse than a status the SDK reads correctly.

- [ ] **Step 4: Write the allowlist and the check**

Append to `internal/exec/passthrough.go`:

```go
// forwardHeaderAllowlist is spec §5.3. Everything else inbound is dropped, so a
// client cannot inject a header into Darkrouter's upstream call — including the
// inbound proxy credential in any of its three dialect spellings, every
// hop-by-hop header RFC 9110 §7.6.1 names, and Darkrouter's own diagnostics.
//
// content-type and openai-beta are here because an earlier draft of the spec
// omitted them: without the first every upstream call goes out untyped and many
// providers reject it, and without the second this phase's fidelity argument
// fails for half its clients.
var forwardHeaderAllowlist = map[string]bool{
	"content-type":      true,
	"accept":            true,
	"user-agent":        true,
	"anthropic-version": true,
	"anthropic-beta":    true,
	"openai-beta":       true,
}

// forwardHeaders is the inbound header set reduced to what may be forwarded.
// Content-Length is deliberately absent: the body may have been rewritten, and
// the builder sets it from the bytes it actually sends.
func forwardHeaders(r *http.Request) http.Header {
	out := make(http.Header, len(forwardHeaderAllowlist))
	for k, vs := range r.Header {
		if !forwardHeaderAllowlist[strings.ToLower(k)] {
			continue
		}
		for _, v := range vs {
			out.Add(k, v)
		}
	}
	return out
}

// compressedBody reports an inbound body Darkrouter will not decode.
//
// Spec §5.4 refuses rather than downgrades: decoding it to rewrite the model
// and re-encoding it defeats the point of the fast path, and forwarding it
// unmodified is impossible when the model must change. The IR path could not
// serve it either — its parser reads the raw bytes — so the refusal costs
// nothing and says something true.
func compressedBody(r *http.Request) bool {
	enc := strings.TrimSpace(r.Header.Get("Content-Encoding"))
	return enc != "" && !strings.EqualFold(enc, "identity")
}
```

Add `net/http` and `strings` to the file's imports.

- [ ] **Step 5: Refuse in `Handle`**

In `internal/exec/exec.go`, at the top of `Handle`, before `d.ParseRequest`:

```go
	if compressedBody(r) {
		start := time.Now()
		rec, done := e.newRecord(start, d.Name(), string(ir.SurfaceLLM))
		defer done()
		w.Header().Set("X-Darkrouter-Request", rec.ID)
		w.Header().Set("X-Darkrouter-Attempts", "0")
		rec.ErrorCode = string(ir.ErrUnsupportedMedia)
		_ = d.WriteError(w, &ir.Error{
			Type:    ir.ErrUnsupportedMedia,
			Message: "content-encoding is not supported: send an uncompressed request body",
		})
		return
	}
```

The record is opened here for the same reason the parse-failure branch below it
does: a body the gateway received and refused is a request the operator is owed
a row for.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/... ./internal/edge/... ./internal/ir/... -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/exec internal/edge internal/ir
git commit -m "feat(exec): allowlist forwarded headers, refuse encoding"
```

---

### Task 6: The per-kind stream recognizer

**Files:**
- Modify: `internal/adapter/adapter.go`, `internal/adapter/openaicompat/forward.go`, `internal/adapter/anthropic/forward.go`, `internal/adapter/gemini/forward.go`
- Test: `internal/adapter/openaicompat/forward_test.go`, `internal/adapter/anthropic/forward_test.go`, `internal/adapter/gemini/forward_test.go`

**Interfaces:**
- Consumes: `adapter.Forwarder` from Task 2 — **this task grows it**. The single-method interface becomes three-method, and the `var _ adapter.Forwarder` assertions Task 2 wrote will fail to compile until all three kinds implement the new methods. That is the intended signal.
- Produces: `adapter.RawEvent{Content bool, ErrPayload string, Usage *ir.Usage, UsageOnly bool}`; `Forwarder.RecognizeEvent(ev sse.Event) RawEvent`; `Forwarder.RecognizeUsage(body []byte) *ir.Usage`.

Spec §6 and §7 put the recognizer in the executor. It goes in the adapters
instead, for one reason: the usage wire shape already lives there —
`openaicompat.wireUsage`, `anthropic.wireUsage` — and a second copy in `exec`
would be the same three field sets maintained in two places, drifting the first
time a vendor adds a token category. The executor keeps everything that is
*not* per-kind: the splitter, the commit rule, the buffer cap, the merge.

**The recognizer reads SSE structure only.** It never reconstructs IR — that is
the distinction the fast path exists for — and `RawEvent` is deliberately not an
`ir.StreamEvent`, so nothing downstream can drift into treating it as one.

**Anthropic usage arrives split across two events** (spec §7). `message_start`
carries input and cache tokens; `message_delta` carries output tokens. The
recognizer reports each event's own numbers and the executor merges them, which
is what the adapter's IR stream parser already does at
`internal/adapter/anthropic/stream.go:161-177`. A recognizer that watched only
`message_delta` would compute a wrong cost on every cached or long-prompt
request.

**Content-bearing follows phase 3, not the spec's event list** — see "Two
decisions this plan makes" above. Anthropic commits on a `content_block_delta`
carrying non-empty `text`, `thinking` or `partial_json`, and on nothing else.

- [ ] **Step 1: Write the failing tests**

Append to `internal/adapter/anthropic/forward_test.go`:

```go
func TestRecognizeEventFollowsPhase3sCommitRule(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     string
		data    string
		content bool
	}{
		{"message_start does not commit", "message_start",
			`{"type":"message_start","message":{"id":"m","model":"x","usage":{"input_tokens":7}}}`, false},
		{"an opening block does not commit", "content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, false},
		{"a ping does not commit", "ping", `{"type":"ping"}`, false},
		{"a text delta commits", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"He"}}`, true},
		{"a thinking delta commits", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hm"}}`, true},
		{"a tool input delta commits", "content_block_delta",
			`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\""}}`, true},
		{"a signature delta carries no content", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`, false},
		{"an empty text delta carries no content", "content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New().RecognizeEvent(sse.Event{Type: tc.typ, Data: tc.data})
			if got.Content != tc.content {
				t.Errorf("Content = %v, want %v", got.Content, tc.content)
			}
		})
	}
}

func TestRecognizeEventReportsUsageFromBothEvents(t *testing.T) {
	// spec §7: message_start carries input and cache, message_delta output.
	start := New().RecognizeEvent(sse.Event{Type: "message_start", Data: `{"type":"message_start",
		"message":{"id":"m","model":"x","usage":{"input_tokens":100,
		"cache_read_input_tokens":40,"cache_creation_input_tokens":10}}}`})
	if start.Usage == nil {
		t.Fatal("message_start reported no usage")
	}
	if start.Usage.InputTokens != 100 || start.Usage.CacheReadTokens != 40 ||
		start.Usage.CacheWriteTokens != 10 {
		t.Errorf("message_start usage = %+v", *start.Usage)
	}

	delta := New().RecognizeEvent(sse.Event{Type: "message_delta",
		Data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":25}}`})
	if delta.Usage == nil || delta.Usage.OutputTokens != 25 {
		t.Errorf("message_delta usage = %+v", delta.Usage)
	}
}

func TestRecognizeEventReportsAnInStreamError(t *testing.T) {
	// Anthropic delivers overloaded_error as an SSE event under a 200. Before
	// commit that must fail over rather than reach the client as content.
	got := New().RecognizeEvent(sse.Event{Type: "error",
		Data: `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`})
	if got.ErrPayload == "" {
		t.Fatal("an error event was not recognized")
	}
	if got.Content {
		t.Error("an error event must not commit")
	}
}

func TestRecognizeUsageReadsAUnaryBody(t *testing.T) {
	u := New().RecognizeUsage([]byte(`{"id":"m","content":[{"type":"text","text":"hi"}],
		"usage":{"input_tokens":11,"output_tokens":3,"cache_read_input_tokens":5}}`))
	if u == nil {
		t.Fatal("no usage")
	}
	if u.InputTokens != 11 || u.OutputTokens != 3 || u.CacheReadTokens != 5 {
		t.Errorf("usage = %+v", *u)
	}
}
```

Append to `internal/adapter/openaicompat/forward_test.go`:

```go
func TestRecognizeEventOnChunks(t *testing.T) {
	for _, tc := range []struct {
		name      string
		data      string
		content   bool
		usageOnly bool
	}{
		{"the role-only first chunk does not commit",
			`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`, false, false},
		{"a content delta commits",
			`{"choices":[{"index":0,"delta":{"content":"He"}}]}`, true, false},
		{"a reasoning delta commits",
			`{"choices":[{"index":0,"delta":{"reasoning_content":"hm"}}]}`, true, false},
		{"a tool call delta commits",
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"f"}}]}}]}`, true, false},
		{"an empty content delta does not commit",
			`{"choices":[{"index":0,"delta":{"content":""}}]}`, false, false},
		{"the final usage chunk carries no choices",
			`{"choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4}}`, false, true},
		{"a chunk carrying both is not usage-only",
			`{"choices":[{"index":0,"delta":{"content":"x"}}],"usage":{"prompt_tokens":9}}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New().RecognizeEvent(sse.Event{Data: tc.data})
			if got.Content != tc.content {
				t.Errorf("Content = %v, want %v", got.Content, tc.content)
			}
			if got.UsageOnly != tc.usageOnly {
				t.Errorf("UsageOnly = %v, want %v", got.UsageOnly, tc.usageOnly)
			}
		})
	}
}

func TestRecognizeEventIgnoresTheDoneSentinel(t *testing.T) {
	// [DONE] is not JSON. Treating it as a parse failure would be harmless but
	// would also mean the log could not tell it from a real one.
	got := New().RecognizeEvent(sse.Event{Data: "[DONE]"})
	if got.Content || got.ErrPayload != "" || got.Usage != nil {
		t.Errorf("[DONE] recognized as %+v", got)
	}
}

func TestRecognizeEventReportsAnInStreamError(t *testing.T) {
	got := New().RecognizeEvent(sse.Event{
		Data: `{"error":{"message":"upstream is on fire","type":"server_error"}}`})
	if got.ErrPayload == "" {
		t.Fatal("an error payload was not recognized")
	}
}

func TestRecognizeUsageReadsCachedAndReasoningDetails(t *testing.T) {
	u := New().RecognizeUsage([]byte(`{"choices":[],"usage":{"prompt_tokens":40,
		"completion_tokens":9,"prompt_tokens_details":{"cached_tokens":12},
		"completion_tokens_details":{"reasoning_tokens":5}}}`))
	if u == nil {
		t.Fatal("no usage")
	}
	if u.InputTokens != 40 || u.OutputTokens != 9 ||
		u.CacheReadTokens != 12 || u.ReasoningTokens != 5 {
		t.Errorf("usage = %+v", *u)
	}
}
```

Append to `internal/adapter/gemini/forward_test.go`:

```go
func TestRecognizeEventOnCandidates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		data    string
		content bool
	}{
		{"a text part commits",
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"He"}]}}]}`, true},
		{"a thought part commits",
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"hm","thought":true}]}}]}`, true},
		{"a function call commits",
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"f","args":{}}}]}}]}`, true},
		{"a bare thought signature does not commit",
			`{"candidates":[{"content":{"role":"model","parts":[{"thoughtSignature":"sig"}]}}]}`, false},
		{"a candidate with no parts does not commit",
			`{"candidates":[{"finishReason":"STOP"}]}`, false},
		{"usage metadata alone does not commit",
			`{"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":2}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := New().RecognizeEvent(sse.Event{Data: tc.data})
			if got.Content != tc.content {
				t.Errorf("Content = %v, want %v", got.Content, tc.content)
			}
		})
	}
}

func TestRecognizeEventReportsUsageMetadata(t *testing.T) {
	got := New().RecognizeEvent(sse.Event{Data: `{"candidates":[],"usageMetadata":
		{"promptTokenCount":8,"candidatesTokenCount":2,"cachedContentTokenCount":3,
		 "thoughtsTokenCount":4}}`})
	if got.Usage == nil {
		t.Fatal("no usage")
	}
	if got.Usage.InputTokens != 8 || got.Usage.OutputTokens != 2 ||
		got.Usage.CacheReadTokens != 3 || got.Usage.ReasoningTokens != 4 {
		t.Errorf("usage = %+v", *got.Usage)
	}
}

func TestRecognizeEventReportsAPromptFeedbackBlock(t *testing.T) {
	// Gemini's SSE has no error event type; a refusal arrives as a chunk
	// carrying promptFeedback.blockReason, and before commit that is a
	// provider answer rather than content.
	got := New().RecognizeEvent(sse.Event{
		Data: `{"promptFeedback":{"blockReason":"SAFETY"}}`})
	if got.ErrPayload == "" {
		t.Fatal("a blocked prompt was not recognized")
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... -run 'Recognize' -count=1
```

Expected: FAIL — `RecognizeEvent undefined`, `adapter.RawEvent undefined`.

- [ ] **Step 3: Grow the interface**

In `internal/adapter/adapter.go`, replace the `Forwarder` interface from Task 2
with:

```go
// RawEvent is what one forwarded SSE event means to the commit rule and to
// accounting.
//
// Deliberately not an ir.StreamEvent: the fast path never reconstructs IR, and
// a type that could be mistaken for one would invite a future change to start.
type RawEvent struct {
	// Content marks a content-bearing event under phase 3's definition — text,
	// thinking, or tool-input content. Pings, comments, message_start and
	// role-only deltas are not, because committing on a keepalive forfeits
	// failover for nothing.
	Content bool
	// ErrPayload is a non-empty in-stream error, whatever the status line said.
	// Anthropic delivers overloaded_error this way under a 200.
	ErrPayload string
	// Usage is what this event alone reported. Anthropic splits it across two
	// events, so the caller merges rather than assigns.
	Usage *ir.Usage
	// UsageOnly marks the extra final chunk an injected stream_options
	// produced, which is stripped when Darkrouter asked for it and the client
	// did not.
	UsageOnly bool
}

// Forwarder is implemented by an adapter whose wire format is close enough to
// an inbound dialect that a body can be forwarded rather than re-rendered.
//
// Optional, like TokenCounter and Embedder above, and for a stronger reason:
// master design §4.1 excludes bedrock because SigV4 signs a payload hash, and
// vertex because its URL encodes both publisher and model. Neither implements
// this interface, so neither can be made eligible by an oversight in a
// predicate somewhere else, and a sixth kind is ineligible until someone
// deliberately writes its builder.
//
// The two Recognize methods live here rather than in the executor because the
// usage wire shape already does. A second copy in exec would be the same field
// sets maintained twice, drifting the first time a vendor adds a category.
type Forwarder interface {
	BuildForward(ctx context.Context, t *Target, f *Forward) (*http.Request, error)
	// RecognizeEvent reads SSE structure only and never builds IR.
	RecognizeEvent(ev sse.Event) RawEvent
	// RecognizeUsage reads a complete unary body. A nil return means the body
	// carried no usage, which is logged as unknown rather than estimated.
	RecognizeUsage(body []byte) *ir.Usage
}
```

Add `"github.com/darkraise/darkrouter/internal/sse"` to the imports. `sse`
imports nothing from `internal`, so there is no cycle.

- [ ] **Step 4: Implement the three recognizers**

Append to `internal/adapter/openaicompat/forward.go`:

```go
// forwardChunk is the subset of a streamed chunk the recognizer reads. It is
// separate from wireChunk in parse.go on purpose: that one exists to build IR,
// this one exists to answer three questions without building anything.
type forwardChunk struct {
	Choices []struct {
		Delta struct {
			Content          string          `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			ToolCalls        json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *wireUsage      `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func (a *Adapter) RecognizeEvent(ev sse.Event) adapter.RawEvent {
	if ev.Data == "" || ev.Data == sse.Done {
		return adapter.RawEvent{}
	}
	var c forwardChunk
	if json.Unmarshal([]byte(ev.Data), &c) != nil {
		// An unparseable event is not a reason to stop forwarding: after
		// commit the recognizer's opinion no longer matters, and before it a
		// silent chunk is not evidence of a fault.
		return adapter.RawEvent{}
	}
	if len(c.Error) > 0 && string(c.Error) != "null" {
		return adapter.RawEvent{ErrPayload: ev.Data}
	}
	out := adapter.RawEvent{}
	for _, ch := range c.Choices {
		if ch.Delta.Content != "" || ch.Delta.ReasoningContent != "" ||
			len(ch.Delta.ToolCalls) > 0 {
			out.Content = true
			break
		}
	}
	if c.Usage != nil {
		u := c.Usage.toIR()
		out.Usage = &u
		out.UsageOnly = len(c.Choices) == 0
	}
	return out
}

func (a *Adapter) RecognizeUsage(body []byte) *ir.Usage {
	var env struct {
		Usage *wireUsage `json:"usage"`
	}
	if json.Unmarshal(body, &env) != nil || env.Usage == nil {
		return nil
	}
	u := env.Usage.toIR()
	return &u
}
```

Append to `internal/adapter/anthropic/forward.go`:

```go
func (a *Adapter) RecognizeEvent(ev sse.Event) adapter.RawEvent {
	if ev.Data == "" {
		return adapter.RawEvent{}
	}
	var w struct {
		Type    string `json:"type"`
		Message *struct {
			Usage wireUsage `json:"usage"`
		} `json:"message"`
		Delta *struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
		Usage *wireUsage      `json:"usage"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal([]byte(ev.Data), &w) != nil {
		return adapter.RawEvent{}
	}

	switch w.Type {
	case "error":
		return adapter.RawEvent{ErrPayload: ev.Data}
	case "message_start":
		if w.Message == nil {
			return adapter.RawEvent{}
		}
		u := w.Message.Usage.toIR()
		return adapter.RawEvent{Usage: &u}
	case "message_delta":
		if w.Usage == nil {
			return adapter.RawEvent{}
		}
		u := w.Usage.toIR()
		return adapter.RawEvent{Usage: &u}
	case "content_block_delta":
		if w.Delta == nil {
			return adapter.RawEvent{}
		}
		// Phase 3's definition, matching stream.go: a signature_delta carries
		// no text, and an empty text_delta is not content. content_block_start
		// is not here either — it maps to EventBlockStart on the IR path, and
		// committing on it would commit one event earlier than the IR path
		// does on every Anthropic stream.
		content := w.Delta.Text != "" || w.Delta.Thinking != "" || w.Delta.PartialJSON != ""
		return adapter.RawEvent{Content: content}
	default:
		return adapter.RawEvent{}
	}
}

func (a *Adapter) RecognizeUsage(body []byte) *ir.Usage {
	var env struct {
		Usage *wireUsage `json:"usage"`
	}
	if json.Unmarshal(body, &env) != nil || env.Usage == nil {
		return nil
	}
	u := env.Usage.toIR()
	return &u
}
```

Append to `internal/adapter/gemini/forward.go`:

```go
func (a *Adapter) RecognizeEvent(ev sse.Event) adapter.RawEvent {
	if ev.Data == "" {
		return adapter.RawEvent{}
	}
	var w struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text         string          `json:"text"`
					FunctionCall json.RawMessage `json:"functionCall"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata  *wireUsageMetadata `json:"usageMetadata"`
		PromptFeedback *struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if json.Unmarshal([]byte(ev.Data), &w) != nil {
		return adapter.RawEvent{}
	}
	// Gemini's SSE defines no error event type, so a refusal arrives as a
	// chunk carrying promptFeedback.blockReason. Before commit that is the
	// provider's answer rather than content.
	if w.PromptFeedback != nil && w.PromptFeedback.BlockReason != "" {
		return adapter.RawEvent{ErrPayload: ev.Data}
	}
	out := adapter.RawEvent{}
	for _, c := range w.Candidates {
		for _, p := range c.Content.Parts {
			// A part carrying only a thoughtSignature yields an empty thinking
			// delta on the IR path, which is not content-bearing there either.
			if p.Text != "" || len(p.FunctionCall) > 0 {
				out.Content = true
			}
		}
	}
	if w.UsageMetadata != nil {
		u := w.UsageMetadata.toIR()
		out.Usage = &u
	}
	return out
}

func (a *Adapter) RecognizeUsage(body []byte) *ir.Usage {
	var env struct {
		UsageMetadata *wireUsageMetadata `json:"usageMetadata"`
	}
	if json.Unmarshal(body, &env) != nil || env.UsageMetadata == nil {
		return nil
	}
	u := env.UsageMetadata.toIR()
	return &u
}
```

`wireUsage` and `wireUsageMetadata` are the existing types in each package's
`parse.go`. **Check their real names and `toIR` signatures before writing this**
— if a package spells its usage type differently, use its name rather than
adding a second one.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/adapter/... -count=1
```

Expected: PASS, including the `var _ adapter.Forwarder` assertions from Task 2.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter
git commit -m "feat(adapter): recognize commit, error and usage raw"
```

---

### Task 7: The event splitter and the usage merge

**Files:**
- Create: `internal/exec/rawstream.go`
- Test: `internal/exec/rawstream_test.go`

**Interfaces:**
- Produces: `exec.eventSplitter` with `push([]byte) ([][]byte, error)` and `flush() []byte`; `exec.parseEvent(raw []byte, maxLine int) (sse.Event, bool)`; `exec.mergeUsage(dst *ir.Usage, u *ir.Usage)`.

The splitter is what makes spec §6's "tolerates lines split across read
boundaries" true. It cuts the forwarded byte stream at SSE event boundaries and
hands back **the exact bytes that arrived** for each event, so the forwarder can
write them verbatim.

**Why event granularity rather than chunk granularity.** Spec §5.2 requires the
injected usage chunk to be stripped from the response, which is impossible while
writing whatever bytes happen to arrive in a read. Holding bytes until an event
completes costs nothing observable: providers flush per event, so the boundary
we wait for is the one they just wrote, and an SSE client cannot see how a
stream was chunked. What is preserved is what matters — each event's bytes,
exactly.

**The cap is the same one phase 3 uses.** `Server.SSE.MaxLineBytes` bounds a
single line, and the forwarder in Task 8 bounds the pre-commit buffer with
`Server.SSE.MaxPrecommitBytes`. A provider that opens a stream and never
terminates an event would otherwise grow the carry without bound.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/rawstream_test.go`:

```go
package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/ir"
)

func TestSplitterCutsAtEventBoundaries(t *testing.T) {
	s := &eventSplitter{max: 1 << 16}
	got, err := s.push([]byte("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %q", len(got), got)
	}
	if string(got[0]) != "event: a\ndata: 1\n\n" {
		t.Errorf("event 0 = %q", got[0])
	}
	if string(got[1]) != "event: b\ndata: 2\n\n" {
		t.Errorf("event 1 = %q", got[1])
	}
}

func TestSplitterCarriesAPartialEventAcrossReads(t *testing.T) {
	// The failure this prevents: a provider writing "data: {" and "...}\n\n" in
	// two TCP segments, and a recognizer that saw neither as an event.
	s := &eventSplitter{max: 1 << 16}
	for _, chunk := range []string{"data: {\"a\"", ":1}\n"} {
		got, err := s.push([]byte(chunk))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Fatalf("premature event from %q: %q", chunk, got)
		}
	}
	got, err := s.push([]byte("\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got[0]) != "data: {\"a\":1}\n\n" {
		t.Errorf("got %q", got)
	}
}

func TestSplitterAcceptsCRLFAndLoneCR(t *testing.T) {
	// The SSE grammar permits all three terminators and providers differ.
	for _, tc := range []struct{ name, in string }{
		{"crlf", "data: 1\r\n\r\n"},
		{"lone cr", "data: 1\r\r"},
		{"lf", "data: 1\n\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &eventSplitter{max: 1 << 16}
			got, err := s.push([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || string(got[0]) != tc.in {
				t.Errorf("got %q, want %q", got, tc.in)
			}
		})
	}
}

func TestSplitterRefusesAnUnboundedCarry(t *testing.T) {
	s := &eventSplitter{max: 64}
	_, err := s.push(make([]byte, 65))
	if err == nil {
		t.Fatal("want an error when the carry exceeds the cap")
	}
}

func TestFlushReturnsAnUnterminatedTail(t *testing.T) {
	// A provider that ends its stream without a trailing blank line still owes
	// the client those bytes.
	s := &eventSplitter{max: 1 << 16}
	if _, err := s.push([]byte("data: last\n")); err != nil {
		t.Fatal(err)
	}
	if got := string(s.flush()); got != "data: last\n" {
		t.Errorf("flush = %q", got)
	}
	if got := s.flush(); len(got) != 0 {
		t.Errorf("a second flush returned %q", got)
	}
}

func TestParseEventReadsTheFieldsBack(t *testing.T) {
	ev, ok := parseEvent([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"), 1<<16)
	if !ok {
		t.Fatal("parseEvent reported failure")
	}
	if ev.Type != "message_stop" || ev.Data != `{"type":"message_stop"}` {
		t.Errorf("ev = %+v", ev)
	}
}

func TestParseEventReportsAComment(t *testing.T) {
	// OpenRouter sends ": OPENROUTER PROCESSING" as a keepalive. It dispatches
	// nothing, and treating it as an unrecognized event would be the same
	// answer by a longer route.
	if _, ok := parseEvent([]byte(": OPENROUTER PROCESSING\n\n"), 1<<16); ok {
		t.Error("a comment dispatched an event")
	}
}

func TestMergeUsageKeepsBothHalvesOfAnAnthropicStream(t *testing.T) {
	// spec §7: assigning message_delta's usage would erase the input count and
	// compute a wrong cost on every cached or long-prompt request.
	var got ir.Usage
	mergeUsage(&got, &ir.Usage{InputTokens: 100, CacheReadTokens: 40, CacheWriteTokens: 10})
	mergeUsage(&got, &ir.Usage{OutputTokens: 25})

	want := ir.Usage{InputTokens: 100, OutputTokens: 25, CacheReadTokens: 40, CacheWriteTokens: 10}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

func TestMergeUsageDoesNotZeroAKnownCount(t *testing.T) {
	got := ir.Usage{InputTokens: 100, OutputTokens: 25}
	mergeUsage(&got, &ir.Usage{OutputTokens: 0})
	if got.OutputTokens != 25 {
		t.Errorf("a later zero erased a known count: %+v", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'Splitter|ParseEvent|MergeUsage|Flush' -count=1
```

Expected: FAIL — `eventSplitter undefined`.

- [ ] **Step 3: Write it**

Create `internal/exec/rawstream.go`:

```go
package exec

import (
	"bytes"
	"errors"
	"io"

	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
)

// ErrEventTooLong means a provider opened an event and never terminated it
// within the configured line cap. It is an attempt failure, not a client error.
var ErrEventTooLong = errors.New("exec: forwarded event exceeds the line cap")

// eventSplitter cuts a forwarded byte stream into whole SSE events, handing
// back the exact bytes that arrived for each one.
//
// The fast path forwards at event granularity rather than chunk granularity
// because spec §5.2's usage-chunk strip is impossible otherwise. Nothing
// observable is lost: providers flush per event, and an SSE client cannot see
// how a stream was chunked. What is preserved is each event's bytes, exactly.
type eventSplitter struct {
	buf []byte
	max int
}

// push appends a chunk and returns every event it completed.
func (s *eventSplitter) push(chunk []byte) ([][]byte, error) {
	s.buf = append(s.buf, chunk...)
	var out [][]byte
	for {
		end := eventEnd(s.buf)
		if end < 0 {
			break
		}
		ev := make([]byte, end)
		copy(ev, s.buf[:end])
		out = append(out, ev)
		s.buf = s.buf[end:]
	}
	if s.max > 0 && len(s.buf) > s.max {
		return out, ErrEventTooLong
	}
	return out, nil
}

// flush returns the unterminated tail and empties the carry. A provider that
// ends without a trailing blank line still owes the client those bytes.
func (s *eventSplitter) flush() []byte {
	out := s.buf
	s.buf = nil
	return out
}

// eventEnd returns the index just past the first event boundary in b, or -1.
//
// The SSE grammar permits LF, CRLF and a lone CR as a line terminator, and a
// boundary is two in a row. Providers differ, so all three combinations have to
// be recognized rather than the one a given upstream happens to send.
func eventEnd(b []byte) int {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case '\n':
			if i+1 < len(b) {
				if b[i+1] == '\n' {
					return i + 2
				}
				if b[i+1] == '\r' {
					if i+2 < len(b) && b[i+2] == '\n' {
						return i + 3
					}
					return i + 2
				}
			}
		case '\r':
			if i+1 < len(b) && b[i+1] == '\n' {
				// CRLF: the boundary is this pair followed by another.
				if i+2 < len(b) {
					if b[i+2] == '\n' {
						return i + 3
					}
					if b[i+2] == '\r' {
						if i+3 < len(b) && b[i+3] == '\n' {
							return i + 4
						}
						return i + 3
					}
				}
				i++ // the LF is consumed by this CRLF
				continue
			}
			if i+1 < len(b) && b[i+1] == '\r' {
				return i + 2
			}
		}
	}
	return -1
}

// parseEvent reads one whole event's bytes back into its fields, reusing the
// same reader the IR path uses so the two cannot disagree about the grammar.
// It reports false for a comment or an empty block, which dispatch nothing.
func parseEvent(raw []byte, maxLine int) (sse.Event, bool) {
	ev, err := sse.NewReader(bytes.NewReader(raw), maxLine).Next()
	if err != nil {
		// io.EOF means the block dispatched nothing — a comment or blank
		// lines. Any other error is a malformed event, which spec §6 says to
		// stop recognizing and simply forward.
		_ = errors.Is(err, io.EOF)
		return sse.Event{}, false
	}
	return ev, true
}

// mergeUsage folds one event's usage into the running total.
//
// Merged rather than assigned because Anthropic splits usage across
// message_start and message_delta: assigning the second would erase the input
// and cache counts and compute a wrong cost on every cached or long-prompt
// request. A later zero never erases a known count, which is also what makes
// Gemini's cumulative usageMetadata safe to fold repeatedly.
func mergeUsage(dst *ir.Usage, u *ir.Usage) {
	if u == nil {
		return
	}
	if u.InputTokens > 0 {
		dst.InputTokens = u.InputTokens
	}
	if u.OutputTokens > 0 {
		dst.OutputTokens = u.OutputTokens
	}
	if u.CacheReadTokens > 0 {
		dst.CacheReadTokens = u.CacheReadTokens
	}
	if u.CacheWriteTokens > 0 {
		dst.CacheWriteTokens = u.CacheWriteTokens
	}
	if u.ReasoningTokens > 0 {
		dst.ReasoningTokens = u.ReasoningTokens
	}
}
```

`eventEnd` is fiddly enough that the three-terminator test above is what proves
it rather than reading it. If a case in that test fails, fix `eventEnd` — do not
relax the test.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'Splitter|ParseEvent|MergeUsage|Flush' -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/exec
git commit -m "feat(exec): split a forwarded stream at event bounds"
```

---

### Task 8: The streaming forwarder

**Files:**
- Create: `internal/exec/forward.go`
- Test: `internal/exec/forward_test.go`

**Interfaces:**
- Consumes: `eventSplitter`, `parseEvent`, `mergeUsage` (Task 7); `adapter.Forwarder.RecognizeEvent` (Task 6); `CommitWriter`, `AttemptCtx`, `applyUsage`, `warningStrings`, `reclassifyStream`, `writeDiagnostics` (existing).
- Produces: `(*Executor).forwardStream(cw *CommitWriter, resp *http.Response, ac *AttemptCtx, fw adapter.Forwarder, strip bool) (adapter.Outcome, *ir.Error)`; `exec.copyResponseHeaders(dst, src http.Header)`.

Phase 3's commit rule, applied to bytes instead of events. Before commit,
nothing reaches the client and a failure is invisible — the buffer is discarded
and the loop tries the next candidate. After commit, failover is impossible and
the stream simply ends.

**The scan is inline, and this is not a style preference.** Spec §7 is explicit:
a `TeeReader` into a piped goroutine is the classic stall — if the scanner falls
behind or exits, the pipe write blocks and the client's stream freezes. Read a
chunk, scan it, write it. No goroutine, no pipe, nothing to deadlock.

**`strip` is `injected` from Task 4**, and only that. When the client asked for
usage itself the chunk is theirs, and removing it would be a fourth mutation.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/forward_test.go`. `fakeForwarder` is a local recognizer
so the forwarder is tested without an adapter's wire format in the way:

```go
package exec

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/sse"
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
```

Add the two helpers at the bottom of the same file:

```go
// forwardFixture builds the smallest AttemptCtx forwardStream reads, over a
// recorder. It uses the same executor constructor the rest of this package's
// tests use; if that helper is named differently, use it rather than adding a
// second.
func forwardFixture(t *testing.T) (*CommitWriter, *AttemptCtx) {
	t.Helper()
	e := newTestExecutor(t)
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
```

`CommitWriter.w` is unexported and this test is in package `exec`, so the
reach-through compiles. If a later refactor exports it, update the helper rather
than the test.

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run ForwardStream -count=1
```

Expected: FAIL — `forwardStream undefined`.

- [ ] **Step 3: Write the forwarder**

Create `internal/exec/forward.go`:

```go
package exec

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/ir"
)

// forwardStream pipes a forwarded SSE response to the client, recognizing
// commit, in-stream errors and usage as the bytes go past.
//
// The scan is inline — read a chunk, split it, write it — rather than a
// TeeReader into a goroutine behind a pipe. Spec §7: if the scanner falls
// behind or exits, the pipe write blocks and the client's stream freezes.
// Inline scanning has no concurrency and cannot stall.
//
// strip is Task 4's injected flag: it removes the extra final usage chunk that
// Darkrouter's own stream_options produced. When the client asked for usage
// itself the chunk is theirs and removing it would be a fourth mutation.
func (e *Executor) forwardStream(cw *CommitWriter, resp *http.Response, ac *AttemptCtx,
	fw adapter.Forwarder, strip bool) (adapter.Outcome, *ir.Error) {

	defer resp.Body.Close()

	cfg, rec, c := ac.Cfg, ac.Rec, ac.Cand
	maxLine := cfg.Server.SSE.MaxLineBytes
	sp := &eventSplitter{max: maxLine}

	var (
		pending      [][]byte
		pendingBytes int
		committed    bool
		usage        ir.Usage
	)

	// Post-commit, policy.timeout.total stops applying and policy.timeout.idle
	// bounds the gap between events instead — the same switch the IR stream
	// path makes, for the same reason.
	resetIdle := func() {
		if d := cfg.Policy.Timeout.Idle; d > 0 {
			ac.Timer.Reset(d)
		}
	}

	commit := func() {
		committed = true
		ttft := time.Since(rec.TS).Milliseconds()
		rec.TTFTMs = &ttft
		rec.FinalProviderID = c.ProviderID
		rec.FinalModel = c.Model
		rec.Warnings = warningStrings(ac.Warns)
		copyResponseHeaders(cw.Header(), resp.Header)
		e.writeDiagnostics(cw, rec.ID, c, ac.Seq)
		cw.WriteHeader(resp.StatusCode)
		for _, raw := range pending {
			_, _ = cw.Write(raw)
		}
		pending, pendingBytes = nil, 0
		cw.Flush()
		resetIdle()
	}

	// step handles one whole event. A non-nil error ends the attempt.
	step := func(raw []byte) (adapter.Outcome, *ir.Error) {
		var re adapter.RawEvent
		if ev, ok := parseEvent(raw, maxLine); ok {
			re = fw.RecognizeEvent(ev)
		}
		if re.Usage != nil {
			mergeUsage(&usage, re.Usage)
			applyUsage(rec, &usage)
		}

		if !committed {
			if re.ErrPayload != "" {
				return adapter.OutcomeRetryableProvider,
					e.reclassifyStream(c, resp, rec, re.ErrPayload)
			}
			if re.Content {
				commit()
				_, _ = cw.Write(raw)
				cw.Flush()
				return adapter.OutcomeSuccess, nil
			}
			if strip && re.UsageOnly {
				return adapter.OutcomeSuccess, nil
			}
			if cap := cfg.Server.SSE.MaxPrecommitBytes; cap > 0 && pendingBytes+len(raw) > cap {
				return adapter.OutcomeRetryableProvider,
					e.reclassifyStream(c, resp, rec, ErrPreCommitBufferFull.Error())
			}
			pendingBytes += len(raw)
			pending = append(pending, raw)
			return adapter.OutcomeSuccess, nil
		}

		if strip && re.UsageOnly {
			return adapter.OutcomeSuccess, nil
		}
		_, _ = cw.Write(raw)
		cw.Flush()
		resetIdle()
		return adapter.OutcomeSuccess, nil
	}

	buf := make([]byte, 32<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			events, serr := sp.push(buf[:n])
			for _, raw := range events {
				if out, ierr := step(raw); ierr != nil {
					return out, ierr
				}
			}
			if serr != nil {
				if !committed {
					return adapter.OutcomeRetryableProvider,
						e.reclassifyStream(c, resp, rec, serr.Error())
				}
				// The client already has bytes; the stream ends here.
				return adapter.OutcomeSuccess, nil
			}
		}
		if rerr != nil {
			if rerr != io.EOF && !committed {
				return adapter.OutcomeRetryableProvider,
					e.reclassifyStream(c, resp, rec, rerr.Error())
			}
			break
		}
	}

	// A provider that ended without a trailing blank line still owes the
	// client those bytes.
	if tail := sp.flush(); len(tail) > 0 {
		if out, ierr := step(tail); ierr != nil {
			return out, ierr
		}
	}
	if !committed {
		// The stream ended with no content-bearing event. That is a
		// legitimately empty completion rather than a fault: failing over here
		// would burn the whole chain every time a model stops immediately.
		commit()
	}
	return adapter.OutcomeSuccess, nil
}

// hopByHop is RFC 9110 §7.6.1's connection-specific header set.
var hopByHop = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true,
}

// copyResponseHeaders forwards the upstream's headers minus the ones that
// describe the connection or the encoding.
//
// Content-Encoding and Content-Length are dropped rather than copied. Spec §8:
// copying them through would label bytes with an encoding or a length that the
// forward no longer matches — and stripping a usage chunk changes the length
// even when nothing else does. Darkrouter's own diagnostics are added after
// this call, so an upstream echoing one cannot spoof it.
func copyResponseHeaders(dst, src http.Header) {
	skip := map[string]bool{"content-length": true, "content-encoding": true}
	for _, v := range src.Values("Connection") {
		for _, name := range strings.Split(v, ",") {
			skip[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	for k, vs := range src {
		lk := strings.ToLower(k)
		if hopByHop[lk] || skip[lk] || strings.HasPrefix(lk, "x-darkrouter-") {
			continue
		}
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run ForwardStream -count=1 -v
go test ./internal/exec/ -race -count=1
```

Expected: PASS, race-clean.

- [ ] **Step 5: Commit**

```bash
git add internal/exec
git commit -m "feat(exec): forward a stream and scan it inline"
```

---

### Task 9: The unary forwarder

**Files:**
- Modify: `internal/exec/forward.go`
- Test: `internal/exec/forward_test.go`

**Interfaces:**
- Produces: `(*Executor).forwardUnary(cw *CommitWriter, resp *http.Response, ac *AttemptCtx, fw adapter.Forwarder) (adapter.Outcome, *ir.Error)`.

See "Two decisions this plan makes" above for why this buffers the whole body
rather than spec §7's bounded tail. The cap exists only to bound a hostile
provider, and breaching it costs the token count, never the response.

- [ ] **Step 1: Write the failing test**

Append to `internal/exec/forward_test.go`:

```go
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
```

- [ ] **Step 2: Run it to verify it fails**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run ForwardUnary -count=1
```

Expected: FAIL — `forwardUnary undefined`.

- [ ] **Step 3: Write it**

Append to `internal/exec/forward.go`:

```go
// maxForwardedUnaryBytes bounds what one unary response buffers for usage
// extraction. A body past it is still forwarded in full — breaching the cap
// costs the token count, never the response.
//
// Buffering the whole body rather than spec §7's bounded tail is deliberate:
// the IR path's ParseResponse already reads the entire unary body into memory,
// so this changes no memory characteristic of the product, and there is no
// truncation point to get wrong.
const maxForwardedUnaryBytes = 32 << 20

func (e *Executor) forwardUnary(cw *CommitWriter, resp *http.Response, ac *AttemptCtx,
	fw adapter.Forwarder) (adapter.Outcome, *ir.Error) {

	defer resp.Body.Close()
	rec, c := ac.Rec, ac.Cand

	body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxForwardedUnaryBytes+1))
	oversize := int64(len(body)) > maxForwardedUnaryBytes
	if rerr != nil && !oversize {
		// Nothing has reached the client, so this is still a failover.
		return adapter.OutcomeRetryableProvider, &ir.Error{Type: ir.ErrAPI, Message: rerr.Error()}
	}

	warns := ac.Warns
	if oversize {
		warns = append(warns, ir.Warning{
			Field: "usage", Target: c.ProviderID + "/" + c.Model,
			Reason: "the response exceeded the buffer for usage extraction; tokens are unknown",
		})
	} else if u := fw.RecognizeUsage(body); u != nil {
		applyUsage(rec, u)
	} else {
		warns = append(warns, ir.Warning{
			Field: "usage", Target: c.ProviderID + "/" + c.Model,
			Reason: "the response carried no usage; tokens are recorded as unknown",
		})
	}

	ttft := time.Since(rec.TS).Milliseconds()
	rec.TTFTMs = &ttft
	rec.FinalProviderID = c.ProviderID
	rec.FinalModel = c.Model
	rec.Warnings = warningStrings(warns)

	copyResponseHeaders(cw.Header(), resp.Header)
	e.writeDiagnostics(cw, rec.ID, c, ac.Seq)
	cw.WriteHeader(resp.StatusCode)
	_, _ = cw.Write(body)
	if oversize {
		// Committed already: a truncated body would be worse than a slow one.
		_, _ = io.Copy(cw, resp.Body)
	}
	return adapter.OutcomeSuccess, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run Forward -count=1 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/exec
git commit -m "feat(exec): forward a unary body and read its usage"
```

---

### Task 10: Record which path each attempt took

**Files:**
- Create: `internal/store/migrations/0005_attempt_path.sql`
- Modify: `internal/store/log.go:14-23,216-218,273-280`, `internal/store/adminstore.go:458-470`, `internal/admin/requests.go:106-113`, `web/src/components/trace-drawer.tsx:20-29,87-97`
- Test: `internal/store/log_test.go`, `internal/store/migrate_test.go`, `internal/admin/requests_test.go`, `web/src/components/trace-drawer.test.tsx`

**Interfaces:**
- Produces: `store.AttemptRecord.Path string`; the `path` field on each entry of the trace endpoint's `attempts` array.

Spec §11's first done criterion is that Claude Code against an Anthropic
provider **takes the passthrough path**. Without this column that is not
observable from outside the process, and the criterion could only be asserted
rather than checked. It is an addition to what is recorded, not a change to what
anything decides — spec §2's "no change to logging semantics" is intact.

It also makes spec §9's IR retry legible: a pre-commit 400 followed by a
successful retry is two rows on one candidate, and without the column they look
like a provider that failed and then inexplicably worked.

- [ ] **Step 1: Write the failing tests**

Append to `internal/store/log_test.go`, following the run-then-cancel pattern
the tests above it use — `NewLogWriter`, a goroutine on `Run`, then `cancel()`
to drain:

```go
func TestAttemptPathRoundTrips(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 8, BatchSize: 1, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	r := rec("r1")
	r.Attempts = []AttemptRecord{
		{Seq: 0, ProviderID: "p", Model: "m", Outcome: "fatal", StatusCode: 400, Path: "passthrough"},
		{Seq: 1, ProviderID: "p", Model: "m", Outcome: "success", StatusCode: 200, Path: "ir"},
	}
	w.Log(r)

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	tr, ok, err := db.RequestTrace(context.Background(), "r1")
	if err != nil || !ok {
		t.Fatalf("RequestTrace: %v ok=%v", err, ok)
	}
	if len(tr.Attempts) != 2 {
		t.Fatalf("got %d attempts", len(tr.Attempts))
	}
	if tr.Attempts[0].Path != "passthrough" || tr.Attempts[1].Path != "ir" {
		t.Errorf("paths = %q, %q", tr.Attempts[0].Path, tr.Attempts[1].Path)
	}
}

func TestAttemptPathDefaultsToIR(t *testing.T) {
	// Every row written before this migration, and every caller not yet taught
	// about the column, means the IR path — which is what each of them took.
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 8, BatchSize: 1, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	w.Log(rec("r2")) // rec() sets no Path

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	tr, _, err := db.RequestTrace(context.Background(), "r2")
	if err != nil {
		t.Fatal(err)
	}
	if tr.Attempts[0].Path != "ir" {
		t.Errorf("Path = %q, want ir", tr.Attempts[0].Path)
	}
}
```

Append to `internal/admin/requests_test.go`, using whatever seeding helper that
file already has to write a request row — the assertion is only that the field
reaches the JSON:

```go
func TestTraceEndpointNamesTheAttemptPath(t *testing.T) {
	// Spec §11's first criterion is only checkable from outside the process if
	// the trace says which path served the request.
	h, db := newAdminFixture(t) // the helper this file already uses
	seedRequestWithAttempts(t, db, "r1", []store.AttemptRecord{
		{Seq: 0, ProviderID: "p", Model: "m", Outcome: "success", StatusCode: 200,
			Path: "passthrough"},
	})

	w := getJSON(t, h, "/api/requests/r1")
	var body struct {
		Attempts []struct {
			Path string `json:"path"`
		} `json:"attempts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Attempts) == 0 || body.Attempts[0].Path != "passthrough" {
		t.Errorf("attempts = %+v", body.Attempts)
	}
}
```

`newAdminFixture`, `seedRequestWithAttempts` and `getJSON` stand for whatever
`requests_test.go` already calls them. Use its names; do not add a fixture.

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ ./internal/admin/ -run 'AttemptPath|TraceEndpointNames' -count=1
```

Expected: FAIL — `AttemptRecord.Path undefined`.

- [ ] **Step 3: Add the migration**

Create `internal/store/migrations/0005_attempt_path.sql`:

```sql
-- Which rendering an attempt used: the canonical-IR translation, or the
-- passthrough fast path. Master design §4 makes this a per-attempt decision, so
-- it belongs on the attempt rather than on the request: one request can forward
-- to its first candidate and translate to its second.
--
-- The default is 'ir' because every row written before this migration took it,
-- and so does every caller that has not been taught about the column.
ALTER TABLE request_attempts ADD COLUMN path TEXT NOT NULL DEFAULT 'ir';
```

`internal/store/migrate.go` requires migrations to be contiguous from 1, so the
`0005_` prefix is load-bearing. `migrate_test.go` counts them — update the
expected count there if it hard-codes one.

- [ ] **Step 4: Carry it through the store**

`internal/store/log.go` — add to `AttemptRecord`:

```go
	// Path is "passthrough" or "ir". Empty means "ir": a caller that predates
	// the fast path is describing the only rendering there was.
	Path string
```

Extend the insert statement's column list and placeholder count, and in
`insertOne`:

```go
	for _, a := range r.Attempts {
		path := a.Path
		if path == "" {
			path = "ir"
		}
		if _, err := attStmt.ExecContext(ctx,
			r.ID, a.Seq, a.ProviderID, a.KeyID, a.Model, a.Outcome,
			a.StatusCode, a.LatencyMs, a.Error, path,
		); err != nil {
			return err
		}
	}
```

`internal/store/adminstore.go:458` — add `path` to the SELECT and `&a.Path` to
the `Scan`, keeping the two lists in the same order.

- [ ] **Step 5: Expose it and show it**

`internal/admin/requests.go:108-112` — add the field:

```go
			"latency_ms": a.LatencyMs, "error": a.Error, "path": a.Path,
```

`web/src/components/trace-drawer.tsx` — add `path: string` to the `Attempt`
type, a `<TableHead>Path</TableHead>` beside Latency, and the cell that renders
it. Follow the file's existing cell style rather than inventing a badge.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ ./internal/admin/ -count=1
cd web && npm test -- --run && cd ..
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store internal/admin web
git commit -m "feat(store): record which path an attempt took"
```

---

### Task 11: Wire the fast path into the attempt loop

**Files:**
- Modify: `internal/exec/exec.go` (`New`, `Handle`, `runAttempts`, `attempt`, `recordAttempt`), `internal/exec/surface.go` (`chatOp`, `passthroughOp`)
- Test: `internal/exec/exec_test.go`, `internal/exec/loop_test.go`

**Interfaces:**
- Consumes: `forwardable`, `rewriteForward`, `forwardHeaders` (Tasks 3-5); `forwardStream`, `forwardUnary` (Tasks 8-9); `store.AttemptRecord.Path` (Task 10).
- Produces: `exec.attemptResult{Outcome adapter.Outcome, Status int, Err *ir.Error, Path string, Committed bool}`; `exec.PathIR`, `exec.PathPassthrough`; `exec.passthroughOp`.

This is where the phase becomes real, and it is deliberately the smallest change
that could work. The loop keeps its budget gate, its live health re-check, its
credential rotation, its classification and its records. What changes is that
one attempt can render two ways, and that spec §9's two fallbacks exist.

**The rewrite failure is not a second attempt.** It happens before any upstream
connection, so it downgrades this attempt to the IR path in place and records a
warning. Counting it as an attempt would burn a slot on a request that never
reached a provider.

**The pre-commit 400 is a second attempt**, on the same candidate, and it is
what stops the optimization from turning a servable request into a hard failure.
A strict `openaicompat` provider rejects fields the IR path drops with a
warning; classifying that as `Fatal` would return a 400 to the client with no
failover at all.

- [ ] **Step 1: Write the failing tests**

Append to `internal/exec/exec_test.go`:

```go
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

func TestARewriteFailureDowngradesInPlace(t *testing.T) {
	// No top-level model field. The IR parse would have refused this body, and
	// its refusal is the right message — but it is not a second attempt.
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer up.Close()

	rec, _ := runChatRaw(t, up.URL, "anthropic",
		`{"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	for _, a := range rec.Attempts {
		if a.Path == PathPassthrough {
			t.Errorf("an unrewritable body was forwarded anyway: %+v", rec.Attempts)
		}
	}
}
```

Three helpers do not exist yet. Build them at the bottom of `exec_test.go`, on
top of one generalization of the fixture that file already has — `newExecutorFor`
below is `newExecutorWith` with the kind and the adapter map lifted out, and
Tasks 12, 13 and 14 all reuse it. **Do not add a second executor fixture.**

```go
// capture keeps the one record a run produces. A Logger must never block, and
// this one cannot.
type capture struct{ rec *store.RequestRecord }

func (c *capture) Log(r *store.RequestRecord) { c.rec = r }

// newExecutorFor is newExecutorWith over a chosen provider kind, with every
// forwardable adapter wired. It takes testing.TB so the benchmarks in Task 14
// can use it too.
func newExecutorFor(t testing.TB, kind, upstreamURL string, deps Deps) *Executor {
	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: up\n    kind: " + kind + "\n    base_url: " + upstreamURL +
		"\n    api_key: ${K}\n    models: [target-model]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk-upstream", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		"gemini":       geminiadapter.New(),
	}, deps)
}

// runChatKind drives one inbound body through Handle and returns the record the
// logger captured alongside the recorder.
func runChatKind(t *testing.T, upstreamURL, dialect, kind, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	cap := &capture{}
	e := newExecutorFor(t, kind, upstreamURL, Deps{Log: cap})

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

// runChat is runChatKind with the kind the dialect passes through to.
func runChat(t *testing.T, upstreamURL, dialect, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	return runChatKind(t, upstreamURL, dialect, forwardKinds[dialect], body)
}

// runChatRaw is runChat, kept as a separate name because the bodies it sends
// are deliberately unparseable by the rewrite and a reader should see that at
// the call site.
func runChatRaw(t *testing.T, upstreamURL, dialect, body string) (*store.RequestRecord, *httptest.ResponseRecorder) {
	t.Helper()
	return runChat(t, upstreamURL, dialect, body)
}
```

Task 13 needs three more shapes over the same base: `runGemini` (a Gemini
inbound path with its own model segment and `geminiedge.NewFor(r)`),
`runChatModel` (a provider serving a model the client did not name, so the
rewrite fires), and `runChatTwoProviders` (two providers in the YAML, the first
on priority 99). Add each as a parameter on `newExecutorFor`'s YAML rather than
as a fourth fixture.

- [ ] **Step 2: Run them to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run 'FastPath|IRPath|PreCommit400|RewriteFailure' -count=1
```

Expected: FAIL — `PathPassthrough undefined`.

- [ ] **Step 3: Let the chat op surrender its bytes**

In `internal/exec/surface.go`, add the optional interface beside `SurfaceOp`:

```go
// passthroughOp is implemented by a SurfaceOp whose inbound bytes can be
// forwarded rather than re-rendered.
//
// Optional, matching adapter.TokenCounter: an op that says nothing takes the IR
// path, which is every auxiliary surface. Multipart and binary bodies are
// excluded by master design §4.1 and by having nothing to return here.
type passthroughOp interface {
	Passthrough() *edge.Passthrough
}
```

Give `chatOp` the field and the method:

```go
type chatOp struct {
	d   edge.Dialect
	req *ir.Request
	pt  *edge.Passthrough
}

func (o *chatOp) Passthrough() *edge.Passthrough { return o.pt }
```

In `exec.go`'s `Handle`, stop discarding the second return:

```go
	req, pt, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	// the existing parse-failure branch is unchanged
	e.RunSurface(w, r, &chatOp{d: d, req: req, pt: pt}, cfg)
```

- [ ] **Step 4: Turn `attempt`'s three returns into one result**

In `internal/exec/exec.go`:

```go
// The two renderings an attempt can use. They are the values of the request
// row's path column, so the strings are part of the schema.
const (
	PathIR          = "ir"
	PathPassthrough = "passthrough"
)

// attemptResult is what one upstream call produced. It replaced three returns
// when passthrough arrived: the loop needs to know which rendering ran and
// whether anything reached the client, and neither fits in an outcome.
type attemptResult struct {
	Outcome   adapter.Outcome
	Status    int
	Err       *ir.Error
	Path      string
	Committed bool
}
```

Change `attempt`'s signature to end `…, firstModel string, allowForward bool) attemptResult`
and every `return` in it to an `attemptResult` literal, carrying `Path` and
`Committed`. Then replace the build block — from `var warns []ir.Warning`
through the `op.Build` error check — with:

```go
	var warns []ir.Warning
	if iw, ok := inferredWarningFor(c, op.Query()); ok {
		warns = append(warns, iw)
	}

	path := PathIR
	var (
		hr        *http.Request
		fw        adapter.Forwarder
		strip     bool
		streaming bool
	)
	if allowForward {
		if pop, ok := op.(passthroughOp); ok {
			pt := pop.Passthrough()
			if f, eligible := forwardable(op.Dialect(), pt, c, p, ad); eligible {
				body, injected, rerr := rewriteForward(pt, op.Query().Model, c.Model, c.Kind)
				if rerr != nil {
					// spec §9: the IR parser produces a proper dialect-shaped
					// error if the body is genuinely invalid, and it can tell
					// the difference where this cannot. Not a second attempt —
					// nothing has reached a provider.
					warns = append(warns, ir.Warning{
						Field: "passthrough", Target: c.ProviderID + "/" + c.Model,
						Reason: "the body could not be forwarded and was translated instead: " +
							rerr.Error(),
					})
				} else {
					built, berr := f.BuildForward(ctx, tgt, &adapter.Forward{
						Body: body, Header: forwardHeaders(r), Stream: pt.Stream,
						Method: pt.Method, Query: pt.Query,
					})
					if berr != nil {
						return attemptResult{Outcome: adapter.OutcomeFatal, Path: PathIR,
							Err: &ir.Error{Type: ir.ErrDarkrouter, Message: berr.Error()}}
					}
					hr, fw, strip, streaming, path = built, f, injected, pt.Stream, PathPassthrough
				}
			}
		}
	}
	if hr == nil {
		built, buildWarns, err := op.Build(ctx, tgt, ad)
		warns = append(warns, buildWarns...)
		if err != nil {
			return attemptResult{Outcome: adapter.OutcomeFatal, Path: path,
				Err: &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()}}
		}
		hr = built
	}
```

Pass `path` into `recordAttempt` (add a trailing `path string` parameter and set
`a.Path = path`), and replace the `op.Respond` call with:

```go
	cw := NewCommitWriter(w)
	ac := &AttemptCtx{
		Exec: e, Cfg: cfg, Cand: c, Rec: rec, Seq: seq, Timer: timer,
		Warns: warns, Adapter: ad, FirstModel: firstModel,
	}
	var aerr *ir.Error
	switch {
	case path == PathPassthrough && streaming:
		outcome, aerr = e.forwardStream(cw, resp, ac, fw, strip)
	case path == PathPassthrough:
		outcome, aerr = e.forwardUnary(cw, resp, ac, fw)
	default:
		outcome, aerr = op.Respond(cw, resp, ac)
	}
	if cw.Committed() && outcome != adapter.OutcomeSuccess {
		rec.ErrorCode = string(ir.ErrAPI)
		return attemptResult{Outcome: adapter.OutcomeSuccess, Status: statusCode,
			Path: path, Committed: true}
	}
	return attemptResult{Outcome: outcome, Status: statusCode, Err: aerr,
		Path: path, Committed: cw.Committed()}
```

- [ ] **Step 5: Add the pre-commit 400 retry to the loop**

In `runAttempts`, replace the `e.attempt(...)` call and the lines that read its
returns with:

```go
		attempts++
		res := e.attempt(w, r, op, cfg, c, byID[c.ProviderID], bud, rec, attempts, ad, cat, firstModel, true)
		if res.Err != nil {
			lastErr = res.Err
		}

		// spec §9: strict openaicompat providers reject fields the IR path
		// would have dropped with a warning. Classifying that as Fatal would
		// convert a request the IR path could have served into a hard failure
		// with no failover — a silent regression introduced by an
		// optimization. The same candidate is retried once through the IR path
		// before any Fatal classification stands, and both attempts are
		// recorded.
		if res.Path == PathPassthrough && !res.Committed &&
			res.Outcome == adapter.OutcomeFatal && res.Status == http.StatusBadRequest &&
			attempts < maxAttempts && bud.canStartAttempt(time.Now()) {

			attempts++
			res = e.attempt(w, r, op, cfg, c, byID[c.ProviderID], bud, rec, attempts, ad, cat, firstModel, false)
			if res.Err != nil {
				lastErr = res.Err
			}
		}

		next, action := nextIndex(cands, i, res.Outcome, res.Status)
		switch action {
		case actionFinish:
			rec.Status = "success"
			return
		case actionReturn:
			if res.Outcome == adapter.OutcomeClientCancelled {
				rec.Status = "cancelled"
			}
			if lastErr != nil {
				rec.ErrorCode = string(lastErr.Type)
				e.writeErrorDiagnostics(w, rec, attempts)
				_ = op.WriteError(w, lastErr)
			}
			return
		default:
			i = next
		}
```

- [ ] **Step 6: Disable transparent compression**

In `New`, add to the `http.Transport` literal:

```go
				// Spec §8: bytes must arrive as the provider sent them, so the
				// forwarder can pass them through unchanged. Go otherwise adds
				// Accept-Encoding: gzip and decompresses transparently, which
				// would make fidelity depend on a precondition rather than on
				// a setting. The IR path pays more bandwidth for it.
				DisableCompression: true,
```

- [ ] **Step 7: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -count=1 && go test ./internal/exec/ -race -count=1
go test ./... -count=1
```

Expected: PASS, race-clean. The whole tree matters here: this is the first task
that changes behavior every existing executor test observes.

- [ ] **Step 8: Commit**

```bash
git add internal/exec
git commit -m "feat(exec): choose passthrough per attempt"
```

---

### Task 12: The differential suite

**Files:**
- Create: `internal/golden/differential_test.go`
- Test: itself

**Interfaces:**
- Consumes: everything. The corpus is `internal/golden/testdata/golden/` exactly as it stands — `<dialect>/<case>/request.json` for inbound bodies, `responses/<kind>/<case>/response.json` and `streams/<kind>/<case>/upstream.sse` as canned upstreams.
- Produces: nothing. This is spec §10, the core of the phase's verification.

The corpus needs no new fixtures. Phase 4 already recorded thinking blocks,
cache control with TTLs, parallel tool calls, multi-part content and mid-stream
errors, and every one of them is an inbound body a client could send. Every
fixture names `target-model`, which is also the target's name for it, so the
same-name byte-identical path is the one under test.

**Byte comparison of upstream requests is wrong by construction** and the suite
must not attempt it. The IR path merges consecutive same-role turns, drops
fields it cannot model, and injects `stream_options`; the passthrough path
forwards what the client wrote. What can be asserted, and is:

1. **The forwarded body is the inbound body**, byte for byte — the fidelity
   claim stated as an assertion rather than a hope.
2. **Neither path invents a top-level field.** The IR body's top-level key set
   must be a subset of the passthrough body's plus `stream_options`. A new key
   on either side is either a fourth mutation or a rendering bug.
3. **Every top-level scalar both paths carry agrees** — `model`, `max_tokens`,
   `temperature`, `top_p`, `stream`, `stop_sequences`. Neither path transforms
   these, so a disagreement is a real defect.

**Client-visible responses are compared on the IR-modeled projection**, with the
passthrough result asserted to be exactly the provider's bytes. Byte equality
between the two paths is impossible: passthrough preserves the provider's
fields, order and chunk boundaries while the IR path re-serializes.

**Usage must agree exactly for every fixture.** This is what makes §5.2's
injection and §7's Anthropic two-event fix verifiable rather than asserted, and
it is the criterion spec §11 names.

- [ ] **Step 1: Write the driver**

Create `internal/golden/differential_test.go`:

```go
// The differential suite runs one corpus through both request paths and
// compares them. Spec §10: the IR path is the correctness baseline, and the
// fast path is validated by proving it agrees with that baseline.
//
// It deliberately does not assert on two known IR-path fidelity gaps recorded
// in docs/PROGRESS.md §3b: the IR path emits a usage chunk the client never
// asked for, and that chunk carries a synthesized choice where OpenAI emits an
// empty array. Both are IR-path defects that predate this phase.
package golden

import (
	"bytes"
	"encoding/json"
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
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/edge"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
	"github.com/darkraise/darkrouter/internal/exec"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// forwardableDialects maps a corpus dialect onto the kind it passes through to.
// The corpus's responses/ and streams/ directories are canned upstreams rather
// than inbound bodies, and are read by name.
var forwardableDialects = map[string]string{
	"openai":    "openaicompat",
	"anthropic": "anthropic",
	"gemini":    "gemini",
}

// unforwardable hides a kind's Forwarder implementation so the same adapter can
// serve as the IR-path baseline against the same upstream. Embedding the
// interface rather than the concrete type is what drops the extra methods.
//
// This is how the suite drives both paths without a production flag that exists
// only for tests — the eligibility predicate declines because the adapter no
// longer claims it can forward, which is the real reason bedrock declines too.
type unforwardable struct{ adapter.Adapter }

// capture keeps the one record a run produces.
type capture struct{ rec *store.RequestRecord }

func (c *capture) Log(r *store.RequestRecord) { c.rec = r }

type pathResult struct {
	UpstreamBody   []byte
	UpstreamHeader http.Header
	ClientBody     []byte
	ClientStatus   int
	Record         *store.RequestRecord
}

// executorFor builds an executor over one provider of the given kind. When
// forward is false every adapter is wrapped so none can forward.
func executorFor(t *testing.T, kind, upstreamURL string, forward bool, cap *capture) *exec.Executor {
	t.Helper()
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: up\n    kind: " + kind + "\n    base_url: " + upstreamURL +
		"\n    api_key: ${K}\n    models: [target-model]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk-upstream", true })
	if err != nil {
		t.Fatal(err)
	}
	ads := map[string]adapter.Adapter{
		"openaicompat": openaicompat.New(),
		"anthropic":    anthropicadapter.New(),
		// The same offline fetcher the rest of this package uses: no golden
		// test makes an outbound request for media.
		"gemini": geminiadapter.NewWithFetcher(&offlineFetcher),
	}
	if !forward {
		hidden := make(map[string]adapter.Adapter, len(ads))
		for k, ad := range ads {
			hidden[k] = unforwardable{ad}
		}
		ads = hidden
	}
	return exec.New(cfgStore, provider.NewYAMLSource(cfgStore), ads, exec.Deps{Log: cap})
}

// dialectFor returns the inbound dialect, per request where it is
// request-scoped. Gemini reads ?alt=sse, which decides its stream wire form.
func dialectFor(dialect string, r *http.Request) edge.Dialect {
	if dialect == "gemini" {
		return geminiedge.NewFor(r)
	}
	return dialects()[dialect]
}

// bothPaths drives one inbound body twice against the same canned upstream:
// once forwarding, once translating.
func bothPaths(t *testing.T, dialect, kind string, m meta, inbound []byte,
	upstream http.HandlerFunc) (fast, irp pathResult) {

	t.Helper()
	for _, run := range []struct {
		forward bool
		out     *pathResult
	}{{true, &fast}, {false, &irp}} {
		var got pathResult
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got.UpstreamBody, _ = io.ReadAll(r.Body)
			got.UpstreamHeader = r.Header.Clone()
			upstream(w, r)
		}))
		cap := &capture{}
		e := executorFor(t, kind, srv.URL, run.forward, cap)

		req := requestFor(t, dialect, m, inbound)
		w := httptest.NewRecorder()
		e.Handle(w, req, dialectFor(dialect, req))
		srv.Close()

		got.ClientBody, got.ClientStatus, got.Record = w.Body.Bytes(), w.Code, cap.rec
		if got.Record == nil {
			t.Fatalf("no request record for %s/%s (forward=%v)", dialect, kind, run.forward)
		}
		*run.out = got
	}
	return fast, irp
}

// serveBytes answers with a fixed body, which is how both paths are given
// identical upstream responses.
func serveBytes(body []byte, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}
}

// minimalBody is the smallest valid inbound body per dialect, used when the
// fixture under test is the upstream response rather than the request.
func minimalBody(dialect string) []byte {
	switch dialect {
	case "anthropic":
		return []byte(`{"model":"target-model","max_tokens":16,` +
			`"messages":[{"role":"user","content":"hi"}]}`)
	case "gemini":
		return []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`)
	default:
		return []byte(`{"model":"target-model","messages":[{"role":"user","content":"hi"}]}`)
	}
}

func streamingBody(dialect string) []byte {
	if dialect == "gemini" {
		// Gemini's stream flag is the URL operation, not a body field.
		return minimalBody(dialect)
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(minimalBody(dialect), &top)
	top["stream"] = json.RawMessage("true")
	out, _ := json.Marshal(top)
	return out
}

// streamMeta is the Gemini path a streaming corpus run needs; the other two
// dialects carry no path value.
func streamMeta(dialect string) meta {
	if dialect == "gemini" {
		return meta{Path: "models/target-model:streamGenerateContent"}
	}
	return meta{}
}

func topLevelKeys(t *testing.T, body []byte) map[string]bool {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("body is not an object: %s", body)
	}
	out := make(map[string]bool, len(top))
	for k := range top {
		out[k] = true
	}
	return out
}

func topLevelValue(t *testing.T, body []byte, key string) (any, bool) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("body is not an object: %s", body)
	}
	raw, ok := top[key]
	if !ok {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v, true
}
```

- [ ] **Step 2: Write the three comparisons**

Append to the same file:

```go
func TestDifferentialUpstreamRequests(t *testing.T) {
	for dialect, kind := range forwardableDialects {
		for _, dir := range caseDirs(t, dialect) {
			t.Run(dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				m := readMeta(t, dir)
				inbound := readFixture(t, filepath.Join(dir, "request.json"))
				fast, irp := bothPaths(t, dialect, kind, m, inbound, cannedUnary(kind))

				// 1. The forwarded body is the inbound body. Every fixture
				// names target-model, which is the target's name for it, so no
				// rewrite fires and the bytes are identical.
				if !bytes.Equal(fast.UpstreamBody, inbound) {
					t.Errorf("the forwarded body is not the inbound body\n got: %s\nwant: %s",
						fast.UpstreamBody, inbound)
				}

				// 2. Neither path invented a top-level field.
				fastKeys := topLevelKeys(t, fast.UpstreamBody)
				for k := range topLevelKeys(t, irp.UpstreamBody) {
					if k == "stream_options" {
						continue // master design §4.2's third permitted mutation
					}
					if !fastKeys[k] {
						t.Errorf("the IR path sent a top-level %q the client did not", k)
					}
				}

				// 3. The scalars neither path transforms agree.
				for _, k := range []string{"model", "max_tokens", "temperature",
					"top_p", "stream", "stop_sequences"} {
					a, aok := topLevelValue(t, fast.UpstreamBody, k)
					b, bok := topLevelValue(t, irp.UpstreamBody, k)
					if aok != bok || (aok && !reflect.DeepEqual(a, b)) {
						t.Errorf("%s differs: passthrough %v (%v), ir %v (%v)", k, a, aok, b, bok)
					}
				}
			})
		}
	}
}

func TestDifferentialClientResponses(t *testing.T) {
	for dialect, kind := range forwardableDialects {
		for _, dir := range caseDirs(t, filepath.Join("responses", kind)) {
			t.Run(dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				canned := readFixture(t, filepath.Join(dir, "response.json"))
				fast, irp := bothPaths(t, dialect, kind, meta{}, minimalBody(dialect),
					serveBytes(canned, "application/json"))

				// The client received the provider's own bytes. This is the
				// phase, stated as one assertion.
				if !bytes.Equal(bytes.TrimSpace(fast.ClientBody), bytes.TrimSpace(canned)) {
					t.Errorf("the client did not receive the provider's bytes\n got: %s\nwant: %s",
						fast.ClientBody, canned)
				}
				// And the IR path agrees on everything the IR models.
				a, b := project(t, dialect, fast.ClientBody), project(t, dialect, irp.ClientBody)
				if !reflect.DeepEqual(a, b) {
					t.Errorf("projections differ\npassthrough: %+v\n        ir: %+v", a, b)
				}
			})
		}
	}
}

func TestDifferentialUsageAgrees(t *testing.T) {
	// spec §11: usage accounting agrees between the two paths across the whole
	// corpus, including Anthropic cache tokens and OpenAI streamed usage.
	for dialect, kind := range forwardableDialects {
		for _, dir := range caseDirs(t, filepath.Join("streams", kind)) {
			t.Run("stream/"+dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				canned := readFixture(t, filepath.Join(dir, "upstream.sse"))
				fast, irp := bothPaths(t, dialect, kind, streamMeta(dialect),
					streamingBody(dialect), serveBytes(canned, "text/event-stream"))
				assertSameUsage(t, fast.Record, irp.Record)
			})
		}
		for _, dir := range caseDirs(t, filepath.Join("responses", kind)) {
			t.Run("unary/"+dialect+"/"+filepath.Base(dir), func(t *testing.T) {
				canned := readFixture(t, filepath.Join(dir, "response.json"))
				fast, irp := bothPaths(t, dialect, kind, meta{}, minimalBody(dialect),
					serveBytes(canned, "application/json"))
				assertSameUsage(t, fast.Record, irp.Record)
			})
		}
	}
}

func assertSameUsage(t *testing.T, fast, irp *store.RequestRecord) {
	t.Helper()
	for _, f := range []struct {
		name        string
		got, wanted int64
	}{
		{"tokens_in", fast.TokensIn, irp.TokensIn},
		{"tokens_out", fast.TokensOut, irp.TokensOut},
		{"cache_read", fast.CacheReadTokens, irp.CacheReadTokens},
		{"cache_write", fast.CacheWriteTokens, irp.CacheWriteTokens},
		{"reasoning", fast.ReasoningTokens, irp.ReasoningTokens},
	} {
		if f.got != f.wanted {
			t.Errorf("%s: passthrough %d, ir %d", f.name, f.got, f.wanted)
		}
	}
}
```

- [ ] **Step 3: Write the two remaining helpers**

`cannedUnary(kind)` returns a minimal valid unary response for that kind, so
the request comparison has something to complete against.
`project(dialect, body)` pulls the IR-modeled fields out of a client response in
that dialect's shape. Both are mechanical:

```go
// cannedUnary is the smallest valid response each kind can return. The request
// comparison does not read it; it exists so the attempt completes.
func cannedUnary(kind string) http.HandlerFunc {
	switch kind {
	case "anthropic":
		return serveBytes([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"hi"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":2}}`),
			"application/json")
	case "gemini":
		return serveBytes([]byte(`{"candidates":[{"content":{"role":"model",
			"parts":[{"text":"hi"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":4,"candidatesTokenCount":2}}`),
			"application/json")
	default:
		return serveBytes([]byte(`{"id":"c","object":"chat.completion","model":"target-model",
			"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},
			"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2}}`),
			"application/json")
	}
}

// projection is what the IR models about a response, read back out of whatever
// the client actually received. Comparing on this rather than on bytes is spec
// §10's rule: the two paths are expected to differ in field order, in fields
// the IR does not carry, and in chunk boundaries.
type projection struct {
	Text  string
	Stop  string
	Model string
	In    float64
	Out   float64
}

func project(t *testing.T, dialect string, body []byte) projection {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("client body is not JSON: %s", body)
	}
	dig := func(path ...any) any {
		cur := any(v)
		for _, p := range path {
			switch k := p.(type) {
			case string:
				m, ok := cur.(map[string]any)
				if !ok {
					return nil
				}
				cur = m[k]
			case int:
				a, ok := cur.([]any)
				if !ok || k >= len(a) {
					return nil
				}
				cur = a[k]
			}
		}
		return cur
	}
	str := func(x any) string { s, _ := x.(string); return s }
	num := func(x any) float64 { n, _ := x.(float64); return n }

	switch dialect {
	case "anthropic":
		return projection{
			Text:  str(dig("content", 0, "text")),
			Stop:  str(dig("stop_reason")),
			Model: str(dig("model")),
			In:    num(dig("usage", "input_tokens")),
			Out:   num(dig("usage", "output_tokens")),
		}
	case "gemini":
		return projection{
			Text:  str(dig("candidates", 0, "content", "parts", 0, "text")),
			Stop:  str(dig("candidates", 0, "finishReason")),
			In:    num(dig("usageMetadata", "promptTokenCount")),
			Out:   num(dig("usageMetadata", "candidatesTokenCount")),
		}
	default:
		return projection{
			Text:  str(dig("choices", 0, "message", "content")),
			Stop:  str(dig("choices", 0, "finish_reason")),
			Model: str(dig("model")),
			In:    num(dig("usage", "prompt_tokens")),
			Out:   num(dig("usage", "completion_tokens")),
		}
	}
}
```

`project` deliberately omits `id`. The IR path re-mints identifiers on some
dialects and passthrough preserves the provider's, so comparing them would fail
for a reason that is not a defect. `Model` is omitted for Gemini, whose response
does not carry one.

- [ ] **Step 4: Run it and triage every failure before changing anything**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -run Differential -count=1 -v 2>&1 | tee /tmp/differential.txt
```

**Expect failures on the first run.** Two are known and are phase 9's
inheritance rather than its defects — both are recorded in `docs/PROGRESS.md`
§3b:

- **The IR path emits a usage chunk the client never asked for.** Master design
  §4.2 scopes the strip to the passthrough path, so
  `internal/adapter/openaicompat/build.go:88` injects `stream_options`
  unconditionally and the resulting chunk is forwarded. The usage *numbers*
  still agree, so `TestDifferentialUsageAgrees` passes. **Do not fix it here** —
  it is an IR-path gap and fixing it would widen this phase.
- **That chunk carries a synthesized choice.** `chunk()` in
  `internal/edge/openai/stream.go:14` always builds one empty-delta choice where
  OpenAI emits `choices: []`. Same reasoning.

Anything else is a defect this task found. Fix it in the code, not in the
assertion — a differential suite relaxed until it passes proves nothing.

- [ ] **Step 5: Verify the whole tree**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/golden/ -count=1 -v | tail -40
go test ./... -count=1
```

- [ ] **Step 6: Commit**

```bash
git add internal/golden
git commit -m "test(golden): compare both paths over the corpus"
```

---

### Task 13: The behaviors spec §10 names individually

**Files:**
- Modify: `internal/exec/passthrough_test.go`, `internal/exec/forward_test.go`, `internal/exec/exec_test.go`
- Test: as above

**Interfaces:**
- Consumes: everything from Tasks 3-11.

Spec §10's list, minus the cases Tasks 3, 4 and 8 already pinned. Each is one
test and each names the thing that would break without it. Group them into the
file that owns the behavior rather than making a fourth test file.

- [ ] **Step 1: Write them**

```go
func TestNoAuxiliarySurfaceIsEverForwarded(t *testing.T) {
	// Master design §4.1: multipart and binary surfaces take the IR path, and
	// what enforces it is the op having no passthrough body to offer. A
	// transcription request's model lives inside a multipart form; an
	// embedding request has no messages at all.
	//
	// Zero values are enough: this asserts on the method set, not on behavior.
	for name, op := range map[string]SurfaceOp{
		"transcription": &transcriptionOp{},
		"embedding":     &embedOp{},
		"image":         &imageOp{},
		"speech":        &speechOp{},
		"rerank":        &rerankOp{},
		"moderation":    &moderationOp{},
	} {
		if _, ok := op.(passthroughOp); ok {
			t.Errorf("%s offered a passthrough body", name)
		}
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

func TestAScannerErrorMidStreamLeavesTheClientStreamIntact(t *testing.T) {
	// spec §7: losing a token count is acceptable, corrupting a response is
	// not. An event the recognizer cannot parse is simply forwarded.
	cw, ac := forwardFixture(t)
	body := "data: c-first\n\ndata: {not json at all\n\ndata: c-second\n\n"
	out, ierr := ac.Exec.forwardStream(cw, streamResponse(body), ac, fakeForwarder{}, false)
	if out != adapter.OutcomeSuccess || ierr != nil {
		t.Fatalf("outcome = %v err = %v", out, ierr)
	}
	if got := recorderBody(cw); got != body {
		t.Errorf("the client stream was altered\n got: %q\nwant: %q", got, body)
	}
}

func TestAPromptWithHTMLCharactersSurvivesTheRewrite(t *testing.T) {
	// The end-to-end version of the unit test in Task 4: the client sends <, >
	// and &, the model name changes so the body must be re-encoded, and the
	// provider must still see the original characters.
	var seen []byte
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant",
			"content":[{"type":"text","text":"ok"}],"model":"target-model",
			"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer up.Close()

	runChatModel(t, up.URL, "anthropic", "other-model",
		`{"model":"client-model","max_tokens":16,
		  "messages":[{"role":"user","content":"if x < y && y > z"}]}`)

	if !bytes.Contains(seen, []byte("if x < y && y > z")) {
		t.Errorf("the prompt was escaped: %s", seen)
	}
}
```

`runGemini`, `runChatTwoProviders` and `runChatModel` are the same fixture from
Task 11 with different arguments. Add parameters to it rather than writing three
more.

- [ ] **Step 2: Run them**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -count=1 -v | tail -40
go test ./internal/exec/ -race -count=1
```

Expected: PASS. A failure here is a defect in Tasks 3-11, not in the test.

- [ ] **Step 3: Commit**

```bash
git add internal/exec
git commit -m "test(exec): cover every case spec section 10 names"
```

---

### Task 14: Benchmarks, so the optimization is measured

**Files:**
- Create: `internal/exec/bench_test.go`
- Test: itself

**Interfaces:**
- Consumes: `newExecutorFor` and `capture` from Task 11.

Spec §10's last line: benchmarks compare time-to-first-token and allocations
between the paths, so the optimization's value is measured rather than assumed.

Spec §3 is honest that the latency saving is modest — every request is parsed to
IR regardless, so the fast path saves the *render* and the per-event
re-serialization, not the parse. The benchmark exists to say by how much. If the
saving turns out to be negligible, that is a result for Task 16 to record, not a
reason to change anything: the fidelity argument is the stronger one and does
not depend on the timing.

- [ ] **Step 1: Write the benchmarks**

Create `internal/exec/bench_test.go`:

```go
package exec

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
)

// The upstream is a local httptest server, so absolute numbers carry no
// network. What the comparison isolates is Darkrouter's own work: the render
// the fast path skips, and the per-event re-serialization it also skips.
//
// The IR baseline is produced by hiding the adapter's Forwarder implementation
// rather than by a production flag, which is the same technique the differential
// suite uses and for the same reason.
type benchUnforwardable struct{ adapter.Adapter }

const benchInbound = `{"model":"target-model","max_tokens":64,` +
	`"messages":[{"role":"user","content":"Summarize the following in one sentence."}]}`

const benchUnaryResponse = `{"id":"msg_1","type":"message","role":"assistant",
	"content":[{"type":"text","text":"A one sentence summary."}],"model":"target-model",
	"stop_reason":"end_turn","usage":{"input_tokens":42,"output_tokens":8}}`

// benchStream writes twelve events, the first content-bearing one third, which
// is what a real Anthropic stream looks like at the front.
func benchStream() string {
	var b strings.Builder
	b.WriteString("event: message_start\ndata: {\"type\":\"message_start\"," +
		"\"message\":{\"id\":\"msg_1\",\"model\":\"target-model\"," +
		"\"usage\":{\"input_tokens\":42}}}\n\n")
	b.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\"," +
		"\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	for i := 0; i < 9; i++ {
		b.WriteString("event: content_block_delta\ndata: {\"type\":\"content_block_delta\"," +
			"\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"word \"}}\n\n")
	}
	b.WriteString("event: message_delta\ndata: {\"type\":\"message_delta\"," +
		"\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":9}}\n\n")
	b.WriteString("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	return b.String()
}

// benchExecutor is newExecutorFor with the fast path optionally hidden.
func benchExecutor(b *testing.B, upstreamURL string, forward bool) *Executor {
	b.Helper()
	e := newExecutorFor(b, "anthropic", upstreamURL, Deps{Log: &capture{}})
	if !forward {
		for k, ad := range e.adapters {
			e.adapters[k] = benchUnforwardable{ad}
		}
	}
	return e
}

func BenchmarkUnaryBothPaths(b *testing.B) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(benchUnaryResponse))
	}))
	defer up.Close()

	for _, tc := range []struct {
		name    string
		forward bool
	}{{"passthrough", true}, {"ir", false}} {
		b.Run(tc.name, func(b *testing.B) {
			e := benchExecutor(b, up.URL, tc.forward)
			d := anthropicedge.New()
			b.ReportAllocs()
			for b.Loop() {
				r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(benchInbound))
				r.Header.Set("Content-Type", "application/json")
				e.Handle(httptest.NewRecorder(), r, d)
			}
		})
	}
}

func BenchmarkStreamTimeToFirstToken(b *testing.B) {
	body := benchStream()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(body))
	}))
	defer up.Close()

	inbound := strings.Replace(benchInbound, `"max_tokens":64`, `"max_tokens":64,"stream":true`, 1)
	for _, tc := range []struct {
		name    string
		forward bool
	}{{"passthrough", true}, {"ir", false}} {
		b.Run(tc.name, func(b *testing.B) {
			e := benchExecutor(b, up.URL, tc.forward)
			d := anthropicedge.New()
			b.ReportAllocs()

			var total time.Duration
			n := 0
			for b.Loop() {
				r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(inbound))
				r.Header.Set("Content-Type", "application/json")
				w := &firstByteRecorder{ResponseRecorder: httptest.NewRecorder()}
				start := time.Now()
				e.Handle(w, r, d)
				// TTFT is to the client's first byte, not to the end: that is
				// the number a user feels, and it is the one the fast path
				// moves.
				total += w.first.Sub(start)
				n++
			}
			b.ReportMetric(float64(total.Nanoseconds())/float64(n), "ns/ttft")
		})
	}
}

// firstByteRecorder stamps the moment the first body byte is written.
type firstByteRecorder struct {
	*httptest.ResponseRecorder
	first time.Time
}

func (w *firstByteRecorder) Write(b []byte) (int, error) {
	if w.first.IsZero() && len(b) > 0 {
		w.first = time.Now()
	}
	return w.ResponseRecorder.Write(b)
}

func (w *firstByteRecorder) Flush() { w.ResponseRecorder.Flush() }
```

`e.adapters` is unexported and this file is in package `exec`, so the
reach-through compiles. `b.Loop()` rather than `for i := 0; i < b.N; i++`: it
keeps setup out of the timed region without `StopTimer` gymnastics.

- [ ] **Step 2: Run them and keep the numbers**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/exec/ -run '^$' -bench 'Unary|TimeToFirst' -benchmem -count=3 \
  | tee /tmp/phase9-bench.txt
cat /tmp/phase9-bench.txt
```

Three runs, because a single one on a loaded machine says nothing. Task 16
quotes this file, so keep it until then.

- [ ] **Step 3: Commit**

```bash
git add internal/exec
git commit -m "test(exec): benchmark both paths for ttft and allocs"
```

---

### Task 15: End to end, through an assembled server

**Files:**
- Create: `internal/e2e/phase9_test.go`
- Test: itself

**Interfaces:**
- Consumes: `internal/e2e/harness_test.go`'s `newGateway`, `mustAdmin`, `seedModel`, `proxy`, `chatBody`, `jsonStr` — exactly as `phase8_test.go` uses them. Do not add a second harness.

Everything before this is a unit or package-level test. This proves the wiring
holds from the proxy port to the upstream and back, through the real router, the
real health tracker, the real request log and the real admin API. Five cases,
each one a spec §11 done criterion.

- [ ] **Step 1: Write the shared fake and the seed helper**

Create `internal/e2e/phase9_test.go`:

```go
package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// echoFake records what one upstream saw and answers with a canned body.
type echoFake struct {
	mu     sync.Mutex
	body   []byte
	header http.Header
	path   string
	query  string
	calls  int

	// status and reply are what it answers with; replyOn400 is the body it
	// serves once it has already refused one request.
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

// attemptPaths reads the trace for a request id and returns the path of each
// attempt in order. This is the only way the fast path is observable from
// outside the process, which is why Task 10 added the column.
func attemptPaths(t *testing.T, g *gateway, requestID string) []string {
	t.Helper()
	w := g.mustAdmin(t, "GET", "/api/requests/"+requestID, "", http.StatusOK)
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
```

The request log is written by a background batcher, so the trace is not readable
the instant the proxy response returns. `phase8_test.go` already solves this —
find how it waits for a request row and use the same helper rather than a sleep.
If it has none, add one that polls `GET /api/requests` for the id with a bounded
deadline, and use it in all five cases.

- [ ] **Step 2: Write the five cases**

```go
func TestClaudeCodeShapedRequestTakesTheFastPath(t *testing.T) {
	// spec §11 criterion 1: Claude Code against an Anthropic provider takes the
	// passthrough path, and a request carrying a parameter the IR does not
	// model reaches the provider intact.
	g := newGateway(t)
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

	a := traceUsage(t, g, fast.Header().Get("X-Darkrouter-Request"))
	b := traceUsage(t, g, irp.Header().Get("X-Darkrouter-Request"))
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

// traceUsage reads the token counts off one request row.
type usageCounts struct{ In, Out, CacheRead int64 }

func traceUsage(t *testing.T, g *gateway, requestID string) usageCounts {
	t.Helper()
	w := g.mustAdmin(t, "GET", "/api/requests/"+requestID, "", http.StatusOK)
	var body struct {
		TokensIn  int64 `json:"tokens_in"`
		TokensOut int64 `json:"tokens_out"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return usageCounts{In: body.TokensIn, Out: body.TokensOut}
}
```

`traceUsage` reads only what the trace endpoint exposes today. If cache reads
are not on it, either add them there or drop the field — do not assert on a
number the API does not carry.

- [ ] **Step 3: Run them**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/e2e/ -count=1 -v
go test ./... -count=1 && go test ./internal/exec/ ./internal/e2e/ ./internal/golden/ -race -count=1
```

Expected: PASS, race-clean.

- [ ] **Step 4: Confirm nothing was left running**

```bash
ss -ltnp 2>/dev/null | grep -E ':(18080|18081)' || echo "no listener held"
```

- [ ] **Step 5: Commit**

```bash
git add internal/e2e
git commit -m "test(e2e): verify phase 9 through an assembled server"
```

---

### Task 16: Documentation

**Files:**
- Modify: `README.md` (§Status, and a new section after §Endpoints), `docs/PROGRESS.md` (phase table, a "Closed by phase 9" section, a "Carried forward from phase 9" section)

Write what is true, including the parts that are not flattering. Three things
belong in the record because a future reader will otherwise re-derive them:

1. **The measured benchmark numbers from Task 14**, with the caveat that the
   upstream was local. If the latency saving is small, say the number rather
   than describing it as modest.
2. **The two IR-path fidelity gaps Task 12 found and deliberately did not fix**
   — the unconditional usage chunk and its synthesized choice. They are already
   in `docs/PROGRESS.md` §3b as phase 4's carry-forward; move them into phase
   9's carried-forward section with the note that the differential suite now
   sees them.
3. **`DisableCompression` costs the IR path bandwidth.** One sentence naming the
   trade and the spec section that chose it.

- [ ] **Step 1: Update the README**

The §Status paragraph names phases 1–4 and is three phases stale. Rewrite it to
name what the gateway does now, and add a section after §Endpoints:

```markdown
## The fast path

When a client's dialect already matches the provider's wire format — Claude Code
against Anthropic, an OpenAI client against Groq — Darkrouter forwards the
request rather than translating it. The model name is rewritten, the credential
is swapped, and everything else the client sent reaches the provider unchanged,
including parameters Darkrouter has never heard of.

Eligibility is decided per attempt, so a request that forwards to its first
provider still translates correctly when it fails over to a different kind. The
trace drawer's Path column says which happened.

Bedrock and Vertex never take the fast path: Bedrock signs a hash of the request
body, and Vertex encodes the model in its URL alongside the publisher.
```

- [ ] **Step 2: Update PROGRESS.md**

Set phase 9's row to complete with the task count. Add "Closed by phase 9" and
"Carried forward from phase 9" sections in the established style — one bolded
claim per bullet, then the evidence.

- [ ] **Step 3: Commit**

```bash
git add README.md docs/PROGRESS.md
git commit -m "docs: document the passthrough fast path"
```

---

### Task 17: Verify live against Groq

**Files:**
- Modify: `docs/PROGRESS.md` (a verification section, in the style of §3 and §5)

Unlike phase 8, this phase is fully verifiable live. Groq is an
OpenAI-compatible provider, an OpenAI client is one of the three inbound
dialects, and the route is exactly the one master design §4 calls the
most-travelled. Nothing here needs a vendor account this machine does not have.

- [ ] **Step 1: Build and run the real binary**

```bash
export PATH=$PATH:/usr/local/go/bin
CGO_ENABLED=0 go build -o /tmp/darkrouter-phase9 ./cmd/darkrouter
ls -lh /tmp/darkrouter-phase9
```

Configure one Groq provider on `openai/gpt-oss-120b` — the model phase 6
verified, since the older `llama-3.3-70b-versatile` has been decommissioned —
and start it on ports 18080/18081 with `DARKROUTER_MASTER_KEY` set. Start it as
a tracked background task and capture the PID; it must be killed before this
task is done.

- [ ] **Step 2: Confirm the fast path is actually taken**

```bash
curl -s -H 'Authorization: Bearer <proxy-token>' -H 'Content-Type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","messages":[{"role":"user","content":"say hi"}]}' \
  http://127.0.0.1:18080/v1/chat/completions -D /tmp/h1 -o /tmp/b1
grep -i x-darkrouter /tmp/h1
```

Then read the trace for that request id from the admin API and confirm
`attempts[0].path` is `passthrough`. **A 200 alone proves nothing** — the IR
path would also return one, which is exactly why Task 10 exists.

- [ ] **Step 3: Confirm an unmodelled parameter survives**

Send a request carrying a top-level field Darkrouter's IR does not model and
confirm the provider's response reflects it rather than ignoring it. `seed` and
`service_tier` are good candidates; check Groq's current API before choosing,
and if the field is rejected, that is itself the proof that the body reached the
provider unmodified — record which.

- [ ] **Step 4: Confirm streamed usage, and that the injected chunk is stripped**

```bash
curl -sN -H 'Authorization: Bearer <proxy-token>' -H 'Content-Type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","stream":true,
       "messages":[{"role":"user","content":"count to five"}]}' \
  http://127.0.0.1:18080/v1/chat/completions | tee /tmp/stream1
```

Two assertions, and the second is the one that matters: the request row reports
non-zero `tokens_in` and `tokens_out`, **and** `/tmp/stream1` carries no
usage-bearing chunk, because the client did not ask for one. Then repeat with
`"stream_options":{"include_usage":true}` in the request and confirm the chunk
*is* present — the pair is what distinguishes stripping from never receiving.

Note the same caveat phase 4 recorded: **Groq returns usage whether or not it is
asked**, so a live call alone cannot distinguish injection from provider
default. The stripped-versus-kept pair above is what can be distinguished
live, and the injection itself stays pinned by Task 12's unit-level evidence.

- [ ] **Step 5: Confirm failover still translates**

Configure a second provider of a different kind behind Groq, force the first to
fail, and confirm the response is still correct and the second attempt's path is
`ir`.

- [ ] **Step 6: Measure it**

Time ten unary requests through the gateway and ten direct to Groq, and record
the medians. Phase 4 found the gateway *faster* than a direct call because
`exec`'s shared transport keeps a warm connection while each direct call pays a
fresh TLS handshake — expect the same confound and say so rather than claiming a
speedup the fast path did not cause.

- [ ] **Step 7: Stop everything**

```bash
pkill -P <pid> 2>/dev/null; kill <pid> 2>/dev/null
ss -ltnp 2>/dev/null | grep -E ':(18080|18081)' || echo "no listener held"
```

- [ ] **Step 8: Write it down and commit**

Add the results to `docs/PROGRESS.md` as a numbered verification section
matching §3 and §5's style, including anything that did not work.

```bash
git add docs/PROGRESS.md
git commit -m "docs: record phase 9 verification against groq"
```

---

## Self-review

Run against the spec after the last task, before merging.

**Spec coverage.** §4 eligibility → Task 3. §5.1 model rewrite → Tasks 1, 4.
§5.2 `stream_options` → Tasks 4, 8. §5.3 headers → Task 5. §5.4 body encoding →
Task 5. §6 recognizer → Tasks 6, 7. §7 usage extraction → Tasks 6, 8, 9. §8
response handling → Tasks 8, 11. §9 failure handling → Task 11. §10 differential
testing → Tasks 12, 13, 14. §11 done criteria → Tasks 15, 17.

**Two spec sections this plan deliberately implements differently**, both argued
above under "Two decisions this plan makes": §6's Anthropic commit trigger, and
§7's non-streaming tail buffer. Anyone reviewing the result against the spec
will notice both, so they are recorded rather than left to be rediscovered as
bugs.

**One spec section this plan moves.** §6 and §7 place the recognizer in the
executor; Task 6 puts it in the adapters, because the usage wire shapes already
live there and a second copy would drift. The executor keeps everything that is
not per-kind.

**Verify before claiming done:**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -count=1 && go vet ./... && gofmt -l . && \
  go test ./internal/exec/ ./internal/e2e/ ./internal/golden/ -race -count=1
```

Then confirm, by reading rather than by assuming: no process left running, no
port held, and `git status` clean.
