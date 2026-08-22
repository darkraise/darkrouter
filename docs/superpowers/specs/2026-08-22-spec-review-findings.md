# Spec Review Findings and Resolutions

**Date:** 2026-08-22
**Status:** Triage complete. The revision pass applies these resolutions to the ten spec files.

---

## 1. Method

Five independent Fable reviewers, read-only, each grounded in the master design:

| Reviewer | Scope |
|---|---|
| R1 | Master design in depth, plus cross-spec consistency across all ten files |
| R2 | Phases 1–2: Go correctness, SSE, SQLite, cryptography |
| R3 | Phases 3 + 9: router purity, outcome classification, streaming commit, passthrough |
| R4 | Phases 4–5: factual accuracy of the dialect mapping tables |
| R5 | Phases 6–8: catalog sources, admin auth, Bedrock/Vertex/OAuth |

R4 and R5 were instructed to verify provider-API claims against live documentation and to state
which claims they could not verify. Their verification ledgers are reproduced in §6.

Roughly 150 findings. No reviewer challenged the core architecture — passthrough over a superset IR,
the pure router, deterministic ordered fallback, the SQLite/YAML split. Every finding below is a
detail error, a self-contradiction, or an unfilled gap.

---

## 2. Decisions

### 2.1 Settled by the operator

**D1 — Streaming usage on the passthrough path: inject `stream_options`, strip on return.**
OpenAI-compatible providers emit stream usage only when the request carries
`stream_options: {"include_usage": true}`. Passthrough forwards bodies untouched, so token counts
would be null on the most-travelled route. Resolution: a second whitelisted body mutation for
`openaicompat` targets, with the extra final usage chunk stripped from the response so the client
sees exactly what it expected. Phase 9 §5's "only two things change" becomes three, documented.

**D2 — Local model capabilities: probe where possible, then route-with-warning.**
Discovery reads Ollama's `/api/show` to learn whether a model's template advertises tools, and
populates capabilities properly. For anything still unknown, the router admits the candidate and
records a warning rather than excluding it. A provider's own error is clearer than Darkrouter
silently refusing to route, and the trace explains the decision.

**D3 — Responses API: hard-fail stateful, support stateless.**
A request carrying `previous_response_id`, `conversation`, or built-in tool declarations that the
resolved target cannot honor returns an explicit error rather than a confident amnesic answer.
Stateless Responses calls are supported everywhere, which still requires specifying the
semantic-event stream writer.

**D4 — Process: ledger first, then revise.** This document.

### 2.2 Taken during triage — override any of these

**D5 — `oauthsub` is not a wire-format kind.** Five kinds are defined by payload shape
(`openaicompat`, `anthropic`, `gemini`, `bedrock`, `vertex`); OAuth is an *auth modifier* composed
with one of them. A preset declares `auth: {style: oauth, ...}` alongside its `kind`. Master §6's
"six adapter kinds" becomes five kinds plus an auth dimension. Without this, phase 8 cannot say what
`oauthsub.BuildRequest` emits, because a Claude subscription speaks Messages and an OpenAI one does
not. *(R1-A2)*

**D6 — Vertex dispatches per publisher.** `publishers/google` uses `generateContent` with the Gemini
payload; `publishers/anthropic` uses `rawPredict`/`streamRawPredict` with the **Anthropic Messages**
payload, the model relocated from body to URL, and a mandatory
`anthropic_version: "vertex-2023-10-16"` field. The publisher is stored on the catalog entry. Llama
and Mistral MaaS on Vertex (a third, OpenAI-compatible shape) are out of scope for v1. *(R5-F1)*

**D7 — Transcription uploads buffer to the size cap.** Streaming the multipart body straight through
makes failover impossible and makes the in-form `model` field unrewritable. Buffering to the existing
cap restores both. Failover is the product; single-attempt is the wrong trade. *(R1-C8, R4-D1)*

