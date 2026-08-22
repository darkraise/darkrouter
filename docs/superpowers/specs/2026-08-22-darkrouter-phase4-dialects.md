# Phase 4 — Dialects

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3.

---

## 1. Goal

Add Anthropic Messages and Google Gemini as both inbound dialects and outbound adapters, so Claude
Code and Gemini CLI point at Darkrouter natively and any dialect can fail over to any other.

## 2. Scope boundary

**In:** `internal/edge/anthropic`, `internal/edge/gemini`, `internal/adapter/anthropic`,
`internal/adapter/gemini`, the complete mapping tables, the lossy-field warning mechanism, per-dialect
in-stream error shapes, and the golden-file suite.

**Out:** auxiliary surfaces (phase 5), the passthrough fast path (phase 9 — every request here goes
through the IR).

## 3. Routes and inbound authentication

```
POST /v1/messages
POST /v1/messages/count_tokens
POST /v1beta/models/{model}:generateContent
POST /v1beta/models/{model}:streamGenerateContent
POST /v1beta/models/{model}:countTokens
GET  /v1beta/models
```

Inbound proxy credentials per master design §13: Anthropic clients send `x-api-key` or an
`Authorization: Bearer`; Gemini clients send `x-goog-api-key` or `?key=`. Both are compared against
`server.proxy_token` in constant time.

The Anthropic adapter always sends an `anthropic-version` header upstream, since Anthropic requires
one; an inbound `anthropic-version` is accepted and forwarded rather than overridden.

### 3.1 Model extraction from the Gemini path

The model occupies one path segment, but alias and `provider/model` names do not always fit one.
Rules, applied in order: strip a leading `models/` resource prefix; split the segment on the last `:`
to separate the method suffix; percent-decode the remainder. A decoded name containing slashes is then
handed to phase 3's normal resolution, so `openrouter%2Fanthropic%2Fclaude-sonnet-4.5` works while an
un-encoded slash simply does not match a segment and resolves as a bare name.

### 3.2 What the Gemini edge emits

`streamGenerateContent` returns SSE when `?alt=sse` is present and a chunked JSON array otherwise.
Both client styles exist, so the edge honors `alt=sse` and emits the JSON-array form when it is
absent.

