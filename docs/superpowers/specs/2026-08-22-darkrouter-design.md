# Darkrouter — Design

**Status:** Approved design. The implementation plan is written against this document.
**Date:** 2026-08-22

---

## 1. Purpose

Darkrouter is a self-hosted LLM gateway for a single-operator homelab. It presents one endpoint that
speaks the three dialects real clients use — OpenAI, Anthropic Messages, and Google Gemini — and
routes each request across a broad set of upstream providers with deterministic failover. Alongside
chat it serves the OpenAI-shaped auxiliary surfaces: embeddings, responses, images, audio, rerank,
and moderations.

It takes OmniRoute's connectivity breadth and discards its feature breadth. OmniRoute is 2,982
TypeScript files, 516k lines, 118 dashboard pages, 677 API routes, and 130 npm dependencies, because
it also does prompt compression, CLI config patching, A2A/MCP/ACP, batch APIs, gamification, and
evals. Darkrouter does four things — unified endpoint, failover with key rotation, routing aliases,
and usage observability — and is sized accordingly: four screens and eighteen admin
endpoints.

### Success criteria

- Any tool configured against one base URL reaches every configured provider without reconfiguration.
- A provider outage or rate limit is invisible to the client, and the trace afterwards explains exactly what happened.
- Adding a catalogued provider is a UI action; adding an uncatalogued one is a base URL and a key. Neither requires code.
- The whole system is one static binary plus one SQLite file plus one YAML file.

## 2. Non-goals

Explicitly out of scope, and not to be added without revisiting this document: prompt compression,
CLI config file patching, MCP/A2A/ACP, batch and files APIs, video/music/OCR/web-search surfaces,
gamification, evals, multi-tenancy and RBAC, web-cookie and browser-scraped providers, cloud sync,
and adaptive or learned routing.

Darkrouter does not inherit code, structure, or roadmap from the existing `llm-proxy` repository.

## 3. Architecture

One Go binary (Go 1.26) embedding a React SPA. Two HTTP servers on separate ports from one process:
the **proxy server** serving `/v1/*` and `/v1beta/*`, and the **admin server** serving `/api/*` plus
the SPA.

| Package | Responsibility |
|---|---|
| `internal/ir` | The canonical request, response, and stream-event types. No I/O, no dependencies on other internal packages. |
| `internal/edge` | One `Dialect` per inbound API shape: openai, anthropic, gemini. Parses HTTP into `ir.Request`, writes `ir` back out in that dialect's shape, including its error shape. |
| `internal/adapter` | One `Adapter` per outbound kind: openaicompat, anthropic, gemini, bedrock, vertex, oauthsub. Renders `ir.Request` to an upstream HTTP request; parses upstream responses and streams back to `ir`. |
| `internal/router` | Resolves a requested model name into an ordered candidate list. Pure function of (request, catalog, config, health snapshot). No I/O. |
| `internal/exec` | The attempt loop: take candidate, send, classify outcome, record health, advance. Owns stream commit semantics. |
| `internal/catalog` | Merges embedded presets, models.dev metadata sync, and live `/v1/models` discovery into one queryable model index. |
| `internal/store` | SQLite. Providers, encrypted keys, request log and attempts, health state, usage rollups, discovery cache. |
| `internal/config` | `darkrouter.yaml` load, validation, and hot reload. |
| `internal/crypto` | PBKDF2 key derivation, AES-GCM for keys at rest, bcrypt for the admin password. |
| `internal/admin` | Admin REST API, session auth, SPA serving, Vite reverse-proxy in dev mode. |
| `internal/server` | Wiring, background workers, graceful shutdown. |
| `web/` | React SPA. |

`router` being a pure function is deliberate. Failover is where subtle bugs live, and this makes the
whole decision surface testable without a network or a database.

Runtime dependencies are kept few: `modernc.org/sqlite` (pure Go, so the binary stays static),
`gopkg.in/yaml.v3`, `fsnotify`, `aws-sdk-go-v2` (SigV4 signer only), `golang.org/x/oauth2/google`,
`golang.org/x/crypto`.

## 4. Request path

Darkrouter is passthrough-first with a canonical-IR fallback.

When the inbound dialect already matches the chosen target's dialect — an OpenAI client hitting Groq,
Claude Code hitting Anthropic — the request takes the **passthrough path**: rewrite the `model` field
in the body, swap auth headers, forward the remaining bytes untouched. The response stream is piped
through as bytes while an SSE tee extracts the usage record for accounting. This is byte-faithful, so
provider parameters Darkrouter has never heard of work on the day they ship, and it costs no
translation latency on the most common route.

