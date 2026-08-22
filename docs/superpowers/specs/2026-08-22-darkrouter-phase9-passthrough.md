# Phase 9 — Passthrough Fast Path

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phases 3, 4, and 6.

---

## 1. Goal

When the inbound dialect already matches the chosen target's dialect, stop re-rendering. Rewrite the
model identifier, swap auth, forward the body, and pipe the response while extracting usage inline.

This is the last phase deliberately. The IR path is the correctness baseline, and the fast path is
validated by proving it agrees with that baseline.

## 2. Scope boundary

**In:** the eligibility predicate's mechanics, request rewriting, header handling, the raw-stream
commit recognizer, inline usage extraction, fallback to the IR path, and differential testing.

**Out:** any change to routing, health, or logging semantics. Passthrough is an optimization inside
the executor, not a second pipeline.

## 3. Why this is worth building

**Fidelity.** Claude Code against a real Anthropic provider is the most common route in this homelab,
and passthrough makes it near-byte-faithful. A parameter Anthropic shipped last week, a beta header,
an experimental block type — all survive. On the IR path they are dropped with a warning, which is
honest but still a loss.

**Latency.** Every request is parsed to IR regardless, because routing needs to know whether it
carries tools, images, or a reasoning budget. Passthrough therefore saves the *render* and the
per-event re-serialization, not the parse. That is a real but modest saving, and the fidelity
argument is the stronger one.

## 4. Eligibility

Master design §4.1 is authoritative. This phase adds only the mechanics.

Eligibility is evaluated **per attempt**. An Anthropic-inbound request may pass through to Anthropic
on attempt one and translate to `openaicompat` on attempt two.

An OAuth-backed credential does not disqualify passthrough. OAuth is an auth strategy, not a payload
shape, so an OAuth-backed Anthropic target is eligible with the bearer token swapped in — which
matters, because a Claude subscription serving Claude Code is one of the routes this phase exists to
optimize.

Bedrock is permanently ineligible: SigV4 signs a payload hash, so the body must be materialized and
re-signed. Vertex is ineligible because its URL encodes both publisher and model.

## 5. Request rewriting

### 5.1 The model identifier

Two forms, by dialect.

**Body-carried** (OpenAI, Anthropic): decode into `map[string]json.RawMessage`, replace `model`, and
re-encode with an `json.Encoder` configured `SetEscapeHTML(false)`.

The guarantee is **semantic preservation, not byte preservation**. An earlier draft claimed values
survive byte-for-byte because they are never decoded; that is false. `json.Marshal` HTML-escapes `<`,
`>`, and `&` inside `RawMessage` values by default — silently rewriting prompt text — compacts
whitespace, reorders top-level keys, and collapses duplicate top-level keys to the last occurrence.
Disabling HTML escaping removes the only consequential one; the rest are semantically equivalent JSON
that no provider distinguishes.

When the requested model name already equals the target's name for it — the common Claude Code case —
**the rewrite is skipped entirely** and the body is forwarded untouched. That is the only path that
achieves true byte fidelity, and it is also the most travelled one.

**URL-carried** (Gemini): the model lives in the path
(`/v1beta/models/{model}:generateContent`), not the body. Passthrough rewrites the path segment and
preserves the method suffix and `?alt=sse`, forwarding the body completely untouched. An earlier
draft listed Gemini as eligible while also requiring a top-level body `model` field, which excluded
every Gemini request or invited an implementer to inject a field that does not belong there.

### 5.2 `stream_options` injection

Per master design §4.2, the third permitted mutation: for `openaicompat` targets on a streaming
request, inject `stream_options: {"include_usage": true}` when absent, then **strip the resulting
extra final usage chunk** from the response before it reaches the client.

Without this, OpenAI-compatible providers emit no stream usage unless the client asked, and token
accounting would be null on the most-travelled route — undermining a pillar of the product. Stripping
the extra chunk keeps the client's view identical to a direct call.

A preset declaring the `rejects-stream-options` quirk makes its provider ineligible for streaming
passthrough; those requests take the IR path.

### 5.3 Headers

Inbound auth is stripped and replaced with the target's. Gemini's `?key=` query credential is stripped
from the rewritten URL, not merely overridden. Hop-by-hop headers are dropped per RFC 9110, and
`Content-Length` is recomputed after any rewrite.

Forwarded allowlist: `content-type`, `accept`, `user-agent`, `anthropic-version`, `anthropic-beta`,
`openai-beta`. Everything else inbound is dropped, so a client cannot inject headers into Darkrouter's
upstream call.

`content-type` was missing from an earlier draft's list, which would have sent every upstream request
without one — rejected by many providers. `openai-beta` was missing too, which broke this phase's own
fidelity argument for exactly half its clients.

### 5.4 Request body encoding

An inbound `Content-Encoding` other than identity is rejected with a dialect-shaped 415: decoding it
to rewrite the model and re-encoding it defeats the point, and forwarding it unmodified is impossible
when the model must change. Inbound bodies are bounded by `server.max_body_bytes`, exceeded bodies
returning 413 before any upstream connection.

## 6. The raw-stream recognizer

Passthrough does not parse into IR, but phase 3's commit rule still applies. The fast path therefore
needs its own minimal per-dialect SSE recognizer, scanning the forwarded byte stream for three things.

**Commit trigger.** The first content-bearing event, using phase 3's definition: text, thinking, or
tool-input content. Per dialect that is `content_block_start`/`content_block_delta` for Anthropic, the
first `choices[].delta` carrying `content`, `reasoning_content`, or `tool_calls` for OpenAI, and the
first `candidates[].content.parts` entry for Gemini. Pings, comments, `message_start`, and role-only
deltas do not trigger commit.