`GET /v1beta/models` returns Gemini's listing shape — `{models: [{name: "models/…",
supportedGenerationMethods: [...], inputTokenLimit, outputTokenLimit}], nextPageToken}` — because some
clients filter on `supportedGenerationMethods`.

## 4. Mapping tables

These are the substance of the phase. Each is a decision that, gotten wrong, produces subtly broken
output rather than an error.

### 4.1 Roles

| IR | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| `system` | `system` or `developer` message | top-level `system` | `systemInstruction` |
| `user` | `user` | `user` | `user` |
| `assistant` | `assistant` | `assistant` | `model` |
| `tool` | `tool` message | `user` message with `tool_result` blocks | `user` message with `functionResponse` parts |

Newer OpenAI clients send `developer` in place of `system`; both are accepted inbound and both emit as
`system` outbound. The legacy `function` role and `functions`/`function_call` fields are accepted
inbound and translated to the tool equivalents.

Gemini names the assistant role `model`; passing `assistant` through produces an API error. Anthropic
and Gemini both carry tool results inside a *user* turn rather than a distinct role.

**Reverse splitting.** Going the other way, an Anthropic user turn mixing `tool_result` blocks and
text becomes, for OpenAI, one `tool` message per `tool_call_id` — placed immediately after the
assistant message carrying the matching `tool_calls` — followed by a separate `user` message with the
remaining text. Wrong ordering is a 400.

**Multiple system messages.** OpenAI permits several, including mid-conversation; Anthropic and Gemini
take one. They are concatenated in order with a warning when any appeared after the first user turn,
since position was meaningful and is being lost.

**Assistant prefill.** A conversation ending in an assistant turn is Anthropic's prefill idiom. It is
preserved for Anthropic targets and dropped with a warning for OpenAI and Gemini, neither of which
supports it.

### 4.2 Content blocks

| IR block | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| `text` | string or `{type:"text"}` part | `{type:"text", text}` | `{text}` part |
| `image` | `image_url` part, URL or data URI | `{type:"image", source}` with `base64`, `url`, or `file` | `inlineData` (base64) — see below |
| `audio` | `{type:"input_audio", input_audio:{data, format}}` | unsupported, dropped with warning | `inlineData` with an audio MIME type |
| `document` | `{type:"file", file:{file_data\|file_id}}` | `{type:"document", source}` | `inlineData` with a document MIME type |
| `thinking` | `reasoning_content` where the provider offers it, else dropped | `{type:"thinking", thinking, signature}` | part with `thought: true` plus `thoughtSignature` |
| `redacted_thinking` | dropped with warning | `{type:"redacted_thinking", data}` | dropped with warning |
| `tool_use` | one entry in `tool_calls` | `{type:"tool_use", id, name, input}` | `functionCall` part |
| `tool_result` | a `tool` message keyed by `tool_call_id` | `{type:"tool_result", tool_use_id, content, is_error}` | `functionResponse` part |
| `cache_control` | dropped with warning | `{type:"ephemeral", ttl}` | dropped with warning |

**Gemini images.** `fileData.fileUri` accepts Files API URIs and YouTube URLs — **not** arbitrary HTTP
image URLs. An IR image carrying a public URL is downloaded and inlined as base64 under a size cap, or
dropped with a warning if the fetch fails or the cap is exceeded. Emitting `fileData` for a
non-Files-API URI is rejected by the API.

**Anthropic thinking blocks** must be passed back **unmodified and in order**, including their
signatures, or subsequent turns lose reasoning state. The IR round trip preserves them verbatim.

**Gemini `thoughtSignature`** plays the same role: a part carrying only `thought: true` does not
restore reasoning state when sent back. Signatures map to `ir.Thinking.Signature`.

**OpenAI documents** are supported at the API level via `file` parts. Most `openaicompat` providers
are not, so the drop is gated on a per-provider capability rather than stated as a protocol absence.

**`cache_control`** carries a `ttl` of `"5m"` or `"1h"`, and a request may hold at most **four**
breakpoints — a fifth is a 400. Markers below a model's minimum cacheable token count are silently
ignored by Anthropic. Darkrouter forwards up to four and drops the rest with a warning rather than
letting the upstream 400, since the client cannot see which marker was surplus.

### 4.3 Tools and tool choice

| Concept | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| Declaration | `tools[].function{name, description, parameters}` | `tools[]{name, description, input_schema}` | all inside a single `tools[0].functionDeclarations[]` |
| Auto | `tool_choice: "auto"` | `tool_choice: {type:"auto"}` | `toolConfig.functionCallingConfig.mode: "AUTO"` |
| None | `tool_choice: "none"` | `tool_choice: {type:"none"}` | `mode: "NONE"` |
| Force any | `tool_choice: "required"` | `tool_choice: {type:"any"}` | `mode: "ANY"` |
| Force one | `tool_choice: {function:{name}}` | `tool_choice: {type:"tool", name}` | `mode: "ANY"` with `allowedFunctionNames: [name]` |
| Parallel | `parallel_tool_calls` | `disable_parallel_tool_use` (inverted) | no equivalent — dropped with warning |

Gemini putting every declaration inside one `tools` entry is the trap: separate entries are reserved
for built-in tools, and emitting one entry per function silently disables function calling.

### 4.4 Tool-call identity

OpenAI's `tool_call_id` and Anthropic's `tool_use_id` are mandatory correlators. Gemini's
`functionCall` and `functionResponse` carry only *optional* `id` fields that most models omit.

The rule: preserve an upstream id when present; otherwise synthesize a deterministic id from the turn
index and call position. Matching a `functionResponse` to its `functionCall` is **positional within
the turn**, not by name — name matching breaks on parallel calls to the same function, which is
exactly what agentic loops do.

### 4.5 Stop reasons

| IR | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| `end_turn` | `stop` | `end_turn` | `STOP` |
| `max_tokens` | `length` | `max_tokens`, `model_context_window_exceeded` | `MAX_TOKENS` |
| `tool_use` | `tool_calls` | `tool_use` | `STOP` with a `functionCall` part present |
| `stop_sequence` | `stop` | `stop_sequence` | `STOP` |
| `content_filter` | `content_filter` | `refusal` | `SAFETY`, `BLOCKLIST`, `PROHIBITED_CONTENT`, `SPII`, `RECITATION`, `IMAGE_SAFETY` |
| `error` | — | — | `MALFORMED_FUNCTION_CALL`, `OTHER`, `LANGUAGE` |
| `pause_turn` | dropped, treated as `end_turn` | `pause_turn` | — |

Anthropic *does* have a content-filter reason — `refusal` — and also returns `pause_turn` for
server-tool loops and `model_context_window_exceeded`. An earlier draft left the Anthropic
content-filter cell empty.

Gemini does not signal tool use in its finish reason, so the IR value is inferred from a `functionCall`
part being present. Getting this wrong makes agentic clients terminate mid-task.

**Blocked prompts have no finish reason at all.** Gemini returns zero candidates with
`promptFeedback.blockReason` set; an adapter reading `candidates[0].finishReason` nil-dereferences or
reports an empty success. That case maps to a `content_filter` error.

Unmapped or future enum values are treated as `end_turn` with a warning rather than as an error.

### 4.6 Response format and reasoning

| IR | OpenAI | Anthropic | Gemini |
|---|---|---|---|
| `ResponseFormat.JSONSchema` | `response_format: {type:"json_schema", json_schema}` | structured-output beta where available, else dropped with warning | `responseMimeType: "application/json"` plus `responseSchema` |
| `Reasoning.Effort` | `reasoning_effort` | converted to `thinking: {type:"enabled", budget_tokens}` | `thinkingConfig.thinkingBudget` with `includeThoughts` |
| `Reasoning.Budget` | converted to the nearest `reasoning_effort` | `budget_tokens` directly | `thinkingBudget` directly |

The effort-to-budget conversion is fixed and documented rather than left to each adapter: `low` =
4,096 tokens, `medium` = 16,384, `high` = 32,768, clamped to the model's maximum output tokens from
the catalog. Two implementers would otherwise choose differently and the same request would reason
differently per target.

Whether Anthropic's structured-output beta is GA must be checked at implementation time; the spec
assumes beta and drops with a warning where unavailable.

### 4.7 Required-field synthesis

Anthropic requires `max_tokens`. An OpenAI- or Gemini-inbound request carrying no cap gets the
model's `max_output_tokens` from the catalog, or a 4,096 default when the catalog does not know it,
recorded as a warning so the substitution is visible.

Its current requiredness should be re-confirmed during implementation.

### 4.8 Streaming

Anthropic's event model is the IR's, so that mapping is near-identity. Its event set is
`message_start`, `content_block_start`, `content_block_delta`, `content_block_stop`, `message_delta`,
`message_stop`, `ping`, `error`. **Unknown event types are ignored, not errors** — Anthropic
explicitly warns clients that new ones will appear.

**OpenAI** streams flat deltas and needs a state machine to reconstruct block structure. The adapter
tracks the current block index and kind, emitting `content_block_start` when a delta first carries a
given kind, `content_delta` for continuations, and `content_block_stop` when the kind changes or the
stream ends. Tool calls are the hard case: arguments arrive as JSON string fragments across many
chunks, indexed by `tool_calls[].index`, so each index accumulates into its own block.

**Gemini** chunks are **incremental, not cumulative**. Each chunk carries new content to append: text
parts are fragments the client concatenates, and structural parts like `functionCall` arrive whole in
a single chunk. The adapter iterates each chunk's parts and emits deltas directly — text becomes
`content_delta`, a `functionCall` becomes block start, full input, and block stop. `usageMetadata`
appears on interim chunks too; the final chunk's value is authoritative.

An earlier draft described Gemini chunks as whole-part snapshots requiring diffing against the
previous chunk. That is wrong, and implementing it against append-semantics chunks produces garbage
output.

### 4.9 In-stream errors

Phase 3 emits an error event inside a committed stream; its shape is per dialect:

| Dialect | Shape |
|---|---|
| Anthropic | a real `error` SSE event carrying the error object |
| OpenAI | `data: {"error": {...}}` followed by `data: [DONE]` |
| Gemini | SSE has no error event type; a final chunk with a `content_filter`-style `finishReason` and a `promptFeedback`-shaped error object |

## 5. Lossy fields are recorded, never silent

An adapter that cannot express an IR field appends a `Warning` naming the field, the target kind, and
the reason. Warnings land in `requests.warnings_json` and surface in phase 7's trace drawer.

Silent loss is the specific failure this design exists to avoid. A user whose cache-control markers
vanish on failover must see it in the trace rather than infer it from a bill.

## 6. Token counting

`POST /v1/messages/count_tokens` and `POST /v1beta/models/{model}:countTokens` are forwarded natively
when the resolved target speaks the same counting dialect — Anthropic for the former, Gemini for the
latter — returning the provider's real count.

Otherwise Darkrouter returns a local estimate, computed with a bundled BPE tokenizer
(`tiktoken`-compatible `cl100k`/`o200k` where the target's family is known, characters-divided-by-four
otherwise) and marked with an `X-Darkrouter-Estimated: true` response header. The body cannot carry a
marker, because clients parse these responses strictly.

Naming the method matters: clients budget context windows against this number, and estimates differ by
a factor of two between plausible approaches.

## 7. Golden-file suite

Fixtures live under `testdata/golden/<dialect>/<case>/` with `request.json`, `ir.json`, and one
rendered file per adapter kind. Each case runs in both directions. Phase 8 extends the suite with
`bedrock` and both `vertex` publisher variants.

The suite must include the awkward cases explicitly, because the easy ones never catch anything:
extended thinking with signatures, Gemini thought signatures, cache control with a `1h` TTL, five
cache breakpoints (asserting the fifth is dropped with a warning), parallel tool calls to the same
function name, a tool result carrying an image, multi-part content mixing text and two images, an
empty assistant turn, an assistant prefill, a `stop_sequence` finish, an Anthropic `refusal`, a Gemini
blocked prompt with zero candidates, a `developer`-role message, and a stream that errors after three
content blocks.

**Tool results carrying images** need a stated mapping, since OpenAI tool messages are text-only and
Gemini's `functionResponse.response` is a JSON struct: the image is hoisted into a following user
message with a warning, preserving the content rather than dropping it.

## 8. Testing beyond golden files

Cross-dialect end-to-end tests through the fake fleet: Anthropic-inbound served by `openaicompat`,
Gemini-inbound served by `anthropic`, and OpenAI-inbound failing over from `anthropic` to `gemini`
mid-chain — asserting the client receives one coherent response in its own dialect throughout.

A property test asserts IR → wire → IR round-trips preserve every field the target kind claims to
support, and that every dropped field produced a warning. `CacheControl.TTL` and
`Thinking.Signature` are explicitly covered, since both are silent-loss candidates.

## 9. Done criteria

- Claude Code against `/v1/messages` works with an Anthropic provider and a Groq one, including tool use across several turns and a cached system prompt.
- Gemini CLI against `/v1beta` works with a Gemini provider and an Anthropic one, including `countTokens`.
- Extended thinking with signatures survives Anthropic-to-Anthropic; Gemini thought signatures survive Gemini-to-Gemini; loss elsewhere appears as a warning.
- Streaming tool calls reconstruct correctly from OpenAI's fragmented arguments and from Gemini's whole-part chunks.
- A Gemini blocked prompt produces a content-filter error rather than an empty success.
- `go test ./...` passes, golden files included.
