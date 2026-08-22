# Phase 7 — Admin API and UI

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 6.

---

## 1. Goal

The dashboard: four screens plus settings, backed by eighteen endpoints, that answer "is anything
broken", "why did that request do that", "what can I route to", and "does this key work".

## 2. Scope boundary

**In:** session authentication, the admin REST API, the darkraise-ui scaffold, four screens plus
settings, dev-mode Vite proxying, production `go:embed`.

**Out:** editing aliases or policy through the UI — those live in `darkrouter.yaml` and get read-only
rendered views. This is the structural reason the API stays at eighteen endpoints instead of growing
without bound.

## 3. Authentication

The proxy port stays open on the LAN with an optional bearer token. The admin port requires a session.

Password verification is bcrypt against `DARKROUTER_ADMIN_PASSWORD_HASH`. A successful login sets an
opaque session cookie: `HttpOnly`, `SameSite=Lax`, `Secure` when served over TLS, with a server-side
expiry.

Mutating endpoints require a CSRF token via double-submit: a readable token cookie echoed in a request
header. `SameSite=Lax` alone is not sufficient for a form-shaped POST from another origin.

The asymmetry between the two ports is deliberate and worth restating: an unauthenticated interface
that can read and write API keys is not acceptable even on a trusted network.

## 4. API

```
GET    /api/overview                     health grid, rates, today's spend
GET    /api/providers                    list with status and masked keys
POST   /api/providers                    create from preset or raw kind+base_url
PATCH  /api/providers/:id                enable, priority, base_url override
DELETE /api/providers/:id
POST   /api/providers/:id/keys           add a key
DELETE /api/providers/:id/keys/:keyId
POST   /api/providers/:id/test           live credential probe
GET    /api/models                       catalog search
GET    /api/requests                     keyset-paginated log
GET    /api/requests/:id                 full trace with attempts and bodies
GET    /api/usage                        rollups for charts
GET    /api/config                       parsed config with validation status
POST   /api/config/reload                force a re-read
POST   /api/playground                   SSE test completion
POST   /api/auth/login
POST   /api/auth/logout
GET    /api/auth/status
```

### 4.1 Keys are never returned

`GET /api/providers` returns a key's id, label, masked suffix, enabled flag, cooldown state, and
last-used time. It never returns plaintext or ciphertext. There is no endpoint that reveals a key —
not for editing, not for export. Replacing a key means adding a new one and deleting the old.

### 4.2 Request list pagination is keyset, not offset

`GET /api/requests` paginates on `(ts, id)` with a cursor, not `LIMIT/OFFSET`. The log is append-heavy
and read newest-first; offset pagination degrades as it grows and skips rows when new ones arrive
mid-scroll. Filters — provider, model, status, alias, time range — compose into the cursor query.

### 4.3 Credential probe

`POST /api/providers/:id/test` performs a real, minimal upstream call: a model listing where the kind
supports it, otherwise a one-token completion. It reports reachability, credential validity, latency,
and the discovered model count, and it triggers an on-demand discovery pass on success.

The probe deliberately does not consult the breaker, since its purpose is often to check whether a
cooling provider has recovered.

## 5. Frontend

Scaffolded with `npm create darkraise-ui` into `web/` — sidebar layout, default preset, slate surface —
giving React 19, Vite, Tailwind 4, TanStack Router, Query, Table and Form, recharts 3, and
`darkraise-ui` 6.4.0.

Built to `web/dist` and embedded with `go:embed all:web/dist`. In dev mode the admin server
reverse-proxies unmatched paths to Vite so hot reload works against the real API.

Server state is TanStack Query throughout; there is no client-side store mirroring server data. The
overview and requests list poll on a short interval rather than holding a websocket — the data is
small, the polling is cheap, and a socket is a reconnection problem in exchange for latency nobody
will notice on a dashboard.

## 6. Screens

**Overview.** A provider health grid — one tile per provider showing state, active cooldowns, and key
count — over an error-rate sparkline, requests per minute, and today's spend. The question it answers
in one glance is "is anything broken right now". Built on `stat`, `chart`, and `card`.

**Requests.** A filterable, keyset-paginated table over the log. Selecting a row opens a drawer with
the full trace: every attempt in order with provider, key label, model, outcome, status, and latency;
the dropped-field warnings from phase 4; token counts and cost; and the captured bodies when present.
Built on `table`, `drawer`, and `json-tree-view`.

The attempt chain is the point of this screen. A failover that took three tries must read as three
labelled rows with reasons, not as a single opaque success.

**Catalog.** A searchable index across every provider, filtered by capability, service kind, price
band, and context window, showing which providers serve each model and which aliases resolve to it.
Built on `command` and `table`.

**Playground.** Pick an alias or a `provider/model`, send a prompt, watch it stream, and follow a link
to the trace it produced. This is how a new key or a reordered alias gets verified without dropping to
`curl`.

**Settings.** Provider and key CRUD, plus the rendered read-only view of `darkrouter.yaml` with its
validation status and a reload button. A config currently failing validation is shown prominently with
the error and the note that the previous configuration is still serving.

## 7. Testing

Go handler tests cover authentication rejection on every mutating endpoint, CSRF rejection, that no
endpoint returns key material, and keyset pagination correctness across an insert that lands
mid-scroll.

Frontend tests with Testing Library cover the trace drawer rendering a multi-attempt failover, the
catalog filter composition, and the settings screen showing a validation error while reporting the
previous config as live.

A build test asserts that the embedded SPA is served correctly from the binary with no working
directory dependency.

## 8. Done criteria

- Logging in from a browser on the LAN reaches the dashboard; an unauthenticated request to any `/api/*` endpoint is rejected.
- The overview shows a provider entering and leaving cooldown in near real time.
- A failover request is findable in the log and its drawer explains every attempt and its reason.
- A new provider can be added from a preset, its key tested, and its models discovered without touching a file.
- The catalog finds a model by name across providers and shows what routes to it.
- The playground streams a completion and links to its trace.
- `go test ./...` and the frontend build both pass.
