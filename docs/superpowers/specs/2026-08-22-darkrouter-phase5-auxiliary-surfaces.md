# Phase 5 — Auxiliary Surfaces

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3; soft dependencies on phase 4 for the warning mechanism and phase 6 for
surface metadata. Before phase 6, every model's surfaces come from its preset declaration.

---

## 1. Goal

Serve the non-chat surfaces — embeddings, responses, images, audio, rerank, and moderations — through
the same router, executor, health model, and log as chat.

## 2. Scope boundary

**In:** the seven routes below, their narrow IR types, surface-based routing, adapter surface
declaration, the adapter matrix, and non-JSON body handling.

**Out:** batch and files APIs, video, music, OCR, web search, and `/v1/audio/translations` —
permanently out per master design §2. Whisper clients do call translations; its absence is deliberate,
not an oversight.

## 3. Routes and shapes

| Route | IR type | Notes |
|---|---|---|
| `POST /v1/embeddings` | `EmbeddingRequest` | Batched input, `float` or `base64` encoding, optional dimensions. |
| `POST /v1/responses` | chat `Request` | Chat-shaped; see §5. |
| `POST /v1/images/generations` | `ImageRequest` | Returns URLs or base64. |
| `POST /v1/audio/speech` | `SpeechRequest` | Binary audio, or SSE when `stream_format: "sse"`. |
| `POST /v1/audio/transcriptions` | `TranscriptionRequest` | Multipart in; JSON, plain text, or SSE out. |
| `POST /v1/rerank` | `RerankRequest` | Cohere v2 schema; see §3.1. |
| `POST /v1/moderations` | `ModerationRequest` | Category flags. |

Each type stays deliberately narrow. Forcing a six-field embedding call through the content-block
message model would obscure both shapes and buy nothing.

### 3.1 Rerank

OpenAI defines no rerank endpoint, so Darkrouter adopts the **Cohere v2** request and response schema
as its contract: document objects, `top_n`, `return_documents`, and a results array of index-and-score
pairs. Each preset declares its own rerank path, since providers expose it at differing URLs.
Providers whose shape deviates materially from Cohere v2 are excluded from the surface rather than
special-cased with quirks.

This is a decision worth revisiting if the provider mix argues for Jina's or Voyage's shape instead.

## 4. Surface routing and the adapter matrix

Every catalog model carries the surfaces it offers. `router.Query.Surface` is matched against them, so
an embedding request never considers a chat-only target, and the failure when none exists is the same
distinguishable "no provider offers this" error as anywhere else.

Adapters declare what they implement via `Surfaces() SurfaceSet`. A surface an adapter does not
implement makes that provider ineligible at routing time — a filter, never a runtime error.

| Surface | openaicompat | anthropic | gemini | bedrock | vertex |
|---|---|---|---|---|---|
| `llm` | yes | yes | yes | yes | yes |
| `embedding` | yes | no | yes (`embedContent`) | yes | yes |
| `image` | yes | no | no (Imagen is out of scope for v1) | no | no |
| `tts` | yes | no | no | no | no |
| `stt` | yes | no | no | no | no |
| `rerank` | yes, where the preset declares a Cohere-v2 path | no | no | no | no |
| `moderation` | yes | no | no | no | no |

Without this matrix the done criterion "each of the seven routes reaches a real provider" is
unfalsifiable.

## 5. Responses API

`/v1/responses` is chat-shaped and maps onto the chat IR rather than getting its own type.

**Stateful requests are rejected, not degraded.** A request carrying `previous_response_id`,
`conversation`, or built-in tool declarations (`web_search`, `file_search`, `code_interpreter`) that
the resolved target cannot honor returns an explicit Darkrouter error. With a server-stored
conversation the body carries only the newest turn, so degrading to a chat completion would return a
fluent, confident, amnesic answer that looks entirely successful — and silently answering without a
requested web search is the same class of lie. An error the client can handle beats a wrong answer it
cannot detect.

Stateless requests are supported against any `llm` target. Responses-specific fields the IR does not
model ride in `Extra` and are re-emitted.

**The response and stream shapes must be synthesized.** Responses returns an item-based body — an
`output` array of `output_text`, `function_call`, and `reasoning` items, with usage named
`input_tokens`/`output_tokens` — and streams *semantic* events (`response.created`,
`response.output_item.added`, `response.output_text.delta`,
`response.function_call_arguments.delta`, `response.completed`) with sequence numbers. That is
effectively a fourth edge stream writer, not a thin adaptation, and it is the largest single piece of
work in this phase.

