# Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every finding of the 2026-09-02 whole-project review, from the two breaker defects through provider translation, admin hardening, console correctness and identity, to CI gates and docs.

**Architecture:** Eight tracks with disjoint file ownership run in parallel git worktrees (`../darkrouter-wt/<track>` on branch `fix/<track>`), each committing on its own branch. The lead merges the branches into master in order, runs the full suites, performs the one cross-cutting change (structured logging with request ids) serially, verifies the console live, and redeploys.

**Tech Stack:** Go 1.26 (stdlib `log/slog`, `net/http`, modernc sqlite), React 19 + TanStack + darkraise-ui 6.5, vitest, Docker multi-stage.

**Spec:** the review report (`Darkrouter Review` artifact, 2026-09-02) and the memory entry `darkrouter WHOLE-PROJECT REVIEW 2026-09-02` in darkmem. Finding ids below refer to that report's sections.

## Global Constraints

- **Ownership is absolute.** A track edits only the paths in its list. Anything else it needs changed is reported back, never edited.
- **Typography:** never `text-xs`, never a pixel size anywhere under `web/`; charts take `fontSize` from a CSS variable via `style`/`tick` props, not a number.
- **Comments:** WHY only; no references to this review, a finding id, a phase, or a task.
- **Commits:** `<type>(<scope>): <subject>`, subject ≤ 50 chars, imperative; body wrapped at 72; end with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` and `Claude-Session: https://claude.ai/code/session_01KCatddMTubckJv1uXxpM5C`. Atomic: one concern per commit.
- **Before each commit:** `gofmt -l` clean on touched Go files; the touched packages' tests pass; for `web/`, `npx tsc --noEmit` and `npx eslint` on touched files pass and `npx vitest run <touched dirs>` passes.
- **No new Go module dependencies.** Hand-roll a token bucket; do not add `x/time`.
- **Never push, never touch `.env`, `.uat-credentials`, `data/`, the running container, or `web/node_modules`.**
- **Shared foundations already on master:** `ir.ToolUse.Signature`, `ir.Reasoning.Disabled`, `ir.Reasoning.Effort` vocabulary `minimal|low|medium|high|xhigh|max`, `ir.ResponseFormat{Type "json_schema"|"json_object", Name, Strict *bool}`; `web/src/lib/format.ts` (`money`, `pricePerMillion`, `duration`, `durationParts`, `count`, `compact`, `percent`, `dateTime`, `dateOnly`, `zoneLabel`, `utcDay`); `api-types.ts` with `RouteSkip` objects, `Spend.micros: number | null`, `RequestTrace.source?`.
- **API contract decisions:** route preview skips stay objects; trace `key_label` becomes the credential's label; override GET emits `omitempty`; `today_spend.micros` stays nullable; `GET /api/requests/{id}` gains `source` and `path`; list rows gain `reasoning_tokens`.
- **Time convention:** timestamps in browser-local time always with the date, zone printed once per screen with `zoneLabel()`; day buckets are UTC days labelled "UTC day".
- **Money convention:** `lib/format.money` everywhere; per-million catalog prices via `pricePerMillion`.

---

## Track core — `fix/core`

**Owns:** `internal/exec`, `internal/health`, `internal/config`, `internal/server`, `internal/tokenize`, `internal/sse`, `internal/provider`, `internal/router`, `internal/localcli`, `cmd/darkrouter`, `internal/e2e`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 2 / coupling 3 / risk 3 / size 3 → over Rule S ceiling, kept as one track because breaker and executor changes are inseparable. **Approach:** release the probe from the executor's single exit, count post-status failures on the same breaker entry.

### Task C1: Probe release on every attempt exit
- Modify `internal/exec/exec.go` `attempt()` so every return path after `Fleet.Available` claimed a probe reaches `recordHealthFor` (wrap the body in a closure, record once on the way out with the outcome the path produced; early credential/build errors record `RetryableCredential`/`Fatal` respectively and must clear `probing`).
- Modify `internal/health/breaker.go` `Record`: `ClientCancelled` and `RetryableModel` clear `st.probing` without changing the ladder; `RetryableCredential` clears `probing` on the model-level key `k` as well as cooling the credential key.
- Tests in `internal/health/breaker_test.go`: after `Available` returns true (probe claimed), `Record(ClientCancelled)` then `Available` returns true again; same for `RetryableModel` and `RetryableCredential`. Test in `internal/exec` (fake fleet): a `credentialFor` failure on a probing candidate leaves the candidate available on the next request.

### Task C2: Breaker trips on "200 then fail"
- In `exec.go` the status-based Success record must not delete the ladder entry until the body has been read. Split `Record` semantics: record status outcome only when it is not Success; record Success from the surface's `Respond`/stream completion once the body parsed or the stream committed. `reclassifyStream`, `failedParse`, and `forwardUnary`'s pre-commit read failure then increment the same entry.
- Test: three consecutive requests where the fake upstream answers 200 with an invalid body trip the breaker (`Available` false on the fourth). Test that a healthy 200 with a good body still resets the ladder.

