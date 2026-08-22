# Phase 3 — Routing and Failover

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 2.

---

## 1. Goal

Turn the single-candidate path into deterministic multi-candidate routing with real failover and key
rotation. This is the phase that makes Darkrouter a gateway rather than a proxy.

## 2. Scope boundary

**In:** `internal/router` as a pure function, alias resolution, candidate filtering, key ordering,
`internal/exec` and its attempt loop, outcome classification wired to the breaker, streaming commit
semantics, diagnostic response headers.

**Out:** dialects beyond OpenAI (phase 4), auxiliary surfaces (phase 5), the catalog that will
eventually supply capability metadata (phase 6 — this phase filters on what the provider source
already declares).

## 3. The router is a pure function

```go
func Resolve(req Query, cat catalog.Snapshot, cfg *config.Config, h health.Snapshot) ([]Candidate, error)
```

No I/O, no clock, no randomness. Given the same four inputs it returns the same ordered list. Every
routing question — why was that provider chosen, why was this one skipped — is answerable by
replaying the function against the recorded snapshots.

```go
type Query struct {
    Model      string
    Surface    Surface   // llm in this phase; phase 5 widens it
    NeedsTools bool
    NeedsVision bool
    NeedsReasoning bool
}

type Candidate struct {
    ProviderID string
    KeyID      string
    Model      string
    Kind       adapter.Kind
}
```

Determinism is not an aesthetic preference. It is what makes the phase 7 trace view worth building: a
nondeterministic router turns every trace into a story about that one request instead of an
explanation of the system.

## 4. Model resolution

Tried in order, first match wins:

1. **Exact alias** — the name matches an `aliases` key in `darkrouter.yaml`. Expands to that alias's ordered target list.
2. **`provider/model`** — an explicit single target. Still expands to multiple candidates when the provider holds several keys.
3. **Bare model name** — resolved against every enabled provider that offers it, ordered by the provider `priority` column and then by declaration order.

Ambiguity in case 3 is not an error. It is the useful case: `llama-3.3-70b` offered by three providers
becomes a three-provider fallback chain for free. A config-load warning names the ambiguity so the
resolution is never a surprise.

An unresolvable name returns a dialect-shaped model-not-found error without consuming an attempt.

## 5. Candidate filtering

A candidate survives when its provider is enabled, its key is not cooling, its `(provider, key,
model)` triple is not cooling, and the model's declared capabilities satisfy the query — tools require
tool support, image content requires vision, a reasoning budget requires a reasoning-capable model.

A request that filters down to zero candidates returns a distinguishable Darkrouter error naming why:
no provider offers the model, versus every provider that offers it is currently cooling. Those are
different problems and the error must not conflate them.

## 6. Key ordering within a provider

**Keys rotate before providers.** Draining the second key on Groq comes before falling back to
Cerebras, because that is the whole point of holding several free-tier keys.

Ordering is least-recently-used by the persisted `provider_keys.last_used_at`, so a restart does not
reset every provider to its first key and re-concentrate load. LRU is deterministic given the snapshot
— the timestamps are inputs, not a live clock read.

## 7. The attempt loop

`internal/exec` drives:

1. Take the next candidate.
2. Build the upstream request (adapter, or phase 9's passthrough).
3. Send under a per-attempt deadline derived from the request deadline.
4. Classify the outcome.
5. Record health and append an attempt row.
6. On a retryable outcome, advance; otherwise finish.

Bounded by `policy.retry.max_attempts` and by `policy.timeout.total` on the request context. The
attempt budget must be checked against the remaining request deadline before starting an attempt —
beginning a 60-second attempt with 5 seconds of budget left wastes both the budget and a provider's
quota.

### 7.1 Outcome classification

| Outcome | Triggers | Behavior |
|---|---|---|
| `Success` | 2xx | Commit. Reset that key's ladder. |
| `RetryableProvider` | 408, 429, 5xx, timeout, connection reset | Cool the triple, advance to the next candidate. |
| `RetryableCredential` | 401, 402, 403 | Cool this key only. Try the next key on the same provider before advancing. |
| `RetryableModel` | 404 model not found | Advance without penalizing the provider — the provider is healthy, it just does not serve that name. |
| `Fatal` | 400, 422, content policy | Return immediately. Do not burn further candidates. |
| `ClientCancelled` | inbound context cancelled | Stop. Record it, but do not penalize the provider. |

`Fatal` returning immediately matters: a malformed request will be rejected by every provider, and
retrying it turns one client error into a fleet-wide burst of identical failures.

`ClientCancelled` must be distinguished from a timeout. Marking a provider unhealthy because a user
pressed Ctrl-C is a self-inflicted outage.

### 7.2 Streaming commit

Failover is possible only before the first byte reaches the client.

The executor holds the response until the first upstream **content** event arrives, then commits: the
status line and headers are written and the stream is forwarded. Before commit, a failure advances to
the next candidate invisibly. After commit, a failure is emitted as an error event within the stream
in the inbound dialect's shape.

The first-byte wait is bounded by `policy.timeout.first_byte`, so a provider that accepts a connection
and then goes silent is treated as a failure rather than hanging the client.

Provider keepalives and empty role-only deltas do not count as content and do not trigger commit.
Committing on a keepalive would forfeit failover for nothing.

## 8. Diagnostic headers

Written on commit, so a terminal user sees routing without opening the UI:

```
X-Darkrouter-Provider: groq
X-Darkrouter-Model:    llama-3.3-70b-versatile
X-Darkrouter-Attempts: 2
X-Darkrouter-Request:  <request id>
```

The request id links a terminal session directly to its trace in the UI.

## 9. Testing

Router tests are a table over (config, catalog, health) inputs asserting the exact candidate sequence.
Because the function is pure, these need no fixtures, no network, and no database.

The fake provider fleet over `httptest` covers: first provider 429s and the second succeeds; the first
key 401s and the second key on the same provider succeeds, proving key-before-provider ordering; every
candidate fails and the client receives a Darkrouter error naming the reason; a 400 returns
immediately without a second attempt; a provider stalls past `first_byte` and failover proceeds; a
provider fails after commit and the client receives an in-stream error rather than a second response;
a client disconnects mid-stream and no provider is penalized.

An attempt-budget test asserts that an attempt is not started when the remaining request deadline
cannot accommodate it.

## 10. Done criteria

- Killing the first provider in an alias chain is invisible to the client, and the trace shows both attempts with outcomes.
- Two keys on one provider are both exercised before the next provider is tried.
- A malformed request produces exactly one attempt.
- A client disconnect leaves all providers healthy.
- Candidate ordering is reproducible from the recorded snapshots.
- `go test ./...` passes.