**D8 — A single 5xx does not cool; it counts toward `trip_after`.** Master §8's "cool this triple"
wording is wrong and phase 2's counter model is right. Additionally: `RetryableProvider` from a
5xx/timeout/reset **skips the provider's remaining keys**, because the next key will hit the same
dead upstream; a 429 advances to the next key of the same provider, because rate limits are usually
per key. This makes the credential/provider distinction meaningful, which it currently is not.
*(R1-C15/A5, R2-C3, R3-C3/A3)*

**D9 — Post-commit streams are governed by an idle timeout, not `total`.** `policy.timeout.total`
bounds the pre-commit phase. Once committed, an inter-event idle timeout (default 120s) applies
instead, so a legitimate ten-minute reasoning response is not killed while a silent provider still
is. *(R1-A7)*

**D10 — Embedding failover across models is permitted but flagged.** Vectors from different models
are not comparable, and a client filling an index across a failover corrupts it silently. Rather than
new config, an embedding request that lands on a different model than the first candidate records a
warning on the request row, and the documentation states that embedding aliases should list
same-model targets only. *(R4-D4)*

**D11 — OAuth completion supports a manual paste path.** Subscription vendors register public-client
redirect URIs like `http://localhost:{port}/callback`; a homelab admin origin will be rejected before
any code flows. The UI presents the authorize URL, the operator completes in a browser, and pastes
the redirected URL back. Where a vendor's registered URI permits it, a temporary localhost listener
is used instead. `state` and PKCE are validated on both paths. *(R5-S1)*

**D12 — Pricing is stored per million tokens.** models.dev prices in USD per million tokens as
floats. `input_price_micros` storing micro-dollars *per token* truncates $0.14/M to integer zero.
The columns become `input_price_micros_per_mtok` / `output_price_micros_per_mtok`, so $0.14/M is
140,000. *(R5-F5)*

**D13 — One term: "surface".** The seven-value enum (`llm`, `embedding`, `image`, `tts`, `stt`,
`rerank`, `moderation`) is called a surface everywhere. "Service kind" is retired. *(R1-C22)*

### 2.3 Applied provisionally — revisit before phase 5

**O1 — The rerank wire shape.** OpenAI has no rerank endpoint; Cohere v2, Jina, and Voyage differ.
The specs adopt **Cohere v2** as Darkrouter's rerank contract, keeping the `openaicompat` kind, with
each preset declaring its own rerank path via the `rerank-path=` quirk. Providers whose shape deviates
materially are excluded from the surface rather than special-cased. This was applied as the
recommendation rather than settled by the operator, and should be revisited if the actual provider mix
argues for Jina's or Voyage's shape. *(R4-F15/A8, R1-C21)*

---

## 3. Factual corrections

Each of these is wrong in the specs as written, verified by the reviewer noted.