### Task C3: Post-commit unary bodies bounded by idle, not attempt start
- Reset the attempt timer to the idle timeout before reading a post-commit unary body in `forward.go` `forwardUnary`, `surface.go` chat unary, `speech.go`, `transcription.go`, image parsing; mirror what `attemptStream` does at `exec.go:676-682`.
- Test: fake upstream that streams a unary body slowly past `connect+first_byte` but within idle completes.

### Task C4: Timeout validation and defaults
- `internal/config/load.go` validation: all `policy.timeout` durations > 0, `total >= connect + first_byte`, `idle > 0`; `shutdown_grace` default (fill the empty branch; use 10s).
- Tests for each rejection message and the default.

### Task C5: Body and connection hygiene
- `exec.go` `ClassifyBody` path: close the original body after the capped read; handle the ReadAll error. Drain (`io.Copy(io.Discard, io.LimitReader(body, 64<<10))`) before closing non-2xx bodies.
- Compressed-body refusal for aux surfaces: move the `Content-Encoding` check into the shared inbound read path so `embed.go` etc. answer the same 415-style error.
- `makeReplayable`: set `GetBody` on requests whose adapters hold `[]byte` (add a `BodyBytes() []byte` optional interface or pass the bytes through `AttemptCtx`) so no second copy is made.
- Transport: `MaxIdleConnsPerHost: 32`.

### Task C6: Per-request SQLite writes off the hot path
- `server.go` proxy-token auth: stop calling the store's last-used UPDATE per request; record into an in-memory map flushed every 30s (reuse the pattern of `SaveLastUsed` in `exec`). Do not edit `internal/store`; if a store method is needed, use the existing `SaveLastUsed`-style batch API or keep the map in `server` and call the existing per-token method from the flusher.
- Cache `hasProxyTokens` for 5s instead of querying per request.
- Test: 100 requests produce ≤ 1 write call on a fake store.

### Task C7: Tokenizer codec cache
- `internal/tokenize`: `sync.Once` per encoding for the codec object, not only the vocab.
- Benchmark-free test: two counts return identical results and the codec constructor is called once (count via a package-level hook in tests).

### Task C8: Listener timeouts and worker recovery
- `server.go`: `ReadTimeout` 60s (proxy: covers `max_body_bytes` at modest bandwidth; make it `2 * timeout.total` if larger), `IdleTimeout` 120s on both servers.
- Wrap each background worker goroutine in a `recover` that logs and restarts the worker (`server.go:553-561`).
- Test: a worker that panics once is restarted and the process survives.

### Task C9: Client-facing error hygiene
- Replace raw `err.Error()` in the client-facing messages at `exec.go:434, 477, 489, 495, 502, 942` and `forward.go:240` with fixed messages ("credential unavailable", "request could not be rendered for this provider", "upstream read failed"); log the detail server-side with the request id.
- Passthrough response headers: allowlist `Content-Type`, `Content-Length`, `Content-Encoding`, `Cache-Control`, `X-Request-Id`, and every `x-ratelimit-*`; drop everything else (`forward.go:199-215`). Test both.

### Task C10: Smaller correctness items
- Attempt counter: increment only when `attempt()` actually issued or attempted a request (move `attempts++` after the early exits, or return a flag).
- `HandleCount`: run under the attempt deadline, consult and record the breaker, apply the authorizer; cap the count response body at 64 KiB.
- Config watcher: match on `filepath.Clean` of the resolved path and also react to `Create`/`Rename` of the parent directory entry (Kubernetes symlink swap); guard `Reload` with a mutex.
- Honour `Retry-After` on 503/529 as a cooldown (`breaker.go`: treat it like the 429 branch when `HasRetryAfter`).
- Second Ctrl-C: call `stop()` after the first signal so the second one kills the process.
- Post-commit passthrough failure: write an in-dialect error event before closing (`forward.go:152-164`) using the dialect's stream error writer.
- `localcli/auggie.go`: start the child in its own process group (`SysProcAttr.Setpgid`) and kill the group on timeout.
- Each with a test.

### Task C11: Metrics and readiness
- `/metrics`: expose `darkrouter_requests_total{dialect,surface,status}`, `darkrouter_attempts_total{provider,outcome}`, `darkrouter_request_duration_seconds` histogram (fixed buckets), `darkrouter_breaker_open{provider,model}` gauge, plus the two existing counters, hand-written in Prometheus text format from atomic counters in a new `internal/server/metrics.go`.
- `/readyz`: 503 unless the DB pings and the config is valid.
- Tests for the text output and readiness states.

### Task C12: Dead code and stale comments
- Remove `provider.Resolve`/`YAMLSource` if no non-test caller, `tapStream`, `CommitWriter.OnCommit`, `openaicompat.ClassifyWithContext` is not yours (report it), `resolved.Cfg`. Fix comments at `server.go:83-88, 414-415`, `commitwriter.go:41-42`, `router/types.go:56`, `provider.go:3-4`. Name the buffer and timeout constants (`exec.go:517`, `forward.go:124, 228`, `transcription.go:23`, `exec.go:110-113`). Reduce `attempt()`'s parameter list by passing `*AttemptCtx`. Extract the repeated TTFT/FinalProvider/Warnings block into one helper used by all seven `Respond`s.

