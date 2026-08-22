# Darkrouter — Design

**Status:** Approved design, revised 2026-08-22 against `2026-08-22-spec-review-findings.md`.
**Date:** 2026-08-22

---

## 1. Purpose

Darkrouter is a self-hosted LLM gateway for a single-operator homelab. It presents one endpoint that
speaks the three dialects real clients use — OpenAI, Anthropic Messages, and Google Gemini — and
routes each request across a broad set of upstream providers with deterministic failover. Alongside
chat it serves the auxiliary surfaces: embeddings, responses, images, audio, rerank, and moderations.

It takes OmniRoute's connectivity breadth and discards its feature breadth. OmniRoute is 2,982
TypeScript files, 516k lines, 118 dashboard pages, 677 API routes, and 130 npm dependencies, because
it also does prompt compression, CLI config patching, A2A/MCP/ACP, batch APIs, gamification, and
evals. Darkrouter does four things — unified endpoint, failover with key rotation, routing aliases,
and usage observability — and is sized accordingly: four screens and twenty-one admin endpoints.

### Success criteria

- Any tool configured against one base URL reaches every configured provider without reconfiguration.
- A provider outage or rate limit is invisible to the client, and the trace afterwards explains exactly what happened.
- Adding a catalogued provider is a UI action; adding an uncatalogued one is a base URL and a key. Neither requires code.
- The whole system is one static binary plus one SQLite file plus one YAML file.

## 2. Non-goals

Explicitly out of scope, and not to be added without revisiting this document: prompt compression,
CLI config file patching, MCP/A2A/ACP, batch and files APIs, video, music, OCR, web search,
`/v1/audio/translations`, gamification, evals, multi-tenancy and RBAC, web-cookie and
browser-scraped providers, cloud sync, and adaptive or learned routing.

Darkrouter does not inherit code, structure, or roadmap from the existing `llm-proxy` repository.

## 3. Architecture

One Go binary (Go 1.26) embedding a React SPA. Two HTTP servers on separate ports from one process:
the **proxy server** serving `/v1/*` and `/v1beta/*`, and the **admin server** serving `/api/*` plus
the SPA.

| Package | Responsibility |
|---|---|
| `internal/ir` | The canonical request, response, and stream-event types. No I/O, no dependencies on other internal packages. |
| `internal/edge` | One `Dialect` per inbound API shape: openai, anthropic, gemini. Parses HTTP into `ir.Request`, writes `ir` back out in that dialect's shape, including its error shape. |
| `internal/adapter` | One `Adapter` per outbound kind: openaicompat, anthropic, gemini, bedrock, vertex. Renders `ir.Request` to an upstream HTTP request; parses upstream responses and streams back to `ir`. |
| `internal/auth` | Upstream credential strategies composed with adapters: static key, SigV4, service-account JWT, OAuth with refresh. |
| `internal/provider` | The `Source` interface and provider/key snapshots consumed by the router. |
| `internal/router` | Resolves a requested model name into an ordered candidate list. Pure function of its inputs. No I/O. |
| `internal/exec` | The attempt loop: take candidate, send, classify outcome, record health, advance. Owns stream commit semantics. |
| `internal/catalog` | Merges embedded presets, models.dev metadata sync, and live discovery into one queryable model index. |
| `internal/store` | SQLite. Providers, encrypted credentials, request log and attempts, health state, usage rollups, discovery cache. |
| `internal/config` | `darkrouter.yaml` load, validation, and hot reload. |
| `internal/crypto` | PBKDF2 key derivation, AES-GCM for credentials at rest, bcrypt for the admin password. |
| `internal/admin` | Admin REST API, session auth, SPA serving, Vite reverse-proxy in dev mode. |
| `internal/server` | Wiring, background workers, graceful shutdown. |
| `web/` | React SPA. |

`router` being a pure function is deliberate. Failover is where subtle bugs live, and this makes the
whole decision surface testable without a network or a database.