| # | Spec | Correction |
|---|---|---|
| F1 | Phase 4 §4.4 | **Gemini streaming chunks are incremental, not cumulative.** Each chunk carries new content to append; text parts are fragments, `functionCall` arrives whole. The instruction to "diff successive chunks" would produce garbage. Replace with append semantics. *(R4)* |
| F2 | Phase 8 §4 | **Vertex third-party models use the Anthropic payload over `rawPredict`,** not Gemini's over `generateContent`. See D6. *(R5, live-verified)* |
| F3 | Phase 8 §3.3 | **Bedrock model IDs are not region-qualified** — region is an endpoint property. What carries a geo prefix (`us.`, `eu.`) is the cross-region inference profile ID. *(R5, live-verified)* |
| F4 | Phase 8 §3.3, Phase 6 §5 | **Bedrock discovery needs two calls.** `ListFoundationModels` (control plane, not `bedrock-runtime`) returns bare IDs that are frequently not on-demand invocable; the invocable profile IDs come from `ListInferenceProfiles`. Cataloguing "as discovered" would store exactly the IDs that fail. *(R5)* |
| F5 | Phase 8 §3.2 | **`ConverseStream` returns AWS binary eventstream**, not SSE. Since the design bypasses the service client for the standalone signer, the adapter must decode eventstream framing itself. "One streaming implementation across every adapter" is false and must be amended. *(R5, live-verified)* |
| F6 | Phase 4 §4.3 | **Anthropic has a content-filter stop reason: `refusal`.** The table's "—" is wrong. It also returns `pause_turn` and `model_context_window_exceeded`, neither mapped. *(R4, live-verified)* |
| F7 | Phase 4 §4.2 | **Anthropic thinking blocks are `{type, thinking, signature}`** and redacted are `{type, data}`. The table omits both payload fields, and omits the invariant that blocks must be passed back unmodified and in order. *(R4, live-verified)* |
| F8 | Phase 4 §4.2, Master §5 | **`cache_control` carries a `ttl` of `5m` or `1h`,** and a request may hold at most 4 breakpoints (a 5th is a 400). The IR has no `ttl` field, so an Anthropic→IR→Anthropic round trip silently drops a paid feature — the exact failure the warning mechanism exists to prevent. Add `ttl` to `ir.CacheControl`. *(R4, live-verified)* |
| F9 | Phase 4 §4.2 | **Gemini `fileData` accepts Files API URIs, not arbitrary HTTP image URLs.** An IR image carrying a public URL must be downloaded and inlined as base64 (under a size cap) or dropped with a warning. *(R4)* |
| F10 | Phase 4 §4.2 | **Gemini reasoning continuity needs `thoughtSignature`,** not just `thought: true`. Signatures must be returned unmodified or reasoning state is lost across turns. *(R4)* |
| F11 | Phase 4 §4.2 | **OpenAI Chat Completions does support documents** via `{type:"file", file:{file_data|file_id}}` parts. The table states an API-level absence that does not exist; it is a per-provider capability, not a protocol gap. *(R4)* |
| F12 | Phase 4 §4.3 | **The Gemini finish-reason enum is larger** than the four mapped: `RECITATION`, `LANGUAGE`, `OTHER`, `PROHIBITED_CONTENT`, `SPII`, `MALFORMED_FUNCTION_CALL`, `IMAGE_SAFETY`. Separately, a blocked *prompt* returns **zero candidates** with `promptFeedback.blockReason` and no finish reason at all — an adapter reading `candidates[0]` nil-derefs. *(R4, partly live-verified)* |
| F13 | Phase 9 §6 | **Anthropic splits usage across two events.** `input_tokens` and cache counts arrive in `message_start`; `message_delta` carries `output_tokens`. Scanning only `message_delta` records output-only usage and computes wrong cost. *(R3, R5)* |
| F14 | Phase 9 §5 | **The byte-for-byte claim is false.** `json.Marshal` HTML-escapes `<`, `>`, `&` inside `RawMessage` values, compacts whitespace, and deduplicates top-level keys. Fix by using an encoder with `SetEscapeHTML(false)`, weakening the claim to semantic preservation, and skipping the rewrite entirely when requested and target model names already match. *(R3)* |
| F15 | Phase 6 §4 | **models.dev field mapping was never specified.** Verified: `https://models.dev/api.json`, keyed by provider, each model carrying `cost.input`/`cost.output` (USD per million, float), `limit.context`/`limit.output`, `tool_call`, `reasoning`, `attachment`, `modalities`. There is no `vision` flag — it is `modalities.input` containing `image`. *(R5, live-verified)* |
| F16 | Phase 4 §4.1 | **OpenAI's `developer` role is missing** from the role table; newer clients send it in place of `system`. Legacy `function` role and `functions`/`function_call` fields also still arrive. *(R4)* |
| F17 | Phase 4 §4.2 | **The `audio` block has no row** despite being declared in the master IR. OpenAI uses `input_audio`, Gemini `inlineData` with an audio MIME type, Anthropic does not support it. *(R4)* |
| F18 | Phase 5 §6 | **Transcriptions break JSON-out too.** `response_format` of `text`/`srt`/`vtt` returns a plain-text body, and `stream=true` yields SSE transcript events. The spec claims only two surfaces break the JSON assumption. *(R4)* |
| F19 | Phase 5 §7 | **`gpt-image-1` does report usage** and always returns base64, ignoring `response_format`. Only the dall-e models report nothing. *(R4)* |
| F20 | Phase 4 §3, §6 | **The Gemini `:countTokens` route is missing,** and native forwarding is possible for Gemini targets, not only Anthropic ones. Gemini CLI calls it for context accounting. *(R4)* |