---

## Track prov1 — `fix/prov1`

**Owns:** `internal/adapter/anthropic`, `internal/adapter/bedrock`, `internal/adapter/vertex`, `internal/adapter/xlate`, `internal/adapter/adapter.go`, `internal/adapter/classify.go`, `internal/edge/anthropic`, `internal/auth/oauth.go`, `internal/catalog/preset.go` (traits only), `internal/catalog/presets.overrides.yaml`, `internal/catalog/pricing.go`, golden fixtures: every `rendered/anthropic.json`, `rendered/bedrock.json`, `rendered/vertex.json`, and `ir.json`/warnings under `golden/anthropic/*`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 2 / coupling 2 / risk 3 / size 3.

### Task P1-1: OAuth beta header
- When the credential is an OAuth bearer (the authorizer in `auth/oauth.go` knows), set `anthropic-beta: oauth-2025-04-20`, merging with any client-supplied beta list on the passthrough path. Test the header on both IR and passthrough requests.

### Task P1-2: Bedrock role merging and reasoning
- `bedrock/build.go`: merge consecutive same-role messages into one Converse message (mirror `anthropic/build.go:218-255`); emit `{}` not `null` for empty tool input; render `req.Reasoning` as `additionalModelRequestFields.reasoning_config` for Anthropic model ids (budget from `xlate.EffortBudget`) and record a warning for other publishers; render `CacheControl` as `cachePoint` blocks; fix the wrong comment at `:243-249` and render reasoning blocks as `reasoningContent` on assistant turns. Regenerate `rendered/bedrock.json` fixtures (the parallel-tool-calls one must now show alternating roles). Parse `reasoningContent.redactedContent` deltas.

### Task P1-3: Anthropic request shaping by model traits
- Extend `TraitRule` in `preset.go` and `adapter.ModelInfo` with `NoPrefill`, `ThinkingAlwaysOn`, `NoForcedToolChoice` booleans (kind-neutral names). Populate `presets.overrides.yaml` for the current Anthropic families: mark Fable 5 / 5.1 and Mythos as `ThinkingAlwaysOn`+`NoForcedToolChoice`; mark 4.6+ and 5-family as `NoPrefill`. Keep the substring rule shape.
- `anthropic/build.go`: drop a trailing assistant prefill when `NoPrefill` (with a warning, always, including the adaptive case at `:182`); never emit `thinking.type: disabled` for `ThinkingAlwaysOn` unless `Reasoning.Disabled` is set, and then record a warning and omit; downgrade forced `tool_choice` to `auto` with a warning on `NoForcedToolChoice`. Tests per trait.

### Task P1-4: Signature and effort hygiene
- `anthropic/content.go:81-85`: drop thinking blocks without a signature, warn.
- `xlate/params.go`: map `minimal`→ Anthropic `low`, `xhigh`/`max` → Anthropic `high` with a budget where the model takes one; add Gemini budget bands for `minimal` (min budget) and `xhigh`/`max` (model cap). Export the per-model cap table (`gemini 2.5 flash` 24576, `pro` 32768, default 32768) so prov2 can call `xlate.GeminiBudgetCap(model)`.
- `anthropic/stream.go:157-160`: warn on unknown stop_reason; parse `server_tool_use`/`web_search_tool_result` into `ir.Extra`-carrying blocks rather than empty text.

### Task P1-5: Anthropic edge completeness
- `edge/anthropic/parse.go`: parse `output_config` (effort → `Reasoning.Effort`; `format` → `ResponseFormat`), capture `anthropic-beta` into `Metadata["anthropic-beta"]` and re-emit it in `anthropic/build.go` and `forward.go`; recognise typed server tools (`type` present) and carry them through as `ir.Tool{Extra: raw}` for the Anthropic adapter to re-emit verbatim, with other adapters warning and dropping; add `cache_control` to `wireTool`.
- `edge/anthropic/stream.go`: `message_delta.usage` carries cumulative `input_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`; `message_start` usage is patched from the final usage when the upstream reports late (buffer `message_start` until first content or usage, whichever is first, matching what the responses writer does).

### Task P1-6: Vertex and pricing
- `vertex/adapter.go:81-84`: `location == "global"` → `https://aiplatform.googleapis.com/v1/projects/{p}/locations/global`. Implement embeddings for the Google publisher via `:predict` or remove `embedding` from the vertex preset's surfaces; pick implement. Dispatch on the target's publisher rather than response shape (`vertex/anthropic.go:120-133`).
- `catalog/pricing.go`: price `ReasoningTokens` at the output rate; `anthropic/parse.go` reads `cache_creation.ephemeral_1h_input_tokens` and `ephemeral_5m_input_tokens` into `ir.Usage` (add fields) and pricing bills 1h at 2× and 5m at 1.25× of input.

### Task P1-7: Cleanup
- Share `escapePathSegment` between bedrock and vertex (put it in `adapter`). Rename `readAndRestore` in `anthropic/count.go` to what it does. Remove the `var _ =` import pin in `vertex/anthropic.go:151`. Delete task-history comments in your files.

---

## Track prov2 — `fix/prov2`

