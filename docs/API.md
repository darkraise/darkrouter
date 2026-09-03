# Admin API

Every route the admin listener (`server.admin_listen`, default `:8081`)
serves, plus the proxy listener's inbound surfaces. This describes the
contract after the 2026-09-02 review fixes land (response envelopes, uniform
deletes, validation rules, session hardening); where the running build
predates them, the differences are the ones listed under "Contract changes"
at the end.

Handlers live in `internal/admin`; routes are registered in
`internal/admin/admin.go`. The three unauthenticated operational routes are
registered in `internal/server/server.go`.

## Conventions

**Authentication.** Every `/api` route except `GET /api/auth/status` and
`POST /api/auth/login` needs the `darkrouter_session` cookie: `HttpOnly`,
`SameSite=Lax`, `Path=/`, `Secure` when the request arrived over TLS or with
`X-Forwarded-Proto: https` from a loopback or private peer. A session slides
for 30 days of inactivity (touched at most once per five minutes) and expires
30 days after creation regardless. A missing or expired session is
`401 {"error":"not authenticated"}`.

**Mutations.** Every `POST`, `PUT`, `PATCH` and `DELETE` additionally requires:

- a same-origin request: `Sec-Fetch-Site: same-origin`, or, when the browser
  sends no `Sec-Fetch-Site`, an `Origin` whose host equals the request host.
  `same-site`, `cross-site` and `none` are refused, as is a request carrying
  neither header. Failure is `403 {"error":"cross-site request refused"}`.
- the `X-CSRF-Token` header, whose value is returned by `GET /api/auth/status`
  and `POST /api/auth/login`. Failure is `403 {"error":"invalid csrf token"}`.

`GET /api/oauth/callback` is the one exception: it is a top-level navigation,
so it needs the session but no CSRF token.

**Errors.** Every error body is `{"error": "<message>"}`. A `500` carries a
fixed message; the detail is in the server log. An unknown `/api` path is
`404 {"error":"no such endpoint"}`.

**Request bodies.** JSON with unknown fields rejected (`400`). Each handler
caps the body: 4 KiB for login and password, 16 KiB for credential, policy,
override and route preview bodies, 64 KiB for provider, config and alias
bodies, 256 KiB for playground chat, count, presets and conversations, 32 MiB
for `POST /api/playground/aux`.

**Times and money.** `*_ms` fields are Unix milliseconds; `*_at` fields are
RFC 3339 strings; `*_micros` fields are US-dollar micros (10⁻⁶), `null` when
the model has no price.

**Response headers.** `X-Content-Type-Options: nosniff`,
`X-Frame-Options: DENY`, `Referrer-Policy: same-origin` and a content security
policy on every admin response; `Cache-Control: no-cache` on `index.html` and
`public, max-age=31536000, immutable` on hashed assets.

**Rate limit.** `POST /api/auth/login` is limited per client address (five
per minute, burst ten) with at most four bcrypt verifications in flight;
excess is `429` with `Retry-After`.

## Operational (no session)

| Route | Response |
|---|---|
| `GET /healthz` | `200 {config_valid, warnings: string[], uptime, version, log_records_dropped, log_records_written, config_error?}` |
| `GET /readyz` | `200` empty while the database answers a ping and the configuration is valid; `503` otherwise |
| `GET /metrics` | `200 text/plain; version=0.0.4`: `darkrouter_requests_total{dialect,surface,status}`, `darkrouter_attempts_total{provider,outcome}`, `darkrouter_request_duration_seconds` (histogram), `darkrouter_breaker_open{provider,model}` (gauge), `darkrouter_log_records_written_total`, `darkrouter_log_records_dropped_total` |

## Auth and sessions

| Route | Body | Response |
|---|---|---|
| `GET /api/auth/status` | — | `200 {authenticated, configured, csrf_token?}`. `configured` is whether a password hash exists; `csrf_token` only when authenticated. |
| `POST /api/auth/login` | `{password}` | `200 {authenticated: true, csrf_token}`, sets the cookie and rotates any existing session id. `401 invalid password` (also when no hash is configured), `400` when the password exceeds 72 bytes, `429` when limited. Same-origin check applies; no CSRF token needed. |
| `POST /api/auth/logout` | — | `200 {authenticated: false}`, clears the cookie |
| `POST /api/auth/password` | `{current, new}` | `200 {revoked}` — the number of other sessions ended. `401` wrong current, `400` new shorter than 12 characters or longer than 72 bytes. |
| `GET /api/sessions` | — | `200 {sessions: [{id, prefix, created_at, expires_at, current}]}`. `id` and `prefix` are the first 8 characters of the session id; the full id is never returned. |
| `DELETE /api/sessions/{id}` | — | `204`. `{id}` is the full id or a prefix of at least 8 characters matching exactly one session; `404` otherwise. |