---

## 4. Contradictions

Statements in the specs that conflict with each other. All are mine, not reviewer misreadings.

| # | Where | Conflict | Resolution |
|---|---|---|---|
| X1 | Phase 2 §3.2 vs §8 | "All writes go through a single owning goroutine" vs three writing background workers plus the health persister plus the first-run import | One write handle with `MaxOpenConns(1)` shared by all writers; rewrite §8's "pause so pruning cannot lock out the writer" accordingly |
| X2 | Phase 2 §6 vs §10 | "Drop the record on saturation" vs the done criterion "every request produces one row" | Qualify the criterion, and state the consequence the spec omits: dropped records mean undercounted spend, so `usage_daily` is a lower bound |
| X3 | Phase 2 §7.1 | "Any response proving reachability, including 400, resets the ladder" vs "401/402/403 cool the key" — both fire on the same event with opposite effects | Reset applies only to `Success` and `Fatal` outcomes, scoped to the exact triple. `RetryableCredential` never resets. Otherwise a billing-exhausted key is resurrected by any malformed request |
| X4 | Master §8 vs Phase 2 §7.1 vs Phase 3 §7.1 | Does one 5xx cool immediately, or count toward `trip_after`? | D8 |
| X5 | Phase 9 §4 | Gemini listed as passthrough-eligible, but eligibility requires a top-level `model` field in the body — Gemini carries the model in the URL path | Define Gemini passthrough as a URL-path rewrite; reword the predicate to "the model is rewritable without decoding the body's semantics" |
| X6 | Phase 5 §6 | Transcription bodies "streamed rather than buffered" vs "subject to the full failover path" — a streamed body cannot be replayed | D7 |
| X7 | Master §17 | The delivery order describes eight phases and never mentions auxiliary surfaces | Insert phase 5 |
| X8 | Master §1/§7/§12 vs Phase 8 | "Stays at eighteen endpoints" vs phase 8 adding two OAuth endpoints | List twenty; drop the "stays at" framing |
| X9 | Master §8 vs Phase 3 §7.1 | Five outcomes vs six — the master lacks `ClientCancelled` | Add it to the master |
| X10 | Master §10 vs Phase 3 §8 | "Three response headers" vs four — the master lacks `X-Darkrouter-Request`, which phase 7's trace linking needs | Add it |
| X11 | Master §4 vs Phase 9 §4 | Two different passthrough eligibility lists, neither a superset of the other | One authoritative list in the master, referenced by phase 9 |
| X12 | Master §4, Phase 9 §4 | Both gate eligibility on "the alias applied no parameter overrides" — a feature no spec defines | Delete the condition; alias overrides are not in scope |
| X13 | Phase 6 §6 vs §3 | The merge table's existence row cites presets, but the preset schema contains no model list | Existence falls back to models.dev, not presets; correct the table |
| X14 | Phase 6 §5 | "A per-provider concurrency cap so forty providers do not open forty connections on boot" — a per-provider cap cannot achieve that | Global cap across the discovery fleet |
| X15 | Phase 5 header | "Independent of phases 4, 6, and 8" while relying on phase 4's warning mechanism and phase 6's surface metadata | Correct the dependency line |

---

## 5. Gaps to fill

Grouped by the revision each requires.

### 5.1 Master design