**Owns:** `internal/adapter/gemini`, `internal/adapter/openaicompat`, `internal/edge/openai`, `internal/edge/gemini`, `internal/edge/edge.go`, `internal/ir`, `internal/catalog` except `preset.go` traits/`presets.overrides.yaml`/`pricing.go`, `internal/catalog/presets.yaml`, `tools/presetgen`, golden fixtures: every `rendered/gemini.json`, `rendered/openaicompat.json`, and `ir.json`/warnings under `golden/openai/*` and `golden/gemini/*`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 2 / coupling 2 / risk 3 / size 3.

### Task P2-1: Quirk consumers
- `openaicompat/build.go`: read the target preset's quirks (`adapter.Target` already carries the preset id; add `Quirks []string` to `Target` via `adapter.go`? No: `adapter.go` belongs to prov1. Instead resolve quirks inside the adapter from `catalog.Embedded()[preset]` as `exec/passthrough.go:81` does, exposed through a small `openaicompat.QuirksFor(preset string) quirkSet` helper). Implement: `max-completion-tokens-name` (emit `max_completion_tokens`), `requires-max-tokens` (default 4096 when unset), `no-system-role` (fold system into the first user message), `no-parallel-tool-calls` (omit the field), `temperature-top-p-exclusive` (keep temperature, drop top_p, warn), `strict-unknown-fields` (omit `stream_options`, `reasoning_effort`, `metadata`, `parallel_tool_calls`), `no-tool-streaming` (force unary when tools present, warn), `usage-final-chunk-only` (already how the parser works; document it as accepted). Populate quirks in `presets.yaml` for openai (`max-completion-tokens-name`), mistral (`strict-unknown-fields`, `temperature-top-p-exclusive`), deepseek/openrouter/qwen (none), groq (`requires-max-tokens` if their docs need it: **skip unless verified**).
- Table-driven test per quirk.

### Task P2-2: Gemini correctness
- `parse.go`/`stream.go`: carry `thoughtSignature` on `functionCall` and text parts into `ir.ToolUse.Signature` / `ir.Thinking.Signature`; re-emit in `content.go` on `functionCall`; keep text of a thought part that also carries a signature.
- `stream.go:111`: keep `hasCall` across chunks for the candidate.
- `stream.go:76-79`, `forward.go`: decode `{"error":...}` chunks and surface as an in-stream error (`ErrPayload`) with rate-limit classification by `status`/`code`.
- `count.go`: build a countTokens body with only `contents` and `systemInstruction` (or `generateContentRequest` wrapping the full request); test with tools and system.
- `build.go:107-116`: clamp budget via `xlate.GeminiBudgetCap` **(from prov1; until merged, define a local `budgetCap(model)` with the same table and switch after merge — leave a one-line note in your report)**; emit `thinkingBudget: 0` when `Reasoning.Disabled`; parse and emit `thinkingLevel` for Gemini 3 ids.
- Tool-result matching by id when ids are present (`content.go:130-137`, `edge/gemini/parse.go:215-220`).
- `edge/gemini/parse.go`: parse `googleSearch`/`codeExecution`/`urlContext` into `ir.Tool{Extra}` and warn when the target adapter is not Gemini; `responseMimeType: application/json` without schema → `ResponseFormat{Type: json_object}`; parse `responseJsonSchema` and `cachedContent` (cachedContent → `Metadata`, re-emitted by the Gemini adapter); `thinkingBudget: 0` → `Reasoning.Disabled`.

### Task P2-3: openaicompat correctness
- `parse.go:61`: `content` as `string | []part`; `reasoning_content`/`reasoning` on the unary path into a thinking block; `n>1` → warning; `tool_calls[].index` missing → sequential index; in-stream error classification by `code`/`type`.
- `build.go:63-72`: emit `strict` only when `ResponseFormat.Strict` is true; emit `name` from `ResponseFormat.Name` (default `response`); `json_object` → `{"type":"json_object"}`.
- `messages.go:122-127`: emit assistant `reasoning_content` when the target preset is deepseek/openrouter (quirk `echo-reasoning-content`, add to vocabulary) instead of dropping.
- `edge/openai/parse.go`: parse `response_format.json_object` and `json_schema.strict/name`; parse assistant `reasoning_content`; capture `n`, `seed`, `logprobs`, `top_logprobs`, `frequency_penalty`, `presence_penalty`, `logit_bias`, `user`, `service_tier`, `prediction`, `modalities`, `audio`, `web_search_options` into `ir.Request.Extra` (raw JSON), and have `openaicompat/build.go` re-emit `Extra` verbatim; warn on message `name`, `image_url.detail`, tool `strict` (carry `strict` into `ir.Tool` if a field exists; else `Extra`).
- `edge/openai/stream.go:129-134`: usage chunk with `choices: []` and token details.
- Golden: regenerate your fixtures; add fixtures `openai/reasoning-content-roundtrip`, `openai/json-object-mode`, `gemini/function-call-signature`.

