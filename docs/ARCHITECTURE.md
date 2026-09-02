# Architecture

Darkrouter is one static Go binary with an embedded React console. It
listens twice: the proxy port serves LLM clients in three dialects, the admin
port serves the console and its REST API. State is a single SQLite database.
Everything below describes the code as it is, except that the admin-surface
session and header hardening reflects the 2026-09-02 admin decisions;
`docs/API.md` has the routes and `docs/DEPLOY.md` the operations.

## Packages and dependency direction

Module `github.com/darkraise/darkrouter`. Dependencies point downward; a
package never imports one listed below it.

| Package | Role |
|---|---|
| `cmd/darkrouter` | The command: flags, subcommands, startup order |
| `internal/server` | Wires everything into two `http.Server`s, owns workers and shutdown |
| `internal/admin` | Admin REST API, session auth, CSRF, OAuth flows, embedded SPA |
| `internal/exec` | The attempt loop: drives a request through candidates until one commits |
| `internal/router` | Pure function from a snapshot and a query to an ordered candidate list |
| `internal/catalog` | Presets, models.dev metadata, discovery, free-tier list, merged index |
| `internal/provider` | The set of configured upstreams and their credentials, read from the store |
| `internal/store` | SQLite handles, migrations, request log writer, rollups, retention |
| `internal/health` | The circuit breaker; in-memory authority with a persisted copy |
| `internal/tokenize` | Local token estimates where a provider has no counting endpoint |
| `internal/edge`, `edge/{openai,anthropic,gemini}` | Inbound dialects: parse a wire request into IR, write IR back out |
| `internal/adapter`, `adapter/{openaicompat,anthropic,gemini,bedrock,vertex}` | Outbound wire formats; `adapter/xlate` holds the conversions they share |
| `internal/auth` | Credential styles applied to a built request; OAuth refresh |
| `internal/localcli` | An `http.RoundTripper` for the `auggie://` scheme that runs the Augment CLI |
| `internal/config` | Loads and validates `darkrouter.yaml`, hot reload, restart-only table |
| `internal/crypto` | Key derivation from the master key and AES-GCM sealing |
| `internal/sse` | Server-sent-events reader and writer |
| `internal/ir` | The intermediate representation; imports nothing internal |
| `tools/presetgen` | Build-time transcription of the upstream registry into `presets.yaml`; never linked |

Leaves: `ir`, `sse`, `config`, `crypto`, `auth`, `localcli`. Concrete adapters
are imported only by `server` (which registers them) and, for Bedrock and
Vertex, by `admin` (credential probes). `config.Store` cannot import
`store`, so the database overlay is injected as a function at startup.

## Startup

`cmd/darkrouter/main.go` runs, in order: load and validate the YAML; open the
database and migrate forward (a database newer than the binary is refused);
open the keyring with `DARKROUTER_MASTER_KEY`, which verifies the key against
a stored sentinel before anything is decrypted; import the file's `providers:`
block into the database if that has never happened; seed the keyless
free-tier providers once if `catalog.seed_free_providers` is on; import
`aliases:` and `policy:` once; install the database overlay on the config
store and reload; warn if the file still carries blocks the database now
owns; build the server; run until SIGTERM or SIGINT.

Subcommands are dispatched before flag parsing: `hash-password` prints a
bcrypt hash, `rotate-key` re-seals every credential under a new master key.
The server takes `-config` and `-db`.

## Request path

```
client ── edge dialect ── ir.Request ── router ── exec attempt loop ── adapter ── provider
                                                        │
                                                   commit point
                                                        │
client ◀─ edge dialect ◀─ ir.Response / ir.StreamEvent ◀┘
```

**Edge.** `internal/server` mounts one dialect per family. OpenAI:
`/v1/chat/completions`, `/v1/responses`, `/v1/models` and the six auxiliary
surfaces (embeddings, moderations, rerank, images, speech, transcriptions).
Anthropic: `/v1/messages` and `count_tokens`. Gemini: `/v1beta/models/{model}`
dispatched on the `:generateContent`, `:streamGenerateContent` and
`:countTokens` suffix, plus the listing. Each dialect extracts the proxy
token in its own convention (bearer, `x-api-key`, `x-goog-api-key`, `?key=`)
and the server compares it against `server.proxy_token` and the tokens minted
in the console; auth is off only when neither exists. The dialect parses the
body into an `ir.Request` or, when the fast path applies, a `Passthrough`
holding the raw body.

