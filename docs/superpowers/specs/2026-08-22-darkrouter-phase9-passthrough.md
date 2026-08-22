# Phase 9 — Passthrough Fast Path

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 4.

---

## 1. Goal

When the inbound dialect already matches the chosen target's dialect, stop translating. Forward the
body with a model-name rewrite and an auth swap, pipe the response as bytes, and extract usage from
the stream without parsing it into the IR.

This is the last phase deliberately. The IR path is the correctness baseline, and the fast path is
validated by proving it agrees with that baseline.

## 2. Scope boundary

**In:** the eligibility predicate, request body rewriting, header handling, the SSE usage tee,
pre-commit fallback to the IR path, and differential testing between the two paths.

**Out:** any change to routing, health, or logging semantics. Passthrough is an optimization inside
the executor, not a second pipeline.

## 3. Why this is worth building

Two reasons, in order of importance.

**Fidelity.** Claude Code against a real Anthropic provider is the single most common route in this
homelab, and passthrough makes it byte-faithful. Any field the IR does not model — a parameter
Anthropic shipped last week, a beta header, an experimental block type — survives untouched. On the IR
path, that field is dropped with a warning, which is honest but still a loss.

**Latency.** A parse and re-serialize per request and per stream event is small, but it is pure
overhead on the route that runs most often.

## 4. Eligibility

A candidate is passthrough-eligible when **all** hold:

- The inbound dialect maps to the target adapter's kind: OpenAI to `openaicompat`, Anthropic to `anthropic`, Gemini to `gemini`.
- The alias applied no parameter overrides.
- The target's preset declares no quirks requiring request rewriting.
- The surface is one whose body is JSON with a top-level `model` field.
- The adapter does not require a materialized, signed body — which excludes `bedrock` permanently, and `vertex` where the URL itself encodes the model.

Eligibility is decided **per attempt, not per request**. An Anthropic-inbound request may pass through
to Anthropic on the first attempt and translate to `openaicompat` on the second. The executor asks per
candidate.

## 5. Request rewriting

Only two things change.

**The model field.** The body is unmarshalled into `map[string]json.RawMessage`, the `model` value is
replaced, and the map is re-marshalled. Every other value survives byte-for-byte because it is never
decoded — only top-level key order shifts, which no provider is sensitive to.

The alternative, a textual or streaming edit of the raw JSON, avoids even that and is not worth its
fragility: a `"model"` string appearing inside a nested prompt would defeat naive matching, and
handling that correctly means writing a parser.

**The headers.** Inbound auth headers are stripped and replaced with the target's. Hop-by-hop headers
are dropped per RFC 9110. A small allowlist of client headers is forwarded — `anthropic-version`,
`anthropic-beta`, `accept`, `user-agent` — because these carry semantics the upstream needs. Every
other inbound header is dropped rather than forwarded, so a client cannot inject headers into
Darkrouter's upstream call.

## 6. Usage extraction

Accounting still needs token counts, so the response is teed: bytes are forwarded to the client
unchanged while a lightweight scanner watches for the usage payload.

For non-streaming responses, the body is teed into a size-capped buffer and parsed after forwarding
completes.

For streaming responses, an `io.TeeReader` feeds a scanner that examines only SSE `data:` lines,
looking for the dialect's usage field — `usage` on the final OpenAI chunk, `message_delta`'s usage for
Anthropic, `usageMetadata` for Gemini. It does not build IR events and does not retain content.

The scanner must never be able to stall or corrupt the client stream. It runs on the forwarding path's
output with a bounded buffer, and any scanner error is recorded on the request row and otherwise
ignored — losing a token count is acceptable, corrupting a response is not.

When usage is unavailable, the request is logged with a null token count rather than an estimate, and
cost is marked unknown. An estimate silently mixed into real accounting makes the whole ledger
untrustworthy.

## 7. Failure handling

Before commit, a passthrough attempt that fails behaves exactly like an IR attempt: classify, record
health, advance to the next candidate. The next candidate is evaluated for eligibility independently,
so a failed passthrough may be followed by an IR attempt against a different kind.

If body rewriting itself fails — malformed JSON, no top-level `model` — the request falls to the IR
path for that same candidate rather than failing. The IR path's parser produces a proper dialect-shaped
error if the body is genuinely invalid.

After commit, phase 3's rule stands unchanged: a failure becomes an in-stream error event.

## 8. Differential testing

The core of this phase's verification. A corpus of recorded requests is run through both paths against
the same fake provider, and the results are compared: identical upstream request semantics after
normalizing key order, identical client-visible responses, and identical usage accounting.

The corpus reuses phase 4's golden fixtures — thinking blocks, cache control, parallel tool calls,
multi-part content, mid-stream errors — plus bodies carrying fields the IR does not model, which is
where the two paths are *expected* to differ. Those cases assert that passthrough preserves the field
and the IR path records a warning about dropping it.

Additional tests: eligibility is denied for every excluded case; a rewrite failure falls back to the IR
path rather than erroring; a scanner failure mid-stream leaves the client stream intact; and a
passthrough attempt failing pre-commit is followed by an IR attempt on the next candidate.

Benchmarks compare time-to-first-token and allocations per request between the paths, so the
optimization's value is measured rather than assumed.

## 9. Done criteria

- Claude Code against an Anthropic provider takes the passthrough path, and a request carrying a parameter the IR does not model reaches the provider intact.
- The same request failing over to a Groq target translates correctly through the IR path, with warnings recorded for the dropped field.
- Usage accounting agrees between the two paths across the whole corpus.
- Differential tests pass for every fixture.
- Time-to-first-token measurably improves on same-dialect routes.
- `go test ./...` passes.
