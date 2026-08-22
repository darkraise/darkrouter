# Phase 7 — Admin API and UI

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 6.

---

## 1. Goal

The dashboard: four screens plus settings, backed by twenty-one endpoints — two of which arrive with
phase 8 — answering "is anything broken", "why did that request do that", "what can I route to", and
"does this credential work".

## 2. Scope boundary

**In:** session authentication, the admin REST API, the darkraise-ui scaffold, four screens plus
settings, dev-mode Vite proxying, production `go:embed`.

**Out:** editing aliases or policy through the UI — those live in `darkrouter.yaml` and get read-only
rendered views. This is the structural reason the API stays at twenty-one endpoints instead of growing
without bound.

## 3. Authentication

The proxy port stays open on the LAN with an optional bearer token. The admin port requires a session.

Password verification is bcrypt (cost 12) against `DARKROUTER_ADMIN_PASSWORD_HASH`. A successful login
issues an opaque session id stored in the `sessions` table with a server-side expiry (default 30 days,
sliding), set as a cookie: `HttpOnly`, `SameSite=Lax`, `Secure` when served over TLS. Sessions in the
database rather than memory means a restart does not log the operator out mid-task; a startup sweep
prunes expired rows. Login rotates the session id, and logout deletes the row rather than only
clearing the cookie.

`SameSite=Lax` is not an arbitrary choice — phase 8's OAuth callback is a state-changing GET reached
by a cross-site top-level redirect, which `Strict` would block. That makes `state` validation the sole
defense there, which phase 8 §5.1 addresses.

Mutating endpoints require a CSRF token **bound to the session by HMAC**, plus an `Origin` or
`Sec-Fetch-Site` check. Naive double-submit is defeated by an attacker who can set a cookie for the
host, and on a plain-HTTP LAN — the default homelab posture, since there is no TLS configuration — an
active network attacker can do exactly that. The SPA is same-origin, so the header check is free and
strictly stronger. Login is covered too, since login CSRF is minor but real.

Cookies are not port-scoped, so the proxy port shares a jar with the admin port. The proxy port must
therefore **never** honor cookies; only the bearer token.

## 4. API

```
GET    /api/overview                     health grid, rates, today's spend
GET    /api/presets                      shipped preset catalog for the create form
GET    /api/providers                    list with status and masked credentials
POST   /api/providers                    create from preset or raw kind+base_url
PATCH  /api/providers/:id                enable, priority, base_url, region, project
DELETE /api/providers/:id
POST   /api/providers/:id/keys           add a credential
DELETE /api/providers/:id/keys/:keyId
POST   /api/providers/:id/test           live credential probe
POST   /api/providers/:id/oauth/start    phase 8
GET    /api/oauth/callback               phase 8
GET    /api/models                       catalog search
GET    /api/requests                     keyset-paginated log
GET    /api/requests/:id                 full trace with candidates, attempts, bodies
GET    /api/usage                        rollups for charts
GET    /api/config                       parsed config with validation status
POST   /api/config/reload                force a re-read
POST   /api/playground                   SSE test completion
POST   /api/auth/login
POST   /api/auth/logout
GET    /api/auth/status
```

`GET /api/auth/status` is reachable without a session, since the SPA calls it to decide whether to
render the login screen. Every other endpoint requires one. `POST /api/playground` is a mutating verb
and carries the CSRF header like any other; its SSE response works through the fetch-with-header
pattern.

### 4.1 Credentials are never returned

`GET /api/providers` returns a credential's id, label, masked suffix, kind, expiry where applicable,
enabled flag, cooldown state, and last-used time. It never returns plaintext or ciphertext, and no
endpoint reveals one — not for editing, not for export. Replacing a credential means adding a new one
and deleting the old.

### 4.2 Keyset pagination

`GET /api/requests` paginates on `(ts, id)` descending. The log is append-heavy and read newest-first;
offset pagination degrades as it grows and skips rows when new ones arrive mid-scroll.

The contract, stated because two implementers would otherwise produce incompatible cursors:

- Sort is `ts DESC, id DESC`. Request ids are ULIDs, which are lexicographically ordered by time, so the tie-break on an identical `ts` is total.
- The page predicate is the lexicographic tuple comparison `(ts, id) < (cursor_ts, cursor_id)`.
- The cursor is opaque base64 of the tuple **plus a hash of the active filters**. A cursor presented with different filters is rejected rather than silently returning nonsense.
- Filters are provider, model, status, alias, surface, and time range.
- A composite index on `(ts DESC, id DESC)` plus indexes on the filter columns; without them the keyset promise is theoretical.

### 4.3 Credential probe

`POST /api/providers/:id/test` performs a real minimal upstream call — a model listing where the kind
supports it, otherwise a one-token completion, and the per-kind variants in phase 8 §6. It reports
reachability, credential validity, latency, and discovered model count, and triggers an on-demand
discovery pass on success.