**IR.** `internal/ir` is the vocabulary every other package speaks: a
`Request` of system blocks, messages of typed content blocks (text, image,
audio, document, thinking, tool use, tool result), tools and tool choice,
sampling parameters, `Reasoning{Effort, Budget, Disabled}`,
`ResponseFormat{Type, Schema, Name, Strict}`; a `Response` with usage broken
into input, output, cache-read, cache-write and reasoning tokens; a
`StreamEvent` sequence mirroring Anthropic's event grammar; and an `Error`
with a type that every dialect maps to its own status and shape. Anything a
target cannot express is recorded as a warning on the request, never dropped
silently. `Request.Needs()` derives the capability requirements (tools,
vision, reasoning) the router filters on.

**Router.** `router.Resolve(snapshot, query)` is a pure function over a
snapshot of providers, catalog, config and breaker state. The model name
resolves by three rules in order: an exact alias, whose targets expand by the
next two rules (aliases do not nest); `provider/model` when the prefix names
a configured provider; a bare model name, which matches every provider
offering it in priority order (`priority DESC, id`). Each target is then
filtered, and every rejection is kept as a skip with a reason: `disabled`,
`surface` (the catalog does not declare it), `removed_upstream`,
`adapter_surface` (the adapter kind cannot serve it), `capability`,
`no_credential`, and per-credential `cooling`. Credentials within a provider
are ordered least-recently-used first so a fleet of keys rotates. The result
is the full ordered candidate list; it is not truncated to `max_attempts`,
and the console's route preview is the same call rendered.

**Executor.** `exec.Executor` takes one config snapshot per request, refuses
compressed request bodies, enforces `server.max_body_bytes`, and runs
`runAttempts`: for each candidate while attempts remain under
`policy.retry.max_attempts`, check the budget, re-check the breaker live (a
candidate that started cooling since the snapshot is skipped), mark the
credential used, build the outbound request through the adapter, and
classify the outcome. Advancement rules: success finishes; a fatal outcome
or client cancellation returns to the client; a credential or model failure
moves to the next candidate; a provider failure with a 429 moves to the next
candidate; any other provider failure skips every remaining candidate on
that provider. A passthrough attempt that comes back 400 is retried once on
the same candidate through the IR path, in case the raw body carried
something the provider rejects.

**Fast path.** When the client's dialect already matches the provider's wire
format, the body is forwarded with only the model name and credential
rewritten. Eligibility is per attempt: LLM surface only, dialect and adapter
kind must pair, the adapter must implement `Forwarder` (Bedrock and Vertex do
not: one signs the body, the other encodes the model in the URL), Gemini
streaming only with `alt=sse`. The trace's `path` column records which path
served each attempt.

**Adapters.** Each kind implements `Adapter{Kind, BuildRequest,
ParseResponse, ParseStream, Classify}` and optionally the auxiliary
interfaces (`Embedder`, `Moderator`, `Reranker`, `ImageGenerator`,
`Transcriber`, `Speaker`, `TokenCounter`, `Forwarder`). `openaicompat`
serves all seven surfaces; `gemini` LLM and embeddings; `anthropic`,
`bedrock` and `vertex` LLM only. Vertex is one kind with two request
builders chosen by publisher (Gemini payload to `:generateContent`,
Anthropic payload to `:rawPredict`). Bedrock speaks Converse and reads AWS
eventstream framing. The Augment CLI is not an adapter: the preset is
`openaicompat` with base URL `auggie://cli/v1`, and a transport registered
for that scheme spawns the CLI and returns an OpenAI-shaped response.

