# Architecture

## Shape

Two HTTP listeners in one process, over one SQLite file.

- The **proxy listener** serves client traffic on thirteen routes across four
  dialect implementations.
- The **admin listener** serves the console, its API, and the unauthenticated
  `/healthz`, `/readyz` and `/metrics` endpoints.

Nothing else runs. There is no queue, no cache server, no second process.

## Packages

| Package | Holds |
|---|---|
| `internal/ir` | The canonical request, response and stream types. No I/O, no internal imports. |
| `internal/edge` | Inbound dialects: parse a client request into the IR, write a response back in that client's shape. |
| `internal/adapter` | Outbound provider kinds: render the IR into a provider's wire format and parse what comes back. |
| `internal/router` | Pure candidate resolution. Given a snapshot and a query, produce an ordered candidate list. |
| `internal/exec` | The attempt loop: try candidates, classify outcomes, commit, record. |
| `internal/health` | Circuit breaker, cooldown ladder, credential recency. |
| `internal/catalog` | Presets, discovery, metadata merge, pricing, free-tier terms. |
| `internal/provider` | The configured fleet as the router sees it. |
| `internal/store` | SQLite: schema, migrations, request log, credentials, settings. |
| `internal/config` | Configuration loading, validation, precedence, reload. |
| `internal/auth` | Credential resolution into a request authorizer. |
| `internal/admin` | Admin API handlers and the embedded console. |
| `internal/server` | Wiring: listeners, routes, adapters, background workers, shutdown. |

**Dependency direction is downward.** `ir` sits at the bottom and imports
nothing internal; `server` sits at the top and is the only package that knows
every concrete adapter.

One violation exists and is known: `internal/adapter/openaicompat/quirks.go`
and `internal/adapter/bedrock/discover.go` import `internal/catalog`, which
sits above `adapter`. The comment on `adapter.Adapter` claiming an adapter
that imported catalog would close a cycle is true of the root package and
false of those two subpackages. Recorded rather than quietly tolerated; see
`plan/status.md`.

## Interfaces

A dialect implements:

```go
ParseRequest(r *http.Request, maxBody int64) (*ir.Request, *edge.Passthrough, error)
ProxyToken(r *http.Request) string
```

An adapter implements exactly five methods — `Kind() string`, `BuildRequest`,
`ParseResponse`, `ParseStream(… , maxLine int)` and `Classify` — and may
additionally implement any of the optional interfaces: `Embedder` and its
siblings for the auxiliary surfaces, `Forwarder` for fast-path eligibility,
`TokenCounter` for native counting, `BodyClassifier` for re-reading an
ambiguous 400, and `SurfaceProvider` for declaring more than the `llm`
surface. An adapter that omits `SurfaceProvider` serves `llm` only.

Passthrough eligibility is decided by interface absence rather than by a
predicate: Bedrock and Vertex do not implement `Forwarder`, so a sixth kind
stays ineligible until someone deliberately writes its builder.

## Startup

1. Load and validate configuration; an unknown key is an error.
2. Open the store, run migrations forward, refuse a database newer than the
   binary.
3. Derive the encryption key from the master key and verify it against a
   stored verifier.
4. Overlay database-held providers, aliases and policy onto the file
   configuration, then publish the first snapshot.
5. Construct the adapters, the executor's shared transport, and the router.
6. Start the background workers.
7. Bind both listeners.

## Request path

A client request is parsed by its dialect into the IR and a `Passthrough`
record. The router resolves an ordered candidate list from the current
snapshot. The executor walks that list: for each candidate it resolves a
credential, renders the request through the target's adapter — or forwards the
original bytes when the fast path applies — sends it, and classifies the
outcome. The first candidate to commit a response wins. Everything that
happened is written to the request log.

See [`execution-and-routing.md`](execution-and-routing.md) for the loop's
rules, and [`ir-and-dialects.md`](ir-and-dialects.md) for the translation
contract.

## Background workers

Eleven, each panic-restarting: a worker that dies is logged, waited on for a
second, and started again, so one failing job cannot take down logging or
health persistence for the life of the process.

| Worker | Does |
|---|---|
| Log writer | Batches request records to SQLite. |
| Log sweeper | Expires request history past its retention. |
| Body sweeper | Expires captured bodies past their retention. |
| Usage rollup | Recomputes daily usage. |
| Health persister | Writes dirty breaker state every five seconds. |
| models.dev sync | Refreshes the upstream metadata index. |
| Free-catalogue sync | Refreshes free-tier terms. |
| LiteLLM sync | Refreshes the community price index. |
| Discovery sweeper | Probes each provider's live model list. |
| OAuth refresh | Renews subscription credentials before expiry. |
| Catalogue rebuild | Republishes the merged catalogue when an input changes. |

## Configuration

Five steps, in order: defaults, then the file (rejecting unknown keys and
interpolating `${VAR}`), then the database overlay for providers, aliases and
policy, then hot reload on a debounced file watch, then warnings for
restart-only fields that changed. See
[`configuration.md`](configuration.md).

## Console

Built with Vite, embedded into the binary at compile time with
`//go:embed all:dist`. Neither `npm run build` nor `go build` alone changes
what a running container serves — only rebuilding the image does. See
[`../operations/deploy.md`](../operations/deploy.md).
