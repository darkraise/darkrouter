# Phase 4 — Dialects

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3.

---

## 1. Goal

Add Anthropic Messages and Google Gemini as both inbound dialects and outbound adapters, so Claude
Code and Gemini CLI point at Darkrouter natively and any dialect can fail over to any other.

## 2. Scope boundary

**In:** `internal/edge/anthropic`, `internal/edge/gemini`, `internal/adapter/anthropic`,
`internal/adapter/gemini`, the complete mapping tables, the lossy-field warning mechanism, and the
golden-file translator suite.

**Out:** auxiliary surfaces (phase 5), the passthrough fast path (phase 9 — every request in this
phase goes through the IR).

## 3. Routes added

```
POST /v1/messages
POST /v1/messages/count_tokens
POST /v1beta/models/{model}:generateContent
POST /v1beta/models/{model}:streamGenerateContent
GET  /v1beta/models
```

Gemini clients pass their API key in an `x-goog-api-key` header or a `?key=` query parameter; both are
accepted as the inbound proxy token.

## 4. Mapping tables

These are the substance of the phase. Each is a decision that, gotten wrong, produces subtly broken
output rather than an error.

### 4.1 Roles

| IR | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| `system` | `system` message | top-level `system` | `systemInstruction` |
| `user` | `user` | `user` | `user` |
| `assistant` | `assistant` | `assistant` | `model` |
| `tool` | `tool` message | `user` message with `tool_result` blocks | `user` message with `functionResponse` parts |

Two traps. Gemini names the assistant role `model`, and a straight passthrough of `assistant`
produces an API error. Anthropic and Gemini both carry tool results inside a *user* turn rather than a
distinct role, so an OpenAI `tool` message must be folded into the following user turn rather than
emitted standalone.

### 4.2 Content blocks

| IR block | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| `text` | string or `{type:text}` part | `{type:text}` | `{text}` part |
| `image` | `image_url` part (URL or data URI) | `{type:image, source}` base64 or URL | `inlineData` or `fileData` part |
| `document` | not supported — dropped with warning | `{type:document}` | `inlineData` with a document MIME type |
| `thinking` | `reasoning_content` where the provider offers it, else dropped | `{type:thinking, signature}` | part with `thought: true` |
| `redacted_thinking` | dropped with warning | `{type:redacted_thinking}` | dropped with warning |
| `tool_use` | one entry in `tool_calls` | `{type:tool_use}` | `functionCall` part |
| `tool_result` | a `tool` message keyed by `tool_call_id` | `{type:tool_result}` | `functionResponse` part |
| `cache_control` | dropped with warning | `cache_control: {type:ephemeral}` | dropped with warning |

### 4.3 Stop reasons

| IR | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| `end_turn` | `stop` | `end_turn` | `STOP` |
| `max_tokens` | `length` | `max_tokens` | `MAX_TOKENS` |
| `tool_use` | `tool_calls` | `tool_use` | `STOP` with a `functionCall` part present |
| `stop_sequence` | `stop` | `stop_sequence` | `STOP` |
| `content_filter` | `content_filter` | — | `SAFETY` or `BLOCKLIST` |

Gemini does not signal tool use in its finish reason, so the IR value is inferred from the presence of
a `functionCall` part. Getting this wrong makes agentic clients terminate their loop mid-task.

### 4.4 Streaming

Anthropic's event model is the IR's model, so that mapping is near-identity.

OpenAI's flat deltas need a **state machine** to reconstruct block structure. The adapter tracks the
current block index and kind, emitting `content_block_start` when a delta first carries a given kind,
`content_delta` for continuations, and `content_block_stop` when the kind changes or the stream ends.
Tool calls are the hard case: OpenAI streams a tool call's arguments as JSON string fragments across
many chunks, indexed by `tool_calls[].index`, so each index maps to its own IR block accumulating
fragments.

Gemini streams whole `candidates[].content.parts` per chunk rather than deltas, so the adapter
diffs successive chunks to produce IR deltas, and emits usage from the final chunk's
`usageMetadata`.

## 5. Lossy fields are recorded, never silent

When an adapter cannot express an IR field, it appends a structured warning to the request record:
the field, the target kind, and the reason. Warnings land in `requests.warnings` and surface in the
phase 7 trace drawer.

Silent loss is the specific failure this design is built to avoid. A user whose cache-control markers
vanish on failover to an OpenAI-compatible provider must be able to see that in the trace rather than
infer it from a bill.

## 6. Token counting

`POST /v1/messages/count_tokens` is served natively when the resolved target is an Anthropic adapter —
the request is forwarded and the real count returned.

For any other target there is no exact answer, so Darkrouter returns a local estimate and marks it. The
response carries the estimate in the normal field and an `x-darkrouter-estimated: true` header. A
plausible number presented as exact is worse than an honest estimate, and clients use this figure for
context-window budgeting.

## 7. Golden-file suite

Fixtures live under `testdata/golden/<dialect>/<case>/` with `request.json`, `ir.json`, and one
rendered file per adapter kind. Each case runs in both directions: inbound wire to IR, and IR to each
outbound wire form.

The suite must include the awkward cases explicitly, because the easy ones never catch anything:
extended thinking with signatures, cache-control markers on a multi-turn conversation, parallel tool
calls in one assistant turn, a tool result carrying an image, multi-part user content mixing text and
two images, an empty assistant turn, a `stop_sequence` finish, a `content_filter` finish, and a stream
that errors after three content blocks.

Regeneration is a single command, and a diff in a golden file is a reviewable change rather than a
mystery.

## 8. Testing beyond golden files

Cross-dialect end-to-end tests through the fake fleet: an Anthropic-inbound request served by an
`openaicompat` target, a Gemini-inbound request served by an `anthropic` target, and an OpenAI-inbound
request that fails over from an `anthropic` target to a `gemini` one mid-chain — asserting the client
receives a single coherent response in its own dialect throughout.

A property test asserts that IR to wire to IR round-trips preserve every field the target kind claims
to support, and that every dropped field produced a warning.

## 9. Done criteria

- Claude Code configured against `/v1/messages` works against an Anthropic provider and against a Groq one, including tool use across several turns.
- Gemini CLI configured against `/v1beta` works against a Gemini provider and against an Anthropic one.
- Extended thinking survives Anthropic-to-Anthropic, and its loss elsewhere appears as a warning in the request record.
- Streaming tool calls reconstruct correctly from OpenAI's fragmented arguments.
- `go test ./...` passes, golden files included.