When dialects differ — Claude Code failing over to Groq, a Gemini client reaching Anthropic — the
request takes the **IR path**: the edge dialect parses to `ir.Request`, the adapter renders it to the
target's wire format, and the response or stream is parsed back to `ir` and re-emitted in the inbound
dialect's shape.

Both paths share one exec loop, one health model, one log record, and one error shape. The
passthrough path is an optimization inside the executor, not a separate pipeline.

### Passthrough eligibility

A request is passthrough-eligible when all of the following hold: the inbound dialect maps to the
target adapter's kind, the alias applied no parameter overrides, body capture is off or the body has
already been captured, and the target needs no request-level rewriting beyond model name and auth.
Anything else falls to the IR path. Eligibility is decided per attempt, not per request — the first
attempt may pass through and the second may translate.

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
    Extra          map[string]json.RawMessage // dialect-specific escape hatch
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
    Thinking     *Thinking     // text and signature
    ToolUse      *ToolUse      // id, name, input
    ToolResult   *ToolResult   // tool_use_id, content, is_error
    CacheControl *CacheControl // ephemeral markers
}
```

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

Any field an adapter cannot express is dropped with a recorded warning on the request log, never
silently. A dropped field is a fact the trace view must be able to show.

### Interfaces

```go
type Dialect interface {
    Name() string
    ParseRequest(*http.Request) (*ir.Request, Passthrough, error)
    WriteResponse(http.ResponseWriter, *ir.Response) error
    WriteStream(http.ResponseWriter, iter.Seq2[ir.StreamEvent, error]) error
    WriteError(http.ResponseWriter, *ir.Error) error
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

Three dialects and six adapters means nine components growing linearly, not a translation matrix.

### Auxiliary surfaces

Beyond chat, Darkrouter serves the OpenAI-shaped surfaces clients actually reach for:
`/v1/embeddings`, `/v1/responses`, `/v1/images/generations`, `/v1/audio/speech`,
`/v1/audio/transcriptions`, `/v1/rerank`, and `/v1/moderations`.

These do not travel through the chat IR. Each gets its own narrow request and response type in
`internal/ir` — `EmbeddingRequest`, `ImageRequest`, `SpeechRequest`, `TranscriptionRequest`,
`RerankRequest`, `ModerationRequest` — because forcing a six-field embedding call through a
content-block message model buys nothing and obscures both shapes.

They share everything that matters: the same router, the same exec loop, the same health model, the
same request log, and the same passthrough rule. Routing filters candidates by **service kind** —
`llm`, `embedding`, `image`, `tts`, `stt`, `rerank`, `moderation` — recorded per model in the
catalog, so an embedding request only ever considers embedding-capable targets.

`/v1/responses` is the exception. It is chat-shaped, so it maps onto the chat IR rather than getting
its own type, with Responses-specific fields carried in `Extra`.

Adapters declare which surfaces they implement. A surface an adapter does not implement makes that
provider ineligible for the route rather than producing a runtime error.

## 6. Providers and the presets catalog

A **provider** is a configured connection: a kind, a base URL, and one or more API keys. A **preset**
is shipped data that fills in the kind, base URL, header style, and known quirks for a named upstream,
so adding a catalogued provider means picking a name and pasting a key.

Six adapter kinds cover the entire space:

| Kind | Covers |
|---|---|
| `openaicompat` | The large majority: Groq, DeepSeek, Cerebras, Together, Fireworks, Mistral, OpenRouter, Nvidia NIM, regional providers, and every local runtime (Ollama, LM Studio, vLLM, llama.cpp, MLX). |
| `anthropic` | Anthropic Messages API natively, including thinking and cache control. |
| `gemini` | Google AI Studio `generateContent`. |
| `bedrock` | AWS Bedrock with SigV4 request signing. |
| `vertex` | Google Vertex with service-account JWT auth. |
| `oauthsub` | Subscription accounts reached over OAuth with token refresh. |

Breadth therefore costs data, not code. `presets.yaml` is embedded in the binary and transcribed from
OmniRoute's provider constants — roughly 4,600 lines of pure data across its apikey, local, noauth,
and oauth families. The result is a curated subset rather than a one-to-one copy: web-cookie,
cloud-agent, upstream-proxy, and media-only entries are dropped, leaving the providers that serve at
least one supported surface. Each preset records its adapter kind, base URL, header style, known
quirks, and the service kinds it offers. This is a data transcription, not a code port — no OmniRoute
architecture comes across with it.

Model metadata arrives from two directions and is merged by the catalog. A periodic **models.dev
sync** supplies capabilities, context windows, and per-token pricing. **Live discovery** probes each
enabled provider's `/v1/models` on startup and on a timer, which is what makes local Ollama models
appear without configuration. On conflict, live discovery wins on availability and models.dev wins on
metadata; presets are the fallback for both.

## 7. Configuration

The rule: **credentials and observed state live in SQLite; intent lives in YAML.**

SQLite holds provider connections, API keys encrypted at rest, health and cooldown state, the request
log, usage rollups, and the discovery cache. Keys are never written to a plaintext file. The
encryption chain is `DARKROUTER_MASTER_KEY` (env) → PBKDF2 with a per-database salt → AES-GCM.

`darkrouter.yaml` holds intent — the things worth diffing in git:

```yaml
server:
  proxy_listen: :8080
  admin_listen: :8081
  proxy_token: ${DARKROUTER_PROXY_TOKEN}   # optional

aliases:
  fast:
    - groq/llama-3.3-70b
    - cerebras/llama-3.3-70b
  coding:
    - anthropic/claude-sonnet-4-5
    - openrouter/anthropic/claude-sonnet-4.5

policy:
  cooldown: { trip_after: 3, max: 15m }
  retry:    { on: [429, 5xx, timeout, conn_reset], max_attempts: 4 }
  timeout:  { connect: 10s, first_byte: 60s, total: 10m }

capture:
  bodies: false
  max_bytes: 256000
  retention: 72h
```

The file is watched and hot-reloaded. A file that fails validation is rejected wholesale and the
previous configuration stays live, with the error surfaced in the UI — a broken edit must never take
the gateway down.

Consequently the admin UI does CRUD on providers and keys only. Aliases and policy get read-only
rendered views with a reload button, which is the structural reason the admin API stays at
eighteen endpoints instead of growing without bound.

## 8. Routing

Model resolution is tried in this order: exact alias match, then `provider/model`, then a bare model
name resolved against the catalog. Ambiguity in the last case is a config-load warning, resolved by
the provider `priority` column and then by declaration order.

Candidates are then filtered on whether the provider is enabled, has at least one key not in cooldown,
and whether the model's capabilities satisfy the request — a request carrying tools requires tool
support, image content requires vision, a reasoning budget requires a reasoning-capable model.

Attempts proceed in declared order. **Key rotation within a provider happens before advancing to the
next provider**, so multiple free-tier keys are actually drained rather than one key being hammered.

Routing is fully deterministic: no weighting, no scoring, no learned behavior. Given a request and a
health snapshot, the candidate sequence is predictable and explainable. That property is what makes
the trace view worth building.

### Outcome classification

| Outcome | Triggers | Behavior |
|---|---|---|
| `Success` | 2xx | Commit. Reset the backoff ladder for that key. |
| `RetryableProvider` | 408, 429, 5xx, timeout, connection reset | Cool this (provider, key, model), advance to the next candidate. |
| `RetryableCredential` | 401, 402, 403 | Cool this key only; try the next key on the same provider before advancing. |
| `RetryableModel` | 404 model not found | Advance to the next candidate without penalizing the provider. |
| `Fatal` | 400, 422, content policy | Return to the client immediately. Do not burn further candidates. |

### Streaming commit

Failover is possible only before the first byte reaches the client. Darkrouter holds the response
until the first upstream content token arrives, then commits to that provider. A failure after commit
is surfaced as an error event within the stream rather than a silent re-route, because a client that
has already received partial content cannot be given a different response.

## 9. Health

A circuit breaker is keyed on `(provider, key, model)`. A 429 cools that triple for the duration in
`Retry-After` when present, otherwise by an exponential ladder. Consecutive 5xx or timeouts trip after
`trip_after` failures, then back off exponentially to `max` with a half-open probe on recovery.

Any response proving the provider is reachable and functioning — including a 400 — resets the ladder
for that key. Reachability, not request success, is what the ladder measures.

Cooldown state is persisted to SQLite so that a restart does not stampede a provider that is still
rate-limited.

## 10. Observability

Each request writes one `requests` row and one `request_attempts` row per attempt. The request row
carries inbound dialect, requested model, resolved alias, final provider and model, status, token
counts, cost, time-to-first-token, and total duration. Each attempt row carries provider, key
identifier, model, outcome, status code, latency, and error. Together they reconstruct the full
decision chain.

Bodies are captured only when `capture.bodies` is on, size-capped, and expired on a retention timer.

Three response headers let a terminal user see routing without opening the UI:
`X-Darkrouter-Provider`, `X-Darkrouter-Model`, `X-Darkrouter-Attempts`.

A daily rollup worker aggregates usage into `usage_daily` so the dashboard charts do not scan the raw
log.

## 11. Data model

```
providers          id, name, preset, kind, base_url, enabled, priority, created_at
provider_keys      id, provider_id, label, ciphertext, nonce, enabled, last_used_at
models             provider_id, model_id, capabilities, context_window,
                   input_price_micros, output_price_micros, source, discovered_at
requests           id, ts, dialect, requested_model, resolved_alias, final_provider_id,
                   final_model, status, tokens_in, tokens_out, cost_micros,
                   ttft_ms, total_ms, error_code, warnings
request_attempts   request_id, seq, provider_id, key_id, model, outcome,
                   status_code, latency_ms, error
request_bodies     request_id, request_json, response_json, expires_at
health             provider_id, key_id, model, cooling_until, backoff_level,
                   consecutive_failures, updated_at
usage_daily        day, provider_id, model, requests, tokens_in, tokens_out, cost_micros
settings           key, value
```

## 12. Admin API and UI

The SPA is scaffolded with `npm create darkraise-ui` — sidebar layout, default preset, slate surface —
giving React 19, Vite, Tailwind 4, TanStack Router/Query/Table/Form, recharts 3, and `darkraise-ui`
6.4.0. It builds to `web/dist` and is embedded via `go:embed all:web/dist`. In dev mode the admin
server reverse-proxies to Vite so hot reload works.

Four screens plus settings:

- **Overview** — provider health grid with active cooldowns, error-rate sparkline, requests per minute, today's spend.
- **Requests** — filterable table opening a trace drawer showing the attempt chain, dropped-field warnings, and captured bodies. Built on `table`, `drawer`, and `json-tree-view`.
- **Catalog** — searchable model index across providers, filtered by capability, price, and context window, showing what each alias resolves to. Built on `command` and `table`.
- **Playground** — streams a test completion against any alias or provider-model and links to the trace it produced.
- **Settings** — rendered read-only config with validation status, a reload button, and provider and key CRUD.

Admin endpoints:

```
GET    /api/overview
GET    /api/providers
POST   /api/providers
PATCH  /api/providers/:id
DELETE /api/providers/:id
POST   /api/providers/:id/keys
DELETE /api/providers/:id/keys/:keyId
POST   /api/providers/:id/test
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

## 13. Security

The proxy port is open on the LAN with an optional bearer token, matching how the existing OmniRoute
instance is used. The admin port sits behind a bcrypt-verified password session. This asymmetry is
deliberate: an unauthenticated interface that can read and write API keys is not acceptable even on a
trusted network.

API keys are AES-GCM encrypted at rest and are never returned by the admin API — only a label and a
masked suffix. Request bodies, when captured, may contain sensitive prompts, so capture defaults off
and expires on a timer.

## 14. Error handling

Errors are normalized into the **inbound** dialect's error shape. An OpenAI client receives an OpenAI
error object; Claude Code receives an Anthropic one; a Gemini client receives Google's. Clients then
handle gateway failures with their existing code rather than special-casing the proxy.

Darkrouter-originated errors — no candidate available, all providers cooling, config invalid — carry a
distinguishable error type and the attempt count in the response headers.

## 15. Testing

Golden-file tests cover every translator direction, unary and streaming, for all three dialects and
all six adapter kinds. Fixtures include the awkward cases specifically: thinking blocks, cache control
markers, parallel tool calls, multi-part image content, and mid-stream errors.

A fake provider fleet built on `httptest` makes failover, key rotation, cooldown, and streaming commit
deterministic and network-free. Because `router` is a pure function, its candidate ordering is tested
directly as a table.

Config validation is table-driven, including the case that a rejected reload leaves the previous
configuration live.

One opt-in live smoke test runs against real providers behind a build tag so it never runs in a normal
build.

## 16. Deployment

A multi-stage Dockerfile builds the SPA with Node, compiles the Go binary with `CGO_ENABLED=0`, and
ships a minimal final image. A compose file mounts one data volume holding the SQLite database and
`darkrouter.yaml`, and passes `DARKROUTER_MASTER_KEY` and the admin password hash from the
environment.

## 17. Delivery order

To be refined by the implementation plan: the IR and the OpenAI dialect end-to-end against one
`openaicompat` provider; then the store, health model, and exec loop with real failover; then the
Anthropic and Gemini dialects with the golden-file suite; then the presets catalog with discovery and
models.dev sync; then the admin API and the four screens; then bedrock, vertex, and oauthsub; finally
the passthrough fast path, which is purely additive and easiest to validate once the IR path is
known-correct.