Runtime dependencies are kept few: `modernc.org/sqlite` (pure Go, so the binary stays static),
`gopkg.in/yaml.v3`, `fsnotify`, `aws-sdk-go-v2` (SigV4 signer and eventstream decoder),
`golang.org/x/oauth2/google`, `golang.org/x/crypto`.

## 4. Request path

Darkrouter is passthrough-first with a canonical-IR fallback.

When the inbound dialect already matches the chosen target's dialect — an OpenAI client hitting Groq,
Claude Code hitting Anthropic — the request takes the **passthrough path**: the model identifier is
rewritten, auth headers are swapped, and the remaining bytes are forwarded. The response stream is
piped through while an inline scanner extracts the usage record for accounting. This is
near-byte-faithful, so provider parameters Darkrouter has never heard of work on the day they ship.

When dialects differ — Claude Code failing over to Groq, a Gemini client reaching Anthropic — the
request takes the **IR path**: the edge dialect parses to `ir.Request`, the adapter renders it to the
target's wire format, and the response or stream is parsed back to `ir` and re-emitted in the inbound
dialect's shape.

Every request is parsed to IR regardless, because routing needs to know whether the request carries
tools, images, or a reasoning budget. Passthrough therefore saves the *render* and the per-event
re-serialization, not the parse. The raw body is retained alongside the IR for the request's lifetime,
because eligibility is decided per attempt.

### 4.1 Passthrough eligibility

This list is authoritative; phase 9 references it rather than restating it. A candidate is eligible
when all hold:

- The inbound dialect maps to the target adapter's kind: OpenAI to `openaicompat`, Anthropic to `anthropic`, Gemini to `gemini`.
- The model identifier is rewritable without decoding the body's semantics — a top-level body field for OpenAI and Anthropic, a URL path segment for Gemini.
- The target's preset declares no quirk requiring request rewriting.
- The adapter does not require a materialized, signed body. This excludes `bedrock` permanently, and `vertex` because its URL encodes both model and publisher.
- The surface's body is JSON. Multipart and binary surfaces take the IR path.

Eligibility is decided **per attempt, not per request**. An Anthropic-inbound request may pass
through to Anthropic on the first attempt and translate to `openaicompat` on the second.

An `oauthsub` credential does not by itself disqualify passthrough: OAuth is an auth strategy, and if
the underlying kind matches the inbound dialect the fast path applies with the bearer token swapped
in.

### 4.2 Permitted body mutations on the passthrough path

Exactly three, and no others:

1. The model identifier, rewritten to the target's name for it.
2. Authentication, swapped from the inbound credential to the target's.
3. For `openaicompat` targets on a streaming request, `stream_options: {"include_usage": true}` is
   injected when absent, and the resulting extra final usage chunk is stripped from the response.

The third exists because OpenAI-compatible providers emit no stream usage unless asked, and without
it token accounting would be blind on the most-travelled route. Stripping the extra chunk keeps the
client's view identical to what it would have received directly. A provider whose preset declares it
rejects `stream_options` is not passthrough-eligible for streaming requests.

## 5. Canonical IR

The IR is a genuine superset, not OpenAI's shape with extras bolted on. Modelling it on OpenAI's chat
format is how gateways silently lose Anthropic thinking blocks and cache control.

```go
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
    Reasoning      *Reasoning       // effort or token budget
    ResponseFormat *ResponseFormat  // json_schema
    Safety         []SafetySetting  // Gemini-only; dropped elsewhere
    Metadata       map[string]string
    Extra          map[string]json.RawMessage
}

type Message struct {
    Role    Role // system | user | assistant | tool
    Content []ContentBlock
}

type ContentBlock struct {
    Type         BlockType // text | image | audio | document | thinking |
                           // redacted_thinking | tool_use | tool_result
    Text         string
    Media        *Media        // mime type plus inline base64 or URL
    Thinking     *Thinking     // Text, Signature, Data (redacted)
    ToolUse      *ToolUse      // ID, Name, Input
    ToolResult   *ToolResult   // ToolUseID, Content, IsError
    CacheControl *CacheControl // Type (ephemeral), TTL ("5m" | "1h")
    Extra        map[string]json.RawMessage
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

type Usage struct {
    InputTokens        int
    OutputTokens       int
    CacheReadTokens    int
    CacheWriteTokens   int
    ReasoningTokens    int
    Estimated          bool
}

type Error struct {
    Type    ErrorType // invalid_request | authentication | permission | not_found |
                      // rate_limit | overloaded | api_error | content_filter | darkrouter
    Message string
    Code    string
}
```

