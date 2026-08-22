# Phase 8 — Signed and Subscription Credentials

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3 for Bedrock and Vertex; phase 7 additionally for the OAuth connect flow.

---

## 1. Goal

Add the credential strategies whose difficulty is authentication rather than payload shape — AWS
SigV4, Google service-account JWT, and OAuth subscription accounts — plus the two adapter kinds that
come with them.

## 2. Scope boundary

**In:** `internal/adapter/bedrock`, `internal/adapter/vertex`, the `sigv4`, `gcp-sa`, and `oauth`
strategies in `internal/auth`, the token refresh worker, credential storage beyond static keys, and
the OAuth connect flow.

**Out:** anything requiring a driven browser session. Web-cookie providers are permanently out.

### 2.1 OAuth is a credential strategy, not an adapter kind

An earlier draft defined `oauthsub` as a sixth adapter kind. That was wrong: an adapter kind is
defined by payload shape, and a Claude subscription speaks Anthropic Messages while an OpenAI
subscription does not — so an "oauthsub adapter" could not say what `BuildRequest` emits.

Per master design §6.1, a preset declares a `kind` and an `auth.style` independently. OAuth composes
with `anthropic`, `openaicompat`, or `gemini`, and only the credential acquisition differs. One
practical consequence: an Anthropic-dialect request to an OAuth-backed Anthropic target remains
passthrough-eligible in phase 9, with the bearer token swapped in.

## 3. Bedrock

### 3.1 Converse, not InvokeModel

`InvokeModel` takes each model family's native payload — a Claude shape, a Llama shape, a Mistral
shape — reintroducing exactly the per-family branching this design exists to avoid. `Converse` and
`ConverseStream` take one unified message shape across families with tool use and image content, and
AWS documents this as the recommended choice. The IR maps to it directly.

Per-model feature support still varies: not every Converse model supports tools or images. That
capability data comes from models.dev and the preset, not from the API, and models Converse does not
serve at all are absent from the catalog for the provider.

### 3.2 Signing and transport

SigV4 via `aws-sdk-go-v2`'s standalone signer, applied to a request Darkrouter builds itself rather
than routing through the Bedrock service client, so one HTTP transport and one timeout policy cover
every adapter.

Two costs of that choice, both real:

The signature covers a payload hash, so the body must be fully materialized before signing. Bedrock is
therefore never passthrough-eligible.

**`ConverseStream` returns AWS binary eventstream framing (`application/vnd.amazon.eventstream`), not
SSE.** The adapter decodes it with `aws-sdk-go-v2`'s eventstream package rather than the shared SSE
reader. "One streaming implementation across every adapter" does not hold here, and pretending
otherwise would leave an implementer parsing binary frames with a line scanner.

Credentials come from the standard chain: explicit access key and secret on the provider row when
given, otherwise environment, shared config, or instance role. Explicit credentials are stored
encrypted like any other.

### 3.3 Model identifiers

Bedrock's region is an **endpoint property**, carried on the provider row — not part of the model
identifier. What carries a geo prefix (`us.`, `eu.`, `apac.`, `global.`) is the cross-region
**inference profile ID**, and newer models are increasingly invocable only through a profile rather
than a bare model ID.

This breaks naive discovery. `ListFoundationModels` — on the `bedrock` control-plane endpoint, not
`bedrock-runtime` — returns bare model IDs, many of which are not on-demand invocable. The invocable
profile IDs come from `ListInferenceProfiles`. Cataloguing only what the first call returns would
store precisely the identifiers that fail.

Discovery therefore calls both, catalogues profile IDs as the routable identifiers, and marks
non-invocable bare IDs as such. Darkrouter does not translate a friendly model name into a Bedrock
identifier; the alias points at what Bedrock actually calls it.

## 4. Vertex

### 4.1 Two request builders, selected by publisher

Vertex is one adapter kind that dispatches on the publisher recorded on the catalog entry:

| Publisher | Route | Payload |
|---|---|---|
| `publishers/google` | `:generateContent` / `:streamGenerateContent` | Gemini — reuses phase 4's translation |
| `publishers/anthropic` | `:rawPredict` / `:streamRawPredict` | **Anthropic Messages** — reuses phase 4's anthropic translation, with the model removed from the body into the URL and `anthropic_version: "vertex-2023-10-16"` injected |

An earlier draft claimed the Gemini payload covers both and "only transport and auth differ." That is
false for exactly the models that justify supporting two URL forms, and an implementer following it
would 400 on every Claude call.

Llama and Mistral MaaS on Vertex use a third, OpenAI-compatible route
(`endpoints/openapi/chat/completions`) and are out of scope for v1.

### 4.2 Authentication

A service-account JSON key exchanged for a short-lived access token via `golang.org/x/oauth2/google`,
which refreshes lazily when a token is inside its expiry delta. That satisfies the requirement that no
request fails on an expiry race; Darkrouter wraps the `TokenSource` so the behavior is testable rather
than assumed.

The service-account JSON is stored encrypted and never returned by the admin API. Project and location
live on the provider row and construct the endpoint.

### 4.3 No discovery

Vertex offers no practical API for listing which models a project may actually call — Model Garden
enumeration is noisy and partner entitlement is not cleanly queryable. Vertex catalog entries are
seeded from presets and models.dev filtered by the publishers the provider row declares, and the
credential probe confirms reachability of one model. Phase 6's discovery worker skips Vertex rather
than pretending.