The password hash in the database (set through `POST /api/auth/password`)
takes precedence over `DARKROUTER_ADMIN_PASSWORD_HASH` unless the environment
value has changed since it was last seen, in which case the environment wins
and the stored row is removed.

## Presets and providers

Provider view, returned wherever a provider is:

```
{id, name, preset, kind, base_url, priority, enabled, auth_style,
 free_models_only, allow_unsanctioned_free,
 credentials: [{id, label, masked, enabled, cooling, kind, expires_at?, scope?}]}
```

`masked` is `…` plus the last four characters of the secret, or `****`.
`expires_at` is Unix seconds and appears on OAuth credentials.

| Route | Body | Response |
|---|---|---|
| `GET /api/presets` | — | `200 {presets: [{id, name, kind, base_url, surfaces, auth_kind, website, free_tier}]}` sorted by id |
| `GET /api/providers` | — | `200 {providers: [provider]}`. Credential counts and states come from a projection that never decrypts a secret. |
| `POST /api/providers` | `{id, name?, preset?, kind?, base_url?, auth_style?, priority?, enabled?, region?, project?, location?, free_models_only?, allow_unsanctioned_free?}` | `201 {id}`. `409` when the id exists. The created resource is not echoed; read it back with `GET /api/providers`. |
| `PATCH /api/providers/{id}` | `{name?, base_url?, priority?, enabled?, region?, project?, free_models_only?, allow_unsanctioned_free?}` | `200 provider` (the updated resource). `404` unknown id. |
| `DELETE /api/providers/{id}` | — | `204`; `404` unknown id. Aliases whose chain named the provider keep the dangling target and are reported as invalid by `GET /api/config`. |
| `POST /api/providers/{id}/test` | query `key` (credential id; default the first) | `200 {ok, probe, latency_ms, model_count?, error?}` — a failed probe is `ok: false` with `error`, not an error status. `probe` names what was exercised: `listing`, `completion`, `signature`, `permission`, `region`, `reachability`, `expiry`, `refresh`. `404` unknown provider or credential, `400` no credential to test. |
| `POST /api/providers/{id}/keys` | `{label?, secret}` | `201 {id, label}`. The secret is never returned again. |
| `PATCH /api/providers/{id}/keys/{keyId}` | `{secret?, enabled?}` (at least one) | `200` credential view. `404` unknown credential; the replacement is scoped to the provider in the path. |
| `DELETE /api/providers/{id}/keys/{keyId}` | — | `204`; `404` |
| `POST /api/providers/{id}/breaker/reset` | — | `200 {reset: id, credentials}`; `404` |
| `POST /api/providers/{id}/discover` | — | `202 {triggered: id}`; `404`; `503` when discovery is not running |

Validation on create and patch (`400` with the rule named):

- `id` matches `^[a-z0-9][a-z0-9._-]{0,63}$`
- `base_url` parses as an `http` or `https` URL (presets may supply a
  non-HTTP scheme such as `auggie://`; an operator-supplied value may not)
- `kind` is a registered adapter (`openaicompat`, `anthropic`, `gemini`,
  `bedrock`, `vertex`)
- `auth_style` is one of the credential vocabulary (`bearer`, `x-api-key`,
  `query`, `optional`, `sigv4`, `gcp-sa`, `oauth`)
- `priority` is 0–1000
- `preset`, when given, is a shipped preset; it fills `kind`, `base_url`,
  `name` and `auth_style` where the body left them empty, and `kind` plus
  `base_url` are required when it does not

## OAuth connection

Registered only when the server has an OAuth flow store; a build without
one answers `404 no such endpoint`.

| Route | Body | Response |
|---|---|---|
| `POST /api/providers/{id}/oauth/start` | `{label?}` | `200 {authorize_url, state, redirect_uri, style, preset, listener_error?}`. `style` is `localhost` when a loopback listener was bound for the vendor's registered redirect, otherwise `manual` (paste the redirected URL back). `400` when the provider's preset declares no OAuth. |
| `POST /api/providers/{id}/oauth/complete` | `{redirected_url}` — the whole URL the browser landed on | `201 {credential_id, label, account}`. `400` for a URL without `code` and `state`, a state that is unknown, expired (10 minutes), used, or bound to another session, or a vendor `error` parameter; `502` when the vendor's token endpoint failed. |
| `GET /api/oauth/callback?code&state` | — | `201 {credential_id, label, account}`; same `400`/`502` rules. Session required, no CSRF token. |

