# Phase 3 — Routing and Failover

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 2.

---

## 1. Goal

Turn the single-candidate path into deterministic multi-candidate routing with real failover and
credential rotation. This is the phase that makes Darkrouter a gateway rather than a proxy.

## 2. Scope boundary

**In:** `internal/router` as a pure function, alias resolution, candidate filtering, credential
ordering, `internal/exec` and its attempt loop, outcome classification wired to the breaker, commit
semantics, diagnostic headers.

**Out:** dialects beyond OpenAI (phase 4), auxiliary surfaces (phase 5), the catalog (phase 6).

Capability metadata does not exist until phase 6. In this phase every model's capabilities are
`inferred`, and per master design §6.4 inferred capabilities pass the filter with a warning — so
capability filtering is wired and tested here but admits everything. Phase 6 supplies the data that
makes it selective.

## 3. The router is a pure function

```go
func Resolve(q Query, snap Snapshot) ([]Candidate, error)

type Snapshot struct {
    At        time.Time             // evaluation instant; the only "clock" the router sees
    Providers []provider.Provider   // enabled providers with their credentials
    Catalog   catalog.Reader        // model lookup; phase 6 supplies the real implementation
    Config    *config.Config
    Health    health.Availability   // resolved at snapshot construction, not re-derived
}

type Query struct {
    Model          string
    Surface        Surface
    NeedsTools     bool
    NeedsVision    bool
    NeedsReasoning bool
}

type Candidate struct {
    ProviderID string
    KeyID      string
    Model      string
    Kind       adapter.Kind
    Publisher  string   // vertex only
}

type Skip struct {
    ProviderID string
    KeyID      string
    Model      string
    Reason     SkipReason   // disabled | cooling | surface | capability | no_credential
}
```

Two corrections to an earlier draft are load-bearing here. The snapshot carries **providers and their
credentials**, because an earlier signature took only a model catalog and therefore could not produce
`Candidate.KeyID` from its own inputs. And it carries an **evaluation instant** with health already
resolved to availability booleans, because "no clock" is impossible while filtering on
`cooling_until`; reading `time.Now()` inside `Resolve` would destroy both purity and reproducibility.

`Resolve` returns the candidate list and, alongside it, the `Skip` records explaining every target
that did not make it. Both are persisted on the request row, because health tables are overwritten in
place and cannot be replayed after the fact.

`catalog.Reader` is a narrow interface this phase defines and phase 6 implements, so phase 3 does not
depend on a package that does not exist yet.

## 4. Model resolution

Tried in order, first match wins:

1. **Exact alias** — the name matches an `aliases` key. Expands to that alias's ordered target list.
2. **`provider/model`** — split on the **first** slash, matching only when the prefix names a configured provider. Model identifiers legitimately contain slashes (`meta-llama/Llama-3.3-70B-Instruct-Turbo`), so a non-matching prefix falls through to rule 3 with the full string intact.
3. **Bare name** — every enabled provider offering it, ordered by the provider `priority` column then `created_at`.

Ambiguity in rule 3 is the useful case, not an error: one model on three providers becomes a
three-provider chain for free, with a config-load warning naming it.

An unresolvable name returns a dialect-shaped model-not-found error without consuming an attempt.

## 5. Candidate filtering

A candidate survives when its provider is enabled, it has a credential neither credential-cooled nor
triple-cooled, its model declares the requested surface, and its capabilities satisfy the query.
Models whose `capabilities_source` is `inferred` pass with a warning rather than being excluded.

Zero surviving candidates returns a distinguishable Darkrouter error naming which case applies: no
provider offers the model, no provider offers it on this surface, or every provider offering it is
currently cooling. Those are different problems with different fixes and must not be conflated.

## 6. Credential ordering

**Credentials rotate before providers.** Draining the second key on Groq comes before falling back to
Cerebras, because that is the point of holding several free-tier keys.

Ordering is least-recently-used, with ties broken by key id so the result is total and deterministic.

`last_used_at` is **authoritative in memory**, updated synchronously under the health mutex at attempt
start, and persisted asynchronously purely for restart continuity. Reading it from SQLite would put
the hot path on the database; relying on the debounced persisted value would leave concurrent requests
seeing a stale timestamp for seconds — exactly the window where "drain several keys in parallel"
matters, and exactly when everything would pile onto one key.

Updating at attempt start rather than on success is deliberate: a credential that always 401s would
otherwise keep a stale timestamp and sort first forever.

## 7. The attempt loop

`internal/exec` drives:

1. Take the next candidate; skip it if live health now says unavailable.
2. Build the upstream request — adapter render, or phase 9's passthrough.
3. Send under a per-attempt deadline.
4. Classify the outcome.
5. Record health and append an attempt row.
6. Advance, skip, or finish per the outcome.

The ordered list is fixed at snapshot time and never re-ordered mid-request, but availability **is**
re-checked per attempt, because another request may have tripped a breaker in the meantime. A skip
for that reason is recorded on the attempt trace so the trace still explains the realized sequence.

The inbound body is read fully into memory before attempt 1, bounded by `server.max_body_bytes`,
because retrying requires replaying it. `http.Request.GetBody` is set so redirects and retries inside
the transport behave.

### 7.1 Deadlines

Pre-commit, an attempt is bounded by `policy.timeout.connect` plus `policy.timeout.first_byte`, and by
whatever remains of `policy.timeout.total`. Post-commit, `total` no longer applies and
`policy.timeout.idle` bounds the gap between events instead — a legitimate ten-minute reasoning
response must not be killed, while a provider that goes silent must be.

