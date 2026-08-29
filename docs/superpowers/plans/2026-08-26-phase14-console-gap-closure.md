# Console Gap Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every feature gap between the phase 10 operator-console spec (§5–§6, §9–§10) and the console shipped in phase 13, backend enablers included, and prove closure against a live gateway.

**Architecture:** Six small Go additions to `internal/admin` (all following the existing handler patterns — synthesize-and-forward like `handlePlayground`, view structs with json tags, `requireSession`/`requireCSRF` wrappers), then frontend feature-folder work on the existing shape: types land in `src/lib/api-types.ts`, queries in `src/lib/queries.ts` behind the key factory, screens under `src/features/<destination>/`. No new dependencies except recharts, already installed.

**Tech Stack:** Go 1.26 stdlib `http.ServeMux`; React 19, TanStack Router/Query, darkraise-ui 6.5.0 (`data-table`, `components/chart`, `components/select`), recharts 3, vitest + testing-library.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` — §4 (ladder), §5 (IA), §6.1–§6.11 (screens), §9 (frontend architecture), §12 (done criteria). Gap inventory: the review of 2026-08-26 recorded in session memory and summarised in `docs/PROGRESS.md`.

## Global Constraints

These apply to every task without restating:

- TDD: a failing test precedes the implementation it tests.
- Gates before any commit: `go build ./... && go vet ./...` clean, `go test -race -count=1 ./internal/...` clean for Go tasks; `npm test`, `npm run typecheck`, `npm run build` clean in `web/` for frontend tasks. Full suite before the gate commit.
- The ladder is defined once (`web/src/features/ladder/ladder.tsx`). Never write ladder markup twice; embed `<Ladder …>`.
- Coral is brand only — position and primary action, never state. State colour has exactly three carve-outs: destructive affordance, request outcome, attention.
- Poll intervals come from `POLL` in `web/src/lib/api.ts` (3s fast, 30s slow); intervals live on hooks in `queries.ts`, never at call sites; paused when the tab is hidden (already set app-wide in `app.tsx`).
- Every filtered view is a URL via `useSearchFilters`. Component state holds nothing a reload should survive.
- Unpriced money renders as `—`, never `$0.00`.
- No endpoint response may contain credential material. New views follow `maskSecret` discipline.
- Comments explain WHY, never WHAT. No comment references this plan or a task.
- Commit subjects: `<type>(<scope>): <subject>`, imperative, ≤50 chars, no trailing period. Stage explicit paths, never `git add -A`. English only.
- Working tree starts from `master` at `fc3f36d` plus current uncommitted state; create branch `feat/console-gap-closure` first.

## Definition of Done (the gate)

Every row is verified in Task 23. "Command" runs from the repo root; "UAT" means against a live gateway started via `docker compose -f compose.uat.yml up` with one real provider configured, exercised through a browser in both light and dark mode.

| # | Criterion | Verification |
|---|---|---|
| D1 | Requests filters on error code and path work end to end | `go test ./internal/admin/ -run TestRequestFilters`; UAT: filter `error_code=rate_limit` returns only those rows; URL survives reload |
| D2 | Playground sends multi-turn messages, temperature, max_tokens, tools, and speaks all three dialects | `go test ./internal/admin/ -run TestPlayground`; UAT: same prompt through `openai` and `anthropic` dialect both return completions |
| D3 | Token count shows native vs estimated | `go test ./internal/admin/ -run TestPlaygroundCount`; UAT: header `X-Darkrouter-Estimated` drives the marker text |
| D4 | Auxiliary surfaces runnable from Playground | `go test ./internal/admin/ -run TestAux`; UAT: embeddings returns a vector preview; transcription accepts a dropped file |
| D5 | Discovery health visible per provider | `go test ./internal/admin/ -run TestDiscoveryHealth`; UAT: a provider with `missing_streak > 0` shows a degraded discovery line |
| D6 | OAuth credential detail (kind, expiry) shown | `go test ./internal/admin/ -run TestProviderViews`; UAT: oauth credential row shows expiry date |
| D7 | Media inlining has a config switch | `go test ./internal/config/ ./internal/adapter/gemini/ -run Media`; UAT: key off ⇒ Gemini image URL requests warn and drop the block |
| D8 | Preset browser adds a provider + credential without touching a file | UAT full flow: browse → filter by surface → create → add credential → probe ok |
| D9 | Model overrides editable; facets and provenance columns render | `npm test`; UAT: override written, catalog reflects it, columns show price/publisher/source |
| D10 | Policy editable hot vs restart marked; aliases drag-reorder with browser validation | `npm test`; UAT: change `total` takes effect after save; `connect` refused with explanation |
| D11 | Requests screen: DataTable sorting/visibility/CSV, combobox filters, time range, saved views, newer pill | `npm test`; UAT each interaction once |
| D12 | Overview: config banner, four sparklines, recent-failovers strip, ops footer | UAT: all four tiles sparkline; footer shows version/uptime/dropped counter |
| D13 | Trace: waterfall, bodies panel explains itself, surface meta, Open-in-playground round-trips | UAT: playground run → trace → seed back into playground |
| D14 | Usage: time-series stacked by provider, range picker, cost line, row click-through | UAT: clicking a provider row lands in Requests filtered |
| D15 | Connect: copyable base URLs, client snippets, live surfaces | UAT: copy button writes clipboard; snippet matches served routes |
| D16 | Settings: password change, reload, sync buttons work; file-owned blocks read-only with source labels | UAT: password change revokes other sessions; invalid YAML edit → reload reports invalid |
| D17 | Login shows identity mark; fresh install with zero providers teaches | UAT: brand-new data dir → first-run explains; empty screens carry legends |
| D18 | All §12 phase-10 criteria still hold | Re-walk `docs/ux/DONE-CRITERIA.md` table |
| D19 | Suites green | `go test -race -count=1 ./... && cd web && npm test && npm run typecheck && npm run build` |

## File Structure

New files:

```
internal/admin/discoveryapi.go            discovery-health rollup handler
internal/admin/playgroundaux.go           aux-surface + count runners
web/src/features/shell/identity-mark.tsx  §3.5 logo SVG
web/src/features/overview/ops-footer.tsx  version/uptime/dropped strip
web/src/features/overview/failovers.tsx   recent-failovers strip
web/src/features/requests/saved-views.ts  localStorage saved filter sets
web/src/features/models/override-editor.tsx
web/src/features/routing/policy-editor.tsx
web/src/features/providers/add-provider-dialog.tsx  preset browser + raw form
web/src/features/connect/snippets.ts      client config snippet templates
web/src/features/playground/chat.tsx      multi-turn chat surface
web/src/features/playground/compare.tsx   side-by-side runner
web/src/features/playground/aux-panels.tsx embeddings/rerank/moderation/images/speech/transcription
```

Modified files are named per task.

---

### Task 1: Requests filters — error_code and path, end to end

The spec's error-code combobox and the passthrough-vs-translated chip both lack server support: `GET /api/requests` cannot filter on `error_code`, and the row view does not expose which path (fast/IR) served. Add both to the filter/view layer in one slice because they touch the same four files and the same cursor-hash discipline.

**Files:**
- Modify: `internal/admin/cursor.go:18-46`
- Modify: `internal/admin/requests.go:10-60`
- Modify: `internal/store/adminstore.go:260-343` (RequestQuery + ListRequests where-loop)
- Modify: `internal/store/adminstore.go` (ListRequests SELECT: add latest-attempt path subquery)
- Test: `internal/admin/requests_test.go`

**Interfaces:**
- Consumes: existing `RequestFilters`/`Hash()`, `ListRequests(ctx, RequestQuery)`.
- Produces: query params `error_code` (exact match) on `/api/requests`; `RequestRow.path` ("fast"|"ir"|"" ) in the JSON view; `RequestQuery.ErrorCode`, `RequestFilters.ErrorCode` fields hashed last among strings.

- [ ] **Step 1: Write the failing tests**

In `internal/admin/requests_test.go` add (pattern follows the existing fixture style in that file):

```go
func TestErrorcodeFilterReturnsOnlyMatchingRows(t *testing.T) {
	fx := setUp(t) // existing helper in this file that seeds rows and serves
	seed := map[string]string{"id-a": "", "id-b": "rate_limit"}
	for id, code := range seed {
		fx.seedRequest(t, id, store.RequestSummary{ID: id, ErrorCode: code})
	}
	var page struct {
		Requests []struct{ ID, ErrorCode string `json:"error_code"` } `json:"requests"`
	}
	fx.get(t, "/api/requests?error_code=rate_limit", &page)
	if len(page.Requests) != 1 || page.Requests[0].ID != "id-b" {
		t.Fatalf("want only id-b, got %+v", page.Requests)
	}
}

