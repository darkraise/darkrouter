# Execution, routing and health

## Resolution is pure

```go
func Resolve(q Query, snap Snapshot) ([]Candidate, []Skip, error)
```

`Resolve` reads no clock, no database and no network. The evaluation instant
and the resolved availability arrive inside the snapshot. This is what lets
the console's route preview share the executor's own `RouteSnapshot` and the
router's own `Resolve`, so a preview and a request cannot drift.

A `Candidate` names a provider, credential, model, kind and publisher, and
carries `Inferred` when it was admitted on guessed capability metadata.

## Model resolution

Three rules, in order:

1. **An exact alias.** Alias targets expand through rules 2 and 3 only —
   aliases never nest.
2. **`provider/model`**, split on the *first* slash, and only when the prefix
   names a configured provider.
3. **A bare name**, matched across the fleet ordered by `priority DESC, id`.

## Filtering, and why the order matters

A candidate is dropped by the first rule that rejects it, and that rule's name
is what the trace records:

`disabled` → `surface` → `removed_upstream` → `unsanctioned` →
`adapter_surface` → `capability` → `no_credential` → `cooling`

Durable configuration problems are reported ahead of transient ones, so an
operator sees "this model is not configured for that surface" rather than
"everything is cooling".

A keyless provider gets a synthetic credential with an empty key id, and every
downstream key — breaker, recency, trace — is keyed on that empty string. The
authentication style is read from the provider row, not the preset: an
operator who overrode it is the authority on their own endpoint.

When nothing survives, one of five sentinels is returned:
`ErrModelNotFound`, `ErrSurfaceUnsupported`, `ErrAllCooling`,
`ErrCapabilityUnsatisfied`, `ErrUnsanctionedFree`. "Cooling" is reported only
when cooling explains *everything*; a mixed result is a configuration problem
wearing a health problem's clothes.

## The attempt loop

The candidate list is fixed at snapshot time and is never reordered. Only
skipping is dynamic: a candidate that started cooling between resolution and
its turn is recorded as `cooling` on the trace.

An attempt that dies before reaching the provider — no credential, nothing to
render — still writes a trace row but consumes no attempt budget.

### Outcome classification

| Signal | Outcome |
|---|---|
| Transport error, nil response, 408, 429, 3xx, ≥500 | retryable provider |
| 401, 402, 403 | retryable credential |
| 404 | retryable model |
| Any other 4xx | fatal |
| Client disconnect before commit | client cancelled |

Redirects are never followed — a redirecting provider endpoint is
misconfigured, and following it would send the credential to whatever host it
names.

An ambiguous 400 is re-read under a bounded cap and reclassified as
*retryable model* when the error names an unknown model. A content filter is
**fatal**, not retryable: a refusal is the provider answering, not the
provider being broken.

### Advancement

- **Success** finishes. **Fatal** and **client-cancelled** return.
- **Credential** and **model** failures step one candidate.
- A **provider** failure skips every remaining candidate on that provider —
  the next credential hits the same dead upstream.
- Except a **429**, which steps to the next credential of the same provider —
  rate limits are usually per credential.

That distinction is the whole reason the credential and provider outcomes are
separate; collapsing them makes the fleet model meaningless.

### The commit point

Exactly one rule, enforced identically on the translated path and both raw
recognisers: **only a delta carrying non-empty text, thinking, or tool-input
JSON commits.** A `content_block_start` does not. A tool-call delta carrying
only a name does not.

Once committed, failover stops and no second set of headers is written. The
executor trusts the response writer, not an operation's returned outcome: a
status-line write, a non-empty body write or a flush is the commit; a
zero-length write is not.

A stream that ends cleanly having emitted nothing is a **success**. Failing
over there would burn the whole chain every time a model stops immediately.

## Timeouts

Before commit an attempt is bounded by `connect + first_byte`, and the whole
request by `total`. An attempt starts only if the remaining total covers a
full per-attempt bound; when it does not, the last upstream error is annotated
rather than replaced by a bare timeout.

After commit the bound switches to `idle`. This is the reason the deadline is
a timer rather than a context deadline — the bound *changes* at commit. A
legitimate ten-minute reasoning response must not be killed while a silent
provider still is.

Darkrouter's own timeout is checked before the client-disconnect signal,
because both cancel the same context and checking the disconnect first would
report a provider timeout as a client hang-up.

## Health

The breaker is keyed on `{provider, credential, model}`. An empty model is the
credential-level entry, checked first and independently, gating every model
that credential serves.

The cooldown ladder is 1s, 2s, 4s, 8s, 15s, 30s, 60s, 120s, then doubling,
clamped to `policy.cooldown.max`. A single 5xx does not cool — it counts
toward `trip_after`. An upstream `Retry-After` on a retryable provider outcome
is honoured exactly, capped at the same maximum, and that entry is not probed.

Success deletes both the triple entry and the credential-level entry. A fatal
outcome deletes the triple entry and releases the credential-level probe. A
`RetryableCredential` never resets the ladder: otherwise a billing-exhausted
key is resurrected by any malformed request.

`Available` **mutates** — calling it claims the half-open probe. The router
therefore never touches the breaker; it reads a frozen availability map
computed alongside the snapshot, so a probe is never burned on a candidate the
router may never reach.

Every attempt emits exactly one health signal, and the first caller wins. A
2xx is not recorded from the status line: success is reported once the body is
read or the stream commits, because the loop claimed the probe on the way in
and an exit that skipped the recorder would leave the entry shut forever with
nothing testing it.

## The fast path

An eligible request is forwarded to the provider without re-rendering. It is
eligible only when the surface is `llm`, the inbound dialect and target kind
pair, the adapter implements `Forwarder`, and — for Gemini — the stream is
`alt=sse` rather than the array form, whose event boundaries the recogniser
cannot find.

Three body mutations are permitted: rewriting the model name, injecting
`stream_options: {include_usage: true}` for `openaicompat` targets, and
stripping the usage chunk that injection produces. A client that set
`include_usage: false` itself is left alone.

Rewriting is skipped entirely when the model names already match, and the
encoder disables HTML escaping, because `json.Marshal` would otherwise rewrite
`<`, `>` and `&` in a body advertised as forwarded unchanged.

A post-commit scanner overflow forwards the unread remainder verbatim, which
is the one route by which a fourth mutation — the un-stripped usage chunk —
can reach a client. Never corrupting bytes on an already-degraded path is the
deliberate trade.