## 5. OAuth subscription credentials

The highest-maintenance strategy, scoped accordingly: Darkrouter runs the flow, stores and refreshes
tokens, and reports when an account breaks. It does not emulate any client beyond what the flow
provides.

### 5.1 Connect flow

Authorization code with PKCE. Preset data supplies the authorize and token endpoints, client id,
scopes, and redirect-URI constraints, per master design §6.3.

**The redirect cannot generally target the admin port.** Subscription vendors register public-client
redirect URIs — typically `http://localhost:{port}/callback` or an out-of-band paste page — and a
homelab admin origin like `http://192.168.0.196:8081/api/oauth/callback` will be rejected at the
authorize step before any code exists. Two completion paths are therefore supported:

- **Manual paste.** The UI presents the authorize URL; the operator completes it in a browser and pastes the full redirected URL back into the UI. This is the default and always works.
- **Localhost listener.** Where a vendor's registered URI is a localhost callback and Darkrouter runs on the operator's own machine, a temporary listener on the registered port receives the redirect directly.

`state` is single-use, expiring, and validated against the initiating session on both paths; the code
exchange happens server-side with the stored PKCE verifier. `GET /api/oauth/callback` exists for the
listener path and is a state-changing GET reachable via a cross-site top-level redirect — which works
only because the session cookie is `SameSite=Lax`, making `state` validation the sole defense against
forced account binding. Admin-port access logging must never record query strings, since the
authorization code arrives in one.

### 5.2 Refresh

A background worker refreshes tokens ahead of expiry with jitter so a fleet does not refresh
simultaneously. A per-account mutex means concurrent requests finding an expired token wait for one
refresh rather than each starting their own; the credential probe shares that mutex, since a probe
that consumes a refresh would otherwise race the worker.

**Many vendors rotate the refresh token on every refresh**, some invalidating the old one
immediately. The new pair is therefore persisted before the old is considered replaced — a crash
between refresh and persist would otherwise brick the account until manual reconnection. Darkrouter is
single-instance by design; two instances sharing one account would trip rotation-reuse detection,
which some vendors treat as theft and respond to by revoking the grant entirely.

Refresh failures split into two outcomes, because treating them alike is wrong in both directions:

| Failure | Handling |
|---|---|
| `invalid_grant` or an equivalent terminal refusal | Disable the credential pending reconnection. **No retries** — hammering a refused refresh endpoint is how an account gets locked rather than recovered. |
| 5xx, timeout, network error from the token endpoint | Transient. Back off on the standard ladder and retry. |

The overview surfaces a disabled-pending-reconnect credential prominently, since only the operator can
resolve it.

### 5.3 Expectations

These credentials break more often than API keys, because they depend on endpoints their vendors never
promised to keep stable. A broken one cools, is routed around, and shows on the overview. It does not
take the gateway down and does not require code changes to disable — it is a credential row like any
other.

## 6. Credential health

`POST /api/providers/:id/test` extends to all three strategies, reporting what specifically failed —
signature, permission, expiry, or reachability — because "it doesn't work" is not actionable for any
of them.

- **SigV4** — a real `ListFoundationModels` call, which also exercises region and endpoint configuration.
- **Vertex** — a token exchange followed by a single-token generation against one catalogued model, since no listing exists.
- **OAuth** — a token refresh under the per-account mutex.

A successful probe resets the ladder for the probed credential, per phase 7 §4.3.

## 7. Testing

**SigV4** is tested against known-answer vectors: fixed request, fixed credentials, fixed timestamp,
fixed `Authorization` header. Canonicalization mistakes otherwise surface only as an opaque 403 from
a live call.

**Eventstream decoding** is tested against recorded `ConverseStream` frames, including a message split
across frame boundaries and a mid-stream exception frame.

**Vertex** tests use a fake token endpoint and cover the refresh delta, cache reuse inside it, a
token-exchange failure surfacing as a credential rather than a provider failure, and — most
importantly — that a `publishers/anthropic` model produces a `rawPredict` URL with an Anthropic body
carrying `anthropic_version`, while a `publishers/google` model produces `generateContent` with a
Gemini body.

**OAuth** tests run against a fake authorization server: state validation rejecting a mismatched
callback, state single-use, PKCE verification, the manual-paste path validating identically to the
listener path, refresh ahead of expiry, rotation persisted before the old token is dropped, an
`invalid_grant` disabling without retry, a 5xx taking the ladder, and concurrent requests plus a
probe triggering exactly one refresh.

**Storage** tests assert service-account JSON and refresh tokens round-trip and appear in no admin API
response.

**Golden files.** This phase adds `bedrock` and `vertex` rendered outputs — both publisher variants —
to phase 4's suite, which master design §15 requires to cover every adapter kind.

## 8. Done criteria

- A Bedrock provider serves a streaming completion with tool use through Converse, decoding eventstream framing, with signing verified against known-answer vectors.
- Bedrock discovery catalogues invocable inference profile IDs, not just bare model IDs.
- A Vertex provider serves both a Gemini model and a Claude model, each through its correct route and payload.
- An OAuth account connects through the manual-paste path, serves traffic, refreshes automatically with rotation handled safely, and shows as needing reconnection on `invalid_grant` without retrying.
- All three appear in the catalog and route through the same failover chain as static-key providers.
- No credential material appears in any admin API response or access log.
- `go test ./...` passes, golden files included.