func TestCursorMintedUnderOneErrorcodeIsRejectedUnderAnother(t *testing.T) {
	fx := setUp(t)
	fx.seedRequest(t, "id-a", store.RequestSummary{ID: "id-a", ErrorCode: "x"})
	first := fx.get(t, "/api/requests?error_code=x&limit=1", nil)
	cursor := first.cursor()
	fx.seedRequest(t, "id-b", store.RequestSummary{ID: "id-b"})
	rec := fx.raw(t, "/api/requests?error_code=y&cursor="+cursor)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("cursor under different filter must be rejected, got %d", rec.Code)
	}
}
```

Match the real helper names in `requests_test.go` — read the file first and adapt the calls, keeping the assertions exactly as written.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'TestErrorcode|TestCursorMinted' -v`
Expected: FAIL — unknown field / filter ignored (both rows returned).

- [ ] **Step 3: Implement**

`cursor.go` — add the field and extend the hash (fixed position, after Surface):

```go
type RequestFilters struct {
	Provider  string
	Model     string
	Status    string
	Alias     string
	Surface   string
	ErrorCode string
	SinceMs   int64
	UntilMs   int64
}
```

and in `Hash()` change the slice literal to include `f.ErrorCode` after `f.Surface`.

`store/adminstore.go` — add `ErrorCode string` to `RequestQuery`; in the `ListRequests` filter loop add `{"r.error_code", q.ErrorCode}` to the slice literal (after surface, before the since/until block so arg order stays deterministic).

`admin/requests.go` — `filtersFrom` gains `ErrorCode: q.Get("error_code")`; pass it into `store.RequestQuery`. In `requestView` add `Path string \`json:"path,omitempty"\`` and populate it (Step 4).

Latest-attempt path: in `ListRequests`' SELECT add a correlated subquery column after the attempts count:

```sql
(SELECT a.path FROM request_attempts a WHERE a.request_id = r.id ORDER BY a.seq DESC LIMIT 1)
```

Scan it into a new `Path string` on `RequestSummary`; map to `requestView.Path` in the admin handler.

- [ ] **Step 4: Run tests, then the package**

Run: `go test -race -count=1 ./internal/store/ ./internal/admin/`
Expected: PASS, including pre-existing cursor tests (their hashes changed only by new-field inclusion; no golden hashes exist for cursors — if a test asserts a literal hash string, update it to compute via `Hash()`).

- [ ] **Step 5: Commit**

```bash
git add internal/admin/cursor.go internal/admin/requests.go internal/admin/requests_test.go internal/store/adminstore.go
git commit -m "feat(admin): filter request log by error code, expose path"
```

---

### Task 2: Playground chat v2 — messages, params, dialects

`handlePlayground` hardcodes the OpenAI dialect and a system+user pair. Spec §6.8 needs multi-turn chat, sampling knobs, tools, and all three inbound dialects testable.

**Files:**
- Modify: `internal/admin/playground.go`
- Test: `internal/admin/playground_test.go`

**Interfaces:**
- Consumes: `exec.Executor.Handle(w, r, d edge.Dialect)`; `openaiedge.New()`, `anthropicedge.New()` (`internal/edge/anthropic`), `geminiedge.New()` (`internal/edge/gemini`).
- Produces: `POST /api/playground` body `{model, prompt?, system?, messages?, temperature?, max_tokens?, tools?, stream?, dialect?}` where `dialect ∈ {"openai","anthropic","gemini"}` (default openai). Response is the executor's streamed/unary answer in the selected inbound dialect.

- [ ] **Step 1: Write the failing tests**

Append to `playground_test.go` (reuse its existing fake-executor harness):

```go
func TestPlaygroundAcceptsFullMessagesAndAnthropicDialect(t *testing.T) {
	h := playgroundHarness(t) // existing helper returning handler + captured *http.Request
	h.post(t, `/api/playground`, map[string]any{
		"model": "claude-sonnet-4-6", "dialect": "anthropic",
		"messages": []map[string]any{
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "hello"},
			{"role": "user", "content": "continue"},
		},
		"temperature": 0.5, "max_tokens": 128,
	})
	if got := h.capturedPath; got != "/v1/messages" {
		t.Fatalf("path = %q", got)
	}
	body := h.capturedJSON(t)
	if len(body["messages"].([]any)) != 3 {
		t.Fatalf("messages not passed through: %v", body["messages"])
	}
	if body["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens missing: %v", body)
	}
}

func TestPlaygroundRejectsUnknownDialect(t *testing.T) {
	h := playgroundHarness(t)
	rec := h.rawPost(t, `/api/playground`, []byte(`{"model":"m","prompt":"p","dialect":"nope"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
}
```

If the existing harness captures differently, adapt the harness, not the assertions.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run TestPlayground -v`
Expected: new tests FAIL (unknown field ignored, path still `/v1/chat/completions`).

- [ ] **Step 3: Implement**

Replace the body struct and request synthesis in `playground.go`:

```go
type playgroundBody struct {
	Model       string           `json:"model"`
	Prompt      string           `json:"prompt,omitempty"`
	System      string           `json:"system,omitempty"`
	Messages    []map[string]any `json:"messages,omitempty"`
	Temperature *float64         `json:"temperature,omitempty"`
	MaxTokens   *int             `json:"max_tokens,omitempty"`
	Tools       []map[string]any `json:"tools,omitempty"`
	Stream      *bool            `json:"stream,omitempty"`
	Dialect     string           `json:"dialect,omitempty"`
}
```

Validation: `model` required; `prompt` required only when `Messages` is empty. Build `msgs`: if `Messages != nil` use it verbatim; else the old system+user pair.

```go
	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}
	payload := map[string]any{"model": body.Model, "stream": stream}
	if body.Messages != nil {
		payload["messages"] = body.Messages
	} else {
		msgs := []map[string]any{}
		if body.System != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": body.System})
		}
		msgs = append(msgs, map[string]any{"role": "user", "content": body.Prompt})
		payload["messages"] = msgs
	}
	if body.Temperature != nil {
		payload["temperature"] = *body.Temperature
	}
	if body.MaxTokens != nil {
		payload["max_tokens"] = *body.MaxTokens
	}
	if body.Tools != nil {
		payload["tools"] = body.Tools
	}
```

Dialect/path selection (imports `anthropicedge "…/internal/edge/anthropic"`, `geminiedge "…/internal/edge/gemini"`, `net/url`). The chat body travels as OpenAI-shaped messages in every case — the dialect parsers translate — except Anthropic's top-level `system`, which must be lifted out or the upstream 400s:

```go
	var d edge.Dialect = openaiedge.New()
	path := "/v1/chat/completions"
	switch body.Dialect {
	case "", "openai":
	case "anthropic":
		d = anthropicedge.New()
		path = "/v1/messages"
		// Anthropic requires max_tokens and carries system outside messages.
		if body.MaxTokens == nil {
			payload["max_tokens"] = 1024
		}
		if msgs, ok := payload["messages"].([]map[string]any); ok &&
			len(msgs) > 0 && msgs[0]["role"] == "system" {
			payload["system"] = msgs[0]["content"]
			payload["messages"] = msgs[1:]
		}
	case "gemini":
		d = geminiedge.New()
		action := ":generateContent"
		if stream {
			action = ":streamGenerateContent?alt=sse"
		}
		path = "/v1beta/models/" + url.PathEscape(body.Model) + action
	default:
		writeError(w, http.StatusBadRequest, "dialect must be openai, anthropic or gemini")
		return
	}
	pr, err := http.NewRequestWithContext(r.Context(), http.MethodPost, path, bytes.NewReader(chat))
```