`Usage` carries cache and reasoning tokens because Anthropic and OpenAI both price them separately;
omitting them makes cost accounting wrong from the first request rather than at some later date.

`CacheControl.TTL` exists because Anthropic's `5m` and `1h` TTLs are a paid feature. Without the
field, an Anthropic-to-Anthropic round trip through the IR would silently drop it — precisely the
failure this design exists to prevent.

`Extra` appears on `Request`, `ContentBlock`, and `Response` so that dialect-specific fields survive
same-dialect IR traversal even when Darkrouter does not model them.

The streaming event model follows Anthropic's block-structured events rather than OpenAI's flat
deltas, because the block model maps down to OpenAI chunks cleanly while the reverse loses
information about which block a delta belongs to:

```go
type StreamEvent struct {
    Type       EventType // message_start | content_block_start | content_delta |
                         // content_block_stop | message_delta | message_stop | error | ping
    Index      int
    Delta      *Delta    // text, thinking, or a tool-input JSON fragment
    Usage      *Usage
    StopReason StopReason
    Err        *Error
}
```

An error yielded by a stream sequence is terminal: the sequence stops. Unknown upstream event types
are ignored rather than treated as errors, because vendors add them without notice.

Any field an adapter cannot express is dropped with a `Warning` recorded on the request, never
silently. A dropped field is a fact the trace view must be able to show.

### 5.1 Interfaces

```go
type Dialect interface {
    Name() string
    ParseRequest(*http.Request) (*ir.Request, *Passthrough, error)
    WriteResponse(http.ResponseWriter, *ir.Response) error
    WriteStream(http.ResponseWriter, iter.Seq2[ir.StreamEvent, error]) error
    WriteError(http.ResponseWriter, *ir.Error) error
}

// Passthrough carries what the fast path needs to forward without re-rendering.
type Passthrough struct {
    Body        []byte  // the raw inbound body, retained for replay across attempts
    ModelField  string  // JSON pointer to the model, or "" when it lives in the URL
    Surface     Surface
}

type Adapter interface {
    Kind() Kind
    Surfaces() SurfaceSet
    BuildRequest(context.Context, *Target, *ir.Request) (*http.Request, error)
    ParseResponse(*http.Response) (*ir.Response, error)
    ParseStream(io.Reader) iter.Seq2[ir.StreamEvent, error]
    Classify(*http.Response, error) Outcome
}
```

Three dialects and five adapters means eight components growing linearly, not a translation matrix.

### 5.2 Auxiliary surfaces

Beyond chat, Darkrouter serves `/v1/embeddings`, `/v1/responses`, `/v1/images/generations`,
`/v1/audio/speech`, `/v1/audio/transcriptions`, `/v1/rerank`, and `/v1/moderations`.

These do not travel through the chat IR. Each gets its own narrow request and response type in
`internal/ir`, because forcing a six-field embedding call through a content-block message model buys
nothing and obscures both shapes.

They share everything that matters: the same router, exec loop, health model, request log, and
passthrough rule. Routing filters candidates by **surface** — `llm`, `embedding`, `image`, `tts`,
`stt`, `rerank`, `moderation` — recorded per model in the catalog, so an embedding request only ever
considers embedding-capable targets.

`/v1/responses` is chat-shaped and maps onto the chat IR. A request carrying `previous_response_id`,
`conversation`, or built-in tool declarations that the resolved target cannot honor is **rejected
with an explicit error**, not degraded: with a server-stored conversation the body carries only the
newest turn, so degrading to a chat completion returns a confident amnesic answer.