**Pre-commit errors.** Anthropic delivers `overloaded_error` as an `error` SSE event under a 200;
OpenAI-compatible providers conventionally emit `data: {"error": ...}`. Detected before commit, these
classify as `RetryableProvider` and fail over. Detected after, they pass through.

**Pre-commit buffering.** Raw bytes preceding the commit trigger are buffered and replayed verbatim at
commit, bounded by the byte cap from phase 3 §7.3.

The recognizer reads SSE structure only — event boundaries and the presence of specific keys — and
never reconstructs IR. It tolerates lines split across read boundaries, ignores comment lines, and on
encountering anything it cannot parse it stops recognizing and simply forwards, because after commit
its opinion no longer matters.

## 7. Usage extraction

Accounting still needs token counts, so the same scan that recognizes commit also watches for usage.

**Streaming.** The scanner runs **inline in the forwarding loop** — read a chunk, scan it, write it —
not in a goroutine behind a pipe. A `TeeReader` into a piped goroutine is the classic stall bug: if
the scanner falls behind or exits, the pipe write blocks and the client stream freezes. Inline scanning
has no concurrency and cannot stall. Any scanner error is recorded on the request row and otherwise
ignored: losing a token count is acceptable, corrupting a response is not.

Per dialect:

| Dialect | Usage source |
|---|---|
| OpenAI | `usage` on the final chunk, present because of §5.2 |
| Anthropic | **both** `message_start.message.usage` for input and cache tokens, and `message_delta.usage` for output tokens |
| Gemini | `usageMetadata`, taking the last chunk's value as authoritative |

Anthropic's split matters: an earlier draft watched only `message_delta`, which records output tokens
alone and computes wrong cost on every cached or long-prompt request.

**Non-streaming.** Usage sits at the *end* of an OpenAI unary body, so a size-capped prefix buffer
truncates precisely what is needed on the longest completions — the ones where cost matters most. The
scan therefore keeps a bounded **tail** buffer, or streams the body through a decoder looking for the
usage object, rather than capping a prefix.

When usage is genuinely unavailable, the request logs null tokens with cost marked unknown. An
estimate silently mixed into real accounting makes the whole ledger untrustworthy.

## 8. Response handling

Upstream response headers are not copied wholesale. Go's transport transparently negotiates and
decompresses gzip when the request carried no explicit `Accept-Encoding` — which the §5.3 allowlist
ensures — so copying `Content-Encoding` and `Content-Length` through would label decompressed bytes as
gzip or state a wrong length.

The transport runs with `DisableCompression` set, so bytes arrive as the provider sent them and are
forwarded unchanged. Hop-by-hop headers are dropped; `Content-Type` and dialect-meaningful headers
pass through; Darkrouter's own diagnostic headers are added at commit. SSE responses are flushed per
event.

## 9. Failure handling

Before commit, a failed passthrough attempt behaves exactly like an IR attempt: classify, record
health, advance. The next candidate is evaluated for eligibility independently.

Two fallbacks to the IR path, both for the **same** candidate:

- **Rewrite failure** — malformed JSON, no top-level `model` where one is expected. The IR parser produces a proper dialect-shaped error if the body is genuinely invalid.
- **A pre-commit 400.** Strict `openaicompat` providers reject fields the IR path would have dropped with a warning. Classifying that as `Fatal` would convert a request the IR path could have served into a hard failure with no failover — a silent regression introduced by an optimization. The same candidate is retried once through the IR path before any `Fatal` classification stands, and the trace records both attempts.

After commit, phase 3's rule stands: a failure becomes an in-stream error event.

## 10. Differential testing

The core of this phase's verification. A corpus runs through both paths against the same fake
provider, comparing three things.

**Upstream request equivalence** — parse both bodies and compare semantically, after normalizing
top-level key order. Byte comparison is wrong here by construction.

**Client-visible response equivalence** — parse both and compare on the IR-modeled projection,
asserting the passthrough result is a *superset*. Byte equality is impossible: passthrough preserves
the provider's exact fields, order, and chunk boundaries while the IR path re-serializes. The corpus
deliberately includes bodies carrying fields the IR does not model, where the two paths are *expected*
to differ — those cases assert passthrough preserved the field and the IR path recorded a warning
about dropping it.

**Usage agreement** — identical token counts across both paths for every fixture, which is what makes
§5.2 and the Anthropic two-event fix verifiable rather than asserted.

The corpus reuses phase 4's golden fixtures: thinking blocks, cache control with TTLs, parallel tool
calls, multi-part content, mid-stream errors.

Additional tests: eligibility denied for every excluded case, including Bedrock, Vertex, a
quirk-declaring preset, and a multipart surface; a Gemini request rewriting the URL path with an
untouched body; a same-name model skipping the rewrite entirely and forwarding byte-identical bytes; a
rewrite failure falling back to the IR path; a pre-commit 400 retried through the IR path and
succeeding; an in-stream `overloaded_error` under a 200 failing over before commit; a scanner error
mid-stream leaving the client stream intact; a prompt containing `<`, `>`, and `&` surviving the
rewrite unescaped.

Benchmarks compare time-to-first-token and allocations between paths, so the optimization's value is
measured rather than assumed.

## 11. Done criteria

- Claude Code against an Anthropic provider takes the passthrough path, and a request carrying a parameter the IR does not model reaches the provider intact.
- The same request failing over to a Groq target translates correctly through the IR path, with warnings recorded for the dropped field.
- A Gemini client passes through with its body untouched and only the URL rewritten.
- Usage accounting agrees between the two paths across the whole corpus, including Anthropic cache tokens and OpenAI streamed usage.
- A prompt containing HTML-significant characters is not escaped by the rewrite.
- A strict provider's 400 is retried through the IR path rather than returned to the client.
- Time-to-first-token measurably improves on same-dialect routes.
- `go test ./...` passes.