(`chat` is the marshalled `payload`; keep the existing `Exec.Handle(w, pr, d)` tail.)

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`
Expected: PASS including old playground tests (default dialect unchanged).

- [ ] **Step 5: Commit**

```bash
git add internal/admin/playground.go internal/admin/playground_test.go
git commit -m "feat(admin): playground multi-turn, params and dialects"
```

---

### Task 3: Token counting endpoint

Both native count endpoints live on the proxy port, which deliberately refuses cookies. The console gets an admin wrapper over `Executor.HandleCount`.

**Files:**
- Create: `internal/admin/playgroundaux.go` (this task adds the count half; Task 4 appends aux surfaces to the same file)
- Modify: `internal/admin/admin.go` routes
- Test: `internal/admin/playground_test.go`

**Interfaces:**
- Consumes: `exec.HandleCount(w, r, d edge.CountWriter, nativeKind string)`; anthropic/gemini edge dialects (they implement `CountWriter`).
- Produces: `POST /api/playground/count` body `{dialect:"anthropic"|"gemini", model, prompt}`; response is the dialect's native count shape; header `X-Darkrouter-Estimated: true` present when locally estimated.

- [ ] **Step 1: Write the failing test**

```go
func TestCountWrapsAnthropicCountTokens(t *testing.T) {
	h := playgroundHarness(t)
	h.post(t, `/api/playground/count`, map[string]any{
		"dialect": "anthropic", "model": "claude-sonnet-4-6", "prompt": "hello world",
	})
	if h.capturedPath != "/v1/messages/count_tokens" {
		t.Fatalf("path = %q", h.capturedPath)
	}
	body := h.capturedJSON(t)
	msgs := body["messages"].([]any)
	if len(msgs) != 1 || body["max_tokens"] != nil {
		t.Fatalf("count bodies reject max_tokens/stream: %v", body)
	}
}

func TestCountRejectsOpenAIDialect(t *testing.T) {
	h := playgroundHarness(t)
	rec := h.rawPost(t, `/api/playground/count`,
		[]byte(`{"dialect":"openai","model":"m","prompt":"p"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run TestCount -v`
Expected: FAIL — 404, route absent.

- [ ] **Step 3: Implement**

In `playgroundaux.go`:

```go
package admin

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/edge"
)

// countBody selects the counting dialect. There is no "openai": the OpenAI
// wire has no counting endpoint, so an OpenAI-dialect count is always the
// local estimate, which the executor reaches through the chat dialects alone.
type countBody struct {
	Dialect string `json:"dialect"`
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
}

func (s *Server) handlePlaygroundCount(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body countBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var d edge.Dialect
	native := ""
	var payload any
	switch body.Dialect {
	case "anthropic":
		d = anthropicedge.New()
		native = "anthropic"
		payload = map[string]any{
			"model":    body.Model,
			"messages": []map[string]any{{"role": "user", "content": body.Prompt}},
		}
	case "gemini":
		d = geminiedge.New()
		native = "gemini"
		payload = map[string]any{
			"contents": []map[string]any{
				{"parts": []map[string]any{{"text": body.Prompt}}},
			},
		}
	default:
		writeError(w, http.StatusBadRequest, "dialect must be anthropic or gemini")
		return
	}
	raw, _ := json.Marshal(payload)
	path := "/v1/messages/count_tokens"
	if body.Dialect == "gemini" {
		path = "/v1beta/models/" + body.Model + ":countTokens"
	}
	pr, err := http.NewRequestWithContext(r.Context(), http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pr.Header.Set("Content-Type", "application/json")
	s.deps.Exec.HandleCount(w, pr, d.(edge.CountWriter), native)
}
```

Register in `routes()`: `s.mux.HandleFunc("POST /api/playground/count", s.requireCSRF(s.handlePlaygroundCount))`.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`
Expected: PASS. If `edge.CountWriter` assertion fails because the dialect stores it elsewhere, check how `server.go:252` passes it and mirror that call exactly.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/playgroundaux.go internal/admin/admin.go internal/admin/playground_test.go
git commit -m "feat(admin): token-count wrapper for the console"
```

---

### Task 4: Auxiliary-surfaces runner

Six proxy surfaces the console cannot reach today. One parameterized admin runner synthesizes the proxy request and lets the executor answer, so failover/log behaviour stays real.

**Files:**
- Modify: `internal/admin/playgroundaux.go`
- Modify: `internal/admin/admin.go` routes
- Test: `internal/admin/playground_test.go`

**Interfaces:**
- Consumes: `exec.Handle` with `openaiedge.New()` (all six surfaces are OpenAI-dialect inbound).
- Produces: `POST /api/playground/aux` body `{surface, model?, body?, file_b64?, filename?}`;
  `surface ∈ {"embeddings","rerank","moderations","images","speech","transcriptions"}`;
  responds verbatim with whatever the executor wrote (JSON, or audio bytes for speech).

- [ ] **Step 1: Write the failing test**

```go
func TestAuxEmbeddingsReachesTheEmbeddingsRoute(t *testing.T) {
	h := playgroundHarness(t)
	h.post(t, `/api/playground/aux`, map[string]any{
		"surface": "embeddings", "model": "text-embedding-3-small",
		"body": map[string]any{"input": "hello"},
	})
	if h.capturedPath != "/v1/embeddings" {
		t.Fatalf("path = %q", h.capturedPath)
	}
	b := h.capturedJSON(t)
	if b["model"] != "text-embedding-3-small" || b["input"] != "hello" {
		t.Fatalf("body merge wrong: %v", b)
	}
}

func TestAuxRejectsUnknownSurface(t *testing.T) {
	h := playgroundHarness(t)
	rec := h.rawPost(t, `/api/playground/aux`, []byte(`{"surface":"nope"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run TestAux -v`
Expected: FAIL — 404.

- [ ] **Step 3: Implement**

```go
// auxPaths mirrors the proxy routes the console may exercise. Speech and
// transcriptions return non-JSON bodies; the executor writes them straight
// through, so this handler copies nothing and simply hands over the ResponseWriter.
var auxPaths = map[string]string{
	"embeddings":     "/v1/embeddings",
	"rerank":         "/v1/rerank",
	"moderations":    "/v1/moderations",
	"images":         "/v1/images/generations",
	"speech":         "/v1/audio/speech",
	"transcriptions": "/v1/audio/transcriptions",
}

type auxBody struct {
	Surface  string          `json:"surface"`
	Model    string          `json:"model,omitempty"`
	Body     json.RawMessage `json:"body,omitempty"`
	FileB64  string          `json:"file_b64,omitempty"`
	Filename string          `json:"filename,omitempty"`
}

func (s *Server) handlePlaygroundAux(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var in auxBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<20)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	path, ok := auxPaths[in.Surface]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown surface "+in.Surface)
		return
	}

	if in.Surface == "transcriptions" {
		s.auxTranscription(w, r, in)
		return
	}
	merged := map[string]any{}
	if len(in.Body) > 0 {
		if err := json.Unmarshal(in.Body, &merged); err != nil {
			writeError(w, http.StatusBadRequest, "body must be a JSON object")
			return
		}
	}
	if in.Model != "" {
		merged["model"] = in.Model // the caller's form owns the model field
	}
	raw, _ := json.Marshal(merged)
	pr, err := http.NewRequestWithContext(r.Context(), http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pr.Header.Set("Content-Type", "application/json")
	s.deps.Exec.Handle(w, pr, openaiedge.New())
}

// auxTranscription rebuilds the multipart form from base64. The model part is
// appended after the file part: spec §6 places it there, and exec's rewrite
// depends on that order.
func (s *Server) auxTranscription(w http.ResponseWriter, r *http.Request, in auxBody) {
	file, err := base64.StdEncoding.DecodeString(in.FileB64)
	if err != nil || len(file) == 0 {
		writeError(w, http.StatusBadRequest, "file_b64 must be non-empty base64")
		return
	}
	name := in.Filename
	if name == "" {
		name = "audio.bin"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(file)
	_ = mw.WriteField("model", in.Model)
	_ = mw.Close()
	pr, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
		auxPaths["transcriptions"], &buf)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pr.Header.Set("Content-Type", mw.FormDataContentType())
	s.deps.Exec.Handle(w, pr, openaiedge.New())
}
```

Add imports `encoding/base64`, `mime/multipart`. Register: `s.mux.HandleFunc("POST /api/playground/aux", s.requireCSRF(s.handlePlaygroundAux))`.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/playgroundaux.go internal/admin/admin.go internal/admin/playground_test.go
git commit -m "feat(admin): auxiliary-surface runner for the playground"
```

---

### Task 5: Discovery health rollup

§6.5's discovery panel needs per-provider rollups. The signals already exist in the `models` table (`state`, `missing_streak`); expose them instead of inventing storage.

**Files:**
- Create: `internal/admin/discoveryapi.go`
- Modify: `internal/admin/admin.go` routes
- Modify: `internal/store/adminstore.go` (add `DiscoveryHealth`)
- Test: `internal/admin/healthapi_test.go`

**Interfaces:**
- Produces: `GET /api/health/discovery` → `{"providers":[{"provider_id","total","live","stale","removed_upstream","max_missing_streak"}]}`, sorted by provider_id. Providers with zero catalogued rows are absent — absence itself is the "never discovered" signal.

- [ ] **Step 1: Write the failing test**

In `healthapi_test.go`, following its seeding helpers:

```go
func TestDiscoveryHealthRollsUpPerProvider(t *testing.T) {
	fx := setUp(t)
	fx.seedModel(t, "groq", "live", 0)
	fx.seedModel(t, "groq", "live", 0)
	fx.seedModel(t, "groq", "stale", 3)
	fx.seedModel(t, "nebius", "removed_upstream", 9)
	var out struct {
		Providers []struct {
			ProviderID       string `json:"provider_id"`
			Total, Live, Stale, Removed int
			MaxMissingStreak int `json:"max_missing_streak"`
		} `json:"providers"`
	}
	fx.get(t, "/api/health/discovery", &out)
	if len(out.Providers) != 2 || out.Providers[0].ProviderID != "groq" {
		t.Fatalf("got %+v", out.Providers)
	}
	g := out.Providers[0]
	if g.Total != 3 || g.Live != 2 || g.Stale != 1 || g.MaxMissingStreak != 3 {
		t.Fatalf("groq rollup wrong: %+v", g)
	}
}
```

Adapt struct tags to one shared local type; adapt `seedModel` to however `fixtures_test.go` inserts catalog rows (read it first).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run TestDiscoveryHealth -v`
Expected: FAIL — 404.

- [ ] **Step 3: Implement**

Store method (verify the column names against migration 0001/0006 for the `models` table first — `state` and `missing_streak` are what PROGRESS records; adjust if the schema differs):

```go
type DiscoveryHealthRow struct {
	ProviderID       string
	Total            int
	Live             int
	Stale            int
	RemovedUpstream  int
	MaxMissingStreak int
}

func (d *DB) DiscoveryHealth(ctx context.Context) ([]DiscoveryHealthRow, error) {
	rows, err := d.Read.QueryContext(ctx, `
	  SELECT provider_id, COUNT(*),
	         SUM(state = 'live'), SUM(state = 'stale'),
	         SUM(state = 'removed_upstream'), COALESCE(MAX(missing_streak), 0)
	    FROM models GROUP BY provider_id ORDER BY provider_id`)
	...
}
```

Handler `discoveryapi.go` maps rows into the JSON above (empty slice, never nil). Register with `requireSession`.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/store/ ./internal/admin/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/discoveryapi.go internal/admin/admin.go internal/admin/healthapi_test.go internal/store/adminstore.go
git commit -m "feat(admin): per-provider discovery health rollup"
```

---

### Task 6: OAuth credential detail in the providers view

`credentialView` drops the `Kind`/`ExpiresAt`/`Scope` that `store.Credential` already carries, so the console cannot show an OAuth account's expiry.

**Files:**
- Modify: `internal/admin/providers.go:37-91`
- Test: `internal/admin/providers_test.go`

**Interfaces:**
- Produces: `credentialView` gains `"kind"` (always), `"expires_at"` and `"scope"` (oauth rows only, omitted otherwise). No secret material — expiry and scope are metadata, verified secret-free by the existing leak tests.

- [ ] **Step 1: Write the failing test**

```go
func TestCredentialViewCarriesOAuthMetadata(t *testing.T) {
	fx := setUp(t)
	id := fx.addCredential(t, "oa", store.Credential{
		ProviderID: "oa", Label: "sub", Kind: "oauth",
		Secret: "refresh-token-value", Scope: "read",
		ExpiresAt: ptr(int64(1790000000)),
	})
	var out struct {
		Providers []struct {
			Credentials []struct {
				ID        string `json:"id"`
				Kind      string `json:"kind"`
				ExpiresAt *int64 `json:"expires_at"`
				Scope     string `json:"scope,omitempty"`
				Masked    string `json:"masked"`
			} `json:"credentials"`
		} `json:"providers"`
	}
	fx.get(t, "/api/providers", &out)
	c := out.Providers[0].Credentials[0]
	if c.Kind != "oauth" || c.ExpiresAt == nil || *c.ExpiresAt != 1790000000 {
		t.Fatalf("oauth metadata missing: %+v", c)
	}
	if c.Masked == "refresh-token-value" || strings.Contains(c.Masked, "refresh-token") {
		t.Fatal("secret leaked")
	}
	_ = id
}
```

If `DB.Credentials` does not currently SELECT `kind/expires_at/scope`, extend its SELECT and scan in `internal/store/credentials.go` within this task and say so in the commit body.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run TestCredentialViewCarriesOAuth -v`
Expected: FAIL — kind empty, expires_at nil.

- [ ] **Step 3: Implement**

```go
type credentialView struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Masked  string `json:"masked"`
	Enabled bool   `json:"enabled"`
	Cooling bool   `json:"cooling"`
	Kind    string `json:"kind"`
	// OAuth-only metadata. A static key has neither, and omitting them keeps
	// the table honest about which rows have an account behind them.
	ExpiresAt *int64 `json:"expires_at,omitempty"`
	Scope     string `json:"scope,omitempty"`
}
```

Populate from `c.Kind`, `c.ExpiresAt`, `c.Scope` where the view is built.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/` (leak tests included).
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/providers.go internal/admin/providers_test.go internal/store/credentials.go
git commit -m "feat(admin): oauth credential metadata in providers view"
```

---

### Task 7: Media-inline config switch

Phase 4's carried-forward hazard: Gemini inlines client-supplied media URLs and nothing can turn that off. Add `catalog.media_inline` (restart-only, default on) and gate the fetcher.

**Files:**
- Modify: `internal/config/config.go:118` (CatalogConfig) + the loader that parses `catalog:` (find it via `rg "sync_interval" internal/config/`)
- Modify: `internal/server/server.go:160-170` (adapters map construction)
- Modify: `internal/adapter/gemini/adapter.go`, `internal/adapter/gemini/media.go`
- Test: `internal/config/load_test.go`, `internal/adapter/gemini/media_test.go`

**Interfaces:**
- Produces: config key `catalog.media_inline` (bool, default true, listed in `config.RestartOnly` as `catalog.media_inline`); `gemini.NewWithFetch(fetch bool) *Adapter` alongside unchanged `New()`; when off, `Fetcher.part` drops URL blocks with warning `"media_inline disabled"`.

- [ ] **Step 1: Write the failing tests**

`media_test.go`:

```go
func TestDisabledFetcherDropsURLBlocksWithAWarning(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("png"))
	}))
	defer up.Close()
	f := NewFetcher()
	f.enabled = false
	got, warns := f.part(context.Background(), &ir.Media{URL: up.URL + "/a.png"}, "image")
	if got != nil {
		t.Fatalf("block should be dropped, got %v", got)
	}
	if len(warns) == 0 || !strings.Contains(warns[0].Error(), "media_inline disabled") {
		t.Fatalf("warnings = %v", warns)
	}
}
```

`load_test.go` (follow its table style):

```go
{"catalog.media_inline=false parses", `catalog: { media_inline: false }`,
 func(t *testing.T, c *Config) {
	if c.Catalog.MediaInline == nil || *c.Catalog.MediaInline {
		t.Fatal("media_inline should parse false")
	}
}},
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/config/ ./internal/adapter/gemini/ -run Media -v`
Expected: compile FAIL (field/method absent).

- [ ] **Step 3: Implement**

- `CatalogConfig` gains `MediaInline *bool \`yaml:"media_inline"\``; loader leaves nil = default true (mirror how `Discovery.Enabled` is handled); append `"catalog.media_inline"` to `config.RestartOnly`.
- `Adapter`/`Fetcher`: give `Fetcher` an `enabled bool` (true from `NewFetcher()`), add:

```go
func NewWithFetch(enabled bool) *Adapter {
	return &Adapter{f: &Fetcher{client: defaultClient(), enabled: enabled}}
}
```

(matching whatever private fields `NewFetcher` sets today). In `part`, before fetching a remote URL: `if !f.enabled { return drop("media_inline disabled") }` — reuse the existing `drop` helper so the warning reaches `warnings_json`.
- `server.go` adapters map: replace `"gemini": geminiadapter.New()` with construction reading `cfg := s.store.Current(); inline := cfg.Catalog.MediaInline == nil || *cfg.Catalog.MediaInline; … geminiadapter.NewWithFetch(inline)`.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/config/ ./internal/adapter/gemini/ ./internal/server/`
Expected: PASS; golden suites untouched (fetcher enabled path identical).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/load.go internal/config/load_test.go internal/adapter/gemini/adapter.go internal/adapter/gemini/media.go internal/adapter/gemini/media_test.go internal/server/server.go
git commit -m "feat(config): catalog.media_inline switch gates gemini fetches"
```

---

### Task 8: Wire types and hooks for everything new

One task so later tasks share exact names.

**Files:**
- Modify: `web/src/lib/api-types.ts`
- Modify: `web/src/lib/queries.ts`
- Test: `web/src/lib/queries.test.tsx`