- Data model additions: surface list and max-output-tokens on `models`; region, project, location, and capability-override columns on `providers`; `expires_at`, `scope`, and credential type on `provider_keys`; the renamed pricing columns from D12. *(R1-C14)*
- `internal/provider` and its `Source` interface are used from phase 1 but absent from the package table. *(R1-C13)*
- `ir.Response`, `ir.Usage`, and `ir.Error` are named but never defined. `Usage` needs cached-token and reasoning-token fields or Anthropic and OpenAI cost accounting is wrong from day one. *(R2-G1)*
- `Extra` exists only on `ir.Request`; `ContentBlock` and `Response` need it too, or the IR path silently narrows responses on same-dialect routes. *(R1-A9)*
- Inbound auth mapping is written down only for Gemini. Add a table covering how OpenAI `Bearer`, Anthropic `x-api-key`, and Gemini `x-goog-api-key`/`?key=` are each checked against `proxy_token`. *(R1-G7, R4-D5)*
- `server.max_body_bytes` and a global in-flight concurrency limit — neither exists anywhere, and failover requires holding the full body across attempts. *(R1-G1/G2)*
- `policy.log_retention` — the retention worker prunes request rows against a window that no config key defines. *(R1-G4, R2-G5)*
- A `/metrics` endpoint and a defined `/healthz` payload; both specs hang counters off "the health endpoint" whose shape is never given. *(R1-G3)*
- The `provider/model` split rule, given that model IDs legitimately contain slashes. *(R1-A11, R3-A5)*
- `policy.retry.on` does not match the classification tables and may be dead config — either make classification configurable or reduce the key to `max_attempts`. *(R1-C17)*
- The backoff ladder ends at 120s but is said to clamp at a 15m default max, which it can never reach. *(R1-C16)*

### 5.2 Phase 1

- **SSE parsing rules are never specified** — the single most consequential gap. Needs: multiple `data:` lines joined with newline; comment lines beginning `:` ignored (OpenRouter emits `: OPENROUTER PROCESSING` keepalives, so a naive parser breaks on the most popular aggregator); `event:`/`id:`/`retry:` fields; optional space after the colon; blank line as delimiter; the `data: [DONE]` sentinel; EOF without `[DONE]`. *(R2-A2)*
- fsnotify must watch the **parent directory** and filter by filename, with a short debounce. Watching the file loses rename-based editor saves entirely. *(R2-C1)*
- `policy.timeout.first_byte` is ambiguous between response headers, first body byte, and first content token — three different implementations. *(R2-A1)*
- The `Passthrough` return value and `iter.Seq2` error semantics are undefined, and phase 1 ships both interfaces. *(R2-A3)*
- Unset optional `${ENV}` must not fail validation the way an unset required one does. *(R2-A5)*
- SSE response headers that make flushing survive reality: `text/event-stream`, `no-cache`, `X-Accel-Buffering: no`, and no compression middleware on the streaming path. *(R2-G2)*
- No global `WriteTimeout` on the proxy server, or long streams die at it; `ReadHeaderTimeout` instead. *(R2-C13)*
- Which config fields are hot-reloadable — listen addresses cannot be. *(R2-A6)*
- Graceful shutdown ordering and a configurable drain deadline. *(R2-G4/G10)*
- Whether aliases appear in client-facing `/v1/models`. *(R1-G11)*

### 5.3 Phase 2

- PBKDF2 iteration count, stored alongside the salt so it can be raised later; expected master-key format; bcrypt cost; session lifetime and storage. *(R2-S1, R5-A6)*
- Bind ciphertexts to their rows with the key ID as GCM additional authenticated data. *(R2-S2)*
- A master-key rotation mechanism — there is no endpoint and no CLI subcommand, and rotation needs both keys simultaneously. *(R2-S3)*
- Rotation and key inserts need `synchronous=FULL` or a WAL checkpoint; a power loss after commit but before sync rolls back a rotation the operator has already switched environments for. *(R2-C11)*
- PRAGMAs must be applied per connection via the DSN, not once — `foreign_keys` silently off on pooled connections otherwise. *(R2-C4)*
- Half-open probe admission needs an atomic claim, or every concurrent request at expiry becomes a probe. *(R2-C10, R1-A4)*
- Clamp `Retry-After` at `policy.cooldown.max`; a provider sending 86400 removes itself for a day. *(R2-C9)*
- Client cancellation must be distinguished from provider timeout at the transport layer, using `context.WithCancelCause`, or a Ctrl-C trips breakers on healthy providers. *(R2-C2, R3-A4)*
- Flush the health map and drain the log channel on shutdown, or "cooldowns survive a restart" is false. *(R2-C6/G7)*
- First-run import must be one transaction covering inserts and marker; state the exact predicate and what happens when a referenced env var is missing. *(R2-G6/A8)*
- Foreign-key delete behavior on log tables — deleting a provider must not cascade away request history. *(R2-A11)*
- `cost_micros` is written in phase 2 but pricing arrives in phase 6: NULL until priceable. *(R2-A12)*
- Rollup day boundary in UTC, keyed on request start, with idempotent recomputation. *(R2-C14)*
- WAL growth under long-running read transactions; `wal_autocheckpoint` and `journal_size_limit`. *(R2-G8)*