**Credentials.** `internal/auth` resolves a stored credential into an
authorizer applied to the built request: bearer, `x-api-key`, query key,
SigV4 for Bedrock, a service-account exchange for Vertex, OAuth with
background refresh. Refresh runs under a per-account mutex; a refusal the
vendor makes terminal disables the credential rather than retrying.

## Streaming and the commit point

A streamed attempt is parsed event by event with `server.sse.max_line_bytes`
as the line cap. Until the first content-bearing event (a text, thinking or
tool-input delta) arrives, everything is held in a pre-commit buffer bounded
by `server.sse.max_precommit_bytes`; a failure in that window, including the
buffer filling, is a retryable provider outcome and the next candidate is
tried with the client none the wiser. The first content-bearing event
commits: headers and buffered events are flushed, the per-attempt timer
switches from the total budget to the idle timeout, and failover is no
longer possible, so any later failure becomes an error event in the stream.
`CommitWriter` records the first header write, non-empty write or flush as
the commit, and the loop trusts that record rather than the operation's own
claim. Unary responses commit when the parsed response is written.

Every proxy response carries `X-Darkrouter-Request` (the ULID that keys the
request log), `X-Darkrouter-Attempts`, and once a provider was tried
`X-Darkrouter-Provider` and `X-Darkrouter-Model`.

## Failover, breaker and timeouts

**Outcome classes.** A transport error, 3xx, 408, 429 or 5xx is a retryable
provider failure; 401, 402 and 403 are retryable credential failures; 404 is
a retryable model failure; other 4xx are fatal. A Darkrouter-side timeout is
a provider failure; an inbound disconnect is a client cancellation. Adapters
with a `BodyClassifier` can turn a 400 into a model failure when the body
says the model is unknown.

**Breaker.** `internal/health` keys entries by `{provider, credential, model}`;
an empty model is a credential-level entry that gates every model on that
key. Success or a fatal outcome deletes the entry. A credential failure cools
the credential-level entry at its current level without advancing the
ladder. A 429 with `Retry-After` cools for exactly that long (capped at
`policy.cooldown.max`) and is not probed. Any other provider failure counts
toward `policy.cooldown.trip_after` (default 3) and cools once reached. The
ladder is 1s, 2s, 4s, 8s, 15s, 30s, 60s, 120s, then doubling, clamped to
`cooldown.max` (default 15m). When a ladder cooldown expires exactly one
request is admitted as a half-open probe; the others still see the entry as
cooling. A failed probe re-trips at the next level because the failure
counter is not reset on cooling. State is written to the `health` table every
five seconds and on shutdown, and restored at start.

**Timeouts** (`policy.timeout`): `connect` (default 10s) is the dial timeout;
`first_byte` (60s) is the response-header timeout; together they bound one
attempt before commit. `total` (10m) is the budget across all attempts of one
request and must be at least `connect + first_byte`. `idle` (120s) is the
longest gap tolerated between stream events after commit. `connect` and `first_byte` live on the shared HTTP transport
and are restart-only; the others are read per request.

**Retry.** `policy.retry.max_attempts` (default 4, range 1–10) caps attempts
per request; there is no per-candidate retry except the passthrough-to-IR
retry above.

## Configuration precedence

1. **Defaults** in `config/load.go`, applied to whatever the file omits.
2. **The file**, `darkrouter.yaml`, parsed with unknown keys rejected,
   `${VAR}` interpolated from the environment. An unresolved
   `server.proxy_token` means no token (auth off, with a warning); an
   unresolved provider `api_key` is an error. Validation rules: breaker
   trip count at least 1, cooldown and every timeout positive, total at
   least connect plus first-byte, `log.retention` at least 48h (the rollup
   rewrites yesterday and today), attempts 1–10, aliases non-empty with
   non-empty targets, provider ids unique with an absolute base URL.
3. **The database overlay.** `providers:`, `aliases:` and `policy:` are
   imported once, on the first start that finds them, and owned by the
   database from then on; the console and API edit them, the file's copies
   are ignored, and a startup warning names any such block still present.
   Every other block (`server`, `log`, `capture`, `catalog`, `media`,
   `playground`) is file-only and shown read-only in the console.