**Interfaces (Produced — later tasks import these):**

```ts
export type Preset = {
  id: string; name: string; kind: string; base_url: string;
  surfaces: string[]; auth_kind: string; website: string; free_tier: boolean;
}
export type Healthz = {
  config_valid: boolean; warnings: string[]; uptime: string; version: string;
  log_records_dropped: number; log_records_written: number; config_error?: string;
}
export type DiscoveryHealthRow = {
  provider_id: string; total: number; live: number; stale: number;
  removed_upstream: number; max_missing_streak: number;
}
export type Credential extends existing + { kind: string; expires_at?: number; scope?: string }
export type CountResult = { tokens: number; estimated: boolean }
export type AuxSurface = "embeddings" | "rerank" | "moderations" | "images" | "speech" | "transcriptions"
export type PlaygroundChatBody = {
  model: string; prompt?: string; system?: string;
  messages?: { role: string; content: string }[];
  temperature?: number; max_tokens?: number;
  tools?: Record<string, unknown>[]; stream?: boolean;
  dialect?: "openai" | "anthropic" | "gemini";
}
export type AuxBody = {
  surface: AuxSurface; model?: string; body?: Record<string, unknown>;
  file_b64?: string; filename?: string;
}
export type RequestRow += { path?: string }
export type Model += { pricing: { input_micros: number | null; output_micros: number | null } | null; publisher: string; merge_source: string }
```

Hooks: `useHealthz()` (POLL.slow, key `keys.healthz`), `useDiscoveryHealth()` (POLL.fast, `keys.discovery`). Extend `keys` accordingly. `useModels` unchanged.

- [ ] **Step 1: Write the failing test** — in `queries.test.tsx` assert `useHealthz` hits `/healthz` and polls slow (copy the existing hook-test pattern in that file, swapping path/interval).

- [ ] **Step 2: Run** `cd web && npm test -- queries` → FAIL.

- [ ] **Step 3: Implement** the types and two hooks exactly as declared above.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api-types.ts web/src/lib/queries.ts web/src/lib/queries.test.tsx
git commit -m "feat(web): wire types and hooks for gap features"
```

---

### Task 9: Identity mark and login

§3.5's mark: buildable from rects and lines, two colours only.

**Files:**
- Create: `web/src/features/shell/identity-mark.tsx`
- Modify: `web/src/routes/login.tsx`
- Test: `web/src/features/shell/nav.test.ts` (extend) or new `identity-mark.test.tsx`

**Interfaces:** `IdentityMark({ size?: number })` — renders the 24×24-grid SVG scaled to `size` (default 36), hairline strokes in `hsl(var(--foreground))` at 45% opacity, pip filled `hsl(var(--primary))`.

Geometry (from §3.5; `docs/ux/mockups/fragments/00-design-language.html` is the contract — if the fragment's pip placement differs from this, copy the fragment): hairline square (2,2)→(22,22); spine segments x=12: y=2→7, y=10→14, y=17→22 (three segments so the hollow squares stay hollow); left stubs y=7 and y=17 from x=2→9; middle stub y=12 from x=2→19; hollow 3×3 squares centred on the spine at (12,7) and (12,17); filled 4×4 accent square starting at (20,10), vertically centred on the middle stub.

- [ ] **Step 1: Write the failing test**

```tsx
import { render } from "@testing-library/react"
import { IdentityMark } from "./identity-mark"

test("mark draws three spine segments and one accent pip", () => {
  const { container } = render(<IdentityMark />)
  expect(container.querySelectorAll("line").length).toBeGreaterThanOrEqual(8)
  expect(container.querySelector(".pip")).not.toBeNull()
  expect(container.querySelectorAll(".spine-seg").length).toBe(3)
})

test("login shows the mark", () => {
  const { container } = render(<LoginScreen onAuthenticated={() => {}} />)
  expect(container.querySelector("svg")).not.toBeNull()
})
```

- [ ] **Step 2: Run** `npm test -- identity-mark` → FAIL.

- [ ] **Step 3: Implement** — SVG with `viewBox="0 0 24 24"`, `strokeWidth="1"`, class `spine-seg` on the three spine lines, pip `<rect className="pip" x="20" y="10" width="4" height="4" fill="hsl(var(--primary))"/>`. Insert `<IdentityMark />` above the `<h1>` in `login.tsx`.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/shell/identity-mark.tsx web/src/features/shell/identity-mark.test.tsx web/src/routes/login.tsx
git commit -m "feat(web): identity mark on login"
```

---

### Task 10: Overview completion

Banner, four sparklines, recent-failovers strip, ops footer, clickable flow-graph providers.

**Files:**
- Create: `web/src/features/overview/failovers.tsx`, `web/src/features/overview/ops-footer.tsx`
- Modify: `web/src/features/overview/overview-screen.tsx`, `web/src/features/overview/flow-graph.tsx`
- Test: `web/src/features/overview/overview-screen.test.tsx`

**Interfaces:** consumes `Overview.failovers` (`FailoverRow[]`), `useHealthz()` (Task 8), `Sparkline` (already in overview-screen). Produces: `<Failovers rows={o.failovers}/>` linking each row to `/requests/{id}`; `<OpsFooter/>` reading useHealthz; `FlowGraph` providers get optional `href` rendered as `<Link>` around `.rf-provider`.

- [ ] **Step 1: Write the failing tests** (pure-function style matching `overview-screen.test.tsx`):

```ts
test("sparkline points derive from series", () => {
  expect(sparkPoints([{ requests: 1 }, { requests: 3 }, { requests: 2 }])).toEqual([1, 3, 2])
})
test("failover strip labels show alias, attempts and provider", () => {
  const label = failoverLabel({ ts: 0, alias: "fast", attempts: 3, final_provider_id: "b", final_model: "m", id: "x", total_ms: 12 })
  expect(label).toContain("fast"); expect(label).toContain("×3"); expect(label).toContain("b")
})
test("dropped counter renders zero honestly and non-zero as attention", () => {
  expect(droppedText(0)).toMatch(/no records dropped/)
  expect(droppedText(7)).toMatch(/7/)
})
```

- [ ] **Step 2: Run** `npm test -- overview` → FAIL.

- [ ] **Step 3: Implement.**

- Config banner: when `config.data && !config.data.valid`, render a destructive banner at the top carrying `config.data.error` and the reassurance "the previous configuration is still serving" (data already available via `useConfig`).
- Sparklines: requests tile keeps its existing `series`-derived sparkline. The other three tiles derive their series from the `byProvider` usage rows already fetched: spend = `cost_micros` summed per day; errors = `(attempts − requests)` per day, floored at 0; latency has no per-day source, so its tile gets **no** sparkline rather than one plotting an unrelated number — spec §8.4 anticipated this degradation ("a missing sparkline series renders as the bare number"). Add pure helpers `spendSeries(rows)`, `errorSeries(rows)` and unit-test them.
- Failovers strip: below the graph, list up to five `o.failovers` rows via `<Failovers>`; each row links to `/requests/{id}`.
- Ops footer: pinned to the page bottom; reads `useHealthz()` and renders `version · up {uptime} · {log_records_dropped} of {log_records_written} log records dropped`. A zero dropped count renders as "no records dropped".
- Flow-graph links: wrap each `.rf-provider` node in a TanStack `<Link to="/providers/$id">` so every provider on the graph opens its detail page.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/overview/
git commit -m "feat(web): complete the overview screen"
```

---

### Task 11: Requests upgrade — DataTable, real filters, saved views, newer pill

**Files:**
- Modify: `web/src/features/requests/requests-screen.tsx`
- Create: `web/src/features/requests/saved-views.ts`
- Test: `web/src/lib/search-filters.test.ts`, new `web/src/features/requests/saved-views.test.ts`, `requests-table.test.tsx`

**Interfaces:** consumes darkraise `DataTable` (`columns`, `facets`, `virtualize`), `ColumnHeader`, `exportToCsv`; `useSearchFilters` extended FIELDS `["provider","model","status","alias","surface","error_code"]` + numeric `since_ms`/`until_ms`; produces `loadSavedViews()/saveView(name,filters)/deleteView(name)` on `localStorage["darkrouter_saved_views"]`; `newerCount(firstPage, heldNewestId)`; derived row field `failover: boolean` (attempts > 1) offered as a DataTable facet — "failovers only" is therefore a one-click facet, not a saved URL view, because the API has no attempts filter and inventing one client-side as a URL param would filter nothing.

- [ ] **Step 1: Write the failing tests**

```ts
// saved-views.test.ts
test("views round-trip through localStorage", () => {
  saveView("errors today", { status: "error", since_ms: "1724630400000" })
  expect(loadSavedViews()).toEqual([
    { name: "errors today", filters: { status: "error", since_ms: "1724630400000" } },
  ])
  deleteView("errors today")
  expect(loadSavedViews()).toEqual([])
})