### 5.4 Phase 3

- The router signature cannot produce `Candidate.KeyID` — nothing in its inputs supplies providers or keys. Add a provider/key snapshot parameter. *(R3-C2, R1-C12)*
- "No clock" is impossible while filtering on `cooling_until`. Either the health snapshot carries resolved availability with an evaluation timestamp, or `now` becomes a parameter. *(R3-C1)*
- A 2xx followed by an in-stream error before commit has no classification row — and Anthropic sends `overloaded_error` as an in-stream event under a 200. *(R3-C5)*
- No default bucket: DNS failure, connection refused, TLS errors, EOF before headers, HTTP/2 GOAWAY, 3xx, 413, 451 are all unclassified. Go's client follows redirects by default, silently converting a POST to a body-less GET. *(R3-C6)*
- Blanket "404 = model not found" means a permanently misconfigured base URL is skipped silently forever with nothing in health state to surface it. *(R3-C7)*
- "Content" is not defined well enough for commit, and a reasoning model can emit thinking deltas for well over 60 seconds before the first text token. *(R3-C8)*
- Pre-commit buffer-and-replay is implied but never stated, and the buffer is unbounded in bytes. *(R3-C9)*
- Retry safety: a pre-commit failover may double-bill when the first provider already processed the prompt. The inbound body must be fully buffered to be replayable, which is never stated. *(R3-C10)*
- LRU key ordering by persisted `last_used_at` does not rotate under concurrent load with async persistence. Keep it in memory, updated synchronously at attempt start. *(R3-CN1)*
- Whether exec re-checks live health per attempt or honors the stale snapshot. *(R3-CN2)*
- "Replay against the recorded snapshots" is promised but nothing records them. Either persist a compact routing snapshot per request or soften the claim. *(R3-CN4)*
- Per-attempt deadline arithmetic and the minimum viable budget. *(R3-A1/A2)*
- Unary commit semantics are undefined. *(R3-A6)*
- Diagnostic headers on the failure path. *(R3-G2)*
- What `NeedsTools` filters against before phase 6 exists. *(R3-G4, R1-C12)*

### 5.5 Phase 4

- **A tools and `tool_choice` mapping table** — the spec claims "the complete mapping tables" and has none. Includes the non-obvious equivalence OpenAI `required` ↔ Anthropic `any` ↔ Gemini `ANY`, and that Gemini puts all declarations inside a single `tools[0].functionDeclarations[]`. *(R4-A5)*
- **Tool-call ID strategy for Gemini**, whose `functionCall`/`functionResponse` IDs are optional and usually absent. Parallel calls to the same function break unless matching is positional. *(R4-F14)*
- `ResponseFormat` and `Reasoning` mappings per target, including the effort-to-token-budget conversion rule. *(R4-A6)*
- Reverse tool-result splitting: an Anthropic user turn mixing `tool_result` blocks and text becomes one OpenAI `tool` message per ID, immediately after the assistant message carrying the matching `tool_calls`. Wrong order is a 400. *(R4-A4)*
- Anthropic `max_tokens` synthesis when the inbound request carries no cap. *(R4-F13)*
- Gemini model-in-path extraction, given aliases are single tokens but `provider/model` names contain slashes. *(R4-A1)*
- What the Gemini edge emits: `?alt=sse` versus the chunked JSON-array form. *(R4-A2)*
- `tool_result` carrying an image has a golden fixture but no defined mapping for OpenAI or Gemini. *(R4-A7)*
- Multiple or mid-conversation system messages, and the Anthropic assistant-prefill idiom. *(R4-A9)*
- Anthropic inbound auth (`x-api-key`), and pinning `anthropic-version` outbound. *(R4-D5)*
- Unknown SSE event types must be tolerated, not error — Anthropic explicitly warns clients about future event types. *(R4-G3)*
- The `x-darkrouter-estimated` header breaks the `X-Darkrouter-*` convention, and no estimation method is named. *(R4-D7)*