### Task P2-4: Catalog and IR hygiene
- `discovery.go:269-275`: seed Vertex from the live models.dev document when present.
- `ir.Delta.Signature` doc comment: "a whole signature or a fragment, per dialect".
- Extract the duplicated `closeAll` state machine and `readCappedBody` into `internal/ir` or `internal/edge` helpers used by your files (report the anthropic copies for prov1).
- `tools/presetgen`: add a `quirks:` override merge from `presets.overrides.yaml` so regenerating does not wipe quirk data; document in the file header.

---

## Track admin — `fix/admin`

**Owns:** `internal/admin`, `internal/auth` except `oauth.go`, `internal/store`, `internal/crypto`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 3 / coupling 2 / risk 2 / size 3.

### Task A1: Policy validation through one path
- `configapi.go` `applyPolicyWrite`: build the merged policy and run the same validator `config.Load` uses (export `config.ValidatePolicy` if needed — that file is core's; if not exported yet, call the store-level reload and reject when `Current()` validation fails, i.e. validate the overlay result in `config/store.go`… **that file is core's too**). Resolution: implement validation in `internal/admin/policyvalidate.go` with the same rules as `config/load.go:170-234` (max_attempts 1–10, trip_after ≥ 1, durations > 0, total ≥ connect+first_byte) and reject with 400 before writing. Test each rule.