The `localhost` style also binds a temporary listener on the loopback port
the preset registers, serving `GET /callback` as plain text for up to ten
minutes. Starting a new flow stops the previous listener.

## Models, overrides, aliases, policy, config

| Route | Body | Response |
|---|---|---|
| `GET /api/models` | query `q` (substring of the model id), `surface`, `min_context`, `tools=true` | `200 {models: [{model, providers, surfaces, context_window, max_output_tokens, tools, vision, reasoning, inferred, state, pricing: {input_micros, output_micros} \| null, publisher?, merge_source}], aliases: [{name, targets}]}` |
| `GET /api/models/{provider}/{model}/override` | — | `200 {surfaces?, capabilities?: {tools, vision, reasoning}, context_window?}` — unset fields are omitted; an empty object when no override exists (not a 404, so the console's inspection of any model stays quiet). |
| `PUT /api/models/{provider}/{model}/override` | same shape | `200` the stored override. `404` unknown provider; `400` when a surface is not one of `llm`, `embedding`, `image`, `tts`, `stt`, `rerank`, `moderation`. |
| `DELETE /api/models/{provider}/{model}/override` | — | `204`; `404` when there was none |
| `GET /api/aliases` | — | `200 {"<alias>": ["provider/model", ...]}` |
| `PUT /api/aliases` | the same map | `200 {valid: true}`, or `200 {valid: false, error, serving}` when the write succeeded but the reload did not (the previous configuration keeps serving). `400` for an empty name, an empty chain, a blank target or a target naming an unknown provider. |
| `GET /api/policy` | — | `200 {cooldown: {trip_after, max}, retry: {max_attempts}, timeout: {connect, first_byte, total, idle}}` — durations as strings |
| `PUT /api/policy` | the same object, every field optional | `200 {valid: true}` or `{valid: false, ...}`. `400` for `max_attempts` outside 1–10, `trip_after` below 1, a non-positive duration, `total` shorter than `connect + first_byte`, or `timeout.connect`/`timeout.first_byte` (restart-only; cannot be written here). |
| `GET /api/config` | — | `200 {valid, warnings, fields: {"<dotted.key>": {source: "file" \| "database" \| "default", hot_reloadable}}, blocks: {server, log, capture, catalog, media, playground, aliases, policy}, error?, serving?}`. `server.proxy_token` is never included. |
| `PUT /api/config` | `{aliases?, policy?}` | As the two `PUT`s above, applied in one transaction so a rejected policy leaves the aliases untouched. |
| `POST /api/config/reload` | — | `200 {valid: true}` or `{valid: false, error, serving}` |
| `POST /api/catalog/sync` | — | `202 {}`; the models.dev sync runs in the background and its outcome is visible in `GET /api/health/discovery` and the startup warnings. `503` when the syncer is not running. |
| `POST /api/route/preview` | `{model, surface?, needs_tools?, needs_vision?, needs_reasoning?}` | `200 {candidates: [{provider_id, key_id, model, kind, publisher?, inferred}], skips: [{provider_id, key_id?, model, reason}], error?}`. `reason` is one of `disabled`, `cooling`, `surface`, `capability`, `no_credential`, `removed_upstream`, `adapter_surface`. "Nothing routes" is `200` with `error` set. `400` without `model`. |

## Health

| Route | Response |
|---|---|
| `GET /api/health/providers` | `200 {providers: [{provider_id, key_id, model, cooling_until?, backoff_level, consecutive_failures}]}` — one row per breaker that has state |
| `GET /api/health/discovery` | `200 {providers: [{provider_id, total, live, stale, removed_upstream, max_missing_streak, filtered_out}]}` |

## Overview and usage

`GET /api/overview` returns:

```
{providers: [{id, name, state, cooling, credentials, enabled, needs_reauth}],
 requests_per_min, error_rate, window_sec,
 today_spend: {micros: int | null, priced, estimated},
 latency: {p50_ms, p95_ms},
 series: [{day, requests, attempts, tokens_in, tokens_out, cost_micros}],
 failovers: [{id, ts, alias, final_provider_id, final_model, attempts, total_ms}],
 failover_edges: [{from_provider_id, to_provider_id, requests}]}
```

`state` is `disabled`, `unconfigured`, `degraded` or `healthy`. `series`
covers the last 30 UTC days; `failovers` is the last five in a five-minute
window. `today_spend.micros` is `null` when nothing priced ran today, and
`today_spend.estimated` is true when any of the total was priced from a
third-party figure rather than the seller's own.

`GET /api/usage?days=30&group_by=` returns
`{days: [{day, requests, attempts, tokens_in, tokens_out, cost_micros, key?}], priced, group_by?}`.
`group_by` is empty, `provider`, `model` or `alias`; `days` is 1–365
(`400` when unparseable or out of range). `day` is a UTC date.

## Requests

`GET /api/requests` filters: `provider`, `model`, `status`, `alias`,
`surface`, `error_code`, `source` (`proxy` or `console`), `since_ms`,
`until_ms`, `limit` (default and maximum 200), `cursor`. An unparseable
numeric filter is `400`.

Response: `{requests: [row], next_cursor?}`. `next_cursor` is present only
when the page was full; a cursor minted under different filters is `400`.
Row:

```
{id, ts_ms, dialect, surface, model, alias?, provider?, final_model?, status,
 source, tokens_in, tokens_out, cache_read_tokens, reasoning_tokens,
 cost_micros, ttft_ms, total_ms, error_code?, attempts, path?}
```

`path` is `passthrough` or `ir`. `status` is `ok` or `error`.

`GET /api/requests/{id}` returns the trace, `404` when unknown:

```
{id, ts_ms, dialect, surface, source, path, model, alias, provider,
 final_model, status, error_code, tokens_in, tokens_out, cache_read_tokens,
 reasoning_tokens, cost_micros, ttft_ms, total_ms,
 candidates: string[], skips: string[],
 attempts: [{seq, provider, key_label, model, outcome, status_code,
             latency_ms, error, path, tokens_in, tokens_out, cost_micros}],
 warnings: string[], surface_meta, response_bytes, response_content_type,
 bodies: [{kind, content}]}
```

`key_label` is the credential's label. `bodies` is empty unless
`capture.bodies` is on.

## Proxy tokens

| Route | Body | Response |
|---|---|---|
| `GET /api/proxy-tokens` | — | `200 {tokens: [{id, name, prefix, created_at, last_used_at}]}` |
| `POST /api/proxy-tokens` | `{name}` | `201 {id, name, prefix, created_at, secret}` — the only time the secret (`dr_…`) is returned |
| `DELETE /api/proxy-tokens/{id}` | — | `204`; `404` |

## Playground

The three run routes build a request for the chosen dialect and hand it to
the same executor the proxy uses. The response is exactly what the proxy
route would return — status, body, stream and `X-Darkrouter-*` headers —
with `X-Darkrouter-Request` set first so the console can link the trace, and
the request recorded with `source: console`.

| Route | Body | Response |
|---|---|---|
| `POST /api/playground` | `{model, prompt?, system?, messages?: [{role, content}], temperature?, max_tokens?, top_p?, top_k?, stop?, response_schema?, reasoning_effort?, reasoning_budget?, tools?, stream? (default true), dialect? (openai, anthropic, gemini)}` — `messages` or `prompt` required | The dialect's chat response. `400` for a missing model or prompt, tools on the gemini dialect, or an unknown dialect. |
| `POST /api/playground/count` | `{dialect: anthropic \| gemini, model, prompt}` | `{input_tokens}` or `{totalTokens}` with `X-Darkrouter-Estimated: true` when counted locally |
| `POST /api/playground/aux` | `{surface: embeddings \| rerank \| moderations \| images \| speech \| transcriptions, model?, body?, file_b64?, filename?}` | The matching OpenAI-dialect surface's response. `transcriptions` needs `file_b64`; every other surface needs `body` as a JSON object. |
| `GET /api/playground/presets` | — | `200 {presets: [{id, name, dialect, model, config, created_at, updated_at}]}` |
| `POST /api/playground/presets` | `{name, dialect, model?, config}` | `201` preset. `409 {error, id}` when the name exists, with the existing preset's id. |
| `PATCH /api/playground/presets/{id}` | same body (full replace) | `200` preset; `404`; `409` when the new name collides with another preset |
| `DELETE /api/playground/presets/{id}` | — | `204`; `404` |
| `GET /api/playground/conversations` | — | `200 {conversations: [{id, title, dialect, model, config, preview, created_at, updated_at}]}`. Listing also removes empty conversations older than an hour. |
| `POST /api/playground/conversations` | `{title, dialect, model?, config}` | `201` conversation; `403` when `playground.save_conversations` is off |
| `DELETE /api/playground/conversations` | — | `200 {deleted}` — purges every conversation |
| `GET /api/playground/conversations/{id}` | — | conversation plus `messages: [{seq, role, content, request_id, created_at}]`; `404` |
| `PATCH /api/playground/conversations/{id}` | same body as create | `200` conversation; `403`; `404` |
| `DELETE /api/playground/conversations/{id}` | — | `204`; `404` |
| `POST /api/playground/conversations/{id}/messages` | `{role: user \| assistant, content, request_id?}` | `201 {seq}`; `403`; `404` |

## Console

Any path that is not `/api/...`, `/healthz`, `/readyz` or `/metrics` is the
console: a real file under the embedded `dist/` is served as-is, anything
else gets `index.html` so deep links resolve client-side. There is no
directory listing under `/assets/`. A binary built without the bundle
answers `404` in plain text.

## Proxy listener

The proxy (`server.proxy_listen`, default `:8080`) accepts the credential
form each dialect's clients send — `Authorization: Bearer` for OpenAI,
`x-api-key` or bearer for Anthropic, `x-goog-api-key` or `?key=` for
Gemini — and compares it against `server.proxy_token` and the tokens minted
by `POST /api/proxy-tokens`. Authentication is off only when the config
token is empty and no minted token exists. A bad token is `401` in the
dialect's own error shape. The dashboard's session cookie is never honoured
here.

| Route | Dialect |
|---|---|
| `POST /v1/chat/completions` | OpenAI chat |
| `POST /v1/responses` | OpenAI Responses (stateless; `previous_response_id`, `conversation`, `background` refused) |
| `POST /v1/embeddings`, `/v1/moderations`, `/v1/rerank`, `/v1/images/generations`, `/v1/audio/speech`, `/v1/audio/transcriptions` | OpenAI-shaped auxiliary surfaces |
| `GET /v1/models` | OpenAI listing: aliases (`owned_by: darkrouter`) then catalog models |
| `POST /v1/messages`, `/v1/messages/count_tokens` | Anthropic |
| `POST /v1beta/models/{model}:generateContent`, `:streamGenerateContent` (`?alt=sse` for SSE, else a JSON array), `:countTokens` | Gemini |
| `GET /v1beta/models` | Gemini listing |

Every proxy response carries `X-Darkrouter-Request` (the request id),
`X-Darkrouter-Attempts`, and, once a provider was tried,
`X-Darkrouter-Provider` and `X-Darkrouter-Model`. Token counts computed
locally add `X-Darkrouter-Estimated: true`. Bodies larger than
`server.max_body_bytes` are `413`; a compressed request body is `415`.

Errors map from the internal error type to each dialect's status and shape:

| Type | OpenAI / Gemini | Anthropic |
|---|---|---|
| invalid request | 400 | 400 `invalid_request_error` |
| authentication | 401 | 401 `authentication_error` |
| permission | 403 | 403 `permission_error` |
| not found | 404 | 404 `not_found_error` |
| rate limit | 429 | 429 `rate_limit_error` |
| overloaded | 503 | 529 `overloaded_error` |
| upstream or gateway error | 502 (Gemini 500) | 502 `api_error` |

## Contract changes

Relative to the build before the 2026-09-02 fixes, in case a client was
written against it:

- `GET /api/health/providers`, `/api/sessions`, `/api/proxy-tokens`,
  `/api/playground/presets` and `/api/playground/conversations` returned a
  bare array; each now returns an object with one key (`providers`,
  `sessions`, `tokens`, `presets`, `conversations`).
- `DELETE /api/providers/{id}`, `/api/providers/{id}/keys/{keyId}` and
  `/api/models/{provider}/{model}/override` returned `200` with a body; all
  deletes are now `204`, and a delete of something absent is `404`.
- `PATCH` routes returned `{id}` or the changed keys; each now returns the
  updated resource.
- `GET /api/requests` rows gained `reasoning_tokens`; the trace gained
  `source` and `path`; `key_label` on an attempt is the credential's label
  rather than its id; `next_cursor` is omitted on a short page instead of
  always present.
- `GET .../override` omits unset fields instead of emitting `null`.
- `POST /api/catalog/sync` returns `202` immediately instead of `200` after
  the sync.
- Unknown JSON fields, a malformed `since_ms`/`until_ms`/`limit`/`days`, and
  the provider validation rules above are `400` where they were previously
  ignored.
- `Sec-Fetch-Site: none` no longer passes the same-origin check.
- `GET /readyz` was an unconditional `200`; `GET /metrics` exposed only the
  two log counters.
