# Functional requirements

Identifiers are stable and never reused. `plan/status.md` says which are met.

## Proxy surfaces — `FR-PRX`

| id | Requirement |
|---|---|
| FR-PRX-1 | Accept OpenAI chat completions, and translate to any configured provider kind. |
| FR-PRX-2 | Accept Anthropic Messages, and translate to any configured provider kind. |
| FR-PRX-3 | Accept Gemini `generateContent`, `streamGenerateContent` and `countTokens`. |
| FR-PRX-4 | Accept the OpenAI Responses API in its stateless form, and refuse its stateful form with an explicit error. |
| FR-PRX-5 | Serve seven surfaces: `llm`, `embedding`, `image`, `tts`, `stt`, `rerank`, `moderation`. |
| FR-PRX-6 | Stream every surface that the inbound dialect streams, in that dialect's own event shape. |
| FR-PRX-7 | Preserve a request's semantics across translation, and warn on the request record whenever a field cannot be expressed against the target. |
| FR-PRX-8 | Forward an eligible request to its provider without re-rendering it, so a body Darkrouter does not model reaches the provider intact. |
| FR-PRX-9 | Report token counts natively where the provider offers a counting endpoint, and mark a local estimate as estimated. |
| FR-PRX-10 | Authenticate inbound requests by shared secret or per-client token, accepting each dialect's native credential form. |

## Routing and failover — `FR-RTE`

| id | Requirement |
|---|---|
| FR-RTE-1 | Resolve a requested model name through an alias chain, a `provider/model` pair, or a bare name matched across the fleet in priority order. |
| FR-RTE-2 | Produce an ordered candidate list from a point-in-time snapshot, with no clock, database or network read during resolution. |
| FR-RTE-3 | Attempt candidates in order until one commits a response or the attempt budget is exhausted. |
| FR-RTE-4 | Classify every attempt outcome into one of: success, retryable provider, retryable credential, retryable model, fatal, client-cancelled. |
| FR-RTE-5 | Advance differently by outcome: a credential or model failure steps one candidate; a provider failure skips that provider's remaining candidates; a rate limit steps to the next credential of the same provider. |
| FR-RTE-6 | Stop failing over once a response has committed to the client, and never emit a second set of headers. |
| FR-RTE-7 | Cool a failing target on an escalating ladder, and honour an upstream `Retry-After` where one is supplied. |
| FR-RTE-8 | Record every skipped candidate with the reason it was skipped. |
| FR-RTE-9 | Return a distinguishable error when no candidate survives, naming which case applied. |
| FR-RTE-10 | Bound a request by connect, first-byte and total timeouts before commit, and by an idle timeout after it. |

## Catalogue and providers — `FR-CAT`

| id | Requirement |
|---|---|
| FR-CAT-1 | Ship a preset catalogue covering common providers, so a provider can be added without hand-writing an endpoint. |
| FR-CAT-2 | Merge model metadata from presets, an upstream index, live discovery and operator overrides, with a defined precedence per field. |
| FR-CAT-3 | Discover each provider's live model list on a schedule and on change. |
| FR-CAT-4 | Mark a model stale after repeated probe failures, and not-routable after it is repeatedly absent from a successful listing. |
| FR-CAT-5 | Route a model whose capabilities are inferred rather than known, with a warning naming the inference. |
| FR-CAT-6 | Carry per-model pricing with a grade recording how it was established. |
| FR-CAT-7 | Carry each model's free-tier terms, and refuse to route a free tier the vendor has not sanctioned unless the operator opts that provider in. |
| FR-CAT-8 | Support five provider kinds, with authentication as an orthogonal dimension composed with the kind. |

## Credentials — `FR-CRD`

| id | Requirement |
|---|---|
| FR-CRD-1 | Hold several credentials per provider and fail over between them. |
| FR-CRD-2 | Encrypt every credential at rest under an operator-supplied master key. |
| FR-CRD-3 | Never return credential material from any endpoint; expose a label and a masked suffix only. |
| FR-CRD-4 | Support signed credentials (AWS SigV4, Google service accounts) and OAuth subscription credentials, refreshing them before expiry. |
| FR-CRD-5 | Re-key the store on demand without losing any credential. |

## Admin API — `FR-ADM`

| id | Requirement |
|---|---|
| FR-ADM-1 | Serve the console and its API on a listener separate from the proxy. |
| FR-ADM-2 | Authenticate the operator by password, and hold the session in a cookie that is never valid on the proxy port. |
| FR-ADM-3 | Require a session-bound CSRF token and a same-origin check on every mutating request. |
| FR-ADM-4 | Expose provider, credential, alias, policy, override and configuration management. |
| FR-ADM-5 | Expose request history, per-request traces, usage rollups and health. |
| FR-ADM-6 | Preview a route without sending a request, agreeing exactly with what the router would resolve. |
| FR-ADM-7 | Run a request from the console against a chosen provider and model, on any supported surface. |
| FR-ADM-8 | Refuse a configuration write that names a restart-only field, and warn — rather than refuse — when a file reload changes one. |
| FR-ADM-9 | Serve unauthenticated liveness, readiness and metrics endpoints. |

## Console — `FR-CON`

| id | Requirement |
|---|---|
| FR-CON-1 | Present nine destinations: Overview, Requests, Usage, Providers, Models, Routing, Playground, Connect, Settings. |
| FR-CON-2 | Reach any failover trace within three clicks of the overview. |
| FR-CON-3 | Explain every attempt and every skipped candidate in a trace, distinguishably without colour. |
| FR-CON-4 | Add a provider and its credential, and probe it, without editing a file. |
| FR-CON-5 | Create, reorder and validate an alias chain in the browser, effective on the next request. |
| FR-CON-6 | Teach rather than show empty grids on a fresh install, and explain itself when no password is set. |
| FR-CON-7 | Render legibly in both light and dark mode, and honour the operator's font-size setting. |

## Observability — `FR-OBS`

| id | Requirement |
|---|---|
| FR-OBS-1 | Record every request with its outcome, timing, tokens, cost and serving path. |
| FR-OBS-2 | Record every attempt within a request, including failed ones, with its own tokens and cost. |
| FR-OBS-3 | Attribute a failed attempt's spend to the provider that burned it. |
| FR-OBS-4 | Roll usage up daily by provider, model and alias, idempotently. |
| FR-OBS-5 | Expire request history on a configured retention, with a floor that keeps the rollup correct. |
| FR-OBS-6 | Never block or fail a request because logging is slow; count what is dropped and expose the count. |
| FR-OBS-7 | Optionally capture request and response bodies, off by default, with their own retention. |