### Task A2: Correctness fixes
- Tag playground count/aux requests with the console source (`playgroundaux.go:87, 195-207`).
- OAuth listener: stop the previous listener before binding (`listener.go`), fix comments.
- Password precedence: when `DARKROUTER_ADMIN_PASSWORD_HASH` differs from the stored row **and** the env value is newer (store the env hash's fingerprint in settings on first use; if the env fingerprint changes, the env wins and the row is deleted), log once at startup.
- 409 on preset rename collision; 400 on password > 72 bytes; `ReplaceCredentialSecret` scoped by provider; nil-check `Key` in `handlePatchCredential` and `handleBreakerReset`; 404 on delete override miss; session delete requires the full id or a ≥ 8-char prefix with exactly one match; 400 on unparseable `since_ms`/`until_ms`/`limit`/`days`; listener `stop` outside the handler goroutine; call `FlowStore.Sweep` from the session sweeper; register OAuth routes only when `Flows != nil`.
- One test per item.

### Task A3: Security
- Login rate limit: per-IP token bucket (5/min burst 10) plus a global concurrency cap of 4 bcrypt verifications; 429 with `Retry-After`.
- Security headers middleware on the admin listener: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`, a CSP `default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; font-src 'self' https://fonts.gstatic.com; style-src-elem 'self' 'unsafe-inline' https://fonts.googleapis.com; connect-src 'self'`; `Cache-Control: no-cache` on `index.html`, `public, max-age=31536000, immutable` on hashed assets; no directory listing under `/assets/`.
- Session touch throttled to once per 5 minutes; absolute session lifetime 30 days from creation (new column via migration 0017, `created_at` exists? use it).
- Session ids hashed (SHA-256) at rest via migration 0017 (drop existing rows; operators log in again).
- Credential count/enabled projection without decrypting (`store.CredentialSummaries(ctx)` one query) used by overview and providers list; decrypt only on detail.
- `Sec-Fetch-Site: none` no longer treated as same-origin for mutations.
- Fixed client messages for 500s with server-side logging; `handlePatchProvider` maps only validation errors to 400.
- Tests for each.

### Task A4: API contract
- Trace `key_label` returns the label (join `provider_keys`); `GET /api/requests/{id}` emits `source` and `path`; override GET uses `omitempty`; list rows emit `reasoning_tokens`; `next_cursor` omitted on a short page; deletes uniformly 204 with 404 on miss; every PATCH returns the updated resource; bare-array endpoints wrapped: `/api/health/providers {providers}`, `/api/sessions {sessions}`, `/api/proxy-tokens {tokens}`, `/api/playground/presets {presets}`, `/api/playground/conversations {conversations}` — **report these five to the lead; the console tracks are told to expect the envelopes.**
- Validation: provider id `^[a-z0-9][a-z0-9._-]{0,63}$`, `base_url` parses as http(s) URL, `kind` in the adapter registry, `auth_style` in the auth vocabulary, priority 0–1000, override surfaces in `ir` surface set; `DisallowUnknownFields` on every decoder. `PUT /api/config` writes aliases and policy in one transaction. Catalog sync returns 202 and runs in the background.
- `ProviderByID` store method replacing the five load-all loops. Sentinel errors `store.ErrNotFound`, `store.ErrConflict` with `errors.Is` mapping in one helper.

### Task A5: Store
- Migration 0017 also adds indexes `(resolved_alias, ts DESC, id DESC)`, `(error_code, ts DESC, id DESC)`, `(final_provider_id, ts DESC, id DESC)`, `request_attempts(outcome, provider_id)`.
- `usage_daily` retention: prune rows older than `retention.usage_days` (default 400) in `Prune`; periodic `SweepSessions` from the same ticker; `probeLocks` entry removal on provider delete; `Forget` on credential and provider delete; `wal_checkpoint(PASSIVE)` after each prune; `PolicyOverrides` in one query; reload after commit on `context.WithoutCancel(r.Context())`; wrap the unwrapped `rows.Err()`/`RowsAffected` errors; preset create/turn append inside a transaction.
- Split `adminstore.go` into `sessions.go`, `providers_store.go`, `requests_store.go`, `analytics.go`, `discovery_store.go` (pure move). Move raw `settings` SQL out of `admin` into exported `store.GetSetting/PutSetting`. Move `store/testing.go` into `internal/store/storetest` so the production binary does not link `testing`. gofmt `admin/requests.go` and the moved test helper.
- Route-table-driven auth and leak sweep tests covering every registered route.

---

## Track web1 — `fix/web1`

**Owns:** `web/src/lib`, `web/src/routes`, `web/src/app.tsx`, `web/src/main.tsx`, `web/index.html`, `web/vite.config.ts`, `web/eslint.config.js`, `web/package.json` (+lockfile), `web/src/styles`, `web/src/theme.config.ts`, `web/src/features/shell`, `overview`, `usage`, `connect`, `settings`, `ladder`, `web/src/design-system.test.ts`, `web/src/app.test.tsx`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 2 / coupling 3 / risk 2 / size 3.

### Task W1-1: Shell identity and palette
- Replace darkraise's "A / App" brand with the darkrouter mark used on the login page; user menu: drop the empty email line, rename "Profile" to "Change password"; remove the bell (nothing feeds it) or feed it from `/healthz` warnings — pick remove; remove the React Flow attribution via the `proOptions` prop in overview's flow graph; per-route `document.title` "Providers · Darkrouter" etc. via a `usePageTitle` hook in `shell`.
- One command palette: disable darkraise's shell palette (its shortcut and the sidebar Search button open the app palette). Palette filters before slicing; matches providers by id and name; hides empty group headers; shows a "no matches" row only when every group is empty; navigates through typed routes.
- Collapsed nav links get `aria-label`s; avatar button named "Account menu".
- Tests: title per route, single palette on Ctrl+K, provider match.

### Task W1-2: Routing and error boundaries
- `lib/router.tsx`: lazy routes with `React.lazy`/TanStack `lazyRouteComponent` for every screen; `errorComponent` and `notFoundComponent` on the router; `ScreenBoundary` keyed by `location.pathname` and a working reset.
- `lib/api.ts`: query functions accept and pass `signal`; retry only on network errors and 5xx; `useInvalidate` invalidates a key, not the cache; flush `TextDecoder` at stream end; remove the forbidden `Sec-Fetch-Site` header and its comment; `loggedOut` export retained only if used.
- `routes/login.tsx`: post with `expectedRejection: "invalid password"`, show "Wrong password. Try again." inline, keep focus in the field, remove the false comments. Test.
- `package.json`: add `lucide-react` explicitly; remove `@tanstack/react-table` if unused (grep); scripts: `tsc --noEmit && vite build` order.
- `eslint.config.js`: add `react-hooks` already; add `no-restricted-imports` patterns forbidding `@/features/*/` deep imports from another feature except `index.ts`, and `import/no-cycle` equivalent via `eslint-plugin-import`? **No new packages beyond lucide-react**: implement the boundary rule with `no-restricted-imports` `patterns` only.
- `index.html`: self-host fonts is out of scope; keep Google Fonts but add `<link rel="preconnect">` and `font-display: swap`; fix `ladder.css` faces to `var(--font-mono)`/`var(--font-sans)`.

### Task W1-3: Overview, Usage, Connect, Settings
- Overview: tiles labelled "Requests / min", "Error rate", "Today's spend (UTC day)", "p50 latency"; empty spend shows "no spend yet"; legend colours derived from the same tokens as the edges; memoise `aliases/providers/failovers` inputs; remove the `Failovers`→screen import cycle by moving `failoverLabel` into `overview/failover-label.ts`.
- Usage: fix the Total breakdown charts (the series keys for the total dimension are empty; ensure the rows map to `{day, requests, tokens, cost}`); axis tick `fontSize` from `var(--text-sm)` via `tick={{ fontSize: "var(--text-sm)" }}` — verify recharts accepts a string; if not, render custom tick components with a class; legend for the provider breakdown; distinct series colours from `--chart-1..5` overridden in `chart-scope.css` to a hue-separated ramp that avoids red/green/amber; "Ranked by requests" heading corrected per dimension; loading skeleton and error banner; validate URL params before casting; `money`/`count` from `lib/format`; "UTC day" label once.
- Connect: empty-state copy points at the form's real position; label the token name input.
- Settings: put "Change password" on the page; mark the current session and sort it first; show one duration form; validate `trip_after` like `max_attempts`; use `NumberBox`; toast on reload and password change via `role="status"`; remove `useHealthz` if unused.
- `design-system.test.ts`: also scan for `fontSize={<number>}` and `fontSize: <number>` in TSX.
- Every screen: loading state = skeleton from shell, error state = `Banner` with retry, empty state = shell `EmptyState`. Tests.

### Task W1-4: Responsive shell
- Header actions collapse into the mobile sheet below 640px; subtitle truncates with ellipsis, not mid-word clipping; ensure no screen's `main` exceeds the viewport width (add `min-w-0` on the grid child and `overflow-x-auto` on table wrappers in shell's `DataTable` wrapper).

---

## Track web2 — `fix/web2`

