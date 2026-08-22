# Phase 1 — Foundation

**Status:** Approved design.
**Date:** 2026-08-22
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

**Out:** persistence of any kind, failover, health tracking, the admin API, the SPA, any dialect other
than OpenAI, any auxiliary surface, the presets catalog.

## 3. Repository layout

```
cmd/darkrouter/main.go
internal/config/          load, validate, watch
internal/ir/              chat request, response, stream event, error, usage
internal/edge/openai/     inbound dialect
internal/adapter/openaicompat/
internal/provider/        ProviderSource interface + YAML implementation
internal/server/          listeners, wiring, shutdown
docs/superpowers/specs/
Dockerfile
compose.yml
darkrouter.example.yaml
```

Module path: `github.com/darkraise/darkrouter`. Go 1.26.

## 4. Design decisions

### 4.1 Provider source is an interface from the start

Providers ultimately live in SQLite (master design §7), but there is no store until phase 2. Rather
than write throwaway code, phase 1 defines:

```go
type Source interface {
    Providers(context.Context) ([]Provider, error)
    Revision() uint64   // changes when the set changes
}
```

with a YAML-backed implementation. Phase 2 adds a SQLite implementation and uses the YAML one to seed
the database on first run. The interface therefore survives; only the implementation behind it grows.

Phase 1's `providers:` block in `darkrouter.yaml` carries `id`, `kind`, `base_url`, `models`, and
`api_key` (via `${ENV}` interpolation only — no plaintext keys in the file, ever).

### 4.2 Configuration is swapped atomically

Config is held in an `atomic.Pointer[Config]`. A request takes one snapshot at entry and uses it for
its whole lifetime, so a reload mid-request cannot change behavior underneath it.

`fsnotify` watches the file. On change the new file is parsed and validated in full; only on success
is the pointer swapped. **A file that fails validation is rejected wholesale and the previous
configuration stays live**, with the error recorded and exposed on the health endpoint. A broken edit
must never take the gateway down.

Editors that write via rename rather than in-place truncation will fire remove-then-create rather than
write; the watcher must re-establish the watch on the path, not the inode.

### 4.3 SSE scanning needs an explicitly raised buffer

`bufio.Scanner` defaults to a 64 KiB maximum token size. A single SSE `data:` line carrying a large
tool-call argument delta or a base64 image exceeds that and the scanner fails with
`bufio.ErrTooLong` — which presents as a truncated stream, not an obvious error.

The SSE reader sets an explicit maximum (1 MiB default, configurable) and treats overflow as a
protocol error with a clear message rather than a silent truncation.

### 4.4 Flushing must not be defeated by buffering

Streaming responses use `http.ResponseController.Flush` after each event. No `bufio.Writer` may wrap
the `ResponseWriter` in the streaming path, and no middleware may buffer the response body, or
time-to-first-token becomes time-to-completion.

### 4.5 Client disconnect cancels upstream

The upstream request derives its context from the inbound request context. A client that hangs up
cancels the upstream call rather than leaving it to run to completion against a metered provider.

## 5. Interfaces introduced

`internal/ir` gains the chat types from master design §5 — `Request`, `Message`, `ContentBlock`,
`Response`, `StreamEvent`, `Usage`, `Error` — with no adapter or dialect coupling.

`internal/edge` gains the `Dialect` interface and its `openai` implementation, serving:

```
POST /v1/chat/completions
GET  /v1/models
```

`/v1/models` in this phase returns the union of models declared on configured providers. Phase 6
replaces its backing with the catalog.

`internal/adapter` gains the `Adapter` interface and its `openaicompat` implementation. `Classify`
returns the outcome taxonomy from master design §8 even though phase 1 has nowhere to fail over to —
defining it now keeps phase 3 from having to revisit every adapter.

## 6. Execution path

Single candidate, no retry: resolve the model to exactly one provider, build the upstream request,
send, and stream or return the response. If it fails, the error is translated to the OpenAI error
shape and returned.

This is the same call sequence phase 3's exec loop will drive, with the loop itself absent. Keeping
the sequence identical means phase 3 adds a loop rather than restructuring.

## 7. Server

Two listeners in one process. The proxy listener serves `/v1/*`. The admin listener serves
`/healthz`, `/readyz`, and — from phase 7 — `/api/*` plus the SPA. Both shut down gracefully on
SIGTERM with a drain deadline, and in-flight streams are given until the deadline to finish.

## 8. Configuration surface

```yaml
server:
  proxy_listen: :8080
  admin_listen: :8081
  proxy_token: ${DARKROUTER_PROXY_TOKEN}   # optional bearer

providers:                                  # phase 1 only; phase 2 moves these to SQLite
  - id: groq
    kind: openaicompat
    base_url: https://api.groq.com/openai/v1
    api_key: ${GROQ_KEY}
    models: [llama-3.3-70b-versatile]

policy:
  timeout: { connect: 10s, first_byte: 60s, total: 10m }
```

Validation rejects: unknown keys, a provider with no `id` or `base_url`, a `base_url` that is not an
absolute URL, an unresolved `${ENV}` reference, and a duplicate provider `id`.

## 9. Testing

Golden-file tests cover OpenAI request and response translation in both directions, including
multi-part content, tool calls, and streaming chunk sequences.

A fake provider over `httptest` covers: a normal completion, a streaming completion, an upstream 500,
an upstream timeout, a malformed SSE stream, an SSE line exceeding the scan buffer, and a client
disconnect mid-stream.

Config tests are table-driven over valid and invalid documents, and include the case that a rejected
reload leaves the previous configuration live and serving.

## 10. Done criteria

- A streaming `curl` against `/v1/chat/completions` reaches a real provider and returns tokens incrementally, with time-to-first-token close to the provider's own.
- Editing `darkrouter.yaml` changes behavior without a restart and without dropping in-flight requests.
- An invalid edit is rejected and the gateway keeps serving on the previous config.
- `go test ./...` passes.
- `docker compose up` yields a working gateway from a clean checkout.