### 5.6 Phase 5

- The seven-surface × adapter matrix — which adapter serves what is never stated, making the done criterion unfalsifiable. *(R4-A8)*
- Responses semantic-event stream writer per D3. *(R4-D3)*
- Transcription response direction: plain-text bodies and `stream=true`. *(R4-F18)*
- `/v1/audio/translations` explicitly declared out. *(R4-G6)*
- Speech `stream_format: "sse"` would be mishandled by binary-outbound handling. *(R4-G7)*
- Commit semantics for binary surfaces: a fast 200 followed by truncated audio cannot be failed over and has no error vocabulary. *(R1-A10)*

### 5.7 Phase 6

- Preset schema needs an `oauth:` block (authorize and token endpoints, client ID, scopes, redirect constraints) or phase 8 hardcodes per-vendor flows, contradicting "breadth costs data, not code". *(R5-A8)*
- Model-ID normalization between discovery, models.dev, and presets — without a join rule the metadata merge silently fails and everything falls to inference. Add `models_dev_id` to presets. *(R5-A2)*
- What a *successful* discovery does with a model that has disappeared. Replace-on-success breaks aliases; union-forever accumulates dead models that cost a `RetryableModel` attempt on every request forever — and by the outcome table nothing ever penalizes them. *(R5-D2)*
- Which key authenticates a discovery probe, and whether a probe 401 cools it. *(R5-A4)*
- Quirk vocabulary growth mechanism, and whether quirks are bare tags or tag-plus-value. *(R5-D6)*
- Vertex has no practical model-listing API; seed from presets and models.dev instead of pretending discovery exists. *(R5-D8)*
- models.dev URL, schema contract, fallback, and data licensing. *(R5-G1)*
- Rewiring client-facing `/v1/models` and `/v1beta/models` to the catalog is never in phase 6's scope. *(R1-C20)*
- Preset upgrade reconciliation when a binary ships a renamed or removed preset that provider rows reference. *(R1-G6)*

### 5.8 Phase 7

- Keyset cursor contract: sort order, tuple comparison, tie-break requiring orderable IDs, wire encoding, filter binding, and the supporting composite index. *(R5-A5)*
- Session storage and lifetime; rotation on login; logout semantics. *(R5-A6)*
- Whether a successful credential probe clears the breaker — the operator seeing "probe OK" beside "still cooling" is the confusion the probe exists to prevent. *(R5-A7)*
- Harden CSRF by binding the token to the session or checking `Origin`/`Sec-Fetch-Site`; on a plain-HTTP LAN naive double-submit is defeated by an attacker who can set a cookie. State that the proxy port never honors cookies. *(R5-S2/S3)*
- Alias-to-provider references must validate as **warnings**, not errors — otherwise deleting a provider in the UI makes every future config reload fail, and the only affordance is a reload button that keeps failing. *(R5-D4)*
- `GET /api/presets` — nothing exposes the preset list that `POST /api/providers` consumes. *(R1-G5)*
- Polling intervals, stated as numbers. *(R5-D5)*
- `GET /api/auth/status` must be exempt from the blanket auth requirement. *(R5-G5)*
- CSRF on the SSE playground endpoint. *(R5-G3)*

### 5.9 Phase 8

- Bedrock per-model Converse feature support varies; capability metadata still matters. *(R5-F4)*
- Refresh-token rotation: persist the new pair before the old is considered replaced, or a crash bricks the account. Note the single-instance assumption. *(R5-S4)*
- Split refresh failures: `invalid_grant` is terminal and must never retry; a 5xx is transient and takes the ladder. *(R5-S5)*
- The probe must share the per-account refresh mutex, since a probe that consumes a refresh races the worker. *(R5-G4)*
- "Credential expiry alerting" is in scope and never designed — either design it or delete the phrase. *(R5-A9)*
- Phase 8's adapter kinds must extend phase 4's golden suite. *(R1-C19)*
- Dependency correction: the OAuth third of phase 8 requires phase 7. *(R1-C10)*