`/v1/rerank` follows the Cohere v2 request and response schema, since OpenAI defines no such
endpoint. Each preset declares its own rerank path. Providers whose shape deviates materially are
excluded from the surface rather than special-cased.

Adapters declare which surfaces they implement. A surface an adapter does not implement makes that
provider ineligible for the route, resolved at routing time rather than as a runtime error.

## 6. Providers, credentials, and the presets catalog

A **provider** is a configured connection: a kind, a base URL, a credential strategy, and one or more
credentials. A **preset** is shipped data supplying the kind, base URL, auth style, known quirks, and
surfaces for a named upstream, so adding a catalogued provider means picking a name and pasting a key.

### 6.1 Five kinds, and auth as a separate dimension

| Kind | Payload shape | Covers |
|---|---|---|
| `openaicompat` | OpenAI | The large majority: Groq, DeepSeek, Cerebras, Together, Fireworks, Mistral, OpenRouter, Nvidia NIM, regional providers, and every local runtime (Ollama, LM Studio, vLLM, llama.cpp, MLX). |
| `anthropic` | Anthropic Messages | Anthropic natively, including thinking and cache control. |
| `gemini` | Gemini | Google AI Studio `generateContent`. |
| `bedrock` | Bedrock Converse | AWS Bedrock. |
| `vertex` | dispatches per publisher | Google Vertex — see §6.2. |

Authentication is orthogonal, not a sixth kind:

| Auth style | Mechanism |
|---|---|
| `bearer`, `x-api-key`, `api-key`, `query-param`, `none` | A static credential in a header or query parameter. |
| `sigv4` | AWS request signing. Implies a materialized body. |
| `gcp-sa` | Service-account JWT exchanged for a short-lived access token. |
| `oauth` | Authorization-code flow with PKCE and background refresh. |

An earlier draft treated OAuth subscription accounts as their own adapter kind. That was wrong: a
Claude subscription speaks Anthropic Messages and an OpenAI one does not, so an "oauthsub adapter"
could not say what it emits. A preset declares a kind *and* an auth style, and the two compose.

### 6.2 Vertex dispatches per publisher

Vertex is one kind with two request builders, selected by the publisher recorded on the catalog entry:

- `publishers/google` — `generateContent` / `streamGenerateContent` with the Gemini payload.
- `publishers/anthropic` — `rawPredict` / `streamRawPredict` with the **Anthropic Messages** payload, the model moved from the body into the URL, and a mandatory `anthropic_version: "vertex-2023-10-16"` field.

Llama and Mistral on Vertex use a third, OpenAI-compatible shape and are out of scope for v1.

Vertex has no practical API for listing which models a project may actually call, so its catalog
entries are seeded from presets and models.dev filtered by declared publisher, with the credential
probe confirming reachability. Discovery is not pretended.

### 6.3 Presets

`presets.yaml` is embedded and transcribed from OmniRoute's provider constants — roughly 4,600 lines
of pure data across its apikey, local, noauth, and oauth families. The result is a curated subset:
web-cookie, cloud-agent, upstream-proxy, and media-only entries are dropped, leaving providers that
serve at least one supported surface. Each preset records its kind, base URL, auth style, quirks,
surfaces, its models.dev provider key, and — for OAuth providers — the authorize and token endpoints,
client ID, scopes, and redirect-URI constraints.

This is a data transcription. No OmniRoute code, structure, or abstraction crosses over.

### 6.4 Model metadata

Two background sources merge into the catalog. A **models.dev sync** fetches
`https://models.dev/api.json` on a long interval, supplying capabilities, context window, maximum
output tokens, and pricing. **Live discovery** probes each enabled provider's listing endpoint,
which is what makes a locally running Ollama's models appear without configuration.

Where a runtime exposes real capability data, discovery reads it — Ollama's `/api/show` reports
whether a model's template advertises tools. For models whose capabilities remain inferred rather
than known, the router **admits the candidate and records a warning** rather than excluding it. A
provider's own error is clearer than Darkrouter silently refusing to route, and hard-filtering on
guessed metadata would make every local model unroutable for agentic traffic.

