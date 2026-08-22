# Phase 8 — Signed and Subscription Providers

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3. Benefits from phase 7 for the OAuth connect flow.

---

## 1. Goal

Add the three provider kinds whose difficulty is authentication rather than payload shape: AWS
Bedrock, Google Vertex, and OAuth subscription accounts.

## 2. Scope boundary

**In:** `internal/adapter/bedrock`, `internal/adapter/vertex`, `internal/adapter/oauthsub`, the token
refresh worker, encrypted credential storage beyond plain API keys, and credential expiry alerting.

**Out:** anything that requires driving a browser session. Web-cookie providers are permanently out
per master design §2.

## 3. Bedrock

### 3.1 Use the Converse API, not InvokeModel

`InvokeModel` takes each model family's native payload, which would mean a Claude shape, a Llama
shape, a Mistral shape, and so on — reintroducing exactly the per-provider branching this design
exists to avoid.

`Converse` and `ConverseStream` take one unified message shape across families, with tool use and
image content. The IR maps to it directly, so Bedrock costs one adapter rather than one per family.
Models that Converse does not support are simply absent from the catalog for this provider.

### 3.2 Signing

SigV4 via `aws-sdk-go-v2`'s standalone signer, applied to a request Darkrouter builds itself, rather
than routing through the Bedrock service client. This keeps one HTTP transport, one timeout policy,
and one streaming implementation across every adapter.

Credentials come from the standard chain: explicit access key and secret on the provider row when
given, otherwise environment, shared config file, or instance role. Explicit credentials are stored
encrypted like any other key.

The signature covers the payload hash, so the request body must be fully materialized before signing —
a genuine difference from the streaming-friendly path elsewhere, and a reason Bedrock is never
passthrough-eligible in phase 9.

### 3.3 Model identifiers

Bedrock model ids are region-qualified and increasingly require an inference profile prefix rather
than a bare model id. The provider row carries the region, and the catalog stores the full identifier
as discovered. Darkrouter does not attempt to translate a bare model name into a Bedrock id — the
alias points at what Bedrock actually calls it.

## 4. Vertex

Authentication is a service-account JSON key exchanged for a short-lived OAuth2 access token via
`golang.org/x/oauth2/google`. Tokens are cached in memory with a refresh margin — refreshed before
expiry rather than on failure, so no request ever pays for a token exchange it could have avoided or
fails on an expiry race.

The service-account JSON is stored encrypted like any other credential, and is never returned by the
admin API.

Endpoint construction needs the project id and location from the provider row, and the URL differs
between Google's own models and third-party ones published on Vertex. Both forms are supported and
recorded on the catalog entry, because guessing wrong produces a 404 that reads like a missing model.

Payload shape is Gemini's, so the phase 4 `gemini` adapter's translation is reused; only the transport
and auth differ. The adapter is thin by design.

## 5. OAuth subscription accounts

The highest-maintenance kind, and scoped accordingly: Darkrouter supports the OAuth flow, stores and
refreshes tokens, and reports when an account breaks. It does not attempt to emulate any client
beyond what the flow provides.

### 5.1 Connect flow

An authorization-code flow with PKCE, initiated from the admin UI and returning to a callback route on
the admin port. This phase adds the only endpoints beyond phase 7's eighteen —
`POST /api/providers/:id/oauth/start` and `GET /api/oauth/callback` — because a redirect flow cannot
be expressed through the existing CRUD surface. The resulting access and refresh tokens are stored encrypted in `provider_keys`
alongside their expiry and scope.

The callback route is on the admin port, behind the session, and validates the `state` parameter
against the initiating session. An unauthenticated callback that accepts arbitrary tokens is a
credential-injection hole.

### 5.2 Refresh

A background worker refreshes tokens ahead of expiry with jitter, so a fleet of accounts does not
refresh simultaneously.

**A refresh failure marks the account unavailable rather than retrying hot.** Repeatedly hammering a
refused refresh endpoint is how an account gets locked rather than recovered. The account is cooled
with the same ladder as any other failure, the failure is recorded, and phase 7's overview shows it as
needing reconnection.

Token refresh races are avoided by a per-account mutex: concurrent requests finding an expired token
wait for one refresh rather than each starting their own.

### 5.3 Expectation setting

These providers break more often than API-key ones, because they depend on endpoints their vendors
never promised to keep stable. The design accommodates that: a broken OAuth provider cools and is
routed around, and the operator sees it on the overview. It does not take the gateway down and it does
not require code changes to disable — it is a provider row like any other.

## 6. Credential health

`POST /api/providers/:id/test` from phase 7 extends to these kinds: SigV4 signing is validated by a
real listing call, Vertex by a token exchange plus a listing call, and OAuth by a token refresh. Each
reports what specifically failed — signature, permission, expiry, or reachability — because "it
doesn't work" is not actionable for any of the three.

## 7. Testing

Bedrock signing is tested against known-answer vectors: a fixed request, fixed credentials, and a
fixed timestamp must produce a fixed `Authorization` header. This catches canonicalization mistakes
that a live call would only surface as an opaque 403.

Vertex tests use a fake token endpoint and assert the refresh margin is honored, the cache is used
within the margin, and a token-exchange failure surfaces as a credential failure rather than a
provider failure.

OAuth tests cover the full flow against a fake authorization server: state validation rejecting a
mismatched callback, PKCE verification, refresh ahead of expiry, a refused refresh marking the account
unavailable without a retry storm, and concurrent requests triggering exactly one refresh.

Encrypted-storage tests assert that service-account JSON and refresh tokens round-trip and are never
present in any admin API response.

## 8. Done criteria

- A Bedrock provider serves a streaming completion with tool use through the Converse API, with signing verified against known-answer vectors.
- A Vertex provider serves a completion using a service-account key, refreshing its token ahead of expiry without a failed request.
- An OAuth subscription account connects through the UI, serves traffic, refreshes automatically, and shows as needing reconnection when its refresh is refused.
- All three appear in the catalog with correct model lists and route through the same failover chain as API-key providers.
- No credential material appears in any admin API response.
- `go test ./...` passes.
