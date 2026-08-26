# Console Gap Closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every gap between the phase 10 operator-console spec and the console shipped in phase 13, backend enablers included, and prove closure against a live gateway.

**Architecture:** Eight small Go additions to `internal/admin`, each following the handler patterns already there — a pure request-builder function beside the handler that calls it (like `filtersFrom`), view structs with json tags, `requireSession`/`requireCSRF` wrappers. Then frontend work on the existing feature-folder shape: wire types land in `web/src/lib/api-types.ts`, hooks in `web/src/lib/queries.ts` behind the `keys` factory, screens under `web/src/features/<destination>/`. No new dependencies: recharts 3 and darkraise-ui 6.5.0 are both already in `web/package.json`.

**Tech Stack:** Go 1.26 stdlib `http.ServeMux`; SQLite via `internal/store`; React 19, TanStack Router + Query, darkraise-ui 6.5.0 (`darkraise-ui/data-table`, `darkraise-ui/components/chart`), recharts 3, vitest + @testing-library/react.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` — §3.5 (identity mark), §4 (ladder), §5 (IA), §6.1–§6.11 (screens), §8 (admin API), §9 (frontend architecture), §11 (testing), §12 (done criteria). The approved mockups in `docs/ux/mockups/fragments/*.html` are the visual contract, per §10.

## Global Constraints

These apply to every task without restating.

- **TDD.** A failing test precedes the implementation it tests. Run it and see it fail before writing the code.
- **Gates before any commit.** Go tasks: `go build ./... && go vet ./...` clean and `go test -race -count=1 ./internal/...` clean. Frontend tasks: `npm test`, `npm run typecheck` clean in `web/`, plus `npm run build` on the tasks that say so. The full suite runs before the gate commit in Task 23.
- **The ladder is defined once**, in `web/src/features/ladder/ladder.tsx`. Never write ladder markup a second time; embed `<Ladder mode=… rows=… />`.
- **Coral is brand only** — position and primary action, never state. State colour has exactly three carve-outs: a destructive affordance, a request outcome, and attention.
- **Poll intervals come from `POLL` in `web/src/lib/api.ts`** (`fast` 3000 ms, `slow` 30000 ms). Intervals live on the hooks in `queries.ts`, never at a call site. Polling pauses when the tab is hidden; the query client already sets that app-wide.
- **Every filtered view is a URL**, via `useSearchFilters`. Component state holds nothing a reload should survive.
- **Unpriced money renders as `—`, never `$0.00`.** A model with no catalog price cost an unknown amount.
- **No endpoint response may contain credential material.** New views follow the `maskSecret` discipline, and the existing leak tests in `internal/admin/leak_test.go` must stay green.
- **Comments explain WHY, never WHAT.** No comment may reference this plan, a task number, or the fact that something was recently added.
- **Commit subjects** are `<type>(<scope>): <subject>`, imperative, 50 characters or fewer, no trailing period. Stage explicit paths — never `git add -A`. English only, everywhere.
- **Branch.** All work lands on `feat/console-gap-closure`, which already exists and currently points at `master`. Check it out before Task 1.

## Definition of Done (the gate)

Every row is verified in Task 23. "Command" runs from the repository root. "UAT" means against a live gateway started with `docker compose -f compose.uat.yml up`, one real provider configured, exercised through a browser in both light and dark mode.

Each command names test functions this plan actually creates. A `-run` pattern that matches nothing exits `ok … [no tests to run]`, which is a green gate that verified nothing — so every pattern below was chosen to match a real function name written in the task it belongs to.

| # | Criterion | Verification |
|---|---|---|
| D1 | Requests filter on error code, and the row says which path served | `go test ./internal/admin/ -run 'TestFilteringByErrorCode\|TestACursorMintedUnderOneErrorCode\|TestARequestRowNamesTheServingPath'` |
| D2 | Playground sends multi-turn messages, temperature, max tokens and tools, and speaks all three dialects | `go test ./internal/admin/ -run 'TestPlaygroundRequestBuild\|TestThePlaygroundSpeaksAnthropic'`; UAT: one prompt through `openai` and through `anthropic` both return completions |
| D3 | Token count shows native versus estimated | `go test ./internal/admin/ -run 'TestCountRequestBuild\|TestTheCountEndpoint'`; UAT: `X-Darkrouter-Estimated` drives the marker text |
| D4 | The six auxiliary surfaces are runnable from the Playground | `go test ./internal/admin/ -run 'TestAuxRequestBuild\|TestTheAuxEndpoint'`; UAT: embeddings returns a vector preview, transcription accepts a dropped file |
| D5 | Discovery health is visible per provider | `go test ./internal/admin/ -run 'TestDiscoveryHealthRollsUp\|TestDiscoveryHealth'`; UAT: a provider with `missing_streak > 0` shows a degraded discovery line |
| D6 | OAuth credential detail — kind, expiry, scope — is shown | `go test ./internal/admin/ -run 'TestACredentialViewCarriesOAuth\|TestLeak'`; UAT: an oauth credential row shows its expiry date |
| D7 | Catalog rows carry pricing, publisher and provenance | `go test ./internal/admin/ -run 'TestAModelViewCarriesPricing\|TestAModelViewNamesItsSource'` |
| D8 | Media inlining has a config switch | `go test ./internal/config/ ./internal/adapter/gemini/ -run 'TestMediaInline\|TestADisabledFetcher'`; UAT: switch off ⇒ a Gemini image-URL request warns and drops the block |
| D9 | Preset browser adds a provider and a credential without touching a file | `cd web && npm test -- add-provider`; UAT full flow: browse → filter by surface → create → add credential → probe ok |
| D10 | Model overrides are editable; facets and provenance columns render | `cd web && npm test -- models override-editor`; UAT: override written, catalog reflects it, columns show price, publisher and source |
| D11 | Policy is editable with hot versus restart marked; alias chains drag-reorder with browser validation | `cd web && npm test -- routing policy-editor`; UAT: changing `total` takes effect after save, `connect` is refused with an explanation |
| D12 | Requests screen: DataTable sorting, column visibility and CSV, real filter controls, time range, saved views, newer pill | `cd web && npm test -- requests saved-views search-filters`; UAT each interaction once |
| D13 | Overview: config banner, sparklines, recent-failovers strip, ops footer | `cd web && npm test -- overview ops-footer failovers`; UAT: footer shows version, uptime and the dropped-record counter |
| D14 | Trace: waterfall, a Bodies panel that explains itself, surface metadata, Open-in-playground round-trips | `cd web && npm test -- trace-drawer`; UAT: playground run → trace → seed back into playground |
| D15 | Usage: time series stacked by provider, range picker, cost line, row click-through | `cd web && npm test -- usage`; UAT: clicking a provider row lands in Requests already filtered |
| D16 | Connect: copyable base URLs, client snippets, live surfaces | `cd web && npm test -- connect snippets`; UAT: the copy button writes the clipboard, and the snippet matches the served routes |
| D17 | Settings: password change, reload and sync work; file-owned blocks stay read-only with source labels | `cd web && npm test -- settings`; UAT: a password change revokes other sessions, an invalid YAML edit makes reload report invalid |
| D18 | Login shows the identity mark; a fresh install with zero providers teaches rather than showing empty grids | `cd web && npm test -- identity-mark first-run`; UAT: a brand-new data directory explains itself, and empty screens carry legends |
| D18a | The ladder's four states are distinguishable with colour stripped | `cd web && npm test -- ladder`; UAT: view a failover trace with the browser forced to greyscale |
| D19 | All of §12's phase-10 criteria still hold | Re-walk the table in `docs/ux/DONE-CRITERIA.md` |
| D20 | Suites green | `go test -race -count=1 ./... && cd web && npm test && npm run typecheck && npm run build` |

## File Structure

New files:

```
internal/admin/discoveryapi.go                       discovery-health rollup handler
internal/admin/discoveryapi_test.go
internal/admin/playgroundaux.go                      count + auxiliary-surface runners
web/src/features/shell/identity-mark.tsx             §3.5 mark, geometry from fragment 15
web/src/features/shell/identity-mark.test.tsx
web/src/features/shell/first-run-providers.tsx       zero-providers teaching state
web/src/features/shell/empty-legend.tsx              shared empty-well legend
web/src/features/overview/ops-footer.tsx             version / uptime / dropped counter
web/src/features/overview/failovers.tsx              recent-failovers strip
web/src/features/requests/saved-views.ts             localStorage saved filter sets
web/src/features/requests/saved-views.test.ts
web/src/features/requests/requests-table.test.tsx
web/src/features/models/override-editor.tsx
web/src/features/models/override-editor.test.tsx
web/src/features/routing/policy-editor.tsx
web/src/features/routing/policy-editor.test.tsx
web/src/features/providers/add-provider-dialog.tsx   preset browser plus a raw form
web/src/features/providers/add-provider-dialog.test.tsx
web/src/features/connect/snippets.ts                 client config snippet templates
web/src/features/connect/snippets.test.ts
web/src/features/playground/chat.tsx                 multi-turn chat surface
web/src/features/playground/chat.test.ts
web/src/features/playground/compare.tsx              side-by-side runner
web/src/features/playground/aux-panels.tsx           the six auxiliary surfaces
web/src/features/playground/aux-panels.test.ts
docs/ux/GAP-CLOSURE-DOD.md                           the gate's results table
```

Each new frontend file owns one responsibility, because a screen file that also owns a dialog, a chart and a form is the 484-line grab bag §9 exists to prevent.

Modified files are named per task.

## What the executor must read first

Three files carry conventions this plan depends on and does not restate:

- `internal/admin/fixtures_test.go` — the Go test harness. The helpers are `testServerFull(t)`, `testServerFullWithAliases(t, aliases)`, `testServerWithCatalog(t, aliases)`, `testServerWithExecutor(t, upstreamURL, model)`, `do(t, s, cookie, token, method, path, body)`, `seedProviderWithKey(...)`, and `login(t, s)` from `auth_test.go`. **No other harness exists.** There is no `setUp`, no `fx`, and no playground capture harness — which is why every backend task below tests a pure builder function directly and drives the handler through `do`.
- `web/src/lib/api-types.ts` — one module mirroring the Go json tags by hand.
- `docs/ux/mockups/fragments/` — the visual contract. Where this plan simplifies a fragment, it says so in the task, and the deviation is recorded in the DoD document rather than left silent.

---

### Task 1: Requests filter on error code, and rows name the serving path

Spec §6.2 wants an error-code combobox and a passthrough-versus-translated chip. Neither has server support: `GET /api/requests` cannot filter on `error_code`, and `requestView` never says which path served. Both land in one task because they touch the same four files and the same cursor-hash discipline — the hash must include the new filter or a cursor minted under one error code would page through another.

**Files:**
- Modify: `internal/admin/cursor.go` (the `RequestFilters` struct at line 18 and `Hash` at line 32)
- Modify: `internal/admin/requests.go` (`filtersFrom`, `requestView`, `handleListRequests`)
- Modify: `internal/store/adminstore.go` (`RequestQuery`, `RequestSummary`, `ListRequests`)
- Test: `internal/admin/requests_test.go`

**Interfaces:**
- Consumes: `RequestFilters.Hash()`, `encodeCursor`, `decodeCursor`, `store.ListRequests(ctx, RequestQuery)`, the `seedLog(t, db, n)` helper at the top of `requests_test.go`.
- Produces: query parameter `error_code` on `GET /api/requests` (exact match); `RequestQuery.ErrorCode string` and `RequestFilters.ErrorCode string`, both hashed and filtered immediately after `Surface`; `store.RequestSummary.Path string`; `requestView.Path string` with json tag `path,omitempty` carrying `"passthrough"`, `"ir"` or empty.

- [ ] **Step 1: Write the failing tests**

Append to `internal/admin/requests_test.go`. `seedLog` writes uniform rows, so these seed their own with `db.WriteBatchForTest` directly, which is what `seedLog` itself does.

```go
func seedRow(t *testing.T, db *store.DB, id, errorCode, path string) {
	t.Helper()
	rec := &store.RequestRecord{
		ID: id, TS: time.UnixMilli(1700000000000), Dialect: "openai",
		Surface: "llm", RequestedModel: "m", FinalProviderID: "groq",
		FinalModel: "m", Status: "success", ErrorCode: errorCode,
	}
	if path != "" {
		rec.Attempts = []store.AttemptRecord{{
			Seq: 0, ProviderID: "groq", Model: "m", Outcome: "success", Path: path,
		}}
	}
	db.WriteBatchForTest(t, []*store.RequestRecord{rec})
}

func TestFilteringByErrorCodeReturnsOnlyMatchingRows(t *testing.T) {
	s, db := testServerFull(t)
	seedRow(t, db, "01REQA", "", "")
	seedRow(t, db, "01REQB", "rate_limit", "")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?error_code=rate_limit", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var page struct {
		Requests []struct {
			ID        string `json:"id"`
			ErrorCode string `json:"error_code"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Requests) != 1 || page.Requests[0].ID != "01REQB" {
		t.Fatalf("want only 01REQB, got %+v", page.Requests)
	}
}

func TestACursorMintedUnderOneErrorCodeIsRejectedUnderAnother(t *testing.T) {
	// The hash is a mismatch detector. A cursor that survived a filter change
	// would page through rows the operator is not looking at, which reads as
	// the table showing the wrong data rather than as a rejected request.
	s, db := testServerFull(t)
	seedRow(t, db, "01REQA", "rate_limit", "")
	seedRow(t, db, "01REQB", "rate_limit", "")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?error_code=rate_limit&limit=1", "")
	var first struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if first.NextCursor == "" {
		t.Fatal("no cursor on a page that has more rows")
	}
	w = do(t, s, cookie, token, "GET",
		"/api/requests?error_code=timeout&cursor="+first.NextCursor, "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestARequestRowNamesTheServingPath(t *testing.T) {
	// §6.2's passthrough-versus-translated chip. The row has no other source
	// for it: the trace carries a path per attempt, and the list does not.
	s, db := testServerFull(t)
	seedRow(t, db, "01REQA", "", "passthrough")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests", "")
	var page struct {
		Requests []struct {
			Path string `json:"path"`
		} `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Requests) != 1 || page.Requests[0].Path != "passthrough" {
		t.Fatalf("path missing from the row view: %+v", page.Requests)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'TestFilteringByErrorCode|TestACursorMintedUnderOneErrorCode|TestARequestRowNamesTheServingPath' -v`

Expected: FAIL. The filter test returns both rows because `error_code` is ignored; the cursor test returns 200 because the hash does not include the new field; the path test finds an empty string.

- [ ] **Step 3: Implement**

In `internal/admin/cursor.go`, add the field after `Surface` and extend the hash in the same position:

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

```go
	for _, s := range []string{f.Provider, f.Model, f.Status, f.Alias, f.Surface, f.ErrorCode} {
```

In `internal/store/adminstore.go`, add `ErrorCode string` to `RequestQuery` after `Surface`, add `Path string` to `RequestSummary` after `Attempts`, and extend the filter sequence in `ListRequests` in the matching position:

```go
		{"r.surface", q.Surface},
		{"r.error_code", q.ErrorCode},
```

The path comes from the last attempt, because that is the one that served. Add a correlated subquery as the final SELECT column, after the attempt count:

```go
		        (SELECT count(*) FROM request_attempts a WHERE a.request_id = r.id),
		        coalesce((SELECT a.path FROM request_attempts a
		                   WHERE a.request_id = r.id
		                   ORDER BY a.seq DESC LIMIT 1), '')
```

and scan it last: `&s.ErrorCode, &s.Attempts, &s.Path`.

In `internal/admin/requests.go`, add `ErrorCode: q.Get("error_code")` to `filtersFrom`, pass `ErrorCode: f.ErrorCode` into the `store.RequestQuery`, add the view field, and populate it:

```go
	ErrorCode       string `json:"error_code,omitempty"`
	Attempts        int    `json:"attempts"`
	// Which rendering served: "passthrough" when the fast path carried the
	// body through untouched, "ir" when it was translated. Empty when nothing
	// served at all.
	Path string `json:"path,omitempty"`
```

```go
			ErrorCode: row.ErrorCode, Attempts: row.Attempts, Path: row.Path,
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/store/ ./internal/admin/`

Expected: PASS, including the pre-existing cursor tests. Those compare hashes computed by `Hash()` rather than golden literals, so adding a field does not break them.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/cursor.go internal/admin/requests.go internal/admin/requests_test.go internal/store/adminstore.go
git commit -m "feat(admin): filter requests by error code, expose path"
```

---


### Task 2: Playground speaks three dialects with full chat parameters

`handlePlayground` hardcodes the OpenAI dialect and a system-plus-user pair. Spec §6.8 needs multi-turn chat, sampling knobs, tools, and all three inbound dialects testable from the console.

Two facts about the edge packages shape this task, and both were verified by reading them rather than assumed:

- **Each dialect parses its own wire, not a shared one.** `internal/edge/anthropic/parse.go` reads `{model, system, messages, max_tokens}`; `internal/edge/gemini/parse.go` reads `{contents:[{role,parts:[{text}]}], systemInstruction, generationConfig}`. Handing an OpenAI `messages` array to the Gemini edge parses as an empty conversation, not as an error. So the builder renders three different bodies.
- **The Gemini edge reads its model from `r.PathValue("model")`**, which the proxy mux populates from the pattern `POST /v1beta/models/{model}`. A request built with `http.NewRequestWithContext` has no path values, so the synthesized request must call `SetPathValue` or the model is silently lost.

The request synthesis therefore moves into a pure function, `playgroundRequest`, testable for the body and path it builds without a mock executor. There is no capture harness in this package, and inventing one would mean making `Deps.Exec` an interface — a large change to prove a small thing. A pure builder plus one end-to-end test through the real executor covers both halves.

**Files:**
- Modify: `internal/admin/playground.go`
- Test: `internal/admin/playground_test.go`

**Interfaces:**
- Consumes: `exec.Executor.Handle(w, r, d edge.Dialect)`; `openaiedge.New()`; `anthropicedge.New()` from `internal/edge/anthropic`; `geminiedge.NewFor(r)` from `internal/edge/gemini`, which reads `?alt=sse` to choose the streaming wire form; `geminiedge.ExtractModel`, whose inverse this builds.
- Produces: `POST /api/playground` accepting `{model, prompt?, system?, messages?, temperature?, max_tokens?, tools?, stream?, dialect?}` where `dialect ∈ {"openai","anthropic","gemini"}` and defaults to `openai`; and the package-private builder

```go
func playgroundRequest(ctx context.Context, body playgroundBody) (*http.Request, edge.Dialect, error)
```

Tools are accepted on the OpenAI and Anthropic dialects and **refused with a 400 on Gemini**, whose `functionDeclarations` shape is a different structure the console's OpenAI-style tools box does not describe. Refusing is the house rule: the config endpoint already refuses writes it cannot apply rather than accepting one that silently does nothing.

- [ ] **Step 1: Write the failing tests**

Append to `internal/admin/playground_test.go`.

```go
func decodeBuilt(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("built body is not JSON: %v\n%s", err, raw)
	}
	return out
}

func TestPlaygroundRequestBuildsAMultiTurnAnthropicCall(t *testing.T) {
	r, d, err := playgroundRequest(context.Background(), playgroundBody{
		Model: "claude-sonnet-4-6", Dialect: "anthropic", System: "be terse",
		Messages: []playgroundMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
			{Role: "user", Content: "continue"},
		},
		Temperature: ptrOf(0.5),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1/messages" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if d.Name() != "anthropic" {
		t.Fatalf("dialect = %q", d.Name())
	}
	body := decodeBuilt(t, r)
	// Anthropic carries the system prompt outside the message list. Left in
	// place as a system-role turn, the upstream refuses the call.
	if body["system"] != "be terse" {
		t.Fatalf("system not lifted out: %v", body)
	}
	if got := body["messages"].([]any); len(got) != 3 {
		t.Fatalf("messages = %v", got)
	}
	// max_tokens is required on this wire, so the builder supplies one rather
	// than passing an upstream 400 back as a mystery.
	if body["max_tokens"] == nil {
		t.Fatalf("no max_tokens default: %v", body)
	}
	if body["temperature"] != 0.5 {
		t.Fatalf("temperature = %v", body["temperature"])
	}
}

func TestPlaygroundRequestBuildsGeminiShapeAndPathValue(t *testing.T) {
	r, _, err := playgroundRequest(context.Background(), playgroundBody{
		Model: "gemini-2.5-pro", Dialect: "gemini", System: "be terse",
		Messages: []playgroundMessage{
			{Role: "user", Content: "hi"},
			{Role: "assistant", Content: "hello"},
		},
		MaxTokens: ptrOf(64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1beta/models/gemini-2.5-pro:streamGenerateContent" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if r.URL.Query().Get("alt") != "sse" {
		t.Fatalf("streaming Gemini needs alt=sse, got %q", r.URL.RawQuery)
	}
	// The Gemini edge reads the model from the mux path value, which a
	// synthesized request only has because the builder set it.
	if got := r.PathValue("model"); got != "gemini-2.5-pro:streamGenerateContent" {
		t.Fatalf("path value = %q; the edge will parse an empty model", got)
	}
	body := decodeBuilt(t, r)
	contents, ok := body["contents"].([]any)
	if !ok || len(contents) != 2 {
		t.Fatalf("gemini takes contents, not messages: %v", body)
	}
	first := contents[0].(map[string]any)
	second := contents[1].(map[string]any)
	if first["role"] != "user" || second["role"] != "model" {
		t.Fatalf("assistant must render as the model role: %v", contents)
	}
	if body["systemInstruction"] == nil {
		t.Fatalf("systemInstruction missing: %v", body)
	}
	gen := body["generationConfig"].(map[string]any)
	if gen["maxOutputTokens"] != float64(64) {
		t.Fatalf("generationConfig = %v", gen)
	}
}

func TestPlaygroundRequestCarriesToolsAndMaxTokens(t *testing.T) {
	r, _, err := playgroundRequest(context.Background(), playgroundBody{
		Model: "m", Prompt: "hi", MaxTokens: ptrOf(128),
		Tools: []map[string]any{{"type": "function"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := decodeBuilt(t, r)
	if body["max_tokens"] != float64(128) {
		t.Fatalf("max_tokens = %v", body["max_tokens"])
	}
	if len(body["tools"].([]any)) != 1 {
		t.Fatalf("tools = %v", body["tools"])
	}
}

func TestPlaygroundRequestRefusesWhatItCannotTranslate(t *testing.T) {
	for name, body := range map[string]playgroundBody{
		"unknown dialect": {Model: "m", Prompt: "p", Dialect: "nope"},
		"no model":        {Prompt: "p"},
		"nothing to say":  {Model: "m"},
		// Gemini declares tools as functionDeclarations, which is not the
		// shape the console's tools box describes. Dropping them silently
		// would make a tool-using prompt answer as if no tools existed.
		"gemini tools": {Model: "m", Prompt: "p", Dialect: "gemini",
			Tools: []map[string]any{{"type": "function"}}},
	} {
		if _, _, err := playgroundRequest(context.Background(), body); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestThePlaygroundSpeaksAnthropicEndToEnd(t *testing.T) {
	// The whole path rather than the builder: the anthropic edge parses what
	// the builder wrote, and the openaicompat adapter renders it upstream.
	var seen map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground",
		`{"model":"m","dialect":"anthropic","stream":false,
		  "messages":[{"role":"user","content":"hi"},
		              {"role":"assistant","content":"ok"},
		              {"role":"user","content":"go on"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	msgs, _ := seen["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("the turns did not survive the translation: %v", seen)
	}
}
```

Add the generic pointer helper at the bottom of the file:

```go
func ptrOf[T any](v T) *T { return &v }
```

The test file gains `context`, `encoding/json` and `io` imports.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'TestPlaygroundRequest|TestThePlaygroundSpeaksAnthropic' -v`

Expected: compile FAIL — `playgroundRequest` and `playgroundMessage` are undefined, and `playgroundBody` has no `Messages`, `Temperature`, `MaxTokens`, `Tools` or `Dialect` field.

- [ ] **Step 3: Implement**

Replace the body struct in `internal/admin/playground.go`. Messages are typed rather than `map[string]any`, because the Gemini renderer has to read the role and the text and a map would make that three assertions per turn:

```go
type playgroundMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type playgroundBody struct {
	Model       string              `json:"model"`
	Prompt      string              `json:"prompt,omitempty"`
	System      string              `json:"system,omitempty"`
	Messages    []playgroundMessage `json:"messages,omitempty"`
	Temperature *float64            `json:"temperature,omitempty"`
	MaxTokens   *int                `json:"max_tokens,omitempty"`
	Tools       []map[string]any    `json:"tools,omitempty"`
	Stream      *bool               `json:"stream,omitempty"`
	Dialect     string              `json:"dialect,omitempty"`
}

// turns is the conversation the caller described, however they described it.
// A bare prompt is one user turn, which is what the old two-field body was.
func (b playgroundBody) turns() []playgroundMessage {
	if len(b.Messages) > 0 {
		return b.Messages
	}
	return []playgroundMessage{{Role: "user", Content: b.Prompt}}
}
```

Then the builder. Each dialect renders its own wire, because each edge parses only its own:

```go
// playgroundRequest synthesizes the proxy request the executor will serve.
//
// Separate from the handler so the shape it builds is assertable without a
// fake executor: the handler's job after this is one call, and every
// interesting decision — which wire, which path, where the system prompt goes
// — is made here.
func playgroundRequest(ctx context.Context, body playgroundBody) (*http.Request, edge.Dialect, error) {
	if body.Model == "" {
		return nil, nil, errors.New("model is required")
	}
	if len(body.Messages) == 0 && body.Prompt == "" {
		return nil, nil, errors.New("prompt is required when messages is empty")
	}
	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}

	switch body.Dialect {
	case "", "openai":
		return buildJSONRequest(ctx, "/v1/chat/completions",
			openaiPlaygroundBody(body, stream), openaiedge.New())
	case "anthropic":
		return buildJSONRequest(ctx, "/v1/messages",
			anthropicPlaygroundBody(body, stream), anthropicedge.New())
	case "gemini":
		if len(body.Tools) > 0 {
			return nil, nil, errors.New(
				"gemini declares tools as functionDeclarations; " +
					"send tools through the openai or anthropic dialect")
		}
		method := "generateContent"
		suffix := ""
		if stream {
			method, suffix = "streamGenerateContent", "?alt=sse"
		}
		segment := body.Model + ":" + method
		r, _, err := buildJSONRequest(ctx,
			"/v1beta/models/"+url.PathEscape(body.Model)+":"+method+suffix,
			geminiPlaygroundBody(body), nil)
		if err != nil {
			return nil, nil, err
		}
		// The Gemini edge reads the model out of the mux path value. A
		// synthesized request has none, and without this the edge parses an
		// empty model and the router is asked to route nothing.
		r.SetPathValue("model", segment)
		return r, geminiedge.NewFor(r), nil
	default:
		return nil, nil, errors.New("dialect must be openai, anthropic or gemini")
	}
}

func buildJSONRequest(ctx context.Context, path string, payload map[string]any,
	d edge.Dialect) (*http.Request, edge.Dialect, error) {

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	return r, d, nil
}

func openaiPlaygroundBody(b playgroundBody, stream bool) map[string]any {
	msgs := []map[string]any{}
	if b.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": b.System})
	}
	for _, m := range b.turns() {
		msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
	}
	out := map[string]any{"model": b.Model, "messages": msgs, "stream": stream}
	if b.Temperature != nil {
		out["temperature"] = *b.Temperature
	}
	if b.MaxTokens != nil {
		out["max_tokens"] = *b.MaxTokens
	}
	if len(b.Tools) > 0 {
		out["tools"] = b.Tools
	}
	return out
}

func anthropicPlaygroundBody(b playgroundBody, stream bool) map[string]any {
	msgs := []map[string]any{}
	for _, m := range b.turns() {
		if m.Role == "system" {
			continue
		}
		msgs = append(msgs, map[string]any{"role": m.Role, "content": m.Content})
	}
	out := map[string]any{"model": b.Model, "messages": msgs, "stream": stream}
	if b.System != "" {
		out["system"] = b.System
	}
	// Required on this wire. A request without one is refused upstream, which
	// reads to the operator as a broken provider rather than a missing field.
	out["max_tokens"] = 1024
	if b.MaxTokens != nil {
		out["max_tokens"] = *b.MaxTokens
	}
	if b.Temperature != nil {
		out["temperature"] = *b.Temperature
	}
	if len(b.Tools) > 0 {
		out["tools"] = b.Tools
	}
	return out
}

func geminiPlaygroundBody(b playgroundBody) map[string]any {
	contents := []map[string]any{}
	for _, m := range b.turns() {
		role := "user"
		if m.Role == "assistant" || m.Role == "model" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]any{{"text": m.Content}},
		})
	}
	out := map[string]any{"contents": contents}
	if b.System != "" {
		out["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": b.System}},
		}
	}
	gen := map[string]any{}
	if b.Temperature != nil {
		gen["temperature"] = *b.Temperature
	}
	if b.MaxTokens != nil {
		gen["maxOutputTokens"] = *b.MaxTokens
	}
	if len(gen) > 0 {
		out["generationConfig"] = gen
	}
	return out
}
```

Note the two lifts: for Anthropic a system-role turn is dropped from the list and `System` carries it, and for Gemini the assistant role becomes `model`. Both are the same fact — the roles a wire recognises are not universal — and both are what the tests pin.

The handler shrinks to decode, build, hand over:

```go
func (s *Server) handlePlayground(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body playgroundBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pr, d, err := playgroundRequest(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// exec.Handle writes X-Darkrouter-Request before anything else, which is
	// what gives the SPA the id for the trace link before the stream starts.
	s.deps.Exec.Handle(w, pr, d)
}
```

Imports gain `context`, `errors`, `net/url`, `github.com/darkraise/darkrouter/internal/edge`, and the two edge packages aliased `anthropicedge` and `geminiedge`, matching how `internal/server/server.go` names them.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`

Expected: PASS, including the five existing playground tests. The default dialect and the prompt-only body are unchanged, so `TestThePlaygroundRejectsAnEmptyPrompt` still gets 400 for both of its bodies.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/playground.go internal/admin/playground_test.go
git commit -m "feat(admin): playground multi-turn, params and dialects"
```

---

### Task 3: A token-counting endpoint the console can reach

Both native counting endpoints live on the proxy port, which deliberately refuses cookies. The console gets an admin wrapper over `Executor.HandleCount`, whose signature is `HandleCount(w, r, d edge.CountWriter, nativeKind string)`. Only the Anthropic and Gemini dialects implement `edge.CountWriter` — `internal/edge/anthropic/write.go:148` and `internal/edge/gemini/write.go:141` assert it — because the OpenAI wire has no counting endpoint at all.

**Files:**
- Create: `internal/admin/playgroundaux.go` (this task adds the counting half; Task 4 appends the auxiliary surfaces to the same file)
- Modify: `internal/admin/admin.go` (routes)
- Test: `internal/admin/playground_test.go`

**Interfaces:**
- Consumes: `exec.Executor.HandleCount`; `anthropicedge.New()` and `geminiedge.New()`, both of which satisfy `edge.CountWriter` directly with no type assertion.
- Produces: `POST /api/playground/count` with body `{dialect:"anthropic"|"gemini", model, prompt}`; the response is the dialect's native count shape, and the header `X-Darkrouter-Estimated: true` is present when the count was estimated locally rather than read from an upstream. Plus the package-private builder

```go
func countRequest(ctx context.Context, body countBody) (*http.Request, edge.CountWriter, string, error)
```

returning the request, the dialect, and the `nativeKind` string `HandleCount` needs.

- [ ] **Step 1: Write the failing tests**

Append to `internal/admin/playground_test.go`:

```go
func TestCountRequestBuildsTheNativeCountingCall(t *testing.T) {
	r, d, kind, err := countRequest(context.Background(), countBody{
		Dialect: "anthropic", Model: "claude-sonnet-4-6", Prompt: "hello world",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1/messages/count_tokens" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if kind != "anthropic" || d.Name() != "anthropic" {
		t.Fatalf("kind = %q, dialect = %q", kind, d.Name())
	}
	body := decodeBuilt(t, r)
	if len(body["messages"].([]any)) != 1 {
		t.Fatalf("messages = %v", body["messages"])
	}
	// A counting body is not a completion body. max_tokens and stream on this
	// endpoint are rejected upstream.
	if body["max_tokens"] != nil || body["stream"] != nil {
		t.Fatalf("counting body carries completion fields: %v", body)
	}
}

func TestCountRequestBuildsGeminiCountTokens(t *testing.T) {
	r, _, kind, err := countRequest(context.Background(), countBody{
		Dialect: "gemini", Model: "gemini-2.5-pro", Prompt: "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1beta/models/gemini-2.5-pro:countTokens" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	if kind != "gemini" {
		t.Fatalf("kind = %q", kind)
	}
	if got := r.PathValue("model"); got != "gemini-2.5-pro:countTokens" {
		t.Fatalf("path value = %q", got)
	}
	if _, ok := decodeBuilt(t, r)["contents"]; !ok {
		t.Fatal("gemini counting takes contents")
	}
}

func TestCountRequestRefusesTheOpenAIDialect(t *testing.T) {
	// There is no OpenAI counting endpoint, so offering the option would mean
	// silently answering a different question — a local estimate presented as
	// a native count.
	if _, _, _, err := countRequest(context.Background(), countBody{
		Dialect: "openai", Model: "m", Prompt: "p",
	}); err == nil {
		t.Fatal("openai must be refused")
	}
}

func TestTheCountEndpointRequiresACSRFToken(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, _ := login(t, s)
	r := httptest.NewRequest("POST", "/api/playground/count",
		strings.NewReader(`{"dialect":"anthropic","model":"m","prompt":"p"}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
}

func TestTheCountEndpointRejectsABadDialect(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground/count",
		`{"dialect":"openai","model":"m","prompt":"p"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'TestCountRequest|TestTheCountEndpoint' -v`

Expected: compile FAIL — `countRequest` and `countBody` are undefined. Once they compile, the endpoint tests fail with 404 until the route is registered.

- [ ] **Step 3: Implement**

Create `internal/admin/playgroundaux.go`:

```go
package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"

	"github.com/darkraise/darkrouter/internal/edge"
	anthropicedge "github.com/darkraise/darkrouter/internal/edge/anthropic"
	geminiedge "github.com/darkraise/darkrouter/internal/edge/gemini"
)

// countBody selects the counting dialect. There is no "openai": that wire has
// no counting endpoint, so an OpenAI-dialect count could only ever be the
// local estimate, and offering it would present an estimate as a reading.
type countBody struct {
	Dialect string `json:"dialect"`
	Model   string `json:"model"`
	Prompt  string `json:"prompt"`
}

func countRequest(ctx context.Context, body countBody) (*http.Request, edge.CountWriter, string, error) {
	if body.Model == "" || body.Prompt == "" {
		return nil, nil, "", errors.New("model and prompt are required")
	}
	var (
		d       edge.CountWriter
		path    string
		segment string
		payload map[string]any
	)
	switch body.Dialect {
	case "anthropic":
		d = anthropicedge.New()
		path = "/v1/messages/count_tokens"
		payload = map[string]any{
			"model":    body.Model,
			"messages": []map[string]any{{"role": "user", "content": body.Prompt}},
		}
	case "gemini":
		d = geminiedge.New()
		segment = body.Model + ":countTokens"
		path = "/v1beta/models/" + url.PathEscape(body.Model) + ":countTokens"
		payload = map[string]any{
			"contents": []map[string]any{
				{"role": "user", "parts": []map[string]any{{"text": body.Prompt}}},
			},
		}
	default:
		return nil, nil, "", errors.New("dialect must be anthropic or gemini")
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, "", err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, "", err
	}
	r.Header.Set("Content-Type", "application/json")
	if segment != "" {
		r.SetPathValue("model", segment)
	}
	return r, d, body.Dialect, nil
}

func (s *Server) handlePlaygroundCount(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body countBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	pr, d, native, err := countRequest(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.deps.Exec.HandleCount(w, pr, d, native)
}
```

Register the route in `routes()` in `internal/admin/admin.go`, beside the existing playground line:

```go
	s.mux.HandleFunc("POST /api/playground/count", s.requireCSRF(s.handlePlaygroundCount))
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/playgroundaux.go internal/admin/admin.go internal/admin/playground_test.go
git commit -m "feat(admin): token-count wrapper for the console"
```

---

### Task 4: An auxiliary-surface runner

Spec §6.8 lists six proxy surfaces the console cannot reach: embeddings, rerank, moderations, images, speech and transcriptions. One parameterized admin runner synthesizes the proxy request and lets the executor answer, so failover, the budget gate and the request log all stay real.

**These six do not go through `exec.Handle`.** `internal/server/server.go:218-234` dispatches each to its own executor method — `HandleEmbeddings`, `HandleModerations`, `HandleRerank`, `HandleImages`, `HandleTranscriptions`, `HandleSpeech` — because each takes a different narrow dialect interface. Routing all six through `Handle` would parse an embeddings body as a chat completion. The runner therefore switches on the surface and calls the matching method, exactly as the proxy mux does.

**Files:**
- Modify: `internal/admin/playgroundaux.go`
- Modify: `internal/admin/admin.go` (routes)
- Test: `internal/admin/playground_test.go`

**Interfaces:**
- Consumes: the six executor methods above, all taking `openaiedge.New()` — `internal/edge/openai/aux.go` asserts that one `*Dialect` satisfies `EmbeddingDialect`, `ModerationDialect`, `RerankDialect`, `ImageDialect` and `SpeechDialect`, and `HandleTranscriptions` takes the plain `edge.Dialect`.
- Produces: `POST /api/playground/aux` with body `{surface, model?, body?, file_b64?, filename?}` where `surface ∈ {"embeddings","rerank","moderations","images","speech","transcriptions"}`. The response is whatever the executor wrote — JSON for five of them, audio bytes for speech. Plus the package-private builder

```go
func auxRequest(ctx context.Context, in auxBody) (*http.Request, error)
```

- [ ] **Step 1: Write the failing tests**

Append to `internal/admin/playground_test.go`:

```go
func TestAuxRequestBuildsTheSurfaceRoute(t *testing.T) {
	for surface, path := range map[string]string{
		"embeddings":  "/v1/embeddings",
		"rerank":      "/v1/rerank",
		"moderations": "/v1/moderations",
		"images":      "/v1/images/generations",
		"speech":      "/v1/audio/speech",
	} {
		r, err := auxRequest(context.Background(), auxBody{
			Surface: surface, Model: "mdl",
			Body: json.RawMessage(`{"input":"hello"}`),
		})
		if err != nil {
			t.Fatalf("%s: %v", surface, err)
		}
		if r.URL.Path != path {
			t.Errorf("%s: path = %q, want %q", surface, r.URL.Path, path)
		}
		body := decodeBuilt(t, r)
		// The form owns the model field, so a model typed into the panel wins
		// over one left in the raw body from a previous run.
		if body["model"] != "mdl" || body["input"] != "hello" {
			t.Errorf("%s: merged body = %v", surface, body)
		}
	}
}

func TestAuxRequestBuildsAMultipartTranscription(t *testing.T) {
	r, err := auxRequest(context.Background(), auxBody{
		Surface: "transcriptions", Model: "whisper-1",
		FileB64: base64.StdEncoding.EncodeToString([]byte("RIFFfake")),
		Filename: "clip.wav",
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL.Path != "/v1/audio/transcriptions" {
		t.Fatalf("path = %q", r.URL.Path)
	}
	ct := r.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "multipart/form-data") {
		t.Fatalf("content-type = %q", ct)
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	if got := r.FormValue("model"); got != "whisper-1" {
		t.Fatalf("model part = %q", got)
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if hdr.Filename != "clip.wav" {
		t.Fatalf("filename = %q", hdr.Filename)
	}
	raw, _ := io.ReadAll(f)
	if string(raw) != "RIFFfake" {
		t.Fatalf("file bytes = %q", raw)
	}
}

func TestAuxRequestRefusesWhatItCannotRun(t *testing.T) {
	for name, in := range map[string]auxBody{
		"unknown surface": {Surface: "nope"},
		"no file":         {Surface: "transcriptions", Model: "whisper-1"},
		"bad base64":      {Surface: "transcriptions", Model: "w", FileB64: "!!!!"},
		"body is a list":  {Surface: "embeddings", Body: json.RawMessage(`[1,2]`)},
	} {
		if _, err := auxRequest(context.Background(), in); err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestTheAuxEndpointReachesTheEmbeddingsSurface(t *testing.T) {
	// Through the real executor: an embeddings body parsed as a chat
	// completion is exactly the failure this endpoint is easy to write.
	var path string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[
		  {"object":"embedding","index":0,"embedding":[0.1,0.2]}],
		  "model":"m","usage":{"prompt_tokens":2,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground/aux",
		`{"surface":"embeddings","model":"m","body":{"input":"hello"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if !strings.HasSuffix(path, "/embeddings") {
		t.Fatalf("upstream path = %q; the request did not reach the embeddings surface", path)
	}
}

func TestTheAuxEndpointRejectsAnUnknownSurface(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground/aux", `{"surface":"nope"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}
```

The test file gains `encoding/base64` and `mime/multipart` is not needed here — `ParseMultipartForm` reads it.

For `testServerWithExecutor` to route an embeddings request, its catalog fixture model must declare the embeddings surface. Extend that helper's snapshot in `fixtures_test.go` so the one model declares both, which no existing test depends on being narrow:

```go
		Surfaces: []ir.Surface{ir.SurfaceLLM, ir.SurfaceEmbedding},
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'TestAuxRequest|TestTheAuxEndpoint' -v`

Expected: compile FAIL — `auxRequest` and `auxBody` are undefined; then 404 on the endpoint tests until the route exists.

- [ ] **Step 3: Implement**

Append to `internal/admin/playgroundaux.go`:

```go
// auxPaths mirrors the proxy routes the console may exercise. The paths are
// the same strings internal/server registers, because the executor's surface
// dispatch and the log both read them.
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

func auxRequest(ctx context.Context, in auxBody) (*http.Request, error) {
	path, ok := auxPaths[in.Surface]
	if !ok {
		return nil, errors.New("unknown surface " + in.Surface)
	}
	if in.Surface == "transcriptions" {
		return auxMultipartRequest(ctx, path, in)
	}

	merged := map[string]any{}
	if len(in.Body) > 0 {
		if err := json.Unmarshal(in.Body, &merged); err != nil {
			return nil, errors.New("body must be a JSON object")
		}
	}
	if in.Model != "" {
		merged["model"] = in.Model
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	return r, nil
}

// auxMultipartRequest rebuilds the upload form from base64. The console cannot
// forward a browser multipart body through a JSON API, so the file arrives
// encoded and is re-encoded here into the form the transcription surface parses.
func auxMultipartRequest(ctx context.Context, path string, in auxBody) (*http.Request, error) {
	file, err := base64.StdEncoding.DecodeString(in.FileB64)
	if err != nil || len(file) == 0 {
		return nil, errors.New("file_b64 must be non-empty base64")
	}
	name := in.Filename
	if name == "" {
		name = "audio.bin"
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write(file); err != nil {
		return nil, err
	}
	if err := mw.WriteField("model", in.Model); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, path, &buf)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r, nil
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
	pr, err := auxRequest(r.Context(), in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Each surface has its own executor entry point and its own narrow
	// dialect. There is no shared Handle for these: an embeddings body sent
	// through the chat path parses as an empty conversation.
	oa := openaiedge.New()
	switch in.Surface {
	case "embeddings":
		s.deps.Exec.HandleEmbeddings(w, pr, oa)
	case "rerank":
		s.deps.Exec.HandleRerank(w, pr, oa)
	case "moderations":
		s.deps.Exec.HandleModerations(w, pr, oa)
	case "images":
		s.deps.Exec.HandleImages(w, pr, oa)
	case "speech":
		s.deps.Exec.HandleSpeech(w, pr, oa)
	case "transcriptions":
		s.deps.Exec.HandleTranscriptions(w, pr, oa)
	}
}
```

Imports gain `encoding/base64`, `mime/multipart`, and `openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"`.

Register in `routes()`:

```go
	s.mux.HandleFunc("POST /api/playground/aux", s.requireCSRF(s.handlePlaygroundAux))
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/playgroundaux.go internal/admin/admin.go internal/admin/playground_test.go internal/admin/fixtures_test.go
git commit -m "feat(admin): auxiliary-surface runner for the playground"
```

---

### Task 5: A discovery-health rollup

Spec §6.5: today a provider whose listing has failed for six hours looks identical to a healthy one. The signals already exist on the `models` table — `state` and `missing_streak`, added by migration `0002_catalog.sql` — so this exposes them rather than inventing storage.

**Files:**
- Create: `internal/admin/discoveryapi.go`
- Create: `internal/admin/discoveryapi_test.go`
- Modify: `internal/admin/admin.go` (routes)
- Modify: `internal/store/adminstore.go` (add `DiscoveryHealth`)
- Test: `internal/store/adminstore_test.go`

**Interfaces:**
- Consumes: the `models` table columns `provider_id`, `state` and `missing_streak`.
- Produces: `store.DiscoveryHealthRow{ProviderID string; Total, Live, Stale, RemovedUpstream, MaxMissingStreak int}` and `(*DB).DiscoveryHealth(ctx) ([]DiscoveryHealthRow, error)`; the endpoint `GET /api/health/discovery` returning `{"providers":[{provider_id,total,live,stale,removed_upstream,max_missing_streak}]}` sorted by `provider_id`, always an array and never null. A provider with no catalogued rows is absent — absence is itself the "never discovered" signal, and inventing a zero row would claim a discovery sweep had run.

- [ ] **Step 1: Write the failing tests**

Create `internal/admin/discoveryapi_test.go`. The `models` table is seeded with a raw insert, which is the pattern `internal/store/adminstore_test.go:255` already uses:

```go
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

func seedModelRow(t *testing.T, db *store.DB, providerID, modelID, state string, streak int) {
	t.Helper()
	if _, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO models (provider_id, model_id, state, missing_streak)
		 VALUES (?, ?, ?, ?)`, providerID, modelID, state, streak); err != nil {
		t.Fatal(err)
	}
}

// Named apart from the handler's discoveryHealthView: both live in package
// admin, and one name would not compile.
type discoveryRollup struct {
	ProviderID       string `json:"provider_id"`
	Total            int    `json:"total"`
	Live             int    `json:"live"`
	Stale            int    `json:"stale"`
	RemovedUpstream  int    `json:"removed_upstream"`
	MaxMissingStreak int    `json:"max_missing_streak"`
}

func discoveryHealth(t *testing.T, s *Server) []discoveryRollup {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/health/discovery", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/health/discovery = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Providers []discoveryRollup `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	return out.Providers
}

func TestDiscoveryHealthRollsUpPerProvider(t *testing.T) {
	s, db := testServerFull(t)
	seedModelRow(t, db, "groq", "m1", "live", 0)
	seedModelRow(t, db, "groq", "m2", "live", 0)
	seedModelRow(t, db, "groq", "m3", "stale", 3)
	seedModelRow(t, db, "nebius", "m4", "removed_upstream", 9)

	got := discoveryHealth(t, s)
	if len(got) != 2 {
		t.Fatalf("want two providers, got %+v", got)
	}
	// Sorted by id: a map iteration order would reshuffle the panel on every
	// poll, which reads as the numbers changing.
	if got[0].ProviderID != "groq" || got[1].ProviderID != "nebius" {
		t.Fatalf("not sorted by provider: %+v", got)
	}
	g := got[0]
	if g.Total != 3 || g.Live != 2 || g.Stale != 1 || g.MaxMissingStreak != 3 {
		t.Fatalf("groq rollup wrong: %+v", g)
	}
	if got[1].RemovedUpstream != 1 || got[1].MaxMissingStreak != 9 {
		t.Fatalf("nebius rollup wrong: %+v", got[1])
	}
}

func TestDiscoveryHealthIsEmptyBeforeAnySweep(t *testing.T) {
	// An empty array, not null: a provider with no catalogued rows has never
	// been discovered, and a zero row would claim a sweep had run and found
	// nothing.
	s, _ := testServerFull(t)
	if got := discoveryHealth(t, s); len(got) != 0 {
		t.Fatalf("want an empty list, got %+v", got)
	}
}

func TestDiscoveryHealthRequiresASession(t *testing.T) {
	s, _ := testServerFull(t)
	r := httptest.NewRequest("GET", "/api/health/discovery", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
```

The last test needs `net/http/httptest` in the import block.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run TestDiscoveryHealth -v`

Expected: FAIL with 404 on all three — the route does not exist.

- [ ] **Step 3: Implement**

In `internal/store/adminstore.go`:

```go
// DiscoveryHealthRow is one provider's catalogue, summarised.
//
// MaxMissingStreak is the loud number: a provider whose listing has been
// failing looks identical to a healthy one until something counts the
// consecutive sweeps that omitted its models.
type DiscoveryHealthRow struct {
	ProviderID       string
	Total            int
	Live             int
	Stale            int
	RemovedUpstream  int
	MaxMissingStreak int
}

func (d *DB) DiscoveryHealth(ctx context.Context) ([]DiscoveryHealthRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, count(*),
		        sum(state = 'live'), sum(state = 'stale'),
		        sum(state = 'removed_upstream'),
		        coalesce(max(missing_streak), 0)
		   FROM models
		  GROUP BY provider_id
		  ORDER BY provider_id`)
	if err != nil {
		return nil, fmt.Errorf("discovery health: %w", err)
	}
	defer rows.Close()

	out := []DiscoveryHealthRow{}
	for rows.Next() {
		var r DiscoveryHealthRow
		if err := rows.Scan(&r.ProviderID, &r.Total, &r.Live, &r.Stale,
			&r.RemovedUpstream, &r.MaxMissingStreak); err != nil {
			return nil, fmt.Errorf("discovery health: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

Create `internal/admin/discoveryapi.go`:

```go
package admin

import "net/http"

type discoveryHealthView struct {
	ProviderID       string `json:"provider_id"`
	Total            int    `json:"total"`
	Live             int    `json:"live"`
	Stale            int    `json:"stale"`
	RemovedUpstream  int    `json:"removed_upstream"`
	MaxMissingStreak int    `json:"max_missing_streak"`
}

func (s *Server) handleDiscoveryHealth(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.DiscoveryHealth(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]discoveryHealthView, 0, len(rows))
	for _, row := range rows {
		out = append(out, discoveryHealthView{
			ProviderID: row.ProviderID, Total: row.Total, Live: row.Live,
			Stale: row.Stale, RemovedUpstream: row.RemovedUpstream,
			MaxMissingStreak: row.MaxMissingStreak,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}
```

Register in `routes()`, beside the existing provider-health line:

```go
	s.mux.HandleFunc("GET /api/health/discovery", s.requireSession(s.handleDiscoveryHealth))
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/store/ ./internal/admin/`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/discoveryapi.go internal/admin/discoveryapi_test.go internal/admin/admin.go internal/store/adminstore.go
git commit -m "feat(admin): per-provider discovery health rollup"
```

---

### Task 6: OAuth credential detail in the providers view

Spec §6.5 asks for an OAuth account showing expiry rather than only "needs reconnection". `store.Credential` already carries `Kind`, `Scope` and `ExpiresAt`, and `DB.Credentials` already selects all three — `credentialView` in `internal/admin/providers.go:37` simply drops them.

None of the three is secret. Expiry and scope are metadata about a token, not the token; the existing leak tests in `internal/admin/leak_test.go` are what keeps that claim honest, and they must stay green.

**Files:**
- Modify: `internal/admin/providers.go` (`credentialView` and the loop that builds it)
- Test: `internal/admin/providers_test.go`

**Interfaces:**
- Consumes: `store.Credential.Kind`, `.Scope`, `.ExpiresAt *int64`.
- Produces: `credentialView` gains `"kind"` (always present), `"expires_at"` and `"scope"` (omitted when absent, which is every static key).

- [ ] **Step 1: Write the failing test**

Append to `internal/admin/providers_test.go`:

```go
func TestACredentialViewCarriesOAuthMetadata(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "oa", "https://oa.example")

	// Written straight to the store: the create endpoint mints static keys,
	// and the shape under test is the one the refresh worker writes.
	expiry := int64(1790000000)
	if _, err := db.AddCredential(context.Background(), s.deps.Key, store.Credential{
		ProviderID: "oa", Label: "subscription", Kind: "oauth",
		Secret: "refresh-token-value", Scope: "read", Enabled: true,
		ExpiresAt: &expiry,
	}); err != nil {
		t.Fatal(err)
	}

	w := do(t, s, cookie, token, "GET", "/api/providers", "")
	var out struct {
		Providers []struct {
			ID          string `json:"id"`
			Credentials []struct {
				Kind      string `json:"kind"`
				Scope     string `json:"scope"`
				ExpiresAt *int64 `json:"expires_at"`
				Masked    string `json:"masked"`
			} `json:"credentials"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	var oauth *struct {
		Kind      string `json:"kind"`
		Scope     string `json:"scope"`
		ExpiresAt *int64 `json:"expires_at"`
		Masked    string `json:"masked"`
	}
	for i := range out.Providers {
		if out.Providers[i].ID != "oa" {
			continue
		}
		for j := range out.Providers[i].Credentials {
			if out.Providers[i].Credentials[j].Kind == "oauth" {
				oauth = &out.Providers[i].Credentials[j]
			}
		}
	}
	if oauth == nil {
		t.Fatalf("no oauth credential in the view: %s", w.Body.String())
	}
	if oauth.ExpiresAt == nil || *oauth.ExpiresAt != expiry {
		t.Fatalf("expiry missing: %+v", oauth)
	}
	if oauth.Scope != "read" {
		t.Fatalf("scope = %q", oauth.Scope)
	}
	if strings.Contains(oauth.Masked, "refresh-token") {
		t.Fatal("the secret leaked into the masked field")
	}
}

func TestAStaticKeyOmitsOAuthOnlyFields(t *testing.T) {
	// Omitted rather than zeroed: an expiry of 0 on a static key reads as
	// "expired in 1970", which is a different claim from "has no expiry".
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "st", "https://st.example")

	w := do(t, s, cookie, token, "GET", "/api/providers", "")
	if strings.Contains(w.Body.String(), `"expires_at"`) {
		t.Fatalf("a static key should carry no expires_at: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"kind"`) {
		t.Fatalf("kind should always be present: %s", w.Body.String())
	}
}
```

The writer is `(*DB).AddCredential(ctx, key, Credential) (string, error)` at `internal/store/credentials.go:40`; it is used directly rather than through the create endpoint because that endpoint mints static keys, and the shape under test is the one the OAuth refresh worker writes.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'TestACredentialViewCarriesOAuth|TestAStaticKeyOmits' -v`

Expected: FAIL — `kind` is absent from the JSON and `expires_at` is nil.

- [ ] **Step 3: Implement**

```go
type credentialView struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Masked  string `json:"masked"`
	Enabled bool   `json:"enabled"`
	Cooling bool   `json:"cooling"`
	Kind    string `json:"kind"`
	// OAuth-only metadata. A static key has neither, and omitting them is what
	// keeps the table honest about which rows have an account behind them.
	// Neither is secret: this is metadata about a token, not the token.
	ExpiresAt *int64 `json:"expires_at,omitempty"`
	Scope     string `json:"scope,omitempty"`
}
```

and in the loop that builds it:

```go
				v.Credentials = append(v.Credentials, credentialView{
					ID: c.ID, Label: c.Label, Masked: maskSecret(c.Secret),
					Enabled: c.Enabled, Cooling: s.cooling(p.ID, c.ID),
					Kind: c.Kind, ExpiresAt: c.ExpiresAt, Scope: c.Scope,
				})
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`

Expected: PASS, leak tests included. If a leak strategy asserts the exact field set of a credential view, extend its allowlist with the three new names in this commit and say so in the commit body.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/providers.go internal/admin/providers_test.go
git commit -m "feat(admin): oauth credential metadata in the providers view"
```

---

### Task 7: Catalog rows carry pricing, publisher and provenance

Spec §6.6: columns gain pricing, max output tokens, publisher and merge source, "so a number's provenance is visible: models.dev, discovery, inference or an override". `catalog.Model` already carries `Publisher`, `Pricing` and `Source`; `modelView` in `internal/admin/catalog.go:13` renders none of the three.

`Source` is the four-valued `catalog.Source` type: `models_dev`, `discovered`, `inferred`, `override`. `Pricing` carries a `Known` flag precisely so a free model and an unpriced one — both zero — stay distinguishable, which is what makes `pricing: null` the honest rendering of "no catalog price" rather than a zero the screen would print as `$0.00`.

**Files:**
- Modify: `internal/admin/catalog.go` (`modelView` and `collectModels`)
- Test: `internal/admin/catalog_test.go`

**Interfaces:**
- Consumes: `catalog.Model.Publisher`, `.Pricing` (`InputMicrosPerMTok`, `OutputMicrosPerMTok`, `Known`), `.Source`.
- Produces: `modelView` gains `"pricing": {"input_micros": n, "output_micros": n} | null`, `"publisher": "…"` and `"merge_source": "models_dev"|"discovered"|"inferred"|"override"`. A model served by several providers folds to one row, so where the providers disagree the view reports the **first** provider in catalog order — which is priority order — and that is stated in a comment because a silently-picked winner is the kind of thing a reader assumes is a bug.

- [ ] **Step 1: Write the failing tests**

Append to `internal/admin/catalog_test.go`. The fixture is built inline rather than reusing `catalogFixture()`, because that one deliberately carries no pricing:

```go
func pricedCatalog() *catalog.Store {
	c := &catalog.Store{}
	c.Set(catalog.NewSnapshot([]catalog.Model{
		{ProviderID: "groq", ModelID: "priced-model", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, Publisher: "meta",
			Source:       catalog.SourceModelsDev,
			Capabilities: catalog.Capabilities{Known: true},
			Pricing: catalog.Pricing{
				InputMicrosPerMTok: 150000, OutputMicrosPerMTok: 600000, Known: true,
			}},
		{ProviderID: "groq", ModelID: "overridden-model", State: catalog.StateLive,
			Surfaces: []ir.Surface{ir.SurfaceLLM}, Publisher: "meta",
			Source:       catalog.SourceOverride,
			Capabilities: catalog.Capabilities{Known: true}},
		{ProviderID: "groq", ModelID: "unpriced-model", State: catalog.StateLive,
			Surfaces:     []ir.Surface{ir.SurfaceLLM},
			Source:       catalog.SourceInferred,
			Capabilities: catalog.Capabilities{Known: false}},
	}, []string{"groq"}))
	return c
}

func modelViews(t *testing.T, s *Server) map[string]struct {
	Publisher   string `json:"publisher"`
	MergeSource string `json:"merge_source"`
	Pricing     *struct {
		InputMicros  int64 `json:"input_micros"`
		OutputMicros int64 `json:"output_micros"`
	} `json:"pricing"`
} {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/models", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/models = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Models []struct {
			Model       string `json:"model"`
			Publisher   string `json:"publisher"`
			MergeSource string `json:"merge_source"`
			Pricing     *struct {
				InputMicros  int64 `json:"input_micros"`
				OutputMicros int64 `json:"output_micros"`
			} `json:"pricing"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	byModel := map[string]struct {
		Publisher   string `json:"publisher"`
		MergeSource string `json:"merge_source"`
		Pricing     *struct {
			InputMicros  int64 `json:"input_micros"`
			OutputMicros int64 `json:"output_micros"`
		} `json:"pricing"`
	}{}
	for _, m := range out.Models {
		byModel[m.Model] = struct {
			Publisher   string `json:"publisher"`
			MergeSource string `json:"merge_source"`
			Pricing     *struct {
				InputMicros  int64 `json:"input_micros"`
				OutputMicros int64 `json:"output_micros"`
			} `json:"pricing"`
		}{m.Publisher, m.MergeSource, m.Pricing}
	}
	return byModel
}

func TestAModelViewCarriesPricingAndPublisher(t *testing.T) {
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	got := modelViews(t, s)["priced-model"]
	if got.Pricing == nil {
		t.Fatal("a priced model must carry a price")
	}
	if got.Pricing.InputMicros != 150000 || got.Pricing.OutputMicros != 600000 {
		t.Fatalf("pricing = %+v", got.Pricing)
	}
	if got.Publisher != "meta" {
		t.Fatalf("publisher = %q", got.Publisher)
	}
}

func TestAnUnpricedModelRendersNullNotZero(t *testing.T) {
	// Zero is a claim that the model is free. Null is the claim that the
	// catalog has no price for it, which is the true one.
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	if got := modelViews(t, s)["unpriced-model"]; got.Pricing != nil {
		t.Fatalf("unpriced model priced as %+v", got.Pricing)
	}
}

func TestAModelViewNamesItsSource(t *testing.T) {
	s, _ := testServerFull(t)
	s.deps.Catalog = pricedCatalog()

	got := modelViews(t, s)
	if got["priced-model"].MergeSource != "models_dev" {
		t.Errorf("merge_source = %q", got["priced-model"].MergeSource)
	}
	if got["overridden-model"].MergeSource != "override" {
		t.Errorf("merge_source = %q", got["overridden-model"].MergeSource)
	}
	if got["unpriced-model"].MergeSource != "inferred" {
		t.Errorf("merge_source = %q", got["unpriced-model"].MergeSource)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/admin/ -run 'TestAModelView|TestAnUnpricedModel' -v`

Expected: FAIL — `pricing` is absent, `publisher` and `merge_source` are empty strings.

- [ ] **Step 3: Implement**

In `internal/admin/catalog.go`:

```go
// pricingView is micro-dollars per million tokens. Null rather than a zeroed
// object when the catalog has no price: catalog.Pricing carries Known for
// exactly this reason, since a free model and an unpriced one are both zero
// and only one of them may be printed as a number.
type pricingView struct {
	InputMicros  int64 `json:"input_micros"`
	OutputMicros int64 `json:"output_micros"`
}

type modelView struct {
	Model         string   `json:"model"`
	Providers     []string `json:"providers"`
	Surfaces      []string `json:"surfaces"`
	ContextWindow int      `json:"context_window"`
	MaxOutput     int      `json:"max_output_tokens"`
	Tools         bool     `json:"tools"`
	Vision        bool     `json:"vision"`
	Reasoning     bool     `json:"reasoning"`
	// Inferred marks a row whose capabilities were guessed rather than read.
	// Master design §6.4 routes these with a warning, and an operator needs to
	// know which they are.
	Inferred bool         `json:"inferred"`
	State    string       `json:"state"`
	Pricing  *pricingView `json:"pricing"`
	// Publisher and MergeSource come from the first provider in catalog order,
	// which is priority order — the row folds several providers into one, and
	// where they disagree the one the router would reach first is the answer
	// that matches what a request would actually cost.
	Publisher   string `json:"publisher,omitempty"`
	MergeSource string `json:"merge_source"`
}
```

In `collectModels`, populate them where the view is first created — inside the `if !ok` branch, so the first provider wins:

```go
			v = &modelView{
				Model: m.ModelID, Providers: []string{},
				Surfaces: surfaceNames(m.Surfaces), State: string(m.State),
				ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutputTokens,
				Tools: m.Capabilities.Tools, Vision: m.Capabilities.Vision,
				Reasoning:   m.Capabilities.Reasoning,
				Publisher:   m.Publisher,
				MergeSource: string(m.Source),
			}
			if m.Pricing.Known {
				v.Pricing = &pricingView{
					InputMicros:  m.Pricing.InputMicrosPerMTok,
					OutputMicros: m.Pricing.OutputMicrosPerMTok,
				}
			}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/admin/`

Expected: PASS, including the existing catalog tests. They read models built by `catalogFixture()`, which carries no pricing, so those rows now carry `"pricing": null` — assert nothing about it there.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/catalog.go internal/admin/catalog_test.go
git commit -m "feat(admin): pricing, publisher and provenance on models"
```

---

### Task 8: A config switch for Gemini media inlining

Phase 4 left a hazard standing: the Gemini adapter fetches client-supplied media URLs so it can inline them, and nothing can turn that off. The fetcher's own comment names the risk — this is the gateway issuing requests to addresses a client chose, and the scheme, redirect, size and timeout constraints are all that stand between it and server-side request forgery. An operator who wants that behaviour off currently has no way to say so.

Spec §6.10 puts "the master switch for outbound traffic the gateway initiates on the operator's behalf" on the Settings screen, which is where this belongs. It gets its own top-level block rather than living under `catalog:`, because `catalog:` governs the two discovery workers and a media fetcher is not one of them — filing it there would make the config's own grouping lie.

**Files:**
- Modify: `internal/config/config.go` (add `MediaConfig`, a `Media` field on `Config`, and one entry in `RestartOnly`)
- Modify: `internal/adapter/gemini/media.go` (`Fetcher` gains `Inline bool`; `part` gates on it)
- Modify: `internal/server/server.go` (adapters map construction)
- Modify: `internal/admin/configapi.go` (the block and field-meta maps)
- Test: `internal/config/load_test.go`, `internal/adapter/gemini/media_test.go`

**Interfaces:**
- Consumes: `catalog.DiscoveryConfig.Enabled *bool`, whose nil-means-default-on handling this mirrors; `gemini.NewWithFetcher(f *Fetcher) *Adapter`, which already exists at `internal/adapter/gemini/adapter.go:20` — no new constructor is needed.
- Produces: config key `media.inline` (bool, default true, listed in `config.RestartOnly`); `gemini.Fetcher.Inline bool`, true from `NewFetcher()`; when false, `Fetcher.part` drops a remote-URL media block with the warning reason `"media inlining is disabled"`.

- [ ] **Step 1: Write the failing tests**

In `internal/adapter/gemini/media_test.go`. `Fetcher.part` returns `(map[string]any, []ir.Warning)`, and `ir.Warning` is a struct with `Field`, `Target` and `Reason` and a `String()` method — it is not an error, so the assertion reads `Reason`:

```go
func TestADisabledFetcherDropsRemoteURLsWithAWarning(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("png"))
	}))
	defer up.Close()

	f := NewFetcher()
	f.Inline = false
	got, warns := f.part(context.Background(), &ir.Media{URL: up.URL + "/a.png"}, "image")
	if got != nil {
		t.Fatalf("the block should be dropped, got %v", got)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Reason, "media inlining is disabled") {
		t.Fatalf("warnings = %v", warns)
	}
}

func TestADisabledFetcherStillPassesWhatNeedsNoFetch(t *testing.T) {
	// The switch governs outbound requests, not media. Inline data and a
	// fileUri Gemini already resolves cost the gateway no traffic, and
	// dropping them would break prompts the switch was never about.
	f := NewFetcher()
	f.Inline = false
	for name, m := range map[string]*ir.Media{
		"inline data": {Data: "aGk=", MIME: "image/png"},
		"file id":     {FileID: "files/abc", MIME: "image/png"},
		"youtube":     {URL: "https://www.youtube.com/watch?v=x"},
	} {
		got, warns := f.part(context.Background(), m, "image")
		if got == nil {
			t.Errorf("%s: dropped, warnings = %v", name, warns)
		}
	}
}
```

In `internal/config/load_test.go`, following that file's existing table style:

```go
func TestMediaInlineDefaultsOnAndParsesOff(t *testing.T) {
	// A pointer, so an explicit false is distinguishable from an absent key.
	// Without that, the default could only be off.
	on, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if !on.MediaInline() {
		t.Fatal("media.inline defaults to on")
	}
	off, err := Parse([]byte(minimal+"media:\n  inline: false\n"),
		env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if off.Media.Inline == nil || *off.Media.Inline {
		t.Fatalf("media.inline should parse false, got %v", off.Media.Inline)
	}
	if off.MediaInline() {
		t.Fatal("MediaInline() must follow the explicit false")
	}
}

func TestMediaInlineIsRestartOnly(t *testing.T) {
	// The adapters map is built once at startup, so a reload changing this
	// would be accepted, warn about nothing, and take effect at the next
	// process start.
	found := false
	for _, f := range RestartOnly {
		if f == "media.inline" {
			found = true
		}
	}
	if !found {
		t.Fatalf("media.inline missing from RestartOnly: %v", RestartOnly)
	}
}
```

`Parse(raw []byte, lookup func(string) (string, bool)) (*Config, error)` and the `env(...)` helper and `minimal` fixture are all already in `load_test.go`. Note that `Parse` rejects unknown keys — `TestParseRejectsUnknownKeys` covers that — so this test fails on the `media:` block until the struct field exists, which is the failure to look for in Step 2.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/config/ ./internal/adapter/gemini/ -run 'TestMediaInline|TestADisabledFetcher' -v`

Expected: compile FAIL — `Config.Media` and `Fetcher.Inline` do not exist.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, add the block, the field on `Config`, and the `RestartOnly` entry:

```go
// MediaConfig governs media the gateway fetches on a client's behalf.
//
// Inlining means Darkrouter issues requests to client-supplied addresses,
// which the fetcher constrains but cannot make risk-free. An operator who does
// not want that outbound traffic needs a way to say so.
type MediaConfig struct {
	// Inline is a pointer so an explicit false is distinguishable from an
	// absent key, which is what lets the default be on.
	Inline *bool `yaml:"inline"`
}
```

```go
	Media MediaConfig `yaml:"media"`
```

```go
var RestartOnly = []string{
	"server.proxy_listen",
	"server.admin_listen",
	"policy.timeout.connect",
	"policy.timeout.first_byte",
	"catalog.sync_interval",
	"catalog.discovery.interval",
	// The adapters map is constructed once at startup and the Gemini adapter
	// captures its fetcher there.
	"media.inline",
}
```

Add a helper beside it so callers do not repeat the nil check:

```go
// MediaInline reports the effective setting: absent means on.
func (c *Config) MediaInline() bool {
	return c.Media.Inline == nil || *c.Media.Inline
}
```

In `internal/adapter/gemini/media.go`, give `Fetcher` the field and default it on:

```go
type Fetcher struct {
	Client   *http.Client
	MaxBytes int64
	// Inline gates the outbound fetch. False drops a remote URL rather than
	// retrieving it; everything that needs no request is unaffected.
	Inline bool
}
```

```go
func NewFetcher() *Fetcher {
	return &Fetcher{
		Client: &http.Client{ /* unchanged */ },
		MaxBytes: DefaultMaxInlineBytes,
		Inline:   true,
	}
}
```

In `part`, the guard goes immediately before the fetch — after the `passthroughURI` branch, so it governs only the case that would issue a request:

```go
	if !f.Inline {
		return drop("media inlining is disabled")
	}
	mime, data, err := f.inline(ctx, m.URL)