4. **Hot reload.** The file's directory is watched; a change is debounced
   100ms, validated whole, and swapped in only if valid, so a broken edit
   leaves the previous configuration serving. `POST /api/config/reload`
   does the same on demand. Requests take one snapshot each, so
   `server.proxy_token`, `max_body_bytes`, the SSE caps, `policy.timeout.total`
   and `idle`, `max_attempts` and the retention windows apply to the next
   request.
5. **Restart-only keys** are listed in one table in `config/config.go`:
   the two listen addresses, `policy.timeout.connect` and `first_byte`, the
   catalog URLs, intervals and switches, `media.inline`. A reload that
   changes one records "`key` changed; takes effect on restart" on
   `/healthz`, and the config API refuses to write them.

Environment variables the process reads: `DARKROUTER_MASTER_KEY` (required),
`DARKROUTER_ADMIN_PASSWORD_HASH` (without it every login is refused and
`/healthz` warns), `AUGGIE_BIN`, the standard `HTTP(S)_PROXY`, and whatever
the YAML interpolates. `DARKROUTER_PROXY_TOKEN` reaches the process only
through the example file's `${DARKROUTER_PROXY_TOKEN}`.

## Data model

One SQLite file, WAL mode, foreign keys on, incremental auto-vacuum, opened
as three handles: a single writer (`synchronous=NORMAL`), a read pool of
four, and a `synchronous=FULL` handle reserved for credential writes and key
rotation. Migrations are numbered SQL files under `store/migrations/`,
applied forward only.

Money is integer micro-dollars; catalog prices are micros per million
tokens. Timestamps are Unix milliseconds unless noted.

| Table | Purpose | Time columns |
|---|---|---|
| `providers` | One row per upstream: id, name, preset, kind, base URL, auth style, priority, enabled, region/project/location, `free_models_only` | `created_at` ms |
| `provider_keys` | Credentials: label, kind, AES-GCM ciphertext and nonce, scope, enabled | `expires_at` seconds (OAuth), `last_used_at` ms |
| `models` | The merged catalog per `(provider, model)`: surfaces, capabilities and their source, context window, max output, per-million prices for input, output, cache read and cache write, lifecycle state (`live`, `stale`, `removed_upstream`) and missing streak | `discovered_at`, `last_seen_at` ms |
| `model_overrides` | Operator overrides of surfaces, capabilities, context window | — |
| `provider_discovery` | Per-provider sweep bookkeeping and failure streak | `last_*_at` ms |
| `requests` | One row per request: dialect, surface, requested model, resolved alias, candidate list, final provider and model, status, token counts, cost, TTFT, total, error code, warnings, source (`proxy` or `console`), response size and content type | `ts` ms |
| `request_attempts` | One row per attempt: provider, credential, model, outcome, status, latency, error, path, tokens, cost | — |
| `request_bodies` | Captured bodies when `capture.bodies` is on | `expires_at` ms |
| `health` | Persisted breaker state per `(provider, key, model)` | `cooling_until`, `updated_at` ms |
| `usage_daily` | Rollup per `(day, provider, model, alias)`: requests, attempts, tokens, cost | `day` as a UTC `YYYY-MM-DD` |
| `aliases` | Alias chains as `(name, seq, target)` | — |
| `sessions` | Console sessions | `created_at`, `expires_at` ms |
| `proxy_tokens` | Per-client proxy tokens: name, prefix, SHA-256 hash | `created_at`, `last_used_at` seconds |
| `playground_presets`, `playground_conversations`, `playground_messages` | Saved playground state | seconds |
| `settings` | Key-value: keyring salt and verifier, CSRF secret, import markers, stored password hash | — |

`requests` deliberately has no foreign key to `providers`, so deleting a
provider keeps its history. Keyset indexes on `(ts DESC, id DESC)` and on
provider, model, status, surface and source back the cursor pagination.