**Owns:** `web/src/features/providers`, `web/src/features/routing`, `web/src/features/models`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 2 / coupling 2 / risk 2 / size 3.

### Task W2-1: Providers
- Row probe reads `ProbeResult.ok` and toasts the rejection (`providers-screen.tsx:146-151`).
- Test drawer resets `model/messages/log/verdict` when `row` changes (key the drawer by `row.id`).
- Discover invalidates `keys.discovery`; toggle invalidates `keys.health` and `keys.discovery`.
- Table rows are not buttons: the provider name is the link, the row keeps table semantics; actions column pinned (sticky right) and visible at 1024 and 1440; virtualise or paginate the 198-row list (DataTable is virtualised: use it); compute `accountMix`/`breakersFor` once per row via `useMemo` over the row set; discovery cell gets a tooltip with the full text.
- Wording: "credentials" everywhere (tabs, wizard, headers); "Signed" tab gets a description "SigV4 and service-account credentials".
- Provider detail: single `h1`; models table gives the name column room (`min-w-[16rem]`); price header says "$ / M tokens" once and cells use `pricePerMillion`.
- Consume the five envelope changes from admin (`/api/health/providers {providers}` etc.) in the queries **only if `lib/queries.ts` is yours — it is not**: report to lead; assume the lead patches `queries.ts` at merge.

### Task W2-2: Routing
- Preview collapses identical `(provider, model)` rows into one with a "× N credentials" chip; graph view likewise.
- Alias reorder gets keyboard controls (Move up/Move down buttons with `aria-label`s) in addition to drag.
- `AliasEditor.save` invalidates `keys.models`; memoise `rows` for `ChainGraph`; stable `fields` array for `useSearchFilters` (module constant); id generation via `useId`, not a render-time counter; `validateChain` takes a narrow type instead of fabricating a `Provider`.
- Disabled primary buttons use the outline variant so a disabled Save is not the loudest thing on screen.

### Task W2-3: Models
- Facet labels: "Surfaces", "State", "Band", "Source"; filter placeholders "Model", "Provider"; the Serves chip shows `provider/model` in full with a tooltip; columns: Model gets `min-w-[16rem]`, Publisher/Surfaces/State/Source visible at 1440 by dropping the per-row Ladder into an expandable row; `buildColumns` and `facetRow` memoised; override editor: opens the override for the row's selected provider (a provider picker when >1), Save disabled while pending and when the load errored, no 404 console noise (treat 404 as "no override" via `expectedRejection` or a `queryFn` that maps 404 to null); loading and error states.
- `priceLabel` moves to `lib/format.pricePerMillion` callers; remove the export.

---

## Track web3 — `fix/web3`

**Owns:** `web/src/features/requests`, `web/src/features/playground`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 2 / coupling 2 / risk 3 / size 3.

### Task W3-1: Requests
- `/requests/$id` opens the drawer: read `useParams`, select the row, and when the row is not in the loaded page fetch the trace directly; a missing id renders a "No request with that id" state in the drawer; opening a trace navigates to `/requests/$id` so reload and links work.
- `loadMore`: in-flight guard, error banner, and a generation counter so a filter change discards a stale page.
- Time column shows `dateTime`, header says the zone via `zoneLabel()` once; Model column shows `alias → final_model` when both exist, else the model; status shown as text plus icon; attempts as a number; facet buttons title-cased; Path column visible (drop a lower-value column or make the table scroll inside its container with sticky first column).
- Trace drawer: replace the capture.bodies commentary with "Bodies are not stored for this request"; attempt rows read `provider/model · 200 · 570 ms · passthrough · $0.0001`; latency bars scaled to the request total with TTFT as a marker; `key_label` displayed as the label; dialog title is the id only, the playground link moves into the body; `money`/`duration` from `lib/format`; error state distinguishes "expired" (404) from a failed fetch.
- `requests-columns.tsx`: `TableRow` type renamed `RequestTableRow`.
- Tests: deep link, loadMore guard, model column.

### Task W3-2: Playground
- Stale transcript: after an append, `setQueryData` on the detail key with the new turns (or invalidate it and have the load effect compare `updated_at`).
- Escape in Manage presets closes only itself (stop propagation / nested dialog handling per darkraise `Dialog`).
- Chat empty state: a visible "Choose a model" button opening the new-conversation dialog; Request pane enabled before the first message; Token Count defaults dialect to the model's provider dialect and explains a disabled Run; aux mode opens on the first listed tool.
- Compare columns show tokens and cost.
- `stream.ts`: split on `\n\n` or `\r\n\r\n`, accept `data:` without a space, join multi-line data, surface `{"error":...}` frames as a run error.
- IME: ignore Enter while `event.nativeEvent.isComposing` in `composer.tsx`, `test-drawer` (web2 owns; report), `tool-inputs.tsx`, `conversation-header.tsx`.
- Markdown: memoise the parse on the finished answer, parse the streaming tail incrementally (or parse only every 50 ms); `React.memo` on turn components; stable keys by turn id.
- `run-readings.tsx`: clear the poll on unmount; `results.tsx`/`history-rail.tsx` relative times tick via a shared 30 s interval hook; `aux-mode.tsx` handle base64 read rejection; `hydrate` limits concurrency to 4; `readOutcome` renamed to what it does; `NumberBox` for numeric inputs; `money`/`duration`/`count` from `lib/format`.
- Tests: stream parser cases, reselect-after-append, Escape nesting.