```

In `internal/server/server.go`, where the adapters map is built, construct the Gemini adapter from a fetcher carrying the setting. `NewWithFetcher` already exists for exactly this:

```go
	mediaFetcher := geminiadapter.NewFetcher()
	mediaFetcher.Inline = cfgStore.Current().MediaInline()
	adapters := map[string]adapter.Adapter{
		// … the other kinds unchanged …
		"gemini": geminiadapter.NewWithFetcher(mediaFetcher),
	}
```

Read the surrounding lines and match how the map is actually assembled — the point is that the fetcher is built from config rather than by `New()`, not the exact literal shape.

Finally, `GET /api/config` must return the new block or the Settings screen has no row for it. In `internal/admin/configapi.go`, append `"media.inline"` to the `configFields` slice at line 32 — `hot_reloadable` is derived from `config.RestartOnly`, so it needs no separate entry — and add the block to the `"blocks"` literal beside `catalog`:

```go
			"media": map[string]any{"inline": cfg.MediaInline()},
```

`sourceOf` also has to know the new field. Read how it classifies `catalog.discovery.enabled` and mirror it.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race -count=1 ./internal/config/ ./internal/adapter/gemini/ ./internal/server/ ./internal/admin/`

Expected: PASS. The golden Gemini suites are untouched: with `Inline` true the fetch path is byte-for-byte what it was.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/load_test.go internal/adapter/gemini/media.go internal/adapter/gemini/media_test.go internal/server/server.go internal/admin/configapi.go
git commit -m "feat(config): media.inline gates gemini url fetches"
```

---

### Task 9: Wire types and hooks for everything the backend now serves

One task so every later task shares exact names. Nothing renders yet; this is the module §9 exists for — "a Go field rename has one place to land instead of five".

Two of the additions correct types that were already wrong rather than merely incomplete: `Preset` declares four of the eight fields `GET /api/presets` has always returned, and `useUsage` takes only a dimension while the endpoint has always accepted `days`.

**Files:**
- Modify: `web/src/lib/api-types.ts`
- Modify: `web/src/lib/queries.ts`
- Test: `web/src/lib/queries.test.tsx`

**Interfaces produced — later tasks import these by these exact names:**

```ts
export type Healthz = {
  config_valid: boolean
  warnings: string[]
  uptime: string
  version: string
  log_records_dropped: number
  log_records_written: number
  config_error?: string
}