**Encryption.** The master key goes through PBKDF2-SHA256 (600,000
iterations, a 16-byte salt stored in `settings`) to a 32-byte AES-256-GCM
key. Each credential is sealed with its own nonce and the row id as
associated data, so a ciphertext cannot be moved between rows. A sentinel
sealed at first start is decrypted at every start to catch a wrong key
before any credential is touched. `rotate-key` re-seals every row under a
new salt in one transaction on the FULL-sync handle.

## Background workers

All are started by `internal/server` on a context independent of any
request and stopped after the listeners drain.

| Worker | Interval | Config |
|---|---|---|
| Config watcher | on file change, 100ms debounce | — |
| Request log writer | batches of 128 or every 250ms from a 4096-deep channel; a full channel drops and counts (`/metrics`, `/healthz`) | — |
| Health persister | every 5s when dirty, and on shutdown | — |
| Credential usage | every 30s writes last-used timestamps | — |
| Token refresh | every 5m with jitter; renews credentials expiring within 15m | — |
| Rollup | hourly; recomputes `usage_daily` for yesterday and today (UTC) | `log.retention` must be at least 48h for this reason |
| Retention | hourly with jitter; prunes requests and attempts past `log.retention` and bodies past their expiry, in batches of 500, then incremental vacuum | `log.retention`, `capture.retention` |
| Discovery | at start and every `catalog.discovery.interval` (15m) with jitter; also on demand from the console; fleet-wide concurrency cap | `catalog.discovery.*` |
| models.dev sync | at start and every `catalog.sync_interval` (12h) | `catalog.models_dev_url`, `sync_timeout` |
| Free catalogue sync | at start and every `catalog.free_catalog_interval` (24h) | `catalog.free_catalog_*` |

Discovery marks a provider stale after three consecutive failed probes and
a model `removed_upstream` after three successful listings that omit it;
stale models stay routable (the breaker, not the catalog, avoids a broken
provider), removed ones do not. The catalog is rebuilt before the listeners
bind, from the embedded snapshot when there is no network.

Shutdown: the proxy listener gets `server.shutdown_grace` to drain, then
in-flight requests are cancelled with a short terminal grace; the admin
listener follows; then the workers stop, which drains the log channel and
flushes breaker state.

## Admin surface

The admin listener registers `/healthz`, `/readyz` and `/metrics` without
auth and mounts `internal/admin` at `/`. Login compares against a bcrypt
hash (cost 12) from `DARKROUTER_ADMIN_PASSWORD_HASH` or one stored through
the password-change route; an empty hash fails closed. A session is a random
id in an `HttpOnly`, `SameSite=Lax` cookie, sliding for 30 days with an
absolute lifetime, stored hashed. Mutations need a same-origin request (by
`Sec-Fetch-Site` or `Origin`) and an `X-CSRF-Token` that is an HMAC of the
session id under a secret in `settings`. The two listeners are separate
ports because cookies are not port-scoped: the proxy never honours the
console's cookie, and the console never accepts the proxy token.

OAuth connection is PKCE authorization-code. Where a vendor registers a
`localhost` redirect, a temporary loopback listener receives it for ten
minutes; otherwise the operator pastes the redirected URL back. Both paths
converge on the same completion and store a refresh token, never returned.

The playground routes build a request in the chosen dialect and hand it to
the same executor the proxy uses, tagged `source: console`, so failover,
breaker, budget and the request log all apply.

## Console build and embed

`web/` is a Vite + React 19 application on TanStack router and query,
`darkraise-ui`, recharts and xyflow. `npm run build` writes to
`internal/admin/dist` (Vite's `outDir`), where `//go:embed all:dist` picks
it up at compile time; the directory is gitignored except for a `.gitkeep`
so a fresh clone compiles. The SPA handler serves a real file from the
embedded tree when the path names one and `index.html` otherwise, so deep
links resolve client-side; a binary built without the bundle answers 404 in
plain text. `web/go.mod` declares `web/` a separate Go module so
`go test ./...` from the root never enters `node_modules`.

The Dockerfile builds the bundle in a Node stage, copies it into the Go
stage before `go build`, and ships one static binary on Alpine. The image
is what a deployment runs; neither `npm run build` nor `go build` on the
host changes a running container.