### 5.10 Phase 9

- **Raw-stream commit detection is entirely unwritten** — the largest single gap in the phase. Passthrough deliberately does not parse to IR, yet phase 3's commit rule requires recognizing the first content event, pre-commit in-stream errors, and buffer-replay. Needs a per-dialect raw recognizer. *(R3-A10)*
- `content-type` is not in the header allowlist, so upstreams receive no content type. `openai-beta` is also missing, breaking the spec's own fidelity argument for OpenAI clients. `Content-Length` recomputation and Gemini `?key=` stripping are unstated. *(R3-C13)*
- Response-header handling is entirely unspecified, and Go's transparent gzip will produce a body labelled with a stale `Content-Encoding` or wrong `Content-Length`. *(R3-C14)*
- A passthrough 400 classified `Fatal` converts a recoverable request into a hard failure, where the IR path would have dropped the offending field and succeeded. Retry the same candidate via the IR path first. *(R3-C17)*
- The non-streaming usage tee caps a *prefix* buffer, but usage sits at the end of the body — it fails precisely on the long completions where cost is highest. *(R3-G6)*
- The tee must scan inline in the forwarding loop, not through a pipe to a goroutine, or a slow scanner stalls the client stream. *(R3-CN3)*
- Response-side comparison rules for the differential suite. *(R3-A11)*
- `oauthsub` exclusion from passthrough is implicit and unexplained, despite an Anthropic subscription being a common Claude Code route. *(R3-A9)*
- Dependency correction: phase 9 also depends on phase 6 for quirk declarations. *(R3-G7, R1-C11)*
- Inbound `Content-Encoding` handling and body size cap. *(R3-G5)*

---

## 6. Verification ledger

**Live-verified by R5:** models.dev exists and serves `/api.json` with the field shapes in F15;
`darkraise-ui` is published at exactly 6.4.0; Bedrock Converse is genuinely a unified cross-family API
and AWS documents it as the recommended choice over InvokeModel; cross-region inference profiles
carry geo prefixes and are increasingly required; Claude on Vertex uses `rawPredict` with the
Anthropic payload.

**Live-verified by R4:** Anthropic's full stop-reason set including `refusal`, `pause_turn`, and
`model_context_window_exceeded`; Anthropic thinking and redacted-thinking field names; Anthropic
image source types including `file`; prompt caching's 4-breakpoint limit, `ephemeral` type, and
`5m`/`1h` TTLs; Gemini `?alt=sse`; Gemini `promptFeedback.blockReason` values.

**Assessed from training knowledge, not live-verified:** Gemini incremental streaming semantics;
`thoughtSignature`; `fileData` URI restrictions; optional `functionCall` IDs;
`toolConfig.functionCallingConfig`; OpenAI `file` content parts; `stream_options.include_usage`;
transcription `stream=true`; Responses API statefulness and event names; `golang.org/x/oauth2/google`
refresh behavior; SigV4 mechanics; OAuth vendor redirect-URI registration; cookie port-scoping.

**Could not verify at review time, both since settled during phase 4 and recorded in that spec's
§4.6 and §4.7:** Anthropic `max_tokens` is still strictly required, and structured output is now
generally available under `output_config.format` with no beta header. Phase 4 also found a third
thing the review did not anticipate — extended thinking split into two mutually exclusive
per-generation shapes — which is why phase 6 moved the shape onto the catalog entry rather than
reading it off the model name.

---

## 7. Revision plan

1. Master design — absorb the drift (X7–X12), the data model additions, the IR completions, and the missing config keys. Everything else references it, so it goes first.
2. Phases 2, 3 — resolve the contradictions and fill the routing gaps. These block implementation.
3. Phases 8, 9 — apply D5, D6, D11, and the factual corrections; write the raw-stream commit recognizer.
4. Phases 4, 5 — correct the mapping tables, add the missing tables, apply D3 and D7.
5. Phases 1, 6, 7 — clarifications and the targeted fixes above.
6. README — dependency corrections for phases 5, 8, and 9.