test("two views are seeded on first read and survive re-reads", () => {
  // fresh storage
  expect(loadSavedViews().map(v => v.name)).toEqual(["errors today", "passthrough misses"])
  expect(loadSavedViews().map(v => v.name)).toEqual(["errors today", "passthrough misses"])
})

// search-filters.test.ts addition
test("numeric since_ms survives a round trip", () => {
  // assert setFilter("since_ms", "1000") yields ?since_ms=1000 in history.replace call
})

// requests-table.test.tsx
test("newer pill counts arrivals beyond the held page", () => {
  expect(newerCount([{ id: "b" }], "a")).toBe(1) // newest id "b" sorts after held "a"
  expect(newerCount([], null)).toBe(0)
})
test("failover flag marks multi-attempt rows", () => {
  expect(withFailover({ attempts: 3 }).failover).toBe(true)
  expect(withFailover({ attempts: 1 }).failover).toBe(false)
})
```

- [ ] **Step 2: Run** `npm test -- saved-views search-filters requests-table` → FAIL.

- [ ] **Step 3: Implement.**

- Swap the hand-rolled `<Table>` for `darkraise-ui/data-table`'s `DataTable` with `facets={["provider","status","surface","error_code","failover"]}` and `virtualize={{ rowHeight: 28, height: 480 }}`. Each row gains the derived scalar fields the facets need (`provider`, `status`, `surface`, `error_code ?? ""`, `failover`). CSV button calls `exportToCsv(rows, "requests.csv", [...])`; sortable column heads use `ColumnHeader`.
- Filter row: `Select` components populated from distinct values seen across loaded pages for provider/model/status/surface/error_code; alias stays a free-text Input; time-range `Select` (1h/24h/7d/custom) writes `since_ms`/`until_ms` into the URL.
- Saved views: on first read seed two entries — "errors today" (`status=error` + midnight `since_ms`) and "passthrough misses" (`path=fast&status=error`). A toolbar dropdown applies a view by writing its filters into the URL; users can save the current filter set under a name.
- Newer pill: hold `heldNewestId` while the operator has paged (`older.length > 0`); when the 3s poll returns a first page whose newest id sorts past it, show "N newer" (N = `newerCount`) which clears `older`/`cursor` and lets the query refetch.
- Chips: attempts badge reads `failover ×N`; `path` chip renders `fast`/`translated`, nothing when absent.

- [ ] **Step 4: Run** `npm test && npm run typecheck && npm run build` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/requests/ web/src/lib/search-filters.test.ts
git commit -m "feat(web): requests table upgrade with real filters"
```

---

### Task 12: Trace drawer completion

**Files:**
- Modify: `web/src/features/requests/trace-drawer.tsx`
- Test: `web/src/features/requests/trace-drawer.test.tsx`

**Interfaces:** consumes `TraceAttempt[]`, `trace.bodies`, `trace.surface_meta`, `ttft_ms`/`total_ms` on the trace; produces `waterfallRows(trace)` → `{ label, ms, fraction }[]` for TTFT and total; `<BodiesPanel bodies/>`; playground link `/playground?seed={id}`.

- [ ] **Step 1: Write the failing tests**

```tsx
test("waterfall scales ttft against total", () => {
  const rows = waterfallRows({ ttft_ms: 250, total_ms: 1000 })
  expect(rows).toEqual([
    { label: "time to first token", ms: 250, fraction: 0.25 },
    { label: "total", ms: 1000, fraction: 1 },
  ])
})
test("absent ttft renders no waterfall row", () => {
  expect(waterfallRows({ ttft_ms: null, total_ms: 500 })).toHaveLength(1)
})
test("bodies panel explains an empty capture", () => {
  render(<BodiesPanel bodies={[]} />)
  screen.getByText(/not captured/)
  screen.getByText(/capture\.bodies/)
})
test("open in playground links to the seeded run", () => {
  const { getByRole } = render(<TraceDrawerInner id="abc" trace={fixture} onClose={() => {}} />)
  expect(getByRole("link", { name: /open in playground/i }).getAttribute("href"))
    .toContain("/playground?seed=abc")
})
```

Note: per-attempt connect/TTFT timings are not stored on `request_attempts`, so the waterfall is request-level TTFT + total beside the ladder's per-attempt bars — record this as a documented deviation in the drawer's header comment rather than inventing data.

- [ ] **Step 2: Run** `npm test -- trace-drawer` → FAIL.

