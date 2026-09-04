# Admin API

Fifty-six routes under `/api`, registered in one table in
`internal/admin/admin.go` and walked by a guard test, so a route cannot be
added without a guard. Three operational routes are registered separately, on
the admin listener but outside the admin handler.

## Conventions

**Guards.** `public` needs nothing. `session` needs the session cookie.
`CSRF` needs the session *and* a session-bound CSRF token *and* a same-origin
check. Every mutating route is `CSRF`.

**Errors** are a JSON envelope with a type and a message. Any verb against an
unmatched `/api/` path is a JSON 404.

**Times** are RFC 3339. **Money** is integer micro-dollars.

**Bodies** are capped per route by a single chokepoint, which is why the caps
can be enumerated exactly: 4 KiB for login, password and OAuth start; 8 KiB
for OAuth completion and token minting; 16 KiB for an alias entry, a model
override and a route preview; 64 KiB for the alias set, a provider, a
credential and the configuration document; 256 KiB for playground bodies; and
32 MiB for a playground file upload.

**Rate limit.** Login is 5 per minute per IP, burst 10, with at most four
bcrypt verifications in flight.

## Operational — no session

| Route | Returns |
|---|---|
| `GET /healthz` | `config_valid`, `warnings`, `uptime`, `version`, `log_records_dropped`, `log_records_written`. |
| `GET /readyz` | `ok\n` as `text/plain`, or 503 naming the database or configuration fault. |
| `GET /metrics` | Prometheus text, including `darkrouter_breaker_open{provider,model}`. |

Liveness does not check the database; readiness does. That split is what makes
readiness the signal an orchestrator should route away from.

## Auth and sessions

| Route | Guard |
|---|---|
| `GET /api/auth/status` | public |
| `POST /api/auth/login` | public |
| `POST /api/auth/logout`, `POST /api/auth/password` | CSRF |
| `GET /api/sessions` | session |
| `DELETE /api/sessions/{id}` | CSRF |

A password is at least 12 and at most 72 bytes — bcrypt's own limit.

**The `id` in a session listing is not the session id.** It is the first eight
characters of the SHA-256 digest of it. Revoking takes that prefix. A prefix
shorter than eight is a 400, one matching several rows is a 409, and one
matching none is a 404.

## Providers and credentials

| Route | Guard |
|---|---|
| `GET /api/presets`, `GET /api/providers` | session |
| `POST /api/providers` | CSRF |
| `PATCH`, `DELETE /api/providers/{id}` | CSRF |
| `POST /api/providers/{id}/test`, `/keys`, `/breaker/reset`, `/discover` | CSRF |
| `PATCH`, `DELETE /api/providers/{id}/keys/{keyId}` | CSRF |

A credential is returned as a label plus a masked suffix, never as material.
`location` is settable at creation and not patchable; `region` and `project`
are both.

## OAuth

Registered only when OAuth flows are wired.

| Route | Guard |
|---|---|
| `POST /api/providers/{id}/oauth/start`, `/oauth/complete` | CSRF |
| `GET /api/oauth/callback` | session |

The callback uses the session guard because a top-level navigation carries no
header to check; `state` does that work. `complete` exists separately from the
callback because the manual-paste path needs a POST of its own.

## Catalogue, routing and configuration

| Route | Guard |
|---|---|
| `GET /api/models` | session — filters `q`, `surface`, `min_context`, `tools` |
| `GET`, `PUT`, `DELETE /api/models/{provider}/{model}/override` | session / CSRF |
| `GET`, `PUT /api/aliases` | session / CSRF |
| `GET`, `PUT /api/policy` | session / CSRF |
| `GET`, `PUT /api/config`, `POST /api/config/reload` | session / CSRF |
| `POST /api/catalog/sync` | CSRF — 202 `{"triggered": true}` |
| `POST /api/route/preview` | CSRF |

A model row carries its free-tier record: `free_type`, `monthly_tokens`,
`credit_tokens`, `pool_key`, `tos` and `opt_in_required`.

Route preview returns the ordered candidates and the skipped ones. There are
**eight** skip reasons: `disabled`, `cooling`, `surface`, `capability`,
`no_credential`, `removed_upstream`, `adapter_surface`, `unsanctioned`.

A `PUT /api/config` naming a restart-only field is refused.

## Observability

| Route | Guard |
|---|---|
| `GET /api/overview`, `/api/usage` | session |
| `GET /api/requests`, `/api/requests/{id}` | session |
| `GET /api/health/providers`, `/api/health/discovery` | session |

`/api/requests` is keyset-paginated and filterable, including by error code.
`/api/requests/{id}` is the trace: every attempt, every skipped candidate,
each attempt's tokens and cost.

## Proxy tokens

| Route | Guard |
|---|---|
| `GET`, `POST /api/proxy-tokens` | session / CSRF |
| `DELETE /api/proxy-tokens/{id}` | CSRF |

Minting returns the secret **once**. It is stored only as a digest.

## Playground

| Route | Guard |
|---|---|
| `POST /api/playground`, `/count`, `/aux` | CSRF |
| `GET`, `POST /api/playground/presets`; `PATCH`, `DELETE /api/playground/presets/{id}` | session / CSRF |
| `GET`, `POST`, `DELETE /api/playground/conversations` | session / CSRF |
| `GET`, `PATCH`, `DELETE /api/playground/conversations/{id}` | session / CSRF |
| `POST /api/playground/conversations/{id}/messages` | CSRF |

`GET /api/playground/conversations` performs a delete — it reaps empty
conversations. The blast radius is small, but it has already caught two test
authors whose backdated fixtures were removed by the call under test.

A local token estimate is marked `X-Darkrouter-Estimated: true` in the header,
never in the body, because clients parse these responses strictly.

## Proxy listener

Thirteen registered routes across four dialect implementations. The three
Gemini operations — `generateContent`, `streamGenerateContent`, `countTokens`
— are served by one registered pattern that dispatches on the `:` suffix.

`/v1/audio/translations` is permanently absent. Rerank and moderation are
served on the OpenAI paths only, because neither Anthropic nor Gemini defines
such an endpoint.