## 7. Configuration

The rule: **credentials and observed state live in SQLite; intent lives in YAML.**

SQLite holds provider connections, credentials encrypted at rest, health and cooldown state, the
request log, usage rollups, and the discovery cache. The encryption chain is `DARKROUTER_MASTER_KEY`
(env) → PBKDF2-HMAC-SHA256 with a per-database salt and a stored iteration count → AES-GCM.

`darkrouter.yaml` holds intent:

```yaml
server:
  proxy_listen: :8080
  admin_listen: :8081
  proxy_token: ${DARKROUTER_PROXY_TOKEN}   # optional
  max_body_bytes: 33554432
  shutdown_grace: 30s

aliases:
  fast:
    - groq/llama-3.3-70b
    - cerebras/llama-3.3-70b
  coding:
    - anthropic/claude-sonnet-4-5
    - openrouter/anthropic/claude-sonnet-4.5

policy:
  cooldown:    { trip_after: 3, max: 15m }
  retry:       { max_attempts: 4 }
  timeout:     { connect: 10s, first_byte: 60s, total: 10m, idle: 120s }
  concurrency: { max_inflight: 256, per_provider: 32 }

log:
  retention: 720h
capture:
  bodies: false
  max_bytes: 256000
  retention: 72h
```

Outcome classification is fixed rather than configurable, so `policy.retry` carries only
`max_attempts`. Listen addresses and `max_body_bytes` are restart-only; a reload changing them is
accepted with a warning that they take effect on restart.

The file is watched and hot-reloaded. A file that fails validation is rejected wholesale and the
previous configuration stays live, with the error surfaced in the UI — a broken edit must never take
the gateway down. An alias naming a provider that does not exist is a **warning, not an error**;
treating it as an error would mean deleting a provider in the UI permanently breaks every future
reload.

The admin UI does CRUD on providers and credentials only. Aliases and policy get read-only rendered
views with a reload button, which is the structural reason the admin API stays at twenty-one endpoints
instead of growing without bound.

## 8. Routing

Model resolution is tried in this order: exact alias match, then `provider/model`, then a bare model
name resolved against the catalog.

The `provider/model` form splits on the **first** slash, and matches only when the prefix names a
configured provider — model identifiers legitimately contain slashes
(`meta-llama/Llama-3.3-70B-Instruct-Turbo`), so a non-matching prefix falls through to bare-name
resolution with the full string intact.

Bare names resolve against every enabled provider offering them, ordered by the provider `priority`
column and then `created_at`. Ambiguity here is not an error but the useful case: one model offered
by three providers becomes a three-provider fallback chain for free. A config-load warning names it.

Candidates are filtered on whether the provider is enabled, has a credential not in cooldown, whether
the model declares the requested surface, and whether its capabilities satisfy the request. Models
whose capabilities are inferred rather than known pass the filter with a warning, per §6.4.

Attempts proceed in declared order. **Credential rotation within a provider happens before advancing
to the next provider**, so multiple free-tier keys are actually drained.

Routing is deterministic: no weighting, no scoring, no learned behavior. Given a request and a health
snapshot, the candidate sequence is predictable and explainable. To make that auditable after the
fact, the realized candidate list and each skip reason are recorded on the request row — replay is
not possible from health tables alone, because those are overwritten in place.

### 8.1 Outcome classification

| Outcome | Triggers | Behavior |
|---|---|---|
| `Success` | 2xx, and for streams only once committed | Finish. Reset that credential's ladder. |
| `RetryableProvider` | 408, 429, unlisted ≥500, timeout, connection reset or refused, DNS failure, TLS error, EOF before headers, HTTP/2 GOAWAY or RST_STREAM | Record a failure with the breaker. On 429 advance to the next credential of the same provider; on everything else **skip the provider's remaining credentials** and advance to the next provider. |
| `RetryableCredential` | 401, 402, 403 | Cool that credential across all its models. Try the next credential on the same provider before advancing. |
| `RetryableModel` | 404, or a 400 whose error code identifies an unknown model | Advance without penalizing the provider, but increment a per-target counter so a permanently misconfigured base URL becomes visible rather than being skipped silently forever. |
| `Fatal` | Unlisted 4xx, 413, 422, content filter | Return immediately. Do not burn further candidates. |
| `ClientCancelled` | `context.Canceled` whose cause is the inbound request context | Stop. Record it; never count it against any provider. |