An attempt is only started when the remaining total is at least `connect + first_byte`. Beginning an
attempt that cannot possibly complete wastes both the budget and the provider's quota. When the budget
gate stops the chain, the client receives the last classified upstream error annotated as
attempts-exhausted-by-deadline, not a bare timeout.

Worst-case pre-commit silence is `max_attempts × first_byte`, which with the defaults is four minutes.
That is longer than some clients tolerate, and it is the reason `first_byte` defaults to 60s rather
than higher.

### 7.2 Outcome classification

Per master design §8.1, which is authoritative. Restated with this phase's mechanics:

| Outcome | Triggers | Advance behavior |
|---|---|---|
| `Success` | 2xx, and for streams only once committed | Finish. Reset that triple's ladder. |
| `RetryableProvider` | 408, 429, unlisted ≥500, timeout, connection reset or refused, DNS failure, TLS error, EOF before headers, HTTP/2 GOAWAY or RST_STREAM, 3xx | 429 → next credential of the same provider. Everything else → **skip the provider's remaining credentials**. |
| `RetryableCredential` | 401, 402, 403 | Next credential on the same provider, then advance. |
| `RetryableModel` | 404, or a 400 whose error body identifies an unknown model | Advance without penalizing the provider; increment a per-target counter. |
| `Fatal` | Unlisted 4xx, 413, 422, content filter | Return immediately. |
| `ClientCancelled` | `context.Canceled` caused by the inbound context | Stop; touch no health state. |

The default buckets matter as much as the listed codes: any unlisted transport-level error is
`RetryableProvider`, any unlisted status at or above 500 is `RetryableProvider`, and any other
unlisted 4xx is `Fatal`. Without stated defaults, DNS failures and TLS errors get bucketed differently
by different implementers.

Redirects are **not followed**. Go's client follows them by default and silently converts a POST into
a body-less GET; `CheckRedirect` returns `http.ErrUseLastResponse` and 3xx classifies as retryable.

`RetryableModel` incrementing a counter is what stops a permanently misconfigured base URL from being
skipped silently forever. A bare 404 from a wrong path looks identical to a model-not-found, so the
counter surfaces on the overview once it crosses a threshold. Some OpenAI-compatible providers report
unknown models as 400 with an identifying error code; that case maps to `RetryableModel` rather than
`Fatal`, or failover would die on the first provider that does not carry the model.

`Fatal` returning immediately is what stops one malformed client request from becoming a fleet-wide
burst of identical failures.

### 7.3 Commit

Per master design §8.2. The mechanics this phase owns:

Pre-commit events from the in-flight attempt are buffered and **replayed at commit**; events from
failed attempts are discarded. Without buffer-and-replay the client receives attempt one's
`message_start` followed by attempt two's content, or no `message_start` at all.

The buffer is bounded in bytes as well as by `first_byte` in time — a provider can emit megabytes of
pings inside sixty seconds — and a cap breach classifies as an attempt failure.

A 2xx whose stream fails before commit is classified from the **stream error**, not the status line.
Anthropic delivers `overloaded_error` as an in-stream event under a 200, and treating the 200 as
success would return an error body to the client with no failover attempted.

Unary responses commit after the full body is read and parsed; a read or parse failure before that is
`RetryableProvider`.

Post-commit failures become an error event inside the stream. The per-dialect shape of that event is
phase 4's responsibility, since OpenAI has no standard in-stream error and Gemini's SSE has no error
event type at all; phase 3 defines only that one is emitted and the stream then ends.

### 7.4 Retry safety

A pre-commit failover after the body was sent may mean the first provider already processed and
billed the prompt — a first-byte timeout while it was generating, a reset after processing, a 5xx
from a proxy in front of a provider that succeeded.

For chat this is an accepted gateway trade-off. It is recorded here rather than left implicit, along
with its accounting consequence: `request_attempts` carries no usage columns, so tokens burned by
failed attempts are invisible to `usage_daily`.

## 8. Diagnostic headers

Written on commit, and also on Darkrouter-originated error responses:

```
X-Darkrouter-Provider: groq
X-Darkrouter-Model:    llama-3.3-70b-versatile
X-Darkrouter-Attempts: 2
X-Darkrouter-Request:  01J...
```

On an error response the provider and model name the last attempted target, or are omitted when no
attempt was made.

## 9. Testing

Router tests are a table over snapshots asserting the exact candidate sequence and the exact skip
reasons. Because the function is pure and takes its evaluation instant as an input, these need no
fixtures, no network, and no database — including the time-dependent cooling cases.

Fake-fleet tests cover: first provider 429s and the second credential on the same provider succeeds;
first provider 500s and the loop skips its remaining credentials; a 401 rotates credentials but a 402
cools the credential across models; every candidate fails and the client receives the correct
distinguishable error; a 400 produces exactly one attempt; a provider stalls past `first_byte`; a 200
followed by an in-stream `overloaded_error` before commit fails over; a provider fails after commit
and the client receives an in-stream error rather than a second response; a client disconnects
mid-stream and no provider is penalized; a redirect is not followed.

Deadline tests assert the budget gate and that a committed stream survives past `total` while a silent
one is cut at `idle`.

A commit test asserts pre-commit events are replayed exactly once and that a ping-flood breaches the
byte cap rather than the time cap.

## 10. Done criteria

- Killing the first provider in an alias chain is invisible to the client, and the trace shows every attempt and every skip with reasons.
- Two credentials on one provider are both exercised on a 429, and both skipped on a 5xx.
- A malformed request produces exactly one attempt; an unknown model advances without penalizing anyone.
- A client disconnect leaves all providers healthy.
- The candidate list and skip reasons on the request row explain the ordering without needing live health.
- `go test ./...` passes.
