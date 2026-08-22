# Phase 1 — Foundation

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** nothing.

---

## 1. Goal

A deployable binary that proxies OpenAI chat completions — streaming and non-streaming, with tools
and vision — to one configured `openaicompat` provider, with configuration hot-reload and Docker
packaging.

This phase deliberately produces something runnable in the homelab on day one. Every later phase is a
redeploy rather than a big-bang integration.

## 2. Scope boundary

**In:** repository and module layout, `internal/config`, chat types in `internal/ir`, the OpenAI edge
dialect, the `openaicompat` adapter, a minimal single-candidate execution path, two HTTP listeners,
graceful shutdown, Dockerfile and compose.

**Out:** persistence, failover, health tracking, the admin API, the SPA, other dialects, auxiliary
surfaces, the presets catalog.

The lossy-field warning mechanism from master design §5 activates in phase 4, once `requests.warnings`
exists. Phase 1's adapter drops unmodelled fields silently; that is a stated exception, not a
violation to be discovered later.

## 3. Repository layout

```
cmd/darkrouter/main.go
internal/config/          load, validate, watch
internal/ir/              chat request, response, stream event, error, usage
internal/edge/openai/     inbound dialect
internal/adapter/openaicompat/
internal/provider/        Source interface + YAML implementation
internal/sse/             reader and writer shared by every dialect
internal/server/          listeners, wiring, shutdown
docs/superpowers/specs/
Dockerfile
compose.yml
darkrouter.example.yaml
```

Module path: `github.com/darkraise/darkrouter`. Go 1.26.

## 4. Design decisions

### 4.1 Provider source is an interface from the start

Providers ultimately live in SQLite, but there is no store until phase 2. Rather than write throwaway
code:

```go
type Source interface {
    Providers(context.Context) ([]Provider, error)
    Revision() uint64
}
```

with a YAML-backed implementation. Phase 2 adds the SQLite implementation and uses this one to seed
the database on first run, so the interface survives and only what sits behind it grows.

Phase 1's `providers:` block carries `id`, `kind`, `base_url`, `models`, `priority`, and `api_key`
(via `${ENV}` interpolation only — no plaintext keys in the file, ever).

Model resolution in this phase is bare-name lookup against declared `models` lists, ordered by
`priority` then declaration order, taking the first match. Duplicates across providers are a
config-load warning, not an error — phase 3 turns that same situation into a fallback chain.

### 4.2 Configuration is swapped atomically

Config lives in an `atomic.Pointer[Config]`. A request takes one snapshot at entry and uses it for its
whole lifetime, so a reload mid-request cannot change behavior underneath it.

**The watcher watches the parent directory and filters by filename**, not the file itself. Editors
that save by rename — vim, and most atomic-write implementations — deliver a rename event for the old
inode and nothing for the new file, so a file watch silently stops working after the first save.
Directory watching with a short debounce (100 ms) also absorbs the multiple write events editors emit
and avoids reading a half-written file.

On change the new file is parsed and validated in full; only on success is the pointer swapped. **A
file that fails validation is rejected wholesale and the previous configuration stays live**, with the
error recorded and exposed on `/healthz`. A broken edit must never take the gateway down.

`server.proxy_listen`, `server.admin_listen`, and `server.max_body_bytes` are restart-only. A reload
changing them is accepted with a warning naming which values await a restart, rather than being
silently ignored or rejected.

### 4.3 SSE parsing has a stated contract

`internal/sse` implements a WHATWG EventSource subset, and getting this wrong is the single most
consequential way phase 1 can quietly break later phases. The contract:

- Lines end `\n`, `\r\n`, or a lone `\r`.
- A line beginning `:` is a **comment and is ignored**. OpenRouter emits `: OPENROUTER PROCESSING` keepalives, so a parser that assumes every line is a `data:` line breaks on the most popular aggregator.
- Recognized fields are `data`, `event`, `id`, `retry`; one optional space after the colon is stripped.
- Multiple `data:` lines in one event are joined with `\n`.
- A blank line dispatches the event.
- `data: [DONE]` is the OpenAI-dialect terminator and is not a JSON payload.
- EOF without `[DONE]` ends the stream normally; it is not an error.

Line length is capped at `sse.max_line_bytes` (default 1 MiB, configurable in `server:`). Overflow is
a protocol error with a clear message, never a silent truncation — `bufio.Scanner`'s 64 KiB default
would otherwise fail on a large tool-call argument delta and present as a truncated stream.

### 4.4 Flushing must survive the real world

Streaming responses set `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection:
keep-alive`, and `X-Accel-Buffering: no` — the last because an nginx in front of a homelab box is the
normal case and will otherwise buffer the whole stream.

`http.ResponseController.Flush` is called after each event. No `bufio.Writer` may wrap the
`ResponseWriter` in the streaming path and no compression middleware may touch it, or
time-to-first-token becomes time-to-completion.

The proxy server sets **no global `WriteTimeout`** — it would kill long streams at a fixed age —
using `ReadHeaderTimeout` for slowloris protection and per-request context deadlines for everything
else.

### 4.5 Client disconnect cancels upstream