---

## Track eng — `fix/eng`

**Owns:** `.dockerignore`, `.github/`, `Dockerfile`, `compose*.yml`, `Caddyfile`, `darkrouter.example.yaml`, `README.md`, `docs/` except this plan, `THIRD_PARTY_NOTICES.md`, `go.mod`, `go.sum`, `web/go.mod` (new), `.gitignore`, `.env.example`, `CLAUDE.md`.

**Implementer:** general-purpose (Fable 5.1). **Evaluation:** spec 3 / coupling 1 / risk 2 / size 2.

### Task E1: Toolchain and CI
- `go.mod`: `go 1.26.6`; `go mod tidy`. `web/go.mod` containing `module web` so `./...` skips node_modules.
- `ci.yml`: `concurrency: { group: ci-${{ github.ref }}, cancel-in-progress: true }`; steps `gofmt -l` (fail on output), `go vet`, `staticcheck` (`go run honnef.co/go/tools/cmd/staticcheck@latest`), `govulncheck` (`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`), `npm audit --audit-level=high`, `go test -race -cover`; image job adds `sbom: true`, `provenance: mode=max`, Trivy scan (`aquasecurity/trivy-action`) on the built image; `.github/dependabot.yml` for gomod, npm (web), github-actions, docker weekly.
- Fix the two gofmt files only if not touched by their track (`admin/requests.go` and `store/testing.go` belong to admin — do not touch; report).

### Task E2: Build and deploy
- `.dockerignore`: add `.env*`, `.uat-credentials`, `data/`, `data-backup/`, `presetgen`, `darkrouter`, `.playwright-mcp/`, `docs/`, `**/node_modules`, `*.db*`, `.agents/`, `.codex/`, `providers.png`.
- Dockerfile: pin `golang:1.26.6-alpine`, `node:24-alpine` and `alpine:3.22` by tag **and** record the digest in a comment (do not pull to resolve digests; leave a TODO-free comment explaining the tag is the pin); `ARG WITH_AUGGIE=1` gating the Node + auggie layers; `HEALTHCHECK CMD wget -qO- http://127.0.0.1:8081/readyz || exit 1`.
- compose.prod.yml: `read_only: true` with `tmpfs: /tmp`, `cap_drop: [ALL]`, `mem_limit: 1g`, `pids_limit: 512`; Caddyfile: HSTS, nosniff, referrer policy headers; rate-limit note for login (Caddy has no built-in limiter; document).
- `darkrouter.example.yaml`: add `server.sse.max_precommit_bytes`; rewrite the Bedrock/Vertex block as a comment that explains the fields live in the console and cannot be set here.
- `.gitignore`: `providers.png`? No — it is an untracked user file; leave it and mention.

### Task E3: Docs
- `README.md`: dev Run section exports `DARKROUTER_MASTER_KEY`; document `rotate-key`, `-db`, the Augment provider and `WITH_AUGGIE`, the admin endpoints list (link to `docs/API.md`), backup/restore section (stop container or `sqlite3 data/darkrouter.db ".backup"`; the master key must accompany the backup; rotate-key interplay; downgrade = restore `data/`).
- `docs/ARCHITECTURE.md`: packages and dependency direction, request path (edge → IR → router → exec → adapter), failover and breaker semantics, config precedence (file, DB overlay, hot reload, restart-only), data model (tables and their time units), background workers, admin surface, console build/embed. ≤ 400 lines, current tense, no phase history.
- `docs/API.md`: every `/api` route with method, body, response envelope, status codes (read `internal/admin/admin.go` routes and handlers; reflect the admin track's contract decisions from the Global Constraints).
- `docs/PROGRESS.md`: replace the stale claims (live gate performed on the UAT instance; origin state; add phase 12/13 rows; point at ARCHITECTURE.md); `docs/ux/DONE-CRITERIA.md` test count.
- `CLAUDE.md`: move the deploy-verify procedure to `docs/DEPLOY.md` and link it; keep the rules.
- `THIRD_PARTY_NOTICES.md`: add `@augmentcode/auggie` (license from its package.json) and drop `@tanstack/react-table` if web1 removes it (coordinate: state both possibilities).

---

## Serial steps after merge (lead)

1. Merge order: eng, core, prov1, prov2, admin, web1, web2, web3. Resolve `queries.ts` envelope consumers and any golden fixture overlap.
2. Structured logging: replace `log.Printf` with `slog` (JSON handler when `DARKROUTER_LOG_FORMAT=json`, text otherwise), request id attribute on every executor log line, one logger passed through `server`.
3. `gofmt -l`, `go vet`, `go test -race ./...`, `npm test`, `npm run lint`, `npm run build`.
4. Live console check on a shadow container, then set a fresh UAT password, update `.env` and `.uat-credentials`, rebuild and redeploy per `docs/DEPLOY.md`, verify bytes.
5. Update darkmem and the file memory.