Darkrouter does not mint resolvable response IDs. Because it cannot honor an echoed
`previous_response_id`, returned IDs are marked as non-resumable in the response and any later request
referencing one is rejected per the rule above.

## 6. Non-JSON bodies

Three cases break the JSON-in, JSON-out assumption, and each needs explicit handling.

**Multipart inbound** (`/v1/audio/transcriptions`). The body is **buffered to `server.max_body_bytes`,
not streamed through.** An earlier draft specified streaming, which is incompatible with failover — a
streamed body cannot be replayed for a second attempt — and also makes the required rewrite
impossible, since the `model` field lives *inside* the multipart form and must be changed to the
target's name. Clients may place that field after the file part, so routing may need most of the body
anyway. Buffering restores failover and makes the rewrite trivial; an oversized upload is rejected
with 413 before any upstream connection.

**Binary outbound** (`/v1/audio/speech` by default). The response streams through without parsing,
bypasses body capture entirely — audio in SQLite is never right — and the log records content type,
byte count, and duration.

**Text and SSE outbound.** `/v1/audio/transcriptions` with `response_format` of `text`, `srt`, or
`vtt` returns a plain-text body, and with `stream=true` returns SSE transcript events
(`transcript.text.delta`/`.done`). `/v1/audio/speech` with `stream_format: "sse"` likewise returns
SSE. Both are forwarded untouched; the adapter selects handling by the response `Content-Type` rather
than assuming from the route.

## 7. Commit semantics for binary surfaces

Phase 3's rule applies: once the first byte reaches the client, no re-route. For audio and images that
has a consequence worth stating — a provider returning a fast 200 followed by a truncated body cannot
be failed over, and unlike chat there is no in-stream error vocabulary to signal it. The client
receives truncated audio. Byte counts are logged so the trace shows the truncation even though the
client could not be warned.

## 8. Embedding failover across models

An embedding request that fails over to a *different model* returns vectors from a different vector
space. A client filling an index across such a failover corrupts it, and nothing in the response
signals that.

Darkrouter permits the failover, records a warning on the request when the serving model differs from
the first candidate, and always sets `X-Darkrouter-Model`. The documentation states plainly that
embedding aliases should list same-model targets across providers, not different models. This is a
documented hazard rather than a new config knob, on the judgement that a homelab operator writing an
embedding alias can be told the rule once.

## 9. Logging

Surface-specific fields are recorded where meaningful: input item count and dimensions for embeddings,
image count and size, audio duration and voice, document count for rerank.

Token counts are recorded where reported. Embeddings usually report input tokens only. `gpt-image-1`
*does* report usage including image input tokens; the dall-e models report none. Where no usage
arrives, cost is computed from per-call or per-unit pricing rather than tokens, and left NULL when
neither exists.

## 10. Testing

Per surface: a successful call, an upstream failure failing over to a second provider, and a call
where no candidate declares the surface — asserting the third returns the distinguishable no-provider
error without attempting anything.

Multipart tests assert the body is buffered and replayable across two attempts, that the in-form
`model` field is rewritten to the target's name, that a field placed after the file part is still
found, and that an oversized upload is rejected before any upstream connection.

Response-type tests assert JSON, plain-text, and SSE transcription responses are each handled by
`Content-Type` rather than by route, and that a binary speech response is never captured even when
`capture.bodies` is on.

Responses tests assert a `previous_response_id` request is rejected with a clear error rather than
served, that a stateless request produces correct semantic streaming events, and that returned IDs are
marked non-resumable.

A routing test asserts an embedding request never selects a chat-only model even when the model name
matches, and that a cross-model embedding failover records a warning.

## 11. Done criteria

- Each of the seven routes reaches a real provider per the §4 matrix and returns a correct response.
- An embedding request fails over between two providers serving the same model, and records a warning if the model differs.
- A transcription request survives a first-provider failure by replaying its buffered body.
- A stateful Responses request is rejected with an explicit error; a stateless one streams correct semantic events.
- A request for a surface no configured provider offers returns a clear error naming that fact.
- A speech response streams without buffering and is not captured into the database.
- `go test ./...` passes.