Distinguishing 429 from other retryable failures matters: rate limits are per credential, so the next
key is worth trying, while a 5xx means the upstream is down and every remaining key will hit it.

`ClientCancelled` must be separated from `context.DeadlineExceeded` using `context.WithCancelCause`.
Marking a provider unhealthy because someone pressed Ctrl-C is a self-inflicted outage.

Redirects are not followed. Go's client follows them by default and silently converts a POST into a
body-less GET; `CheckRedirect` disables this and 3xx classifies as `RetryableProvider`.

### 8.2 Commit semantics

Failover is possible only before the first byte reaches the client.

For **streams**, the executor holds the response until the first **content-bearing event**: a
`content_block_start` or `content_delta` carrying text, thinking, or tool input. `message_start`,
pings, keepalive comments, and role-only deltas do not count — committing on a keepalive forfeits
failover for nothing, and a reasoning model can legitimately think for a minute before its first
text token, which is why thinking counts as content.

Pre-commit events from the committing attempt are buffered and replayed at commit; those from failed
attempts are discarded. The buffer is bounded in bytes as well as by `first_byte` in time, and a cap
breach is treated as an attempt failure.

A 2xx whose stream fails before commit — including Anthropic's `overloaded_error` delivered as an
in-stream event under a 200 — is classified from the stream error, not from the status line.

For **unary** responses, commit happens after the full upstream body is read and parsed successfully;
a read or parse failure before that is `RetryableProvider`. `first_byte` bounds time to response
headers.

After commit, a failure is emitted as an error event within the stream in the inbound dialect's
shape, and post-commit streams are governed by `policy.timeout.idle` rather than `total`, so a long
legitimate response is not killed while a silent provider still is.

Because the inbound body must be replayable across attempts, it is read fully into memory (bounded by
`server.max_body_bytes`) before the first attempt. One consequence is worth stating plainly: a
pre-commit failover after the body was sent may mean the first provider already processed and billed
the prompt. That is an accepted gateway trade-off, and the cost of failed attempts is not captured in
`usage_daily`.

## 9. Health

A circuit breaker keyed on `(provider, credential, model)`.

A 429 cools that triple for the duration in `Retry-After` when present, clamped to
`policy.cooldown.max`, otherwise by an exponential ladder. The ladder runs 1s, 2s, 4s, 8s, 15s, 30s,
60s, 120s and continues doubling until clamped at `max`. Other retryable provider failures increment
a consecutive-failure counter and trip once `trip_after` is reached — **a single 5xx does not cool a
candidate**.

`Success` and `Fatal` outcomes reset the ladder for the exact triple, because both prove the provider
is reachable and functioning. `RetryableCredential` never resets: without that exclusion, a
billing-exhausted key cooled by a 402 would be resurrected by any client's malformed request.

On expiry a triple enters half-open. Exactly one probe is admitted via an atomic claim, so concurrent
requests at expiry do not all become probes; success closes the breaker and failure re-trips at the
next ladder level.

Cooldown state is authoritative in memory on the hot path and persisted asynchronously, flushed on
shutdown, and rehydrated at startup so a restart does not stampede a provider that is still
rate-limited.

## 10. Observability

Each request writes one `requests` row and one `request_attempts` row per attempt. The request row
carries inbound dialect, requested model, resolved alias, the realized candidate list with skip
reasons, final provider and model, status, token counts, cost, time-to-first-token, total duration,
and any warnings. Each attempt row carries provider, credential, model, outcome, status code,
latency, and error.