export type DiscoveryHealthRow = {
  provider_id: string
  total: number
  live: number
  stale: number
  removed_upstream: number
  max_missing_streak: number
}

export type DiscoveryHealthResponse = { providers: DiscoveryHealthRow[] }

export type CountResult = { tokens: number; estimated: boolean }

export type AuxSurface =
  | "embeddings" | "rerank" | "moderations"
  | "images" | "speech" | "transcriptions"

export type PlaygroundMessage = { role: string; content: string }

export type PlaygroundDialect = "openai" | "anthropic" | "gemini"

export type PlaygroundChatBody = {
  model: string
  prompt?: string
  system?: string
  messages?: PlaygroundMessage[]
  temperature?: number
  max_tokens?: number
  tools?: Record<string, unknown>[]
  stream?: boolean
  dialect?: PlaygroundDialect
}

export type AuxBody = {
  surface: AuxSurface
  model?: string
  body?: Record<string, unknown>
  file_b64?: string
  filename?: string
}

export type Pricing = { input_micros: number; output_micros: number }

export type MergeSource = "models_dev" | "discovered" | "inferred" | "override"
```

Three existing types gain fields — written out in full here because "add a field to X" is the instruction that produces two divergent versions of X:

```ts
export type Credential = {
  id: string
  label: string
  masked: string
  enabled: boolean
  cooling: boolean
  kind: string
  /** OAuth only. Seconds since the epoch. */
  expires_at?: number
  scope?: string
}