The probe **deliberately bypasses the circuit breaker**, since its most common purpose is checking
whether a cooling provider has recovered. It is admin-authenticated, CSRF-protected, manual, and
single-operator, so the abuse surface is nil. Two consequences are specified rather than left implied:
a **successful probe resets the ladder** for the probed credential — otherwise the operator reads
"probe OK" beside "still cooling", which is the confusion the probe exists to prevent — and the probe
result is written to the health table so the trace explains the state change. A per-provider mutex
prevents a double-click from issuing two probes, and phase 8's OAuth probe shares the refresh mutex.

Note that the one-token-completion fallback spends real money and consumes quota on a rate-limited
provider. That is accepted for a manual operator action.

## 5. Frontend

Scaffolded with `npm create darkraise-ui` into `web/` — sidebar layout, default preset, slate surface
— giving React 19, Vite, Tailwind 4, TanStack Router, Query, Table and Form, recharts 3, and
`darkraise-ui` 6.4.0.

Built to `web/dist` and embedded with `go:embed all:web/dist`, served with no working-directory
dependency. In dev mode the admin server reverse-proxies unmatched paths to Vite so hot reload works
against the real API.

Server state is TanStack Query throughout; no client store mirrors server data. Polling rather than
websockets: the data is small, polling is cheap, and a socket buys latency nobody notices on a
dashboard in exchange for a reconnection problem. Intervals are stated so "near real time" is
testable — overview and the requests first page at 3s, catalog and usage at 30s, all paused when the
tab is hidden.

## 6. Screens

**Overview.** A provider health grid — one tile per provider with state, active cooldowns, and
credential count — over an error-rate sparkline, requests per minute, and today's spend. Provider-level
signals surface here rather than raw triples: "many triples cooling on one provider" reads as one dead
provider, and a credential disabled pending OAuth reconnection is called out, since only the operator
can fix it. Built on `stat`, `chart`, and `card`.

**Requests.** A filterable, keyset-paginated table. Selecting a row opens a drawer with the full
trace: the candidate list with a skip reason for every target that was not tried, every attempt in
order with provider, credential label, model, outcome, status and latency, the dropped-field warnings
from phase 4, token counts and cost, and captured bodies when present. Built on `table`, `drawer`, and
`json-tree-view`.

The candidate list is what makes this screen worth building. A failover that took three tries must
read as three labelled rows with reasons, and the four candidates that were never tried must say why.

**Catalog.** A searchable index across every provider, filtered by surface, capability, price band,
and context window, showing which providers serve each model, what each alias resolves to, and
whether metadata is known or inferred — an inferred model is marked, since it routes with a warning.
Built on `command` and `table`.

**Playground.** Pick an alias or `provider/model`, send a prompt, watch it stream, follow a link to
the trace it produced. This is how a new credential or a reordered alias gets verified without
dropping to `curl`.

**Settings.** Provider and credential CRUD, plus the rendered read-only view of `darkrouter.yaml` with
validation status and a reload button. A config failing validation is shown prominently with the error
and a note that the previous configuration is still serving.

Deleting a provider lists the aliases that will be left dangling in the confirmation dialog. Per
master design §7 a dangling alias is a **warning, not a validation error** — treating it as an error
would mean one UI delete makes every subsequent config reload fail, leaving the operator with a reload
button that keeps failing and no way out but SSH. The settings screen links from a dangling-alias
warning to the file path that needs editing, since that edit cannot be made in the UI.

## 7. Testing

Go handler tests cover authentication rejection on every endpoint except `auth/status`, CSRF
rejection including a forged `Origin`, that no endpoint returns credential material, keyset
correctness across an insert landing mid-scroll, and cursor rejection when filters change.

Probe tests assert a successful probe resets the ladder and writes health, and that a concurrent
double-click issues one probe.

Frontend tests cover the trace drawer rendering a multi-attempt failover with skip reasons, the
catalog filter composition, the inferred-metadata marking, and the settings screen showing a
validation error while reporting the previous config as live.

A build test asserts the embedded SPA serves correctly from the binary.

## 8. Done criteria

- Logging in from a browser on the LAN reaches the dashboard; an unauthenticated `/api/*` request other than `auth/status` is rejected, as is one with a bad CSRF token or foreign `Origin`.
- The overview shows a provider entering and leaving cooldown within one poll interval.
- A failover request is findable and its drawer explains every attempt and every skipped candidate.
- A new provider can be added from a preset, its credential tested, and its models discovered without touching a file; a successful probe clears an existing cooldown.
- Deleting a provider warns about dangling aliases and does not break the next config reload.
- The catalog finds a model across providers and shows what routes to it.
- `go test ./...` and the frontend build both pass.