- [ ] **Step 3: Implement** the three sections: waterfall (two labelled bars scaled to total), BodiesPanel (`bodies === undefined || length === 0` → explanatory copy naming `capture.bodies` and that nothing writes bodies yet; otherwise `<pre>` per entry labelled by `kind`), surface_meta `<dl>` (skip when absent), and the Link. 

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/requests/trace-drawer.tsx web/src/features/requests/trace-drawer.test.tsx
git commit -m "feat(web): trace waterfall, bodies panel and replay link"
```

---

### Task 13: Usage rebuild — time series, range picker, click-through

**Files:**
- Modify: `web/src/features/usage/usage-screen.tsx`
- Test: `web/src/features/usage/usage-screen.test.ts`

**Interfaces:** consumes `useUsage` — extend the hook to `useUsage(dimensionOrOpts?: UsageDimension | { dimension?: UsageDimension; days?: number })`, keeping the existing string-call form (`useUsage("alias")`) working so Overview and the command palette stay untouched; darkraise chart subpath `darkraise-ui/components/chart` wrapping recharts; produces `stackByDay(rows, keys)` → `{ day, [key]: requests }[]`; `rangeToDays(range)` → 7|30|90|365.

- [ ] **Step 1: Write the failing tests**

```ts
test("stackByDay pivots provider keys into series columns", () => {
  const out = stackByDay([
    { day: "2026-08-25", key: "groq", requests: 3 },
    { day: "2026-08-25", key: "nebius", requests: 1 },
    { day: "2026-08-26", key: "groq", requests: 2 },
  ], ["groq", "nebius"])
  expect(out).toEqual([
    { day: "2026-08-25", groq: 3, nebius: 1 },
    { day: "2026-08-26", groq: 2, nebius: 0 },
  ])
})
test("range picker maps to days", () => {
  expect(rangeToDays("7d")).toBe(7); expect(rangeToDays("all")).toBe(365)
})
```

- [ ] **Step 2: Run** `npm test -- usage` → FAIL.

- [ ] **Step 3: Implement.** Range `ToggleGroup` (7d/30d/90d/all) driving `days`; `Chart` from the library subpath rendering two stacked-area charts (requests, tokens) with series = top five keys by volume, plus a cost line chart; keep `.chart-scope` class on the wrapper (the monochrome ramp override stays load-bearing); ranked table rows become links: provider row → `/requests?provider={key}&since_ms={range start}`, model row → `?model={key}`, alias row → `?alias={key}`. Dimension toggle and `summarise` stay.

- [ ] **Step 4: Run** `npm test && npm run typecheck && npm run build` → PASS (recharts resolves through the library's optional peer — it is in package.json).

- [ ] **Step 5: Commit**

```bash
git add web/src/features/usage/
git commit -m "feat(web): usage time series with range and drill-through"
```

---

### Task 14: Catalog view gains provenance (backend)

**Files:**
- Modify: `internal/admin/catalog.go:13-102`
- Test: `internal/admin/catalog_test.go`

**Interfaces (Produces):** `modelView` gains `"pricing":{"input_micros","output_micros"} | null`, `"publisher":"…"`, `"merge_source":"override|discovery|models_dev|inferred"` — taken from the merged `catalog.Model` the loop already walks. First read `catalog.Model`'s exported fields (`internal/catalog/*.go`) and map: pricing from wherever 11a's commit-time costing reads it (it exists — cost is computed from catalog pricing today), publisher from the row/preset merge, merge_source from the precedence marker the phase 5 merge defect fix introduced. Write the mapping in this task; if a field genuinely has no source on the struct, emit `""` and note it in the commit body rather than inventing one.

- [ ] **Step 1: Failing test** — seed a snapshot with a priced groq row and an overridden context window; assert `pricing.input_micros == 150000`, `merge_source == "models_dev"` for the plain row and `"override"` for the overridden one.

- [ ] **Step 2: Run** `go test ./internal/admin/ -run TestModelView -v` → FAIL.

- [ ] **Step 3: Implement** the struct fields + population in `collectModels`.

- [ ] **Step 4: Run** `go test -race -count=1 ./internal/admin/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/catalog.go internal/admin/catalog_test.go
git commit -m "feat(admin): catalog rows carry pricing and provenance"
```

---

### Task 15: Models completion — facets, columns, override editor

**Files:**
- Create: `web/src/features/models/override-editor.tsx`
- Modify: `web/src/features/models/models-screen.tsx`
- Test: `web/src/features/models/models-screen.test.ts`, new `override-editor.test.tsx`

**Interfaces:** DataTable `facets={["surfaces","state","capabilities"]}` (preprocess each row into scalar facet fields `surface_list`, `state`, `caps`); columns Price (`$X.XX/MTok` from `pricing`, em-dash when null), Max output, Publisher, Source (`merge_source`); `OverrideEditor({provider, model})` loads `GET /api/models/{p}/{m}/override`, saves `PUT`, removes `DELETE`; opening a model = expandable row rendering the compressed ladder per provider with an Edit-override button per provider row.

- [ ] **Step 1: Failing tests**

```ts
test("price band facet derives band from micros", () => {
  expect(priceBand({ input_micros: 150000 })).toBe("<$1/MTok")
  expect(priceBand(null)).toBe("unpriced")
})
test("facet scalars flatten list fields", () => {
  expect(facetRow(model)).toMatchObject({ state: "live", caps: "tools" })
})
```

Override editor component test: mock fetch PUT, submit changed context window, assert request path and body.

- [ ] **Step 2: Run** `npm test -- models` → FAIL.

- [ ] **Step 3: Implement** per interfaces; mutation goes through `useApiMutation` with `invalidates: [keys.models]`.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/models/
git commit -m "feat(web): model facets, provenance columns, override editor"
```

---

### Task 16: Routing completion — policy editor, drag-reorder, browser validation

**Files:**
- Create: `web/src/features/routing/policy-editor.tsx`
- Modify: `web/src/features/routing/routing-screen.tsx`
- Test: `web/src/features/routing/routing-screen.test.ts`, `policy-editor.test.tsx`

**Interfaces:** consumes `GET/PUT /api/policy` (shape = `PolicyBlock` in api-types; PUT accepts partial `{cooldown?,retry?,timeout?}` and REFUSES `connect`/`first_byte` with 400 naming the field); produces `validateChain(targets, knownProviders)` → `string[]` of problems (dangling `provider/model` targets); `moveTarget(chain, from, to)` pure DnD reorder.

- [ ] **Step 1: Failing tests**

```ts
test("dangling qualified target is named", () => {
  expect(validateChain(["groq/m-a", "ghost/m-b"], ["groq"]))
    .toEqual(["ghost does not serve via any configured provider: ghost/m-b"])
})
test("bare model targets need no provider", () => {
  expect(validateChain(["m-a"], ["groq"])).toEqual([])
})
test("moveTarget reorders without mutating", () => {
  const src = ["a", "b", "c"]
  expect(moveTarget(src, 0, 2)).toEqual(["b", "c", "a"])
  expect(src).toEqual(["a", "b", "c"])
})
```

Component tests: policy form renders restart-only inputs disabled with badge; saving posts only dirty fields; a 400 message renders inline.

- [ ] **Step 2: Run** `npm test -- routing policy-editor` → FAIL.

- [ ] **Step 3: Implement.** PolicyEditor card under the preview: fields for cooldown trip_after/max, retry.max_attempts, timeout total/idle (editable) and connect/first_byte (rendered disabled with a "restart" badge — the API refuses them, so offering them would lie). Save via `PUT /api/policy` through `useApiMutation` invalidating `[keys.config, keys.policy?]` (add `keys.policy`). Alias chains: HTML5 draggable list items per chain (`draggable`, `onDragStart/onDragOver/onDrop` calling `moveTarget`), browser-side `validateChain` shown inline before save (server validation remains the authority — the client check just makes it immediate). Replace `window.prompt` naming with a small inline input row.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/routing/
git commit -m "feat(web): policy editing and drag-reorderable chains"
```

---

### Task 17: Providers completion — preset browser, credential forms, detail fields

**Files:**
- Create: `web/src/features/providers/add-provider-dialog.tsx`
- Modify: `web/src/features/providers/providers-screen.tsx`, `provider-detail.tsx`
- Test: `web/src/features/providers/providers-screen.test.ts`, new `add-provider-dialog.test.tsx`

**Interfaces:** consumes `usePresets()` (now full-shape), `POST /api/providers` (create body already accepts region/project/location/auth_style/kind/base_url), `POST /api/providers/{id}/keys {label,secret}`, `DELETE .../keys/{keyId}`, `PATCH .../keys/{keyId} {enabled?,secret?}`; produces `filterPresets(presets, {q, surface, authKind, freeTier})` pure fn; dialog with facet `Select`s + search + grid of preset cards (website external link) + "raw form" tab exposing kind/base_url/auth_style/priority/enabled/region/project/location; detail screen gains region/project Inputs (patch), per-credential Replace-secret inline input, Remove button (confirm), Add-credential form, OAuth metadata display (kind badge, expiry date, scope), probe result card (from `POST .../test` response: ok/model_count/latency_ms retained and rendered), discovery panel fed by `useDiscoveryHealth()` (Task 8) showing totals + max_missing_streak with attention styling when streak > 0.

- [ ] **Step 1: Failing tests** (pure functions + dialog interaction):

```ts
test("presets filter by surface auth and tier", () => {
  const out = filterPresets(fixtures, { q: "gr", surface: "llm", authKind: "bearer", freeTier: true })
  expect(out.every(p => p.free_tier && p.auth_kind === "bearer" && p.name.toLowerCase().includes("gr"))).toBe(true)
})
test("create-from-preset omits fields the preset supplies", () => {
  expect(createBodyFromPreset(preset, { id: "mine", secret: "sk" })).toEqual({
    id: "mine", preset: preset.id,
  })
})
```

Dialog test renders with mocked `usePresets`, searches, clicks a card, submits, asserts POST body and navigation to the new detail page.

- [ ] **Step 2: Run** `npm test -- providers add-provider` → FAIL.

- [ ] **Step 3: Implement.** Providers screen header gains "Add provider" button opening the dialog. Detail: add the forms/fields enumerated above; delete-provider button lives here too, rendering `dangling_aliases` from the DELETE response as a warning toast.

- [ ] **Step 4: Run** `npm test && npm run typecheck && npm run build` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/providers/
git commit -m "feat(web): preset browser and full provider management"
```

---

### Task 18: Connect completion

**Files:**
- Create: `web/src/features/connect/snippets.ts`
- Modify: `web/src/features/connect/connect-screen.tsx`
- Test: `web/src/features/connect/connect-screen.test.ts`

**Interfaces:** produces `baseUrlFor(listen, dialect)` (openai → `http://{listen}/v1`, anthropic → `http://{listen}`, gemini → `http://{listen}/v1beta`), `snippetFor(tool, baseUrl)` for claude-code, codex, cursor, openai-sdk, anthropic-sdk (template literals in `snippets.ts`, tested verbatim); live-surfaces strip from `useModels()` union of `surfaces` across rows.

- [ ] **Step 1: Failing tests**

```ts
test("snippet for claude code sets ANTHROPIC_BASE_URL", () => {
  const s = snippetFor("claude-code", "http://127.0.0.1:8080")
  expect(s).toContain('ANTHROPIC_BASE_URL=http://127.0.0.1:8080')
})
test("every tool has a snippet template", () => {
  for (const t of TOOLS) expect(snippetFor(t, "http://x")).not.toBe("")
})
```

- [ ] **Step 2: Run** `npm test -- connect` → FAIL.

- [ ] **Step 3: Implement.** Card 1: three base URLs with copy buttons (`navigator.clipboard.writeText`, toast on success). Card 2: `Tabs` per tool rendering `<pre>{snippet}</pre>` + copy. Card 3: badges of live surfaces from the catalog union. Existing token management stays.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/connect/
git commit -m "feat(web): connect base urls, snippets, live surfaces"
```

---

### Task 19: Settings completion — password, reload, sync

**Files:**
- Modify: `web/src/features/settings/settings-screen.tsx`
- Test: `web/src/features/settings/settings-screen.test.ts`

**Interfaces:** consumes `POST /api/auth/password {current,new}` (min 12 chars, revokes other sessions), `POST /api/config/reload` → `{valid,error?}`, `POST /api/catalog/sync`; file-owned blocks remain read-only rows (source labels already correct per §8.1).

- [ ] **Step 1: Failing tests**

```ts
test("password form refuses short new passwords client-side", () => {
  expect(passwordProblem("short")).toMatch(/12 characters/)
  expect(passwordProblem("long-enough-passphrase")).toBeNull()
})
test("reload reports an invalid file without throwing", () => {
  expect(reloadMessage({ valid: false, error: "yaml: bad" })).toMatch(/previous configuration is still serving/)
})
```

- [ ] **Step 2: Run** `npm test -- settings` → FAIL.

- [ ] **Step 3: Implement.** Account card: current/new/confirm fields → mutation; success toast states how many other sessions were revoked (`{revoked}` from response). Header actions: Reload config (renders `{valid:false}` responses as the destructive banner copy), Sync catalog now. Both through `useApiMutation`.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/settings/
git commit -m "feat(web): settings password change, reload and sync"
```

---

### Task 20: First-run teaching state and empty states

**Files:**
- Modify: `web/src/features/shell/first-run.tsx`
- Modify: `web/src/app.tsx` (route to FirstRun when authenticated but provider count is zero? No — teaching state lives on Overview, not a gate: render `<FirstRunProviders/>` inside OverviewScreen when `providers` query returns empty)
- Create: `web/src/features/shell/first-run-providers.tsx`
- Test: `web/src/features/shell/first-run.test.tsx` (extend)

**Interfaces:** `FirstRunProviders({onAdd})` — legend explaining what will appear here, the three steps (add provider → discover → point a client at Connect), dimmed example flow-graph placeholder, CTA opening Task 17's dialog.

- [ ] **Step 1: Failing test** — asserts the component renders steps 1-3 and the CTA; and that `EmptyLegend({what, hint})` renders both lines (shared little component used by requests/usage/models empty rows).

- [ ] **Step 2: Run** `npm test -- first-run` → FAIL.

- [ ] **Step 3: Implement**, and swap the three plain-text empty lines (requests/usage/models) for `<EmptyLegend>` with per-screen legends.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/shell/ web/src/features/overview/overview-screen.tsx web/src/features/requests/requests-screen.tsx web/src/features/usage/usage-screen.tsx web/src/features/models/models-screen.tsx
git commit -m "feat(web): teaching first-run and first-class empty states"
```

---

### Task 21: Playground rebuild A — chat, params, dialects, trace round-trip

**Files:**
- Create: `web/src/features/playground/chat.tsx`
- Modify: `web/src/features/playground/playground-screen.tsx`
- Test: `web/src/features/playground/playground.test.ts` (extend existing)

**Interfaces:** consumes `stream()` from `lib/api` (SSE reader exists) and `POST /api/playground` v2; produces `buildChatBody(state)` pure fn; URL params `?seed={requestId}&model=&dialect=` — on mount with `seed`, fetch the trace and hydrate messages/system/params from it ("every trace can seed a run"); after each send, the existing trace link stays.

- [ ] **Step 1: Failing tests** (pure functions):

```ts
test("buildChatBody maps transcript to messages and keeps params", () => {
  const b = buildChatBody({
    model: "m", dialect: "openai", temperature: 0.7, maxTokens: 99,
    system: "be brief", transcript: [
      { role: "assistant", content: "hi" },
      { role: "user", content: "go on" },
    ],
  })
  expect(b.messages).toHaveLength(3) // system first
  expect(b.messages[0]).toEqual({ role: "system", content: "be brief" })
  expect(b.temperature).toBe(0.7)
})
test("seed hydration restores roles from a trace", () => {
  const t = seedFromTrace(fixtureTrace)
  expect(t.transcript.at(-1)?.role).toBe("user")
})
```

- [ ] **Step 2: Run** `npm test -- playground` → FAIL.

- [ ] **Step 3: Implement.** Transcript state (role-tagged bubbles), system textarea, temperature/max_tokens numeric inputs, tools textarea accepting a JSON array (parsed on send, error shown inline), stream toggle, dialect Select (openai/anthropic/gemini), Send streaming into the active bubble via the existing SSE drain; `?seed=` hydration effect; keep the request-id → trace link.

- [ ] **Step 4: Run** `npm test && npm run typecheck` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/playground/
git commit -m "feat(web): playground multi-turn chat with dialects"
```

---

### Task 22: Playground rebuild B — compare, aux panels, token counting

**Files:**
- Create: `web/src/features/playground/compare.tsx`, `web/src/features/playground/aux-panels.tsx`
- Modify: `web/src/features/playground/playground-screen.tsx` (Tabs: Chat | Compare | Surfaces | Count)
- Test: `web/src/features/playground/compare.test.ts`, `aux-panels.test.tsx`

**Interfaces:** `runCompare(body, modelA, modelB)` → fires two `/api/playground` streams concurrently, renders two panes side by side with latencies; aux panels per surface — embeddings (input text + dimensions field → vector preview: first 8 floats + dimension from response length), rerank (query + documents), moderations (input), images (prompt + size → returned image/b64 render), speech (text + voice → audio blob player), transcription (file drop → base64 → text result); count panel (dialect Select anthropic/gemini, prompt) rendering `X-Darkrouter-Estimated`-driven marker: "native" vs "local estimate".

- [ ] **Step 1: Failing tests**

```ts
test("compare runs both models and reports each latency", async () => {
  const out = await runCompare(fakeStreamFactory, { body: base, a: "m1", b: "m2" })
  expect(out.map(r => r.model)).toEqual(["m1", "m2"])
  expect(out.every(r => r.ms >= 0)).toBe(true)
})
test("vector preview truncates to eight floats", () => {
  expect(vectorPreview({ data: [{ embedding: [1,2,3,4,5,6,7,8,9,10] }] }).preview)
    .toBe("[1, 2, 3, 4, 5, 6, 7, 8, …]")
})
test("estimated flag flips the marker", () => {
  expect(countMarker(true)).toMatch(/local estimate/)
  expect(countMarker(false)).toMatch(/native/)
})
```

- [ ] **Step 2: Run** `npm test -- compare aux-panels` → FAIL.

- [ ] **Step 3: Implement** the four tabs; every run links its request id to the trace (aux/count handlers inherit ids from the executor's `X-Darkrouter-Request` header — surface it the way chat already does via the `StreamStart` hook; for unary aux calls read the response header).

- [ ] **Step 4: Run** `npm test && npm run typecheck && npm run build` → PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/playground/
git commit -m "feat(web): playground compare, aux surfaces and counts"
```

---

### Task 23: The gate

**Files:**
- Create: `docs/ux/GAP-CLOSURE-DOD.md` (results table mirroring the DoD section, one row per criterion: met / not met + evidence)
- Modify: `docs/PROGRESS.md` (phase status row for this work)

- [ ] **Step 1: Static gates**

```bash
go vet ./... && go build ./...
go test -race -count=1 ./...
cd web && npm test && npm run typecheck && npm run build
```

All green or the failing item is a finding recorded in the DoD doc, never quietly dropped.

- [ ] **Step 2: Live gate.** Start the UAT stack (compose.uat.yml, one real provider, `DARKROUTER_ADMIN_PASSWORD_HASH` set). Walk D1–D18 one at a time in a browser, both modes (theme switcher toggles mode), recording each row of `GAP-CLOSURE-DOD.md` with what was clicked and what was seen. A criterion that cannot be met is a finding to report, not to drop.

- [ ] **Step 3: Cross-check the original criteria.** Re-open `docs/ux/DONE-CRITERIA.md` and confirm none regressed; note criterion 1 (live rendering) as finally exercisable.

- [ ] **Step 4: Commit**

```bash
git add docs/ux/GAP-CLOSURE-DOD.md docs/PROGRESS.md
git commit -m "test(docs): gate console gap closure against dod"
```

---

## Notes for the executor

- **Read the referenced test files before writing tests.** Fixtures and harness helpers (`setUp`, `playgroundHarness`, seeding helpers) already exist in the admin packages; adapt call names, keep assertions.
- **The mockups remain the contract** for anything visual: `docs/ux/mockups/fragments/*.html`. Where this plan simplifies a fragment (e.g., the request-level waterfall), the deviation is stated in code comments and the DoD doc, never silent.
- **Backend tasks 1–7 are independently shippable**; frontend tasks 8–22 depend on their named backend tasks only (each Interfaces block says which). Tasks may proceed in parallel across that boundary once Task 8 lands.
- **Expect UAT findings.** Phase 9's live verification found defects every suite had passed over; treat them as the point of the exercise.