Bodies are captured only when `capture.bodies` is on, size-capped, and expired on a retention timer.
Binary and multipart bodies are never captured; only content type and byte count are recorded.

Log writing never blocks a request. Records go to a buffered channel drained by a batching writer; a
saturated channel drops records and increments a counter exposed on `/healthz` and `/metrics`.
Because the log is also the sole input to `usage_daily`, dropped records mean **spend figures are a
lower bound**, not an exact ledger.

Four response headers let a terminal user see routing without opening the UI:
`X-Darkrouter-Provider`, `X-Darkrouter-Model`, `X-Darkrouter-Attempts`, and `X-Darkrouter-Request`.
The last links a terminal session to its trace in the UI, and the attempt count and request id are
written on error responses too, not only on success.

The admin port also serves `/metrics` in Prometheus text format and a `/healthz` payload reporting
config validity, log-drop count, database reachability, and per-provider breaker state.

## 11. Data model

```
providers          id, name, preset, kind, base_url, auth_style, enabled, priority,
                   region, project, location, settings_json, created_at
provider_keys      id, provider_id, label, kind (static|sigv4|gcp_sa|oauth),
                   ciphertext, nonce, expires_at, scope, enabled, last_used_at
models             provider_id, model_id, publisher, surfaces, capabilities,
                   capabilities_source (models_dev|discovered|inferred|override),
                   context_window, max_output_tokens,
                   input_price_micros_per_mtok, output_price_micros_per_mtok,
                   cache_read_price_micros_per_mtok, discovered_at, state
model_overrides    provider_id, model_id, surfaces, capabilities, context_window
requests           id, ts, dialect, surface, requested_model, resolved_alias,
                   candidates_json, final_provider_id, final_model, status,
                   tokens_in, tokens_out, cache_read_tokens, cache_write_tokens,
                   reasoning_tokens, cost_micros, ttft_ms, total_ms,
                   error_code, warnings_json
request_attempts   request_id, seq, provider_id, key_id, model, outcome,
                   status_code, latency_ms, error
request_bodies     request_id, request_json, response_json, expires_at
health             provider_id, key_id, model, cooling_until, backoff_level,
                   consecutive_failures, updated_at
usage_daily        day, provider_id, model, requests, tokens_in, tokens_out, cost_micros
sessions           id, created_at, expires_at
settings           key, value
```

Prices are micro-dollars **per million tokens** — models.dev publishes dollars per million as floats,
and storing micro-dollars per token would truncate a $0.14/M model to zero. `cost_micros` is NULL
until pricing exists for the model rather than being recorded as zero.

`day` in `usage_daily` is UTC, keyed on request start, and finalization is idempotent recomputation
so a request that starts before midnight and finishes after it is counted once, in the right day.

`request_attempts` and `requests` hold no foreign key onto `providers`, so deleting a provider in the
UI preserves history rather than cascading it away.

## 12. Admin API and UI

The SPA is scaffolded with `npm create darkraise-ui` — sidebar layout, default preset, slate surface —
giving React 19, Vite, Tailwind 4, TanStack Router/Query/Table/Form, recharts 3, and `darkraise-ui`
6.4.0. It builds to `web/dist` and is embedded via `go:embed all:web/dist`. In dev mode the admin
server reverse-proxies to Vite.

Four screens plus settings:

- **Overview** — provider health grid with active cooldowns, error-rate sparkline, requests per minute, today's spend.
- **Requests** — keyset-paginated table opening a trace drawer showing the candidate list with skip reasons, every attempt in order, dropped-field warnings, tokens, cost, and captured bodies.
- **Catalog** — searchable model index filtered by surface, capability, price, and context window, showing what each alias resolves to and whether metadata is known or inferred.
- **Playground** — streams a test completion against any alias or provider-model and links to the trace it produced.
- **Settings** — provider and credential CRUD, plus the rendered read-only config with validation status and a reload button.

Twenty-one endpoints — the two OAuth routes are implemented in phase 8:

```
GET    /api/overview
GET    /api/presets
GET    /api/providers
POST   /api/providers
PATCH  /api/providers/:id
DELETE /api/providers/:id
POST   /api/providers/:id/keys
DELETE /api/providers/:id/keys/:keyId
POST   /api/providers/:id/test
POST   /api/providers/:id/oauth/start
GET    /api/oauth/callback
GET    /api/models
GET    /api/requests
GET    /api/requests/:id
GET    /api/usage
GET    /api/config
POST   /api/config/reload
POST   /api/playground        (SSE)
POST   /api/auth/login
POST   /api/auth/logout
GET    /api/auth/status
```

`GET /api/auth/status` is reachable without a session, since the SPA calls it to decide whether to
render the login screen. Every other endpoint requires one.

## 13. Security

The proxy port is open on the LAN with an optional bearer token; the admin port sits behind a
bcrypt-verified password session. An unauthenticated interface that can read and write credentials is
not acceptable even on a trusted network.

Inbound proxy authentication accepts each dialect's native credential form, all compared against
`server.proxy_token` in constant time:

| Dialect | Accepted |
|---|---|
| OpenAI | `Authorization: Bearer <token>` |
| Anthropic | `x-api-key: <token>`, or `Authorization: Bearer <token>` |
| Gemini | `x-goog-api-key: <token>`, or `?key=<token>` |

Mutating admin endpoints require a CSRF token bound to the session via HMAC, plus an `Origin` or
`Sec-Fetch-Site` check — the SPA is same-origin, so the check is free and stronger than naive
double-submit, which an attacker who can set a cookie on a plain-HTTP LAN defeats. Cookies are not
port-scoped, so the proxy port must never honor them.

Credentials are AES-GCM encrypted at rest with the credential row id as additional authenticated
data, and are never returned by the admin API — only a label and a masked suffix. Request bodies may
contain sensitive prompts, so capture defaults off and expires on a timer.

## 14. Error handling

Errors are normalized into the **inbound** dialect's error shape. An OpenAI client receives an OpenAI
error object; Claude Code receives an Anthropic one; a Gemini client receives Google's. Clients then
handle gateway failures with their existing code rather than special-casing the proxy.

Darkrouter-originated errors — no candidate available, every candidate cooling, config invalid,
stateful Responses request the target cannot honor — carry a distinguishable error type. "No provider
offers this model" and "every provider offering it is cooling" are different errors and must not be
conflated.

## 15. Testing

Golden-file tests cover every translator direction, unary and streaming, for all three dialects and
all five adapter kinds. Fixtures include the awkward cases specifically: thinking blocks with
signatures, cache-control markers with TTLs, parallel tool calls, tool results carrying images,
multi-part content, blocked prompts, and mid-stream errors.

A fake provider fleet built on `httptest` makes failover, credential rotation, cooldown, and commit
semantics deterministic and network-free. Because `router` is a pure function, its candidate ordering
is tested directly as a table.

Config validation is table-driven, including that a rejected reload leaves the previous configuration
live and that a dangling alias reference warns rather than fails.

One opt-in live smoke test runs against real providers behind a build tag.

## 16. Deployment

A multi-stage Dockerfile builds the SPA with Node, compiles the Go binary with `CGO_ENABLED=0`, and
ships a minimal final image. A compose file mounts one data volume holding the SQLite database and
`darkrouter.yaml`, and passes `DARKROUTER_MASTER_KEY` and the admin password hash from the
environment.

Graceful shutdown drains in that order: stop accepting proxy connections, let in-flight requests
finish within `server.shutdown_grace`, drain the log channel, flush health state, close the database.

## 17. Delivery order

Phase specs in `README.md` carry the detail. In sequence: the IR and OpenAI dialect end-to-end
against one `openaicompat` provider; the store, health model, and exec loop with real failover; the
Anthropic and Gemini dialects with the golden-file suite; the auxiliary surfaces; the presets catalog
with discovery and models.dev sync; the admin API and the four screens; bedrock, vertex, and OAuth
credentials; and finally the passthrough fast path, which is purely additive and easiest to validate
once the IR path is known-correct.