The upstream request context derives from the inbound one, so a client hanging up cancels the upstream
call rather than leaving it running against a metered provider.

Contexts are built with `context.WithCancelCause` from the outset. Phase 2 needs the cause to
distinguish a client disconnect from a Darkrouter-imposed deadline — both surface as
`context.Canceled` — and retrofitting that is harder than starting with it.

## 5. Interfaces introduced

`internal/ir` gains the chat types from master design §5, complete — including `Response`, `Usage`
with its cache and reasoning fields, `Error`, and `Extra` on `Request`, `ContentBlock`, and
`Response`. Fields phase 1 cannot populate are still defined, because adding them later touches every
adapter.

`internal/edge` gains `Dialect` and its `openai` implementation, serving:

```
POST /v1/chat/completions
GET  /v1/models
```

`Passthrough` is defined here per master design §5.1 even though nothing consumes it until phase 9,
since the interface signature ships now. `iter.Seq2` error semantics are likewise fixed now: a yielded
error is terminal and the sequence stops.

`/v1/models` returns the union of models declared on configured providers, plus configured aliases
listed first — aliases are invisible to model-picker UIs otherwise, which defeats the feature. Phase 6
replaces the backing with the catalog.

`internal/adapter` gains `Adapter` and `openaicompat`. `Classify` returns the full outcome taxonomy
from master design §8.1 even though phase 1 has nowhere to fail over to; defining it now keeps phase 3
from revisiting every adapter.

The adapter injects `stream_options: {"include_usage": true}` on streaming requests, because
OpenAI-compatible providers report no stream usage otherwise and phase 2's accounting would be blind
on the dominant path from its first day.

## 6. Execution path

Single candidate, no retry: resolve the model to one provider, build the upstream request, send, and
stream or return. On failure the error is translated to the OpenAI error shape and returned.

This is the same call sequence phase 3's exec loop will drive, with the loop absent. Keeping the
sequence identical means phase 3 adds a loop rather than restructuring.

## 7. Server and shutdown

Two listeners in one process. The proxy listener serves `/v1/*`. The admin listener serves `/healthz`,
`/readyz`, and `/metrics`, gaining `/api/*` and the SPA in phase 7.

`/healthz` returns a JSON body — config validity and any validation error, uptime, version — because
later phases hang counters off it and an unspecified payload becomes an unspecified contract.
`/readyz` fails only when the process cannot serve at all.

Shutdown on SIGTERM drains in order: stop accepting proxy connections, let in-flight requests finish
within `server.shutdown_grace` (default 30s), then close. `BaseContext` is not cancelled at the start
of shutdown, or in-flight streams die instantly. Streams still running at the deadline receive an
in-stream error event before the connection closes, rather than a silent truncation — and with
ten-minute streams permitted, the operator should expect a redeploy to cut some.

## 8. Configuration surface

```yaml
server:
  proxy_listen: :8080
  admin_listen: :8081
  proxy_token: ${DARKROUTER_PROXY_TOKEN}   # optional
  max_body_bytes: 33554432
  shutdown_grace: 30s
  sse: { max_line_bytes: 1048576 }

providers:                                  # phase 1 only; phase 2 moves these to SQLite
  - id: groq
    kind: openaicompat
    base_url: https://api.groq.com/openai/v1
    api_key: ${GROQ_KEY}
    priority: 10
    models: [llama-3.3-70b-versatile]

policy:
  timeout: { connect: 10s, first_byte: 60s, total: 10m, idle: 120s }
```

Validation rejects unknown keys, a provider with no `id` or `base_url`, a `base_url` that is not an
absolute URL, a duplicate provider `id`, and an unresolved `${ENV}` reference on a **required** field.

An unresolved reference on an **optional** field means the feature is off, not a validation failure —
`proxy_token: ${DARKROUTER_PROXY_TOKEN}` on a machine without that variable must start with
authentication disabled, or the shipped example config fails to load out of the box.

## 9. Testing

Golden-file tests cover OpenAI request and response translation both directions, including multi-part
content, tool calls, and streaming chunk sequences.

SSE tests cover the §4.3 contract explicitly: comment lines, multi-line `data`, `\r` terminators,
missing `[DONE]`, a line exceeding the cap, and an event split across read boundaries.

A fake provider over `httptest` covers a normal completion, a streaming completion, an upstream 500, a
timeout, malformed SSE, and a client disconnect mid-stream — the last asserting the cancel cause
identifies the inbound context.

Config tests are table-driven over valid and invalid documents, including that a rejected reload leaves
the previous configuration live, that a rename-based save is detected, and that an unset optional
environment reference does not fail validation.

## 10. Done criteria

- A streaming `curl` against `/v1/chat/completions` reaches a real provider and returns tokens incrementally, with time-to-first-token close to the provider's own.
- A streamed response reports token usage, because `stream_options` was injected.
- Saving `darkrouter.yaml` from vim changes behavior without a restart and without dropping in-flight requests.
- An invalid edit is rejected and the gateway keeps serving the previous config, with the error visible on `/healthz`.
- A client disconnect is distinguishable from a timeout in logs.
- `go test ./...` passes.
- `docker compose up` yields a working gateway from a clean checkout.