export type Preset = {
  id: string
  name: string
  kind: string
  base_url: string
  surfaces: string[]
  auth_kind: string
  website: string
  free_tier: boolean
}

export type Model = {
  model: string
  providers: string[]
  surfaces: string[]
  context_window: number
  max_output_tokens: number
  tools: boolean
  vision: boolean
  reasoning: boolean
  inferred: boolean
  state: string
  /** Null when the catalog has no price. Zero would claim the model is free. */
  pricing: Pricing | null
  publisher?: string
  merge_source: MergeSource
}
```

`RequestRow` gains one field, added inside the existing declaration after `attempts`:

```ts
  /** Which rendering served: the fast path untouched, or translated through
   *  the IR. Absent when nothing served. */
  path?: "passthrough" | "ir"
```

Hooks and keys:

```ts
  healthz: ["healthz"] as const,
  discovery: ["health", "discovery"] as const,
  policy: ["policy"] as const,
  override: (provider: string, model: string) =>
    ["models", "override", provider, model] as const,
```

```ts
export function useHealthz(extra?: Extra<Healthz>) {
  return useQuery({
    queryKey: keys.healthz,
    queryFn: () => api.get<Healthz>("/healthz"),
    // The ops footer's numbers move with the log writer, not with a request.
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function useDiscoveryHealth(extra?: Extra<DiscoveryHealthResponse>) {
  return useQuery({
    queryKey: keys.discovery,
    queryFn: () => api.get<DiscoveryHealthResponse>("/api/health/discovery"),
    // A sweep interval, not a request interval: this changes when discovery
    // runs, which is minutes apart.
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function usePolicy(extra?: Extra<PolicyBlock>) {
  return useQuery({
    queryKey: keys.policy,
    queryFn: () => api.get<PolicyBlock>("/api/policy"),
    ...extra,
  })
}
```

`useUsage` is widened to accept a range without breaking the eleven existing string call sites:

```ts
type UsageOpts = { dimension?: UsageDimension; days?: number }

export function useUsage(
  opts?: UsageDimension | UsageOpts,
  extra?: Extra<UsageResponse>,
) {
  const { dimension, days } =
    typeof opts === "string" ? { dimension: opts, days: undefined } : (opts ?? {})
  const params = new URLSearchParams()
  if (dimension) params.set("group_by", dimension)
  if (days) params.set("days", String(days))
  const query = params.toString()
  return useQuery({
    queryKey: keys.usage(dimension, days),
    queryFn: () => api.get<UsageResponse>(`/api/usage${query ? `?${query}` : ""}`),
    refetchInterval: POLL.slow,
    ...extra,
  })
}
```

and `keys.usage` gains the second argument, because a 7-day and a 90-day view of one dimension are two answers and must be two cache entries:

```ts
  usage: (dimension?: UsageDimension, days?: number) =>
    ["usage", dimension ?? "day", days ?? 0] as const,
```

- [ ] **Step 1: Write the failing tests**

Append to `web/src/lib/queries.test.tsx`, matching that file's key-comparison style:

```ts
describe("the new surfaces", () => {
  it("keys healthz, discovery and policy distinctly", () => {
    const flat = [keys.healthz, keys.discovery, keys.policy, keys.health].map((k) =>
      JSON.stringify(k),
    )
    expect(new Set(flat).size).toBe(flat.length)
  })

  it("varies the usage key with its range", () => {
    // A 7-day and a 90-day view of one dimension are two answers. One key
    // would show the first range's rows under the second range's label.
    expect(JSON.stringify(keys.usage("provider", 7))).not.toBe(
      JSON.stringify(keys.usage("provider", 90)),
    )
  })

  it("keys an override by provider and model", () => {
    expect(JSON.stringify(keys.override("groq", "m"))).not.toBe(
      JSON.stringify(keys.override("groq", "n")),
    )
  })

  it("keeps the existing string call form of the usage key", () => {
    // Overview and the command palette call useUsage("alias"). Widening the
    // signature must not move their cache entry.
    expect(JSON.stringify(keys.usage("alias"))).toBe(
      JSON.stringify(["usage", "alias", 0]),
    )
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- queries`

Expected: FAIL — `keys.healthz`, `keys.discovery`, `keys.policy` and `keys.override` do not exist, and `keys.usage` takes one argument.

- [ ] **Step 3: Implement** the types, keys and hooks exactly as declared above.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS. Widening `keys.usage` changes the cache key shape for the existing Overview and Usage calls, which is harmless — nothing persists a query key.

- [ ] **Step 5: Commit**

```bash
git add web/src/lib/api-types.ts web/src/lib/queries.ts web/src/lib/queries.test.tsx
git commit -m "feat(web): wire types and hooks for the new endpoints"
```

---

### Task 10: The identity mark, and a ladder proved legible without colour

Two drawings that have to say what they mean. §3.5's mark, and §11's one untested requirement — "the ladder ... is asserted legible in greyscale, meaning every state is distinguishable with colour stripped" — which `docs/ux/DONE-CRITERIA.md` records as backed by nothing: "colour-independence is a design property of the marks, not something tested".

Spec §3.5's mark is a graticule with one branch taken, buildable from rects with no curves and no gradients.

**The geometry comes from the mockup, not from the prose.** `docs/ux/mockups/fragments/15-login.html` lines 32–43 draw it, §10 makes the mockups the contract, and the fragment differs from the §3.5 prose in two places — the pip sits at x=18 rather than x=20, and the whole mark is rects rather than lines. Copy the fragment.

The spine is three separate rects rather than one. A single rule from top to bottom would fill the 1px cores of both hollow squares, and the two skip marks would render solid and read as served — which is the ladder saying the opposite of what happened.

**Files:**
- Create: `web/src/features/shell/identity-mark.tsx`
- Create: `web/src/features/shell/identity-mark.test.tsx`
- Modify: `web/src/routes/login.tsx`
- Test: `web/src/features/ladder/ladder.test.tsx` (extend)

**Interfaces:** `IdentityMark({ size }: { size?: number })` renders the 24×24 SVG scaled to `size` (default 96 on login, per the fragment's `login-mark-96`). Hairlines take `hsl(var(--legend))`, the pip takes `hsl(var(--primary))`. Class names `spine-seg` on the three spine rects and `pip` on the accent square, so the test can name them without depending on element order. The ladder half changes no production code: the greyscale channels are already in `web/src/styles/ladder.css` — fill versus hollow, a 1px versus 2px stroke, a centre dot, a smaller silhouette — and this pins them so a later edit cannot quietly remove one.

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/shell/identity-mark.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { IdentityMark } from "./identity-mark"
import { LoginScreen } from "../../routes/login"

describe("the identity mark", () => {
  it("draws the spine in three segments", () => {
    // One rule top to bottom would fill the 1px cores of both hollow squares,
    // and two skip marks would render solid — the ladder saying "served"
    // where it means "considered".
    const { container } = render(<IdentityMark />)
    expect(container.querySelectorAll(".spine-seg")).toHaveLength(3)
  })

  it("carries exactly one accent pip", () => {
    // Two colours only, per §3.5: one neutral hairline and one accent.
    const { container } = render(<IdentityMark />)
    expect(container.querySelectorAll(".pip")).toHaveLength(1)
  })

  it("scales through the viewBox rather than by redrawing", () => {
    const { container } = render(<IdentityMark size={36} />)
    const svg = container.querySelector("svg")
    expect(svg?.getAttribute("viewBox")).toBe("0 0 24 24")
    expect(svg?.getAttribute("width")).toBe("36")
  })

  it("names itself for a reader who cannot see it", () => {
    render(<IdentityMark />)
    expect(screen.getByRole("img", { name: /darkrouter/i })).toBeInTheDocument()
  })
})

describe("login", () => {
  it("shows the mark", () => {
    const { container } = render(<LoginScreen onAuthenticated={() => {}} />)
    expect(container.querySelector("svg .pip")).not.toBeNull()
  })
})
```

Append to `web/src/features/ladder/ladder.test.tsx`. jsdom does not apply an imported stylesheet, so a computed-colour assertion would pass against an empty rule and prove nothing. The stylesheet is read as text instead, which is where the property actually lives:

```tsx
import { readFileSync } from "node:fs"
import { fileURLToPath } from "node:url"

/** One mark rule's declarations, as written. */
function markRule(css: string, mark: string): string {
  const match = css.match(new RegExp(`\\.mark-${mark}\\s*\\{([^}]*)\\}`))
  return match?.[1] ?? ""
}

describe("the marks read without colour", () => {
  // Fill, stroke weight, a centre dot and silhouette size are the channels
  // that survive greyscale. Colour is a fifth, and must never be the only one.
  const css = readFileSync(
    fileURLToPath(new URL("../../styles/ladder.css", import.meta.url)),
    "utf8",
  )

  it("separates a hollow skip from a filled attempt by fill", () => {
    expect(markRule(css, "skipped")).toMatch(/background:\s*transparent/)
    expect(markRule(css, "failed")).toMatch(/background:\s*var\(/)
    expect(markRule(css, "served")).toMatch(/background:\s*var\(/)
  })

  it("gives cooling a second channel beyond its stroke weight", () => {
    // 1px against 2px is the weakest pairing in the greyscale proof, so
    // cooling also carries a centre dot.
    expect(markRule(css, "cooling")).toMatch(/border:\s*2px/)
    expect(css).toMatch(/\.mark-cooling::after\s*\{/)
  })

  it("makes a terminated mark a smaller silhouette, not a paler one", () => {
    const rule = markRule(css, "terminated")
    expect(rule).toMatch(/width:\s*6px/)
    expect(rule).toMatch(/background:\s*transparent/)
  })

  it("gives all five states a rule of their own", () => {
    for (const mark of ["skipped", "cooling", "failed", "served", "terminated"]) {
      expect(markRule(css, mark)).not.toBe("")
    }
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- identity-mark ladder`

Expected: the identity-mark tests FAIL because the module does not exist. The ladder tests may already pass — the stylesheet has carried these channels since phase 13. That is the intended outcome here and only here: this half is a characterisation test pinning a property nothing was holding in place. Everywhere else in this plan a new test that passes on first run means the test is wrong.

- [ ] **Step 3: Implement**

Create `web/src/features/shell/identity-mark.tsx`. Every coordinate below is transcribed from fragment 15; the fragment's `var(--legend)` and `var(--accent)` become the app's `hsl(var(--legend))` and `hsl(var(--primary))`, which is the same token under this codebase's naming:

```tsx
/**
 * §3.5's mark: a graticule with one branch taken.
 *
 * Geometry transcribed from `docs/ux/mockups/fragments/15-login.html`, which
 * §10 makes the contract. Rects rather than lines and `shapeRendering` crisp,
 * so a 1px hairline lands on a pixel boundary at every size instead of
 * softening into two grey rows.
 */
export function IdentityMark({ size = 96 }: { size?: number }) {
  const line = "hsl(var(--legend))"
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      role="img"
      aria-label="darkrouter"
      shapeRendering="crispEdges"
    >
      <rect x="2.5" y="2.5" width="19" height="19" fill="none" stroke={line} strokeWidth="1" />
      {/* Three segments, not one rule: a continuous spine would fill the
          hollow squares and render two skip marks as served. */}
      <rect className="spine-seg" x="11.5" y="2" width="1" height="3.5" fill={line} />
      <rect className="spine-seg" x="11.5" y="8.5" width="1" height="7" fill={line} />
      <rect className="spine-seg" x="11.5" y="18.5" width="1" height="3.5" fill={line} />
      <rect x="2" y="6.5" width="10" height="1" fill={line} />
      {/* The branch that was taken: it crosses the spine and reaches the pip. */}
      <rect x="2" y="11.5" width="17" height="1" fill={line} />
      <rect x="2" y="16.5" width="10" height="1" fill={line} />
      <rect x="11" y="6" width="2" height="2" fill="none" stroke={line} strokeWidth="1" />
      <rect x="11" y="16" width="2" height="2" fill="none" stroke={line} strokeWidth="1" />
      <rect className="pip" x="18" y="10" width="4" height="4" fill="hsl(var(--primary))" />
    </svg>
  )
}
```

In `web/src/routes/login.tsx`, import it and put it above the heading, inside the form's flex column:

```tsx
          <div className="flex flex-col items-center gap-2">
            <IdentityMark size={72} />
            <h1 className="text-xl font-medium">darkrouter</h1>
          </div>
```

The wordmark is lowercase per §3.5, and never in the accent — a product name is not a reading.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS. `first-run.test.tsx` looks for a button named `/sign in|log in/i` and is unaffected.

If a ladder assertion fails, that is a finding about the stylesheet, not a regex to relax. Report it.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/shell/identity-mark.tsx web/src/features/shell/identity-mark.test.tsx web/src/routes/login.tsx web/src/features/ladder/ladder.test.tsx
git commit -m "feat(web): identity mark, and pin the ladder's greyscale"
```

---

### Task 11: Overview completion

Spec §6.1 asks for four things the screen lacks: a config-invalid banner, sparklines under every tile rather than one, a recent-failovers strip, and an ops footer carrying the dropped-log-record counter.

One deliberate degradation. Latency has no per-day source — `UsageRow` carries requests, attempts, tokens and cost, and nothing else — so the latency tile gets **no** sparkline rather than one plotting an unrelated number. §8.4 anticipates exactly this: "a missing sparkline series renders as the bare number".

**Files:**
- Create: `web/src/features/overview/failovers.tsx`
- Create: `web/src/features/overview/ops-footer.tsx`
- Modify: `web/src/features/overview/overview-screen.tsx`
- Test: `web/src/features/overview/overview-screen.test.tsx`

**Interfaces:**
- Consumes: `Overview.failovers` (`FailoverRow[]`), `useConfig()`, `useHealthz()` from Task 9, the `Sparkline` component already in `overview-screen.tsx`.
- Produces, all exported from `overview-screen.tsx` so the pure-function tests can reach them: `spendSeries(rows: UsageRow[]): number[]`, `errorSeries(rows: UsageRow[]): number[]`, `failoverLabel(row: FailoverRow): string`, `droppedText(dropped: number, written: number): string`. Components: `<Failovers rows={FailoverRow[]} />` in `failovers.tsx`, linking each row to `/requests/$id`; `<OpsFooter />` in `ops-footer.tsx`.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/overview/overview-screen.test.tsx`, reusing its existing `usage` factory:

```ts
describe("tile series", () => {
  it("sums spend per day across a dimension's rows", () => {
    expect(
      spendSeries([
        { ...usage("groq", 1), day: "2026-08-25", cost_micros: 100 },
        { ...usage("nebius", 1), day: "2026-08-25", cost_micros: 50 },
        { ...usage("groq", 1), day: "2026-08-26", cost_micros: 70 },
      ]),
    ).toEqual([150, 70])
  })

  it("treats an unpriced day as no spend rather than dropping the day", () => {
    // The shape has to keep its x-axis: a missing day would compress the
    // sparkline and misreport when spending happened.
    expect(
      spendSeries([
        { ...usage("groq", 1), day: "2026-08-25", cost_micros: null },
        { ...usage("groq", 1), day: "2026-08-26", cost_micros: 70 },
      ]),
    ).toEqual([0, 70])
  })

  it("derives errors from attempts beyond requests, floored at zero", () => {
    expect(
      errorSeries([
        { ...usage("groq", 3), day: "2026-08-25", attempts: 5 },
        { ...usage("groq", 4), day: "2026-08-26", attempts: 4 },
      ]),
    ).toEqual([2, 0])
  })
})

describe("the failover strip", () => {
  it("labels a row with its alias, attempt count and serving provider", () => {
    const label = failoverLabel({
      id: "x", ts: 0, alias: "fast", attempts: 3,
      final_provider_id: "nebius", final_model: "m", total_ms: 12,
    })
    expect(label).toContain("fast")
    expect(label).toContain("×3")
    expect(label).toContain("nebius")
  })

  it("says so when a request had no alias", () => {
    // A bare model name is not an alias, and printing an empty arrow would
    // read as a rendering fault.
    const label = failoverLabel({
      id: "x", ts: 0, alias: "", attempts: 2,
      final_provider_id: "groq", final_model: "m-4", total_ms: 9,
    })
    expect(label).toContain("m-4")
    expect(label).not.toContain("→ →")
  })
})

describe("the dropped-record counter", () => {
  it("reads zero as a statement rather than a number", () => {
    expect(droppedText(0, 400)).toMatch(/no records dropped/i)
  })

  it("names the shortfall when records were dropped", () => {
    // A non-zero count means usage_daily is a lower bound, which is the one
    // thing that makes every spend figure on this screen approximate.
    const text = droppedText(7, 400)
    expect(text).toContain("7")
    expect(text).toContain("400")
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- overview`

Expected: FAIL — none of the four functions is exported.

- [ ] **Step 3: Implement**

Add the four pure functions to `overview-screen.tsx`:

```ts
/** Daily totals in day order, so a sparkline's x-axis is time. */
function byDay(rows: UsageRow[], value: (r: UsageRow) => number): number[] {
  const acc = new Map<string, number>()
  for (const row of rows) acc.set(row.day, (acc.get(row.day) ?? 0) + value(row))
  return [...acc.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([, v]) => v)
}

export function spendSeries(rows: UsageRow[]): number[] {
  // Null contributes nothing rather than removing the day: dropping it would
  // compress the shape and misreport when the spending happened.
  return byDay(rows, (r) => r.cost_micros ?? 0)
}

export function errorSeries(rows: UsageRow[]): number[] {
  // Attempts beyond requests are failovers. Floored, because a rollup that
  // straddles a day boundary can land one attempt short of its request.
  return byDay(rows, (r) => Math.max(0, r.attempts - r.requests))
}

export function failoverLabel(row: FailoverRow): string {
  const asked = row.alias || row.final_model
  return `${asked} → ${row.final_provider_id}/${row.final_model} ×${row.attempts}`
}

export function droppedText(dropped: number, written: number): string {
  if (dropped === 0) return "no records dropped"
  // A non-zero count is the honest signal that every usage figure on this
  // screen is a lower bound.
  return `${dropped} of ${written + dropped} log records dropped`
}
```

Then the screen changes, in order down the page:

- **Config banner.** When `config.data && !config.data.valid`, render a card bordered `hsl(var(--destructive))` above the tiles, carrying `config.data.error` and the sentence "the previous configuration is still serving". `useConfig()` already supplies it, and the same copy is already on Settings — extract it if you prefer, but do not write two different sentences for one state.
- **Tiles.** Requests keeps its existing sparkline from `o.series`. Spend gets `<Sparkline points={spendSeries(byProvider.data?.days ?? [])} />` and errors gets `errorSeries(...)` over the same rows. Latency gets none, and the tile carries a `text-xs` legend reading "no daily series for latency" so the absence reads as a decision.
- **Failovers strip**, below the routing graph: `<Failovers rows={o.failovers.slice(0, 5)} />`.
- **Ops footer**, last on the page: `<OpsFooter />`.
- **Flow-graph links.** In `flow-graph.tsx`, wrap each `.rf-provider` node in `<Link to="/providers/$id" params={{ id }}>` so every provider on the graph opens its detail page.

`failovers.tsx`:

```tsx
import { Link } from "@tanstack/react-router"
import { Card } from "darkraise-ui"
import type { FailoverRow } from "../../lib/api-types"
import { failoverLabel } from "./overview-screen"

/**
 * The last handful of requests that took more than one attempt.
 *
 * A fleet-wide error rate hides one provider quietly degrading; this is the
 * early warning the rate cannot give.
 */
export function Failovers({ rows }: { rows: FailoverRow[] }) {
  if (rows.length === 0) return null
  return (
    <Card className="mt-6 p-4">
      <h2 className="mb-2 text-sm font-medium">Recent failovers</h2>
      <ul className="flex flex-col gap-1 font-mono text-xs">
        {rows.map((row) => (
          <li key={row.id}>
            <Link to="/requests/$id" params={{ id: row.id }} className="underline">
              {failoverLabel(row)}
            </Link>
            <span className="ml-2 text-[hsl(var(--legend))]">
              {new Date(row.ts).toLocaleTimeString()} · {row.total_ms}ms
            </span>
          </li>
        ))}
      </ul>
    </Card>
  )
}
```

`ops-footer.tsx`:

```tsx
import { useHealthz } from "../../lib/queries"
import { droppedText } from "./overview-screen"

/**
 * Version, uptime and the dropped-record counter.
 *
 * The counter is the honest signal that usage figures are a lower bound, and
 * nothing else in the console surfaces it.
 */
export function OpsFooter() {
  const health = useHealthz()
  if (!health.data) return null
  const h = health.data
  const dropped = droppedText(h.log_records_dropped, h.log_records_written)
  return (
    <footer className="mt-8 border-t pt-3 font-mono text-xs text-[hsl(var(--legend))]">
      {h.version} · up {h.uptime} ·{" "}
      <span
        className={h.log_records_dropped > 0 ? "text-[hsl(var(--warning))]" : undefined}
      >
        {dropped}
      </span>
    </footer>
  )
}
```

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/overview/
git commit -m "feat(web): complete the overview screen"
```

---

### Task 12: Requests on a DataTable, with real filters and saved views

Spec §6.2. The screen currently uses the plain `Table` with five text inputs. It gains `darkraise-ui/data-table` for sorting, column visibility and CSV; comboboxes populated from live data; a time-range picker; saved views; and the "3 newer" pill.

One constraint from reading the library: **`DataTable` has no row-click prop.** Its props are `columns`, `data`, `isLoading`, `searchKey`, `searchPlaceholder`, `facets` and `virtualize`. Opening the trace therefore lives in a column — a trailing actions column whose cell is a button — rather than on the row. That is the deviation from fragment 03, and it is recorded here and in the DoD document rather than left for a reader to discover.

`failover` becomes a derived boolean column offered as a DataTable facet rather than a URL filter, because the API has no `attempts` filter and a client-side one written as a URL parameter would filter nothing on the server and silently disagree with the page it claims to describe.

**Files:**
- Modify: `web/src/features/requests/requests-screen.tsx`
- Create: `web/src/features/requests/saved-views.ts`
- Create: `web/src/features/requests/saved-views.test.ts`
- Create: `web/src/features/requests/requests-table.test.tsx`
- Test: `web/src/lib/search-filters.test.ts`

**Interfaces:**
- Consumes: `DataTable`, `ColumnHeader`, `exportToCsv` from `darkraise-ui/data-table`; `Select` and `ToggleGroup` from `darkraise-ui`; `useSearchFilters` with the widened field list.
- Produces: `type SavedView = { name: string; filters: Record<string, string> }` declared in `api-types.ts` beside the request types — not a wire type, but a shape two modules share, and a second definition is how the two drift — then re-exported from `saved-views.ts` alongside `loadSavedViews(): SavedView[]`, `saveView(name: string, filters: Record<string, string>): SavedView[]`, `deleteView(name: string): SavedView[]`, all over `localStorage["darkrouter_saved_views"]`. From `requests-screen.tsx`: `newerCount(firstPage: RequestRow[], heldNewestId: string): number` and `optionsFrom(rows: RequestRow[], field: keyof RequestRow): string[]`.
- Field list becomes `["provider","model","status","alias","surface","error_code","since_ms","until_ms"] as const`.

- [ ] **Step 1: Write the failing tests**

`web/src/features/requests/saved-views.test.ts`:

```ts
import { beforeEach, describe, expect, it } from "vitest"
import { deleteView, loadSavedViews, saveView } from "./saved-views"

beforeEach(() => localStorage.clear())

describe("saved views", () => {
  it("round-trips a view through localStorage", () => {
    saveView("errors today", { status: "error", since_ms: "1724630400000" })
    expect(loadSavedViews()).toEqual([
      { name: "errors today", filters: { status: "error", since_ms: "1724630400000" } },
    ])
  })

  it("replaces a view of the same name rather than duplicating it", () => {
    saveView("mine", { status: "error" })
    saveView("mine", { status: "success" })
    const views = loadSavedViews()
    expect(views).toHaveLength(1)
    expect(views[0]?.filters.status).toBe("success")
  })

  it("drops empty filters so a saved view carries only what it filters", () => {
    saveView("providers", { provider: "groq", model: "" })
    expect(loadSavedViews()[0]?.filters).toEqual({ provider: "groq" })
  })

  it("deletes by name", () => {
    saveView("a", { status: "error" })
    saveView("b", { status: "success" })
    expect(deleteView("a").map((v) => v.name)).toEqual(["b"])
  })

  it("survives corrupt storage rather than throwing on every render", () => {
    // Anything can write to localStorage, including an older build of this
    // app. A parse failure must lose the views, not the screen.
    localStorage.setItem("darkrouter_saved_views", "{not json")
    expect(loadSavedViews()).toEqual([])
  })
})
```

`web/src/features/requests/requests-table.test.tsx`:

```ts
import { describe, expect, it } from "vitest"
import { newerCount, optionsFrom } from "./requests-screen"
import type { RequestRow } from "../../lib/api-types"

const row = (over: Partial<RequestRow> & { id: string }): RequestRow => ({
  ts_ms: 0, dialect: "openai", surface: "llm", model: "m", status: "success",
  tokens_in: 0, tokens_out: 0, cache_read_tokens: 0,
  cost_micros: null, ttft_ms: null, total_ms: null, attempts: 1,
  ...over,
})

describe("the newer pill", () => {
  it("counts rows ahead of the one the reader is anchored to", () => {
    // The poll must not shift the scroll position out from under a reader,
    // so new rows are counted and held rather than inserted.
    const page = [row({ id: "c" }), row({ id: "b" }), row({ id: "a" })]
    expect(newerCount(page, "a")).toBe(2)
  })

  it("counts nothing when the anchor is still the newest", () => {
    expect(newerCount([row({ id: "a" })], "a")).toBe(0)
  })

  it("counts nothing before the first page has an anchor", () => {
    expect(newerCount([row({ id: "a" })], "")).toBe(0)
  })

  it("counts the whole page when the anchor has aged out of it", () => {
    // Retention or a long absence: the anchor is gone, and claiming zero new
    // rows would be the one answer that is certainly wrong.
    expect(newerCount([row({ id: "c" }), row({ id: "b" })], "gone")).toBe(2)
  })
})

describe("filter options", () => {
  it("offers each distinct value once, sorted", () => {
    const rows = [
      row({ id: "1", provider: "nebius" }),
      row({ id: "2", provider: "groq" }),
      row({ id: "3", provider: "groq" }),
    ]
    expect(optionsFrom(rows, "provider")).toEqual(["groq", "nebius"])
  })

  it("omits rows where the field is absent", () => {
    // A request nothing served has no provider, and an empty option would
    // filter on the empty string, which matches nothing.
    expect(optionsFrom([row({ id: "1" })], "provider")).toEqual([])
  })
})
```

Append to `web/src/lib/search-filters.test.ts`:

```ts
it("keeps the query string free of empty filters", () => {
  expect(filterQuery({ provider: "groq", model: "", status: "error" })).toBe(
    "?provider=groq&status=error",
  )
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- saved-views requests-table search-filters`

Expected: FAIL — `saved-views.ts` does not exist, and neither `newerCount` nor `optionsFrom` is exported.

- [ ] **Step 3: Implement**

`saved-views.ts`:

```ts
import type { SavedView } from "../../lib/api-types"

const KEY = "darkrouter_saved_views"

export type { SavedView }

export function loadSavedViews(): SavedView[] {
  try {
    const raw = localStorage.getItem(KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? (parsed as SavedView[]) : []
  } catch {
    // Anything can write here, including an older build of this app. Losing
    // the views beats throwing on every render of the screen.
    return []
  }
}

function write(views: SavedView[]): SavedView[] {
  try {
    localStorage.setItem(KEY, JSON.stringify(views))
  } catch {
    // A full or blocked store is not a reason to refuse the filter change the
    // operator actually asked for.
  }
  return views
}

export function saveView(name: string, filters: Record<string, string>): SavedView[] {
  const kept = Object.fromEntries(Object.entries(filters).filter(([, v]) => v !== ""))
  const rest = loadSavedViews().filter((v) => v.name !== name)
  return write([...rest, { name, filters: kept }])
}

export function deleteView(name: string): SavedView[] {
  return write(loadSavedViews().filter((v) => v.name !== name))
}
```

In `requests-screen.tsx`, add the two pure functions:

```ts
/**
 * How many rows arrived ahead of the one the reader is anchored to.
 *
 * Counted rather than inserted: rows appearing at the top would shift the
 * scroll position out from under someone reading, which is the one thing a
 * three-second poll must not do.
 */
export function newerCount(firstPage: RequestRow[], heldNewestId: string): number {
  if (heldNewestId === "") return 0
  const i = firstPage.findIndex((r) => r.id === heldNewestId)
  // Not on the page at all: retention or a long absence carried it away, and
  // reporting zero would be the one answer that is certainly wrong.
  return i === -1 ? firstPage.length : i
}

/** Distinct values for a combobox, drawn from what the log actually holds. */
export function optionsFrom(rows: RequestRow[], field: keyof RequestRow): string[] {
  const seen = new Set<string>()
  for (const row of rows) {
    const value = row[field]
    if (typeof value === "string" && value !== "") seen.add(value)
  }
  return [...seen].sort()
}
```

Then the screen:

- **Filters.** `provider`, `model`, `alias`, `surface` and `error_code` become `Select` controls whose options come from `optionsFrom(rows, field)` plus a blank "any" entry; `status` is a fixed `Select` over `success` and `error`. A time range is a `ToggleGroup` over 1h, 24h, 7d and All, writing `since_ms` as `String(Date.now() - windowMs)` and clearing it for All. Every one of them goes through the existing `onFilter`, which resets the cursor.
- **Table.** Replace the `<Table>` block with `<DataTable columns={columns} data={rows} facets={["surface", "status", "failover"]} virtualize={{ rowHeight: 36, height: 640 }} />`. Rows are preprocessed into a shape carrying the scalar facet fields: `{...r, failover: r.attempts > 1 ? "failover" : "single"}`. Columns are built with `ColumnHeader` for the sortable ones — time, model, provider, status, attempts, tokens, latency — plus a `path` column rendering a `passthrough`/`translated` badge, and a trailing actions column whose cell is a `Button` calling `setSelected(row.original.id)`.
- **CSV.** A toolbar button calling `exportToCsv(rows, "requests.csv", columns.map(…))` with the same column list the table shows.
- **Saved views.** A row of `Button`s from `loadSavedViews()`, each applying its filter set through the same `onFilter` path; a "Save this view" button prompting for a name via a small inline input, not `window.prompt`.
- **Newer pill.** Hold the newest id in state, set on first load and on each click of the pill. When `newerCount(first.data.requests, held) > 0`, render a pill reading "N newer" whose click sets `held` to the current newest id, which is what lets the table render them.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck && npm run build`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/requests/ web/src/lib/api-types.ts web/src/lib/search-filters.test.ts
git commit -m "feat(web): rebuild requests on the data table"
```

---

### Task 13: Trace drawer completion

Spec §6.3 wants four things the drawer lacks: a latency waterfall, a Bodies panel that explains itself, surface metadata, and an Open-in-playground button.

One simplification, stated because §6.3 asks for more than the data supports: the spec describes a waterfall "showing connect, time-to-first-token and total per attempt". `TraceAttempt` carries only `latency_ms`; connect timing is not recorded anywhere, and per-attempt TTFT is not either. The waterfall is therefore drawn at request level from `ttft_ms` and `total_ms`, and the per-attempt latency bars stay where they already are, on the ladder rows. This deviation is recorded in the DoD document.

**Files:**
- Modify: `web/src/features/requests/trace-drawer.tsx`
- Test: `web/src/features/requests/trace-drawer.test.tsx`

**Interfaces:**
- Consumes: `RequestTrace.ttft_ms`, `.total_ms`, `.bodies`, `.surface_meta`; the `Link` from TanStack Router.
- Produces: `waterfallRows(trace: Pick<RequestTrace, "ttft_ms" | "total_ms">): { label: string; ms: number; fraction: number }[]` and `<BodiesPanel bodies={trace.bodies} />`, both exported from `trace-drawer.tsx`. The playground link is `/playground?seed={id}`.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/requests/trace-drawer.test.tsx`:

```tsx
describe("the waterfall", () => {
  it("scales time-to-first-token against the total", () => {
    expect(waterfallRows({ ttft_ms: 250, total_ms: 1000 })).toEqual([
      { label: "time to first token", ms: 250, fraction: 0.25 },
      { label: "total", ms: 1000, fraction: 1 },
    ])
  })

  it("renders no ttft row for a unary response", () => {
    // A non-streamed request has no first token, and a zero-width bar would
    // read as an instant one.
    expect(waterfallRows({ ttft_ms: null, total_ms: 900 })).toEqual([
      { label: "total", ms: 900, fraction: 1 },
    ])
  })

  it("renders nothing at all when the request never completed", () => {
    expect(waterfallRows({ ttft_ms: null, total_ms: null })).toEqual([])
  })

  it("clamps a ttft that exceeds the total rather than overflowing the bar", () => {
    // Clock skew across the two measurements is possible, and a fraction
    // above one draws a bar outside its own track.
    const rows = waterfallRows({ ttft_ms: 1200, total_ms: 1000 })
    expect(rows[0]?.fraction).toBe(1)
  })
})

describe("the bodies panel", () => {
  it("explains an empty panel instead of drawing an empty box", () => {
    // capture.bodies has a retention sweep and no writer, so this is the
    // permanent state today. §2 makes saying so the requirement.
    render(<BodiesPanel bodies={undefined} />)
    expect(screen.getByText(/capture\.bodies/i)).toBeInTheDocument()
    expect(screen.getByText(/not captured/i)).toBeInTheDocument()
  })

  it("says the same thing for an empty list as for an absent one", () => {
    render(<BodiesPanel bodies={[]} />)
    expect(screen.getByText(/not captured/i)).toBeInTheDocument()
  })

  it("renders each captured body under its kind", () => {
    render(<BodiesPanel bodies={[{ kind: "request", content: "{\"a\":1}" }]} />)
    expect(screen.getByText("request")).toBeInTheDocument()
    expect(screen.getByText(/"a":1/)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- trace-drawer`

Expected: FAIL — neither `waterfallRows` nor `BodiesPanel` is exported.

- [ ] **Step 3: Implement**

```tsx
/**
 * The request-level waterfall.
 *
 * §6.3 describes connect, first-token and total per attempt. Only the request
 * carries a first-token measurement and nothing records connect timing at all,
 * so this draws the two facts that exist; per-attempt duration stays on the
 * ladder rows, which is where it already is.
 */
export function waterfallRows(
  trace: Pick<RequestTrace, "ttft_ms" | "total_ms">,
): { label: string; ms: number; fraction: number }[] {
  const total = trace.total_ms
  if (total === null || total <= 0) return []
  const rows: { label: string; ms: number; fraction: number }[] = []
  if (trace.ttft_ms !== null) {
    rows.push({
      label: "time to first token",
      ms: trace.ttft_ms,
      // Clamped: the two measurements can skew, and a fraction above one
      // draws a bar outside its own track.
      fraction: Math.min(1, trace.ttft_ms / total),
    })
  }
  rows.push({ label: "total", ms: total, fraction: 1 })
  return rows
}

export function BodiesPanel({ bodies }: { bodies?: TraceBody[] }) {
  if (bodies === undefined || bodies.length === 0) {
    return (
      <p className="text-sm text-[hsl(var(--muted-foreground))]">
        Bodies were not captured. <code>capture.bodies</code> has a retention
        sweep and no writer, so nothing in the gateway records them yet — this
        panel is empty for every request, not just this one.
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-3">
      {bodies.map((b) => (
        <div key={b.kind}>
          <p className="text-xs text-[hsl(var(--legend))]">{b.kind}</p>
          <pre className="mt-1 overflow-x-auto rounded bg-[hsl(var(--muted))] p-3 font-mono text-xs">
            {b.content}
          </pre>
        </div>
      ))}
    </div>
  )
}
```

In the drawer's section list, between Attempts and Skipped candidates, add the waterfall — two labelled tracks, each an outer div at `bg-[hsl(var(--muted))]` and an inner div at `width: ${fraction * 100}%` — then, after Warnings, a `<section>` for Bodies wrapping `<BodiesPanel bodies={trace.data.bodies} />`, and a `<section>` for surface metadata rendering `trace.data.surface_meta` as a `<dl>`, skipped entirely when absent.

In the header, beside the request id, add:

```tsx
        <Link
          to="/playground"
          search={{ seed: trace.data.id }}
          className="text-sm underline"
        >
          Open in playground
        </Link>
```

Task 21 is what makes `?seed=` do anything; the link is inert until then, which is why the round-trip is verified in Task 21's step rather than here.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/requests/trace-drawer.tsx web/src/features/requests/trace-drawer.test.tsx
git commit -m "feat(web): waterfall, bodies and surface meta in the trace"
```

---

### Task 14: Usage as a time series with click-through

Spec §6.4: a range picker, requests and tokens over time stacked by provider, cost over time, and ranked tables where clicking a row lands in Requests already filtered.

The range picker's widest option is **365d**, labelled as such. The endpoint serves 365 days and no more, so labelling that button "All" would claim a completeness the data does not have — the same reason the screen prints `—` rather than `$0.00`.

The screen keeps its `.chart-scope` class. That override is load-bearing: without it the engine's ramp puts `--chart-4` and `--chart-5` in the orange and lime neighbourhood, which on this screen read as the reserved cooling amber and healthy green, so a series would look like a state.

**Files:**
- Modify: `web/src/features/usage/usage-screen.tsx`
- Test: `web/src/features/usage/usage-screen.test.ts`

**Interfaces:**
- Consumes: `useUsage({ dimension, days })` from Task 9; `ChartContainer`, `ChartTooltip`, `ChartTooltipContent` from `darkraise-ui/components/chart`; `AreaChart`, `Area`, `Line`, `LineChart`, `XAxis`, `YAxis` from `recharts`.
- Produces, exported from `usage-screen.tsx`: `RANGES: readonly { value: string; label: string; days: number }[]`, `topKeys(rows: UsageRow[], n: number): string[]`, `stackByDay(rows: UsageRow[], keys: string[], value: (r: UsageRow) => number): Record<string, number | string>[]`, and `requestsHref(dimension: UsageDimension, key: string, days: number): string`.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/usage/usage-screen.test.ts`:

```ts
const at = (day: string, key: string, requests: number, cost: number | null = null): UsageRow => ({
  day, key, requests, attempts: requests, tokens_in: 0, tokens_out: 0, cost_micros: cost,
})

describe("stackByDay", () => {
  it("pivots keys into one column each, in day order", () => {
    expect(
      stackByDay(
        [at("2026-08-25", "groq", 3), at("2026-08-25", "nebius", 1), at("2026-08-26", "groq", 2)],
        ["groq", "nebius"],
        (r) => r.requests,
      ),
    ).toEqual([
      { day: "2026-08-25", groq: 3, nebius: 1 },
      { day: "2026-08-26", groq: 2, nebius: 0 },
    ])
  })

  it("zero-fills a key absent on a day rather than leaving a gap", () => {
    // A stacked area with a missing key renders a hole through the stack,
    // which reads as traffic stopping everywhere rather than at one provider.
    const out = stackByDay([at("2026-08-25", "groq", 3)], ["groq", "nebius"], (r) => r.requests)
    expect(out[0]?.nebius).toBe(0)
  })

  it("ignores a key not in the series list", () => {
    const out = stackByDay(
      [at("2026-08-25", "groq", 3), at("2026-08-25", "other", 9)],
      ["groq"],
      (r) => r.requests,
    )
    expect(out[0]).toEqual({ day: "2026-08-25", groq: 3 })
  })
})

describe("topKeys", () => {
  it("ranks by total volume and caps the series count", () => {
    // Five is the ramp's width. A sixth series would reuse a fill and two
    // providers would be indistinguishable.
    const rows = ["a", "b", "c", "d", "e", "f"].map((k, i) => at("2026-08-25", k, i + 1))
    expect(topKeys(rows, 5)).toEqual(["f", "e", "d", "c", "b"])
  })
})

describe("row click-through", () => {
  it("lands in Requests filtered by the dimension that was clicked", () => {
    const href = requestsHref("provider", "groq", 7)
    expect(href).toContain("/requests")
    expect(href).toContain("provider=groq")
    expect(href).toContain("since_ms=")
  })

  it("filters by alias when the alias dimension is showing", () => {
    expect(requestsHref("alias", "fast", 30)).toContain("alias=fast")
  })
})

describe("the range picker", () => {
  it("labels its widest option by its actual span", () => {
    // The endpoint serves 365 days. Calling that "all" claims a completeness
    // the data does not have.
    const widest = RANGES[RANGES.length - 1]
    expect(widest?.days).toBe(365)
    expect(widest?.label).not.toMatch(/all/i)
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- usage`

Expected: FAIL — none of the four is exported.

- [ ] **Step 3: Implement**

```ts
export const RANGES = [
  { value: "7", label: "7d", days: 7 },
  { value: "30", label: "30d", days: 30 },
  { value: "90", label: "90d", days: 90 },
  // The endpoint serves 365 days and no more. Labelling this "all" would
  // claim a completeness the data does not have.
  { value: "365", label: "365d", days: 365 },
] as const

/** The busiest keys, capped at the ramp's width — a sixth series would reuse
 *  a fill and two providers would be indistinguishable. */
export function topKeys(rows: UsageRow[], n: number): string[] {
  const total = new Map<string, number>()
  for (const r of rows) {
    if (!r.key) continue
    total.set(r.key, (total.get(r.key) ?? 0) + r.requests)
  }
  return [...total.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, n)
    .map(([k]) => k)
}

export function stackByDay(
  rows: UsageRow[],
  keys: string[],
  value: (r: UsageRow) => number,
): Record<string, number | string>[] {
  const byDay = new Map<string, Record<string, number | string>>()
  for (const r of rows) {
    if (!r.key || !keys.includes(r.key)) continue
    let day = byDay.get(r.day)
    if (!day) {
      // Zero-filled: a stacked area with a missing key renders a hole through
      // the stack, which reads as traffic stopping everywhere at once.
      day = { day: r.day }
      for (const k of keys) day[k] = 0
      byDay.set(r.day, day)
    }
    day[r.key] = (day[r.key] as number) + value(r)
  }
  return [...byDay.values()].sort((a, b) => String(a.day).localeCompare(String(b.day)))
}

export function requestsHref(
  dimension: UsageDimension,
  key: string,
  days: number,
): string {
  const since = Date.now() - days * 24 * 60 * 60 * 1000
  return `/requests?${dimension}=${encodeURIComponent(key)}&since_ms=${Math.round(since)}`
}
```

The screen gains, in order: a `ToggleGroup` over `RANGES` driving a `days` state passed as `useUsage({ dimension, days })`; a requests chart and a tokens chart, both `ChartContainer` wrapping a recharts `AreaChart` over `stackByDay(rows, topKeys(rows, 5), …)` with one stacked `<Area>` per key; a cost chart as a `LineChart` over the same pivot summing `cost_micros`; and the ranked table's first cell becomes a `<Link to={requestsHref(dimension, r.key, days)}>` when a dimension is selected, staying plain text on the day view where there is nothing to filter by. `Bars`, `summarise` and `formatCost` stay as they are.

Keep `.chart-scope` on the wrapper around every chart.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck && npm run build`

Expected: PASS. recharts resolves through darkraise-ui's optional peer, and it is already a dependency.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/usage/
git commit -m "feat(web): usage over time with click-through"
```

---

### Task 15: Models gains facets, provenance columns and an override editor

Spec §6.6: facets instead of two text boxes, columns for pricing, max output, publisher and merge source, and an override editor — `model_overrides` sits at the top of the merge precedence and has never had a writer anywhere in the product.

**Files:**
- Create: `web/src/features/models/override-editor.tsx`
- Create: `web/src/features/models/override-editor.test.tsx`
- Modify: `web/src/features/models/models-screen.tsx`
- Test: `web/src/features/models/models-screen.test.ts`

**Interfaces:**
- Consumes: `Model.pricing`, `.publisher`, `.merge_source` from Task 7; `GET/PUT/DELETE /api/models/{provider}/{model}/override`, all three already registered; `DataTable` with `facets`; `useApiMutation`.
- Produces, from `models-screen.tsx`: `priceLabel(p: Pricing | null): string`, `priceBand(p: Pricing | null): string`, `facetRow(m: Model): Model & { surface_list: string; caps: string; band: string }`. From `override-editor.tsx`: `<OverrideEditor provider={string} model={string} onClose={() => void} />`.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/models/models-screen.test.ts`:

```ts
describe("price rendering", () => {
  it("prints dollars per million tokens", () => {
    expect(priceLabel({ input_micros: 150000, output_micros: 600000 })).toBe(
      "$0.15 / $0.60",
    )
  })

  it("prints an em-dash for an unpriced model", () => {
    // Not $0.00: a model with no catalog price cost an unknown amount, and
    // zero would claim it was free.
    expect(priceLabel(null)).toBe("—")
  })
})

describe("the price-band facet", () => {
  it("bands by input price", () => {
    expect(priceBand({ input_micros: 150000, output_micros: 0 })).toBe("under $1/MTok")
    expect(priceBand({ input_micros: 3000000, output_micros: 0 })).toBe("$1–$5/MTok")
    expect(priceBand({ input_micros: 9000000, output_micros: 0 })).toBe("over $5/MTok")
  })

  it("bands an unpriced model as unpriced rather than as free", () => {
    expect(priceBand(null)).toBe("unpriced")
  })
})

describe("facet rows", () => {
  it("flattens list fields into scalars a facet can group by", () => {
    // DataTable facets take a scalar column. An array renders as one distinct
    // value per permutation, which is one facet entry per row.
    const row = facetRow({
      model: "m", providers: ["groq"], surfaces: ["llm", "embedding"],
      context_window: 128000, max_output_tokens: 4096,
      tools: true, vision: false, reasoning: false,
      inferred: false, state: "live", pricing: null, merge_source: "discovered",
    })
    expect(row.surface_list).toBe("llm, embedding")
    expect(row.caps).toBe("tools")
    expect(row.band).toBe("unpriced")
  })

  it("says none rather than blank when a model declares no capabilities", () => {
    const row = facetRow({
      model: "m", providers: [], surfaces: [], context_window: 0,
      max_output_tokens: 0, tools: false, vision: false, reasoning: false,
      inferred: false, state: "live", pricing: null, merge_source: "inferred",
    })
    // An empty facet value groups every capability-less model under a blank
    // label, which reads as a broken facet rather than as a real category.
    expect(row.caps).toBe("none")
  })
})
```

Create `web/src/features/models/override-editor.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { OverrideEditor } from "./override-editor"

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

beforeEach(() => vi.unstubAllGlobals())

describe("the override editor", () => {
  it("loads the current override and offers it for editing", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      new Response(JSON.stringify({ context_window: 64000, capabilities: { tools: true } }), {
        status: 200, headers: { "Content-Type": "application/json" },
      }),
    ))
    mount(<OverrideEditor provider="groq" model="m" onClose={() => {}} />)
    await waitFor(() =>
      expect(screen.getByLabelText(/context window/i)).toHaveValue(64000),
    )
  })

  it("treats a 404 as no override rather than as an error", async () => {
    // A model with no override is the normal case, and an error banner over
    // the normal case teaches the operator to ignore banners.
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })))
    mount(<OverrideEditor provider="groq" model="m" onClose={() => {}} />)
    await waitFor(() =>
      expect(screen.getByLabelText(/context window/i)).toHaveValue(null),
    )
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("sends a PUT carrying only the fields that were set", async () => {
    const fetchMock = vi.fn(async () =>
      new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }),
    )
    vi.stubGlobal("fetch", fetchMock)
    mount(<OverrideEditor provider="groq" model="m" onClose={() => {}} />)
    await userEvent.type(await screen.findByLabelText(/context window/i), "32000")
    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "PUT")
      expect(put).toBeDefined()
      const body = JSON.parse((put?.[1] as RequestInit).body as string)
      expect(body).toEqual({ context_window: 32000 })
    })
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- models override-editor`

Expected: FAIL — the helpers are not exported and the editor does not exist.

- [ ] **Step 3: Implement**

```ts
export function priceLabel(p: Pricing | null): string {
  // Unpriced is not free. An em-dash is the same claim the spend tile and
  // every cost cell already make.
  if (p === null) return "—"
  const dollars = (micros: number) => `$${(micros / 1_000_000).toFixed(2)}`
  return `${dollars(p.input_micros)} / ${dollars(p.output_micros)}`
}

export function priceBand(p: Pricing | null): string {
  if (p === null) return "unpriced"
  const perMTok = p.input_micros / 1_000_000
  if (perMTok < 1) return "under $1/MTok"
  if (perMTok <= 5) return "$1–$5/MTok"
  return "over $5/MTok"
}

export function facetRow(m: Model) {
  const caps = [
    m.tools && "tools",
    m.vision && "vision",
    m.reasoning && "reasoning",
  ].filter(Boolean) as string[]
  return {
    ...m,
    surface_list: m.surfaces.join(", "),
    // "none" rather than blank: an empty facet value groups every
    // capability-less model under a label that reads as a broken facet.
    caps: caps.length > 0 ? caps.join(", ") : "none",
    band: priceBand(m.pricing),
  }
}
```

The screen swaps its `Table` for `<DataTable data={models.map(facetRow)} columns={columns} facets={["surface_list", "state", "caps", "band", "merge_source"]} searchKey="model" virtualize={{ rowHeight: 40, height: 640 }} />`. Columns are model, the compressed `<Ladder>` (unchanged), context window, max output, price via `priceLabel`, publisher, capabilities badges (unchanged, `inferred` still amber), state, merge source as a mono badge, and a trailing actions column whose button opens the override editor in a `Sheet` for that model's first provider.

`override-editor.tsx` loads with `useQuery` keyed `keys.override(provider, model)`, treating a 404 as `null` rather than an error; renders labelled `Input`s for context window and three capability `Switch`es plus a surfaces text field; saves with `useApiMutation` PUTting only the fields that were touched and invalidating `[keys.models, keys.override(provider, model)]`; and offers a Remove button issuing the DELETE. Every input carries a `<Label htmlFor>` — the tests select by label, and so does anyone using a screen reader.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/models/
git commit -m "feat(web): model facets, provenance and overrides"
```

---

### Task 16: Routing gains a policy editor and drag-reorderable chains

Spec §6.7: the policy knobs with hot-reloadable and restart-only marked distinctly, and alias chains as editable ordered lists with drag-to-reorder and validation for dangling targets.

`policy.timeout.connect` and `first_byte` configure the one shared transport built at startup. They are in `config.RestartOnly`, and `PUT /api/policy` refuses them. The editor therefore renders them **disabled with a restart badge** rather than as fields that accept a value and throw it away — offering an input the API will refuse is a lie the screen tells before the server gets a chance to.

The client-side chain validation is a convenience, not the authority. The server validates on `PUT /api/aliases` and remains the last word; the browser check exists so a typo is caught before a round trip.

**Files:**
- Create: `web/src/features/routing/policy-editor.tsx`
- Create: `web/src/features/routing/policy-editor.test.tsx`
- Modify: `web/src/features/routing/routing-screen.tsx`
- Test: `web/src/features/routing/routing-screen.test.ts`

**Interfaces:**
- Consumes: `usePolicy()` and `keys.policy` from Task 9; `PolicyBlock`; `PUT /api/policy`; `useProviders()` for the known-provider list.
- Produces, from `routing-screen.tsx`: `moveTarget(chain: string[], from: number, to: number): string[]` and `validateChain(targets: string[], knownProviders: string[]): string[]`. From `policy-editor.tsx`: `<PolicyEditor />` and `RESTART_ONLY_TIMEOUTS: readonly string[]`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/routing/routing-screen.test.ts` additions:

```ts
describe("moveTarget", () => {
  it("reorders without mutating the original chain", () => {
    const chain = ["a", "b", "c"]
    expect(moveTarget(chain, 2, 0)).toEqual(["c", "a", "b"])
    expect(chain).toEqual(["a", "b", "c"])
  })

  it("is a no-op when a target is dropped on itself", () => {
    expect(moveTarget(["a", "b"], 1, 1)).toEqual(["a", "b"])
  })

  it("ignores an index outside the chain", () => {
    // A drop outside the list is a cancelled drag, not a reorder to position
    // minus one.
    expect(moveTarget(["a", "b"], 0, 5)).toEqual(["a", "b"])
  })
})

describe("validateChain", () => {
  it("names a qualified target whose provider is not configured", () => {
    expect(validateChain(["groq/m-a", "ghost/m-b"], ["groq"])).toEqual([
      "ghost/m-b: no provider named ghost is configured",
    ])
  })

  it("accepts a bare model target, which any provider may serve", () => {
    expect(validateChain(["m-a"], ["groq"])).toEqual([])
  })

  it("refuses an empty chain, which routes nowhere", () => {
    expect(validateChain([], ["groq"])).toEqual([
      "an alias with no targets routes nowhere",
    ])
  })

  it("reports every problem rather than stopping at the first", () => {
    // A save that fixes one typo and fails on the next reads as the editor
    // refusing arbitrarily.
    expect(validateChain(["x/m", "y/m"], ["groq"])).toHaveLength(2)
  })
})
```

Create `web/src/features/routing/policy-editor.test.tsx`:

```tsx
describe("the policy editor", () => {
  it("offers the two timeouts a running request re-reads", async () => {
    mountWithPolicy()
    expect(await screen.findByLabelText(/total/i)).toBeEnabled()
    expect(screen.getByLabelText(/idle/i)).toBeEnabled()
  })

  it("disables the two that need a restart, and says why", async () => {
    // These configure the one shared transport built at startup. An input
    // that accepts a value the API refuses is a lie the screen tells first.
    mountWithPolicy()
    expect(await screen.findByLabelText(/connect/i)).toBeDisabled()
    expect(screen.getByLabelText(/first byte/i)).toBeDisabled()
    expect(screen.getAllByText(/restart/i).length).toBeGreaterThan(0)
  })

  it("names the four fields the API accepts", () => {
    expect(RESTART_ONLY_TIMEOUTS).toEqual(["connect", "first_byte"])
  })
})
```

`mountWithPolicy` is a local helper stubbing `fetch` to return a `PolicyBlock` and wrapping in a `QueryClientProvider`, the same shape Task 15's `mount` uses.

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- routing policy-editor`

Expected: FAIL — neither pure function is exported and the editor does not exist.

- [ ] **Step 3: Implement**

```ts
/** Reorder one target. Returns a new array: the draft is React state, and a
 *  mutation in place would not re-render. */
export function moveTarget(chain: string[], from: number, to: number): string[] {
  if (from === to) return chain
  if (from < 0 || to < 0 || from >= chain.length || to >= chain.length) return chain
  const next = [...chain]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  return next
}

/**
 * Problems a browser can see without asking the server.
 *
 * The server validates on PUT and stays the authority; this exists so a typo
 * is caught before a round trip rather than instead of one.
 */
export function validateChain(targets: string[], knownProviders: string[]): string[] {
  if (targets.length === 0) return ["an alias with no targets routes nowhere"]
  const problems: string[] = []
  for (const target of targets) {
    const slash = target.indexOf("/")
    // A bare model name is not qualified, so any provider offering it may
    // serve — there is nothing to check.
    if (slash < 0) continue
    const provider = target.slice(0, slash)
    if (!knownProviders.includes(provider)) {
      problems.push(`${target}: no provider named ${provider} is configured`)
    }
  }
  return problems
}
```

`policy-editor.tsx` renders a card under the preview with `RESTART_ONLY_TIMEOUTS = ["connect", "first_byte"] as const`; labelled numeric inputs for `cooldown.trip_after`, `cooldown.max`, `retry.max_attempts`, `timeout.total` and `timeout.idle`; disabled inputs carrying a `<Badge variant="secondary">restart</Badge>` for `timeout.connect` and `timeout.first_byte`; and a Save calling `useApiMutation` that PUTs `{cooldown, retry, timeout: {total, idle}}` — the two restart-only keys are omitted from the payload rather than sent and refused — invalidating `[keys.policy, keys.config]`.

In `routing-screen.tsx`, `AliasEditor` gains: draggable `<li>` per target with `draggable`, `onDragStart`, `onDragOver` and `onDrop` calling `moveTarget`; the `validateChain` output rendered inline above the Save button, with Save disabled while any problem stands; and the `window.prompt("Alias name")` replaced by an inline `Input` and Add button, because a browser prompt cannot be styled, cannot be tested, and is blocked outright in some embeddings.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/routing/
git commit -m "feat(web): policy editor and reorderable alias chains"
```

---

### Task 17: Providers gains a preset browser, credential forms and discovery health

Spec §6.5, and the largest single task here. Adding a provider becomes a real browser over all the shipped presets — searchable, filterable by surface, auth kind and free tier, linking to each provider's website — with a raw form behind it. `GET /api/presets` has always returned the full shape; only the frontend type was narrow, and Task 9 widened it.

**Files:**
- Create: `web/src/features/providers/add-provider-dialog.tsx`
- Create: `web/src/features/providers/add-provider-dialog.test.tsx`
- Modify: `web/src/features/providers/providers-screen.tsx`
- Modify: `web/src/features/providers/provider-detail.tsx`
- Test: `web/src/features/providers/providers-screen.test.ts`

**Interfaces:**
- Consumes: `usePresets()` now returning the full `Preset`; `useDiscoveryHealth()` from Task 9; `POST /api/providers`, which already accepts `kind`, `base_url`, `auth_style`, `priority`, `enabled`, `region`, `project` and `location`; `POST /api/providers/{id}/keys`, `PATCH .../keys/{keyId}`, `DELETE .../keys/{keyId}`; `POST /api/providers/{id}/test` returning `{ok, probe, latency_ms, model_count?, error?}`; `DELETE /api/providers/{id}` returning `{id, dangling_aliases}`.
- Produces, from `add-provider-dialog.tsx`: `filterPresets(presets: Preset[], f: { q?: string; surface?: string; authKind?: string; freeTier?: boolean }): Preset[]`, `createBodyFromPreset(preset: Preset, form: { id: string }): Record<string, unknown>`, and `<AddProviderDialog open onOpenChange />`. From `providers-screen.tsx`: `discoveryLine(row: DiscoveryHealthRow | undefined): string`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/providers/add-provider-dialog.test.tsx`:

```tsx
const preset = (over: Partial<Preset> & { id: string }): Preset => ({
  name: over.id, kind: "openaicompat", base_url: "https://x.example",
  surfaces: ["llm"], auth_kind: "bearer", website: "", free_tier: false,
  ...over,
})

describe("filterPresets", () => {
  it("matches the search against name and id, case-insensitively", () => {
    const out = filterPresets([preset({ id: "groq", name: "Groq" }), preset({ id: "nebius" })], {
      q: "GRO",
    })
    expect(out.map((p) => p.id)).toEqual(["groq"])
  })

  it("filters by declared surface", () => {
    const out = filterPresets(
      [preset({ id: "a", surfaces: ["llm"] }), preset({ id: "b", surfaces: ["embedding"] })],
      { surface: "embedding" },
    )
    expect(out.map((p) => p.id)).toEqual(["b"])
  })

  it("treats free tier as a narrowing switch, not a toggle between two sets", () => {
    // Unset shows everything. A false that hid free-tier providers would make
    // the filter impossible to clear.
    const all = [preset({ id: "a", free_tier: true }), preset({ id: "b" })]
    expect(filterPresets(all, {})).toHaveLength(2)
    expect(filterPresets(all, { freeTier: true }).map((p) => p.id)).toEqual(["a"])
  })

  it("combines every filter", () => {
    const out = filterPresets(
      [
        preset({ id: "groq", surfaces: ["llm"], auth_kind: "bearer", free_tier: true }),
        preset({ id: "grok", surfaces: ["llm"], auth_kind: "bearer" }),
      ],
      { q: "gro", surface: "llm", authKind: "bearer", freeTier: true },
    )
    expect(out.map((p) => p.id)).toEqual(["groq"])
  })
})

describe("createBodyFromPreset", () => {
  it("sends the preset name and lets the server supply the rest", () => {
    // The preset already carries kind, base_url and auth_style. Echoing them
    // back would freeze this provider against a later preset correction.
    expect(createBodyFromPreset(preset({ id: "groq" }), { id: "my-groq" })).toEqual({
      id: "my-groq",
      preset: "groq",
    })
  })
})

describe("the dialog", () => {
  // `mount` wraps in a QueryClientProvider and `stubPresets` stubs fetch to
  // return a presets envelope — both local to this file, the same two helpers
  // Task 15's override-editor test defines for itself. Two test files sharing
  // a mount helper through a third module is a dependency neither needs.
  it("shows a preset card per matching provider and creates from it", async () => {
    const fetchMock = stubPresets([preset({ id: "groq", name: "Groq" })])
    mount(<AddProviderDialog open onOpenChange={() => {}} />)
    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/provider id/i), "my-groq")
    await userEvent.click(screen.getByRole("button", { name: /create/i }))
    await waitFor(() => {
      const post = fetchMock.mock.calls.find(
        ([url, init]) => url === "/api/providers" && (init as RequestInit)?.method === "POST",
      )
      expect(JSON.parse((post?.[1] as RequestInit).body as string)).toEqual({
        id: "my-groq", preset: "groq",
      })
    })
  })
})
```

Append to `web/src/features/providers/providers-screen.test.ts`:

```ts
describe("the discovery line", () => {
  it("reports a healthy catalogue as live out of total", () => {
    expect(
      discoveryLine({
        provider_id: "groq", total: 40, live: 40, stale: 0,
        removed_upstream: 0, max_missing_streak: 0,
      }),
    ).toBe("40 of 40 live")
  })

  it("names the missing streak, which is the number that matters", () => {
    // A provider whose listing has been failing for six hours looks identical
    // to a healthy one until something counts the sweeps that omitted it.
    const line = discoveryLine({
      provider_id: "groq", total: 40, live: 30, stale: 10,
      removed_upstream: 0, max_missing_streak: 6,
    })
    expect(line).toContain("10 stale")
    expect(line).toContain("6")
  })

  it("says never discovered when the provider has no rows at all", () => {
    // Absence is the signal. "0 of 0 live" reads as a sweep that ran and
    // found nothing, which is a different fact.
    expect(discoveryLine(undefined)).toBe("never discovered")
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- providers add-provider`

Expected: FAIL — the dialog module and `discoveryLine` do not exist.

- [ ] **Step 3: Implement**

```ts
export function filterPresets(
  presets: Preset[],
  f: { q?: string; surface?: string; authKind?: string; freeTier?: boolean },
): Preset[] {
  const q = (f.q ?? "").toLowerCase()
  return presets.filter((p) => {
    if (q && !p.id.toLowerCase().includes(q) && !p.name.toLowerCase().includes(q)) return false
    if (f.surface && !p.surfaces.includes(f.surface)) return false
    if (f.authKind && p.auth_kind !== f.authKind) return false
    // Narrowing rather than a two-way toggle: false would hide free-tier
    // providers and leave the filter impossible to clear.
    if (f.freeTier && !p.free_tier) return false
    return true
  })
}

export function createBodyFromPreset(
  preset: Preset,
  form: { id: string },
): Record<string, unknown> {
  // The preset carries kind, base_url and auth_style already. Echoing them
  // back would freeze this provider against a later preset correction.
  return { id: form.id, preset: preset.id }
}
```

```ts
export function discoveryLine(row: DiscoveryHealthRow | undefined): string {
  // Absence is the signal: "0 of 0 live" would read as a sweep that ran and
  // found nothing, which is a different fact from one that never ran.
  if (!row) return "never discovered"
  const parts = [`${row.live} of ${row.total} live`]
  if (row.stale > 0) parts.push(`${row.stale} stale`)
  if (row.removed_upstream > 0) parts.push(`${row.removed_upstream} removed upstream`)
  if (row.max_missing_streak > 0) {
    parts.push(`missing for ${row.max_missing_streak} sweeps`)
  }
  return parts.join(" · ")
}
```

`add-provider-dialog.tsx` is a `Dialog` with two `Tabs`: **Browse**, holding a search `Input`, three `Select` facets, a `Switch` for free tier, and a scrollable grid of preset cards — each showing name, kind, surface badges, a free-tier badge, and an external `website` link with `rel="noreferrer"` — which on selection reveals a provider-id field and a Create button; and **Raw**, a form over `id`, `kind`, `base_url`, `auth_style`, `priority`, `enabled`, `region`, `project` and `location`, every one of which the create endpoint already accepts and none of which today's two-field form can express. Creation goes through `useApiMutation` invalidating `[keys.providers]`, and on success opens the new provider's detail page.

`providers-screen.tsx` gains an "Add provider" button in the header opening the dialog, and a discovery cell per row rendering `discoveryLine(discovery.data?.providers.find(d => d.provider_id === p.id))`, styled `text-[hsl(var(--warning))]` when `max_missing_streak > 0`.

`provider-detail.tsx` gains: `region` and `project` inputs saving through the existing `PATCH /api/providers/{id}`; an Add-credential form; a per-credential Replace-secret inline input issuing `PATCH .../keys/{keyId} {secret}`; a Remove button behind an `AlertDialog`; OAuth metadata on each credential row — a kind badge, the expiry as a local date, and the scope — from Task 6's fields; a probe result card rendering `{ok, latency_ms, model_count, error}` rather than discarding it into a toast; a discovery panel over `useDiscoveryHealth()`; and a Delete-provider button rendering the response's `dangling_aliases` as a warning toast, because an alias pointing at nothing is the consequence the operator needs to see at the moment they cause it.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck && npm run build`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/providers/
git commit -m "feat(web): preset browser and full provider detail"
```

---

### Task 18: Connect gains base URLs, snippets and live surfaces

Spec §6.9: base URLs per dialect, copyable; ready-made config snippets for Claude Code, Codex, Cursor, and the OpenAI and Anthropic SDKs; and which surfaces are live. Proxy-token management already works and stays as it is.

The base URLs are derived from the served routes rather than typed as prose, because a snippet that disagrees with the mux is worse than no snippet: it fails at the client, far from the screen that produced it. `internal/server/server.go` registers `/v1/chat/completions` and friends for OpenAI, `/v1/messages` for Anthropic, and `/v1beta/models/{model}` for Gemini, which is what fixes the three suffixes below.

**Files:**
- Create: `web/src/features/connect/snippets.ts`
- Create: `web/src/features/connect/snippets.test.ts`
- Modify: `web/src/features/connect/connect-screen.tsx`
- Test: `web/src/features/connect/connect-screen.test.ts`

**Interfaces:**
- Consumes: `useConfig()` for `blocks.server.proxy_listen`; `useModels()` for the live-surface union.
- Produces, from `snippets.ts`: `TOOLS: readonly ["claude-code","codex","cursor","openai-sdk","anthropic-sdk"]`, `baseUrlFor(origin: string, dialect: "openai"|"anthropic"|"gemini"): string`, `snippetFor(tool: Tool, baseUrl: string, token: string): string`. From `connect-screen.tsx`: `liveSurfaces(models: Model[]): string[]`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/connect/snippets.test.ts`:

```ts
import { describe, expect, it } from "vitest"
import { baseUrlFor, snippetFor, TOOLS } from "./snippets"

describe("baseUrlFor", () => {
  it("suffixes each dialect the way its routes are served", () => {
    // These come from the proxy mux, not from prose: a snippet that disagrees
    // with the served routes fails at the client, far from this screen.
    expect(baseUrlFor("http://127.0.0.1:8080", "openai")).toBe("http://127.0.0.1:8080/v1")
    expect(baseUrlFor("http://127.0.0.1:8080", "anthropic")).toBe("http://127.0.0.1:8080")
    expect(baseUrlFor("http://127.0.0.1:8080", "gemini")).toBe("http://127.0.0.1:8080/v1beta")
  })

  it("does not double a trailing slash", () => {
    expect(baseUrlFor("http://x/", "openai")).toBe("http://x/v1")
  })
})

describe("snippetFor", () => {
  it("points Claude Code at the anthropic base url", () => {
    const s = snippetFor("claude-code", "http://127.0.0.1:8080", "dr_tok")
    expect(s).toContain("ANTHROPIC_BASE_URL=http://127.0.0.1:8080")
    expect(s).toContain("dr_tok")
  })

  it("points the OpenAI SDK at the /v1 base url", () => {
    expect(snippetFor("openai-sdk", "http://127.0.0.1:8080/v1", "dr_tok")).toContain(
      "http://127.0.0.1:8080/v1",
    )
  })

  it("gives every tool a snippet", () => {
    // A tab that renders an empty block reads as a broken screen rather than
    // as a tool nobody wrote a snippet for.
    for (const tool of TOOLS) {
      expect(snippetFor(tool, "http://x", "t").trim()).not.toBe("")
    }
  })

  it("shows a placeholder rather than an empty value when no token exists", () => {
    // A fresh install has no proxy token, and a snippet ending in "=" is one
    // an operator will paste and then debug.
    expect(snippetFor("claude-code", "http://x", "")).toContain("<your-token>")
  })
})
```

Append to `web/src/features/connect/connect-screen.test.ts`:

```ts
describe("live surfaces", () => {
  it("unions the surfaces the catalog actually serves, sorted", () => {
    expect(
      liveSurfaces([
        { ...model("a"), surfaces: ["llm", "embedding"] },
        { ...model("b"), surfaces: ["llm"] },
      ]),
    ).toEqual(["embedding", "llm"])
  })

  it("is empty before anything is catalogued", () => {
    expect(liveSurfaces([])).toEqual([])
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- connect snippets`

Expected: FAIL — `snippets.ts` does not exist and `liveSurfaces` is not exported.

- [ ] **Step 3: Implement**

```ts
export const TOOLS = [
  "claude-code", "codex", "cursor", "openai-sdk", "anthropic-sdk",
] as const

export type Tool = (typeof TOOLS)[number]

/**
 * The base URL each dialect is served at.
 *
 * Derived from the routes the proxy mux registers rather than written as
 * prose: Anthropic is served at the root because its path is /v1/messages,
 * while the OpenAI SDK appends its own /chat/completions to a /v1 base.
 */
export function baseUrlFor(origin: string, dialect: "openai" | "anthropic" | "gemini"): string {
  const root = origin.replace(/\/+$/, "")
  switch (dialect) {
    case "openai":
      return `${root}/v1`
    case "gemini":
      return `${root}/v1beta`
    case "anthropic":
      return root
  }
}

export function snippetFor(tool: Tool, baseUrl: string, token: string): string {
  // A snippet ending in "=" is one an operator pastes and then debugs.
  const key = token || "<your-token>"
  switch (tool) {
    case "claude-code":
      return `export ANTHROPIC_BASE_URL=${baseUrl}
export ANTHROPIC_AUTH_TOKEN=${key}
claude`
    case "codex":
      return `export OPENAI_BASE_URL=${baseUrl}
export OPENAI_API_KEY=${key}
codex`
    case "cursor":
      return `Settings → Models → Override OpenAI Base URL
  ${baseUrl}
API key
  ${key}`
    case "openai-sdk":
      return `from openai import OpenAI

client = OpenAI(base_url="${baseUrl}", api_key="${key}")`
    case "anthropic-sdk":
      return `from anthropic import Anthropic

client = Anthropic(base_url="${baseUrl}", auth_token="${key}")`
  }
}
```

```ts
export function liveSurfaces(models: Model[]): string[] {
  const seen = new Set<string>()
  for (const m of models) for (const s of m.surfaces) seen.add(s)
  return [...seen].sort()
}
```

The screen gains three cards above the existing token management. **Base URLs**: three rows, each `baseUrlFor(origin, dialect)` in mono with a copy button calling `navigator.clipboard.writeText` and toasting on success — `origin` is `window.location.origin` when `proxy_listen` and `admin_listen` are the same port, and otherwise `http://` plus the host from the current location with `proxy_listen`'s port, which is the honest reading of a two-port deployment. **Client snippets**: `Tabs` over `TOOLS`, each rendering `<pre>{snippetFor(tool, baseUrlFor(origin, dialectOf(tool)), tokenPrefix)}</pre>` with a copy button. **Live surfaces**: badges from `liveSurfaces(models.data?.models ?? [])`, with a plain sentence when the catalog is empty — Task 20 is what swaps that sentence for a legend, and reaching forward to a component that does not exist yet would leave this task unable to compile on its own.

The token used in a snippet is a prefix, never a secret: the store holds a digest and cannot reproduce one. Say so beside the snippet, the way the minted-token card already does.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/connect/
git commit -m "feat(web): base urls, snippets and live surfaces"
```

---

### Task 19: Settings gains password change, reload and sync

Spec §6.10. Three endpoints exist and no screen calls them: `POST /api/auth/password` (minimum twelve characters, revokes every other session, returns `{revoked}`), `POST /api/config/reload` (returns `{valid:false, error, serving}` on a bad file, with a 200 — the reload happened and its outcome is the answer), and `POST /api/catalog/sync`.

**Files:**
- Modify: `web/src/features/settings/settings-screen.tsx`
- Test: `web/src/features/settings/settings-screen.test.ts`

**Interfaces:**
- Consumes: the three endpoints above; `useApiMutation`.
- Produces, exported from `settings-screen.tsx`: `passwordProblem(next: string, confirm: string): string | null`, `revokedText(revoked: number): string`, `reloadMessage(res: { valid: boolean; error?: string; serving?: string }): string`.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/settings/settings-screen.test.ts`:

```ts
describe("the password form", () => {
  it("refuses a short password before spending a round trip", () => {
    // The server's floor is twelve. Checking it here is a courtesy; the
    // server stays the authority.
    expect(passwordProblem("short", "short")).toMatch(/12 characters/)
  })

  it("refuses a mismatched confirmation", () => {
    expect(passwordProblem("long-enough-passphrase", "long-enough-passphras")).toMatch(
      /do not match/i,
    )
  })

  it("accepts a long matching pair", () => {
    expect(passwordProblem("long-enough-passphrase", "long-enough-passphrase")).toBeNull()
  })
})

describe("the revocation notice", () => {
  it("says how many other sessions were ended", () => {
    // The operator has just logged every other browser out. Not saying so
    // makes the next login failure elsewhere look like a fault.
    expect(revokedText(3)).toMatch(/3 other sessions/)
  })

  it("says none rather than zero", () => {
    expect(revokedText(0)).toMatch(/no other sessions/i)
  })

  it("says one session in the singular", () => {
    expect(revokedText(1)).toMatch(/1 other session\b/)
  })
})

describe("the reload result", () => {
  it("reports an invalid file without claiming the gateway stopped", () => {
    expect(
      reloadMessage({ valid: false, error: "yaml: bad", serving: "the previous configuration is still serving" }),
    ).toMatch(/previous configuration is still serving/)
  })

  it("carries the parse error so the operator knows what to fix", () => {
    expect(reloadMessage({ valid: false, error: "yaml: line 4" })).toContain("yaml: line 4")
  })

  it("confirms a clean reload", () => {
    expect(reloadMessage({ valid: true })).toMatch(/reloaded/i)
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- settings`

Expected: FAIL — none of the three is exported.

- [ ] **Step 3: Implement**

```ts
export function passwordProblem(next: string, confirm: string): string | null {
  // The server's floor, checked here as a courtesy. It remains the authority:
  // this is about not spending a round trip on a typo.
  if (next.length < 12) return "The new password must be at least 12 characters."
  if (next !== confirm) return "The two entries do not match."
  return null
}

export function revokedText(revoked: number): string {
  // The operator has just logged every other browser out. Not saying so makes
  // the next login failure elsewhere look like a fault.
  if (revoked === 0) return "Password changed. There were no other sessions to revoke."
  const plural = revoked === 1 ? "session" : "sessions"
  return `Password changed, and ${revoked} other ${plural} revoked.`
}

export function reloadMessage(res: { valid: boolean; error?: string; serving?: string }): string {
  if (res.valid) return "Configuration reloaded."
  // A 200 with valid:false is the honest shape: the reload was performed and
  // this is its outcome, not a failed request.
  return [res.error, res.serving ?? "the previous configuration is still serving"]
    .filter(Boolean)
    .join(" — ")
}
```

The screen gains an **Account** card with three labelled password fields — current, new, confirm — showing `passwordProblem` inline and disabling Save while it is non-null, posting through `useApiMutation` and toasting `revokedText(res.revoked)` on success while invalidating `[keys.sessions]`. The header gains two buttons: **Reload config**, posting to `/api/config/reload` and rendering `reloadMessage` — as a destructive banner when `valid` is false, since a toast for a config that is still broken disappears before it can be acted on — and invalidating `[keys.config]`; and **Sync catalog now**, posting to `/api/catalog/sync` and invalidating `[keys.models]`.

The file-owned config rows stay read-only. §8.1's source labels are already correct and this task does not touch them.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/settings/
git commit -m "feat(web): password change, reload and catalog sync"
```

---

### Task 20: First-run teaching and first-class empty states

Spec §6.11: "A fresh install with zero requests must not render as flat rectangles with faint grids, which is indistinguishable from broken equipment. Each empty well carries a legend explaining what it will show and a dimmed example."

The existing `FirstRun` covers only the no-password case. The zero-providers case is separate and lives on the Overview rather than gating the app — an operator with no providers is logged in and can reach every screen; showing them a wall instead would take the console away at the moment they need it.

**Files:**
- Create: `web/src/features/shell/first-run-providers.tsx`
- Create: `web/src/features/shell/empty-legend.tsx`
- Modify: `web/src/features/overview/overview-screen.tsx`
- Modify: `web/src/features/requests/requests-screen.tsx`, `web/src/features/usage/usage-screen.tsx`, `web/src/features/models/models-screen.tsx`, `web/src/features/connect/connect-screen.tsx`
- Test: `web/src/features/shell/first-run.test.tsx`

**Interfaces:**
- Produces: `<EmptyLegend what={string} hint={string} />` in `empty-legend.tsx`; `<FirstRunProviders onAdd={() => void} />` in `first-run-providers.tsx`.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/features/shell/first-run.test.tsx`:

```tsx
describe("the empty legend", () => {
  it("says what the well will show and what to do about it", () => {
    // A blank panel is indistinguishable from broken equipment, which is the
    // whole reason §6.11 makes empty states first-class.
    render(<EmptyLegend what="Requests appear here as clients call the gateway." hint="Point a client at Connect to see the first one." />)
    expect(screen.getByText(/requests appear here/i)).toBeInTheDocument()
    expect(screen.getByText(/point a client at connect/i)).toBeInTheDocument()
  })
})

describe("the zero-providers state", () => {
  it("teaches the three steps rather than showing an empty grid", () => {
    render(<FirstRunProviders onAdd={() => {}} />)
    expect(screen.getByText(/add a provider/i)).toBeInTheDocument()
    expect(screen.getByText(/discover/i)).toBeInTheDocument()
    expect(screen.getByText(/connect/i)).toBeInTheDocument()
  })

  it("offers the action it is teaching", () => {
    const onAdd = vi.fn()
    render(<FirstRunProviders onAdd={onAdd} />)
    screen.getByRole("button", { name: /add a provider/i }).click()
    expect(onAdd).toHaveBeenCalled()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- first-run`

Expected: FAIL — neither component exists.

- [ ] **Step 3: Implement**

```tsx
/**
 * What an empty well says.
 *
 * A blank panel with a faint grid is indistinguishable from broken equipment,
 * so every empty state names what will appear here and what produces it.
 */
export function EmptyLegend({ what, hint }: { what: string; hint: string }) {
  return (
    <div className="mt-4 rounded border border-dashed p-6 text-center">
      <p className="text-sm">{what}</p>
      <p className="mt-1 text-xs text-[hsl(var(--muted-foreground))]">{hint}</p>
    </div>
  )
}
```

`first-run-providers.tsx` renders a card with the heading "Nothing is configured yet", three numbered steps — "Add a provider", "Let discovery find its models", "Point a client at Connect" — a dimmed placeholder standing in for the routing graph, and a primary button reading "Add a provider" calling `onAdd`.

Then wire them:

- `overview-screen.tsx` renders `<FirstRunProviders onAdd={…} />` in place of the tiles and graph when `providers.data?.providers.length === 0`, with `onAdd` opening Task 17's dialog.
- The four plain-text empty lines become `<EmptyLegend>` with per-screen copy: Requests — "Requests appear here as clients call the gateway." / "Point a client at Connect to see the first one."; Usage — "Usage rolls up daily once requests start arriving." / "Spend needs a priced model; unpriced ones show an em-dash."; Models — "Models appear here after a discovery sweep." / "Add a provider and probe it to trigger one."; Connect — "No surfaces are live yet." / "The catalog fills in after the first discovery sweep."

The Requests legend replaces only the zero-rows-and-no-filters case. When filters are set, "No requests match these filters" is still the right sentence — a legend there would explain the screen to someone who already understands it.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/shell/ web/src/features/overview/ web/src/features/requests/ web/src/features/usage/ web/src/features/models/ web/src/features/connect/
git commit -m "feat(web): teach a fresh install and label empty wells"
```

---

### Task 21: Playground rebuild — chat, parameters, dialects, trace round-trip

Spec §6.8's first half. The screen is currently two inputs and a card. It becomes multi-turn chat with a system prompt, temperature, max tokens, tools and a stream toggle, plus the dialect selector Task 2 made real, and it hydrates from `?seed=` so a trace can seed a run — which is what closes the round trip Task 13's link opened.

**Files:**
- Create: `web/src/features/playground/chat.tsx`
- Create: `web/src/features/playground/chat.test.ts`
- Modify: `web/src/features/playground/playground-screen.tsx`
- Modify: `web/src/lib/router.tsx` (nothing structural — the root route's open search schema already carries `seed`)

**Interfaces:**
- Consumes: `stream(path, body, onStart)` and `StreamStart` from `lib/api`; `PlaygroundChatBody`, `PlaygroundMessage`, `PlaygroundDialect` from Task 9; `useTrace(id)` for seeding.
- Produces, from `chat.tsx`: `parseTools(raw: string): { tools?: Record<string, unknown>[]; error?: string }`, `chatBody(state: ChatState): PlaygroundChatBody`, `seedFromTrace(trace: RequestTrace): Partial<ChatState>`, and `<Chat />`. `drainSSE` moves from the screen into `chat.tsx` unchanged and is exported so its existing behaviour stays covered.

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/playground/chat.test.ts`:

```ts
describe("parseTools", () => {
  it("accepts a JSON array of objects", () => {
    expect(parseTools('[{"type":"function"}]')).toEqual({ tools: [{ type: "function" }] })
  })

  it("treats an empty box as no tools rather than as an error", () => {
    expect(parseTools("   ")).toEqual({})
  })

  it("names a parse failure instead of sending nothing", () => {
    // Silently dropping malformed tools would answer a different question
    // from the one the operator asked, and look like the model ignoring them.
    const out = parseTools("{not json")
    expect(out.tools).toBeUndefined()
    expect(out.error).toMatch(/json/i)
  })

  it("refuses a bare object, which is not the wire shape", () => {
    expect(parseTools('{"type":"function"}').error).toMatch(/array/i)
  })
})

describe("chatBody", () => {
  it("sends the transcript, not just the last turn", () => {
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "", maxTokens: "", toolsRaw: "",
      messages: [
        { role: "user", content: "hi" },
        { role: "assistant", content: "hello" },
        { role: "user", content: "go on" },
      ],
    })
    expect(body.messages).toHaveLength(3)
  })

  it("omits empty numeric fields rather than sending zero", () => {
    // A temperature of 0 is a real setting. An empty box is not, and sending
    // zero for it would quietly make every run deterministic.
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.temperature).toBeUndefined()
    expect(body.max_tokens).toBeUndefined()
  })

  it("sends an explicit zero temperature", () => {
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "0", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.temperature).toBe(0)
  })

  it("carries the dialect through", () => {
    const body = chatBody({
      model: "m", dialect: "anthropic", system: "be terse", stream: false,
      temperature: "", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.dialect).toBe("anthropic")
    expect(body.system).toBe("be terse")
    expect(body.stream).toBe(false)
  })
})

describe("seedFromTrace", () => {
  it("takes the model the client asked for, not the one that served", () => {
    // Replaying against the serving provider would not reproduce the routing
    // decision, which is usually the thing under investigation.
    const seeded = seedFromTrace({
      id: "r1", model: "fast", final_model: "llama-3.3", provider: "groq",
    } as RequestTrace)
    expect(seeded.model).toBe("fast")
  })

  it("carries the dialect the request arrived on", () => {
    const seeded = seedFromTrace({ id: "r1", model: "m", dialect: "anthropic" } as RequestTrace)
    expect(seeded.dialect).toBe("anthropic")
  })

  it("falls back to openai for a dialect the playground cannot send", () => {
    // The log records every inbound dialect, including the OpenAI Responses
    // wire, which this screen has no control for.
    const seeded = seedFromTrace({ id: "r1", model: "m", dialect: "responses" } as RequestTrace)
    expect(seeded.dialect).toBe("openai")
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- playground chat`

Expected: FAIL — `chat.ts` does not exist.

- [ ] **Step 3: Implement**

```ts
export type ChatState = {
  model: string
  dialect: PlaygroundDialect
  system: string
  stream: boolean
  /** Held as strings: an empty box and a zero are different settings, and a
   *  number state cannot hold both. */
  temperature: string
  maxTokens: string
  toolsRaw: string
  messages: PlaygroundMessage[]
}

export function parseTools(raw: string): { tools?: Record<string, unknown>[]; error?: string } {
  const trimmed = raw.trim()
  if (trimmed === "") return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch (err) {
    // Named rather than dropped: sending nothing would answer a different
    // question and read as the model ignoring the tools.
    return { error: `tools must be JSON: ${(err as Error).message}` }
  }
  if (!Array.isArray(parsed)) return { error: "tools must be a JSON array" }
  return { tools: parsed as Record<string, unknown>[] }
}

export function chatBody(state: ChatState): PlaygroundChatBody {
  const body: PlaygroundChatBody = {
    model: state.model,
    messages: state.messages,
    stream: state.stream,
    dialect: state.dialect,
  }
  if (state.system !== "") body.system = state.system
  if (state.temperature !== "") body.temperature = Number(state.temperature)
  if (state.maxTokens !== "") body.max_tokens = Number(state.maxTokens)
  const { tools } = parseTools(state.toolsRaw)
  if (tools) body.tools = tools
  return body
}

const DIALECTS: PlaygroundDialect[] = ["openai", "anthropic", "gemini"]

export function seedFromTrace(trace: RequestTrace): Partial<ChatState> {
  // The model the client asked for, not the one that served: replaying
  // against the serving provider would skip the routing decision, which is
  // usually the thing under investigation.
  const dialect = trace.dialect as PlaygroundDialect
  return {
    model: trace.alias || trace.model,
    // The log records inbound dialects this screen has no control for, the
    // OpenAI Responses wire among them.
    dialect: DIALECTS.includes(dialect) ? dialect : "openai",
  }
}
```

`<Chat />` holds `ChatState` plus a transcript of role-tagged bubbles. Send appends the typed turn as `{role:"user"}`, appends an empty `{role:"assistant"}`, calls `stream("/api/playground", chatBody(state), onStart)` and grows the assistant bubble from `drainSSE`. `parseTools`' error renders inline under the tools textarea and disables Send. The request id from `StreamStart` renders as the existing trace link under the transcript.

Seeding: an effect reads `useSearch({ strict: false }).seed`, calls `useTrace(seed, { enabled: seed !== undefined })`, and applies `seedFromTrace` once when the trace arrives. Bodies are not captured, so the prompt itself cannot be recovered — the seeded run carries the model and dialect, and the transcript starts empty with a legend saying why. That limitation is stated on screen rather than left for the operator to discover, and it is recorded in the DoD document.

Controls, laid out above the transcript: model `Input`, dialect `Select` over the three, system `Textarea`, temperature and max-tokens numeric `Input`s, tools `Textarea`, stream `Switch`.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/playground/
git commit -m "feat(web): multi-turn playground chat with dialects"
```

---

### Task 22: Playground compare mode, auxiliary panels and token counting

Spec §6.8's second half: two models side by side on the same prompt, the six auxiliary surfaces, and token counting across both count endpoints showing the native-versus-estimate marker.

The estimate marker comes from the `X-Darkrouter-Estimated` response header, which `internal/exec/count.go:73` sets — "the body cannot carry a marker: clients parse these responses strictly". So the count call must read a header, which means it cannot go through `api.post`; it uses `fetch` directly and reads both the header and the body.

**Files:**
- Create: `web/src/features/playground/compare.tsx`
- Create: `web/src/features/playground/aux-panels.tsx`
- Create: `web/src/features/playground/aux-panels.test.ts`
- Modify: `web/src/features/playground/playground-screen.tsx`

**Interfaces:**
- Consumes: `POST /api/playground/aux` and `POST /api/playground/count` from Tasks 3 and 4; `AuxBody`, `AuxSurface`, `CountResult` from Task 9.
- Produces, from `aux-panels.tsx`: `AUX_SURFACES: readonly { surface: AuxSurface; label: string; needsFile: boolean }[]`, `auxBodyFor(surface: AuxSurface, form: Record<string, string>): AuxBody`, `vectorPreview(embedding: number[], n: number): string`, `readCount(res: Response, body: unknown): CountResult`, and `<AuxPanels />`. From `compare.tsx`: `<Compare />`.

- [ ] **Step 1: Write the failing tests**

Create `web/src/features/playground/aux-panels.test.ts`:

```ts
describe("auxBodyFor", () => {
  it("puts surface-specific fields under body and the model beside it", () => {
    // The runner merges the model over the body, so a model typed in the
    // panel wins over one left in a raw body from a previous run.
    expect(auxBodyFor("embeddings", { model: "e5", input: "hello", dimensions: "256" })).toEqual({
      surface: "embeddings",
      model: "e5",
      body: { input: "hello", dimensions: 256 },
    })
  })

  it("omits a blank optional field rather than sending an empty string", () => {
    expect(auxBodyFor("embeddings", { model: "e5", input: "hi", dimensions: "" })).toEqual({
      surface: "embeddings", model: "e5", body: { input: "hi" },
    })
  })

  it("carries a file and its name for transcription", () => {
    expect(
      auxBodyFor("transcriptions", { model: "whisper-1", file_b64: "AAA=", filename: "a.wav" }),
    ).toEqual({
      surface: "transcriptions", model: "whisper-1",
      file_b64: "AAA=", filename: "a.wav", body: {},
    })
  })
})

describe("vectorPreview", () => {
  it("shows the leading components and the full length", () => {
    // A 1536-component vector printed whole is unreadable; the length is the
    // fact an operator is checking.
    const preview = vectorPreview([0.11111, 0.22222, 0.33333, 0.44444], 2)
    expect(preview).toContain("0.111")
    expect(preview).toContain("0.222")
    expect(preview).not.toContain("0.333")
    expect(preview).toContain("4")
  })

  it("does not claim a truncation that did not happen", () => {
    expect(vectorPreview([0.5], 4)).not.toContain("…")
  })
})

describe("readCount", () => {
  it("marks a locally estimated count from the response header", () => {
    // The body cannot carry a marker — clients parse these responses
    // strictly — so the header is the only signal there is.
    const res = new Response("{}", { headers: { "X-Darkrouter-Estimated": "true" } })
    expect(readCount(res, { input_tokens: 12 })).toEqual({ tokens: 12, estimated: true })
  })

  it("reads a native count as native", () => {
    expect(readCount(new Response("{}"), { input_tokens: 12 })).toEqual({
      tokens: 12, estimated: false,
    })
  })

  it("reads Gemini's field name too", () => {
    expect(readCount(new Response("{}"), { totalTokens: 40 }).tokens).toBe(40)
  })

  it("reports zero rather than NaN for a shape it does not recognise", () => {
    expect(readCount(new Response("{}"), { surprise: 1 }).tokens).toBe(0)
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npm test -- aux-panels`

Expected: FAIL — the module does not exist.

- [ ] **Step 3: Implement**

```ts
export const AUX_SURFACES = [
  { surface: "embeddings", label: "Embeddings", needsFile: false },
  { surface: "rerank", label: "Rerank", needsFile: false },
  { surface: "moderations", label: "Moderation", needsFile: false },
  { surface: "images", label: "Images", needsFile: false },
  { surface: "speech", label: "Speech", needsFile: false },
  { surface: "transcriptions", label: "Transcription", needsFile: true },
] as const

const NUMERIC = new Set(["dimensions", "n", "top_n"])

export function auxBodyFor(surface: AuxSurface, form: Record<string, string>): AuxBody {
  const body: Record<string, unknown> = {}
  const out: AuxBody = { surface, model: form.model, body }
  for (const [k, v] of Object.entries(form)) {
    if (k === "model" || v === "") continue
    if (k === "file_b64") {
      out.file_b64 = v
      continue
    }
    if (k === "filename") {
      out.filename = v
      continue
    }
    body[k] = NUMERIC.has(k) ? Number(v) : v
  }
  return out
}

export function vectorPreview(embedding: number[], n: number): string {
  const head = embedding.slice(0, n).map((v) => v.toFixed(3)).join(", ")
  // The length is the fact an operator is checking; a 1536-component vector
  // printed whole is unreadable.
  const ellipsis = embedding.length > n ? ", …" : ""
  return `[${head}${ellipsis}] (${embedding.length} components)`
}

export function readCount(res: Response, body: unknown): CountResult {
  const shape = (body ?? {}) as Record<string, unknown>
  const tokens =
    typeof shape.input_tokens === "number"
      ? shape.input_tokens
      : typeof shape.totalTokens === "number"
        ? shape.totalTokens
        : 0
  return {
    tokens,
    // Set by exec.HandleCount when no candidate spoke the native counting
    // dialect. The body cannot carry it, so the header is the only signal.
    estimated: res.headers.get("X-Darkrouter-Estimated") === "true",
  }
}
```

`<AuxPanels />` renders `Tabs` over `AUX_SURFACES`. Each panel has a model field, its own fields — embeddings: input and dimensions; rerank: query and documents; moderation: input; images: prompt, n and size; speech: input and voice; transcription: a file input reading into base64 through `FileReader` plus the filename — and a Run button posting `auxBodyFor(...)`. Responses render per surface: an embedding through `vectorPreview`, speech as an `<audio>` element over a blob URL, images as `<img>` from the returned URL or base64, everything else as pretty-printed JSON. Every run shows its request id as a trace link, read from the `X-Darkrouter-Request` response header the executor sets on all of them.

Counting is a small card above the tabs: a dialect `Select` over anthropic and gemini only — there is no OpenAI counting endpoint, and Task 3's handler refuses it — a model field, a prompt textarea, and a result reading `{tokens} tokens` plus, when `estimated`, the marker "estimated locally — no candidate provider speaks this counting dialect".

`<Compare />` renders two model fields over one shared prompt, runs both through `stream("/api/playground", …)` concurrently, and shows the two transcripts side by side with each one's latency and trace link. It reuses Task 21's `chatBody` so a compare run and a chat run are the same request.

`playground-screen.tsx` becomes a thin shell: `Tabs` over Chat, Compare, Auxiliary, and Count, each rendering the component that owns it. The screen file holds layout and nothing else — the 484-line grab bag §9 exists to prevent is exactly what a playground accumulates.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test && npm run typecheck && npm run build`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/playground/
git commit -m "feat(web): compare mode, aux surfaces and token counting"
```

---

### Task 23: The gate

Static suites, then the console walked against a live gateway. §11 and §12 both make the live walk the point: phase 13's own criteria document records that "nothing in this table has been rendered against a running gateway", and the previous criteria walk found three defects a green suite had said nothing about.

**Files:**
- Create: `docs/ux/GAP-CLOSURE-DOD.md`
- Modify: `docs/PROGRESS.md`
- Modify: `docs/ux/DONE-CRITERIA.md`

- [ ] **Step 1: Static gates**

```bash
go vet ./... && go build ./...
go test -race -count=1 ./...
cd web && npm test && npm run typecheck && npm run build
```

Then run each row of the Definition of Done table's command column and record its output. A `-run` pattern that reports `[no tests to run]` is a failed gate, not a passed one — check for that string explicitly:

```bash
go test ./internal/admin/ -run 'TestFilteringByErrorCode|TestACursorMintedUnderOneErrorCode|TestARequestRowNamesTheServingPath' -v 2>&1 | tee /tmp/d1.txt
grep -q 'no tests to run' /tmp/d1.txt && echo 'D1 GATE IS VACUOUS' || echo 'D1 ran real tests'
```

Repeat for D2 through D8. Anything failing is a finding recorded in the DoD document, never quietly dropped.

- [ ] **Step 2: Create the results document**

Create `docs/ux/GAP-CLOSURE-DOD.md` with one row per criterion D1–D20, four columns: criterion, met or not met, what was clicked or run, and what was seen. Below the table, a section for the deviations this plan made deliberately, each with its reason:

- The Requests trace opens from a column button, not a row click — `DataTable` exposes no row-click prop (Task 12).
- The trace waterfall is request-level, not per-attempt — no connect timing is recorded and `TraceAttempt` carries no TTFT (Task 13).
- The latency tile has no sparkline — `UsageRow` carries no per-day latency (Task 11).
- The usage range picker's widest option is labelled 365d, not "All" — the endpoint serves 365 days (Task 14).
- Playground seeding carries the model and dialect but not the prompt — `capture.bodies` has no writer (Task 21).
- Tools are refused on the Gemini dialect — `functionDeclarations` is a different shape from the OpenAI tools box (Task 2).

- [ ] **Step 3: Live gate**

Start the UAT stack: `docker compose -f compose.uat.yml up`, one real provider configured, `DARKROUTER_ADMIN_PASSWORD_HASH` set. Walk D1–D18 one at a time in a browser, in **both** modes — the theme switcher in the sidebar toggles it, and §10 makes light and dark structurally different screens rather than a palette swap, so each is its own pass.

Record each row of `GAP-CLOSURE-DOD.md` with what was clicked and what was seen. A criterion that cannot be met is a finding to report, not to drop.

Then check the two states no suite can reach: point a fresh data directory at the gateway and confirm the zero-providers teaching state renders rather than empty grids, and unset the password hash and confirm the existing `FirstRun` still explains itself.

- [ ] **Step 4: Cross-check the original criteria**

Re-open `docs/ux/DONE-CRITERIA.md` and confirm none of its nine rows regressed. Update row 1 — "Every screen in §6 renders against a real gateway in both modes" — from **Unverified** to whatever the live walk actually found, since this is the first pass that could exercise it. Update `docs/PROGRESS.md` with the phase status row for this work.

- [ ] **Step 5: Commit**

```bash
git add docs/ux/GAP-CLOSURE-DOD.md docs/ux/DONE-CRITERIA.md docs/PROGRESS.md
git commit -m "test(docs): gate console gap closure against the dod"
```

---

## Notes for the executor

- **The Go test harness is `fixtures_test.go` and nothing else.** `testServerFull`, `testServerFullWithAliases`, `testServerWithCatalog`, `testServerWithExecutor`, `do`, `seedProviderWithKey`, `catalogFixture`, and `login` from `auth_test.go`. Every backend task above is written against those names. If a call does not compile, read the file rather than inventing a helper.
- **Backend tasks 1–8 are independently shippable** and touch no frontend file. Frontend tasks 9–22 each name the backend task they depend on in their Interfaces block; Task 9 gates all of them. Once Task 9 lands, the frontend tasks may proceed in parallel with any remaining backend work.
- **The mockups are the visual contract.** Where this plan simplifies a fragment, the task says so and Task 23 records it. A silent deviation is the failure mode to avoid.
- **Expect UAT findings.** Both previous live walks found defects every suite had passed over. That is the point of Step 3, not a sign something went wrong.
