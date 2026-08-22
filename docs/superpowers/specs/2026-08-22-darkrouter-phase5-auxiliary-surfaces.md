# Phase 5 — Auxiliary Surfaces

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3. Independent of phases 4, 6, and 8.

---

## 1. Goal

Serve the non-chat OpenAI-shaped surfaces — embeddings, responses, images, audio, rerank, and
moderations — through the same router, executor, health model, and log as chat.

## 2. Scope boundary

**In:** the routes below, their narrow IR types, service-kind routing, adapter surface declaration,
and the handling of non-JSON bodies.

**Out:** batch and files APIs, video, music, OCR, and web search — permanently out per master design
§2.

## 3. Routes and shapes

| Route | IR type | Notes |
|---|---|---|
| `POST /v1/embeddings` | `EmbeddingRequest` | Batched input, float or base64 encoding format, optional dimensions. |
| `POST /v1/responses` | chat `Request` | Chat-shaped; see §5. |
| `POST /v1/images/generations` | `ImageRequest` | Returns URLs or base64 payloads. |
| `POST /v1/audio/speech` | `SpeechRequest` | Binary audio response, streamed. |
| `POST /v1/audio/transcriptions` | `TranscriptionRequest` | Multipart upload inbound. |
| `POST /v1/rerank` | `RerankRequest` | Query plus documents, returns scored indices. |
| `POST /v1/moderations` | `ModerationRequest` | Returns category flags. |

Each type stays deliberately narrow. Forcing a six-field embedding call through the content-block
message model would obscure both shapes and buy nothing — these surfaces have no conversation, no
tools, and no streaming structure to share.

## 4. Service-kind routing

Every model in the catalog carries the service kinds it offers: `llm`, `embedding`, `image`, `tts`,
`stt`, `rerank`, `moderation`.

The `router.Query` gains a `Surface` field, and candidate filtering requires the model to declare the
matching kind. An embedding request therefore never considers a chat-only target, and the failure mode
when none exists is the same distinguishable "no provider offers this" error as anywhere else.

Adapters declare which surfaces they implement:

```go
type Adapter interface {
    // ...
    Surfaces() SurfaceSet
}
```

A surface an adapter does not implement makes that provider ineligible for the route, resolved at
routing time. This is a filter, not a runtime error — an adapter must never be handed a request it
cannot build.

## 5. Responses API

`/v1/responses` is chat-shaped, so it maps onto the chat IR rather than getting its own type.
Responses-specific fields that the IR does not model — the stored-conversation identifier, the
built-in tool declarations, the reasoning-item structure — are carried in `Extra` and re-emitted on
the way out.

When the resolved target is an `openaicompat` adapter that supports Responses, this is close to a
passthrough even before phase 9. When it is not, the request degrades to a chat completion and the
Responses-only fields are dropped with warnings, per the phase 4 mechanism.

## 6. Non-JSON bodies

Two surfaces break the JSON-in, JSON-out assumption, and both need explicit handling rather than
being allowed to fall through the normal path.

**Multipart inbound** (`/v1/audio/transcriptions`) is streamed to the upstream rather than buffered.
Buffering an audio file into memory to re-encode it wastes the memory and gains nothing, since the
multipart envelope is forwarded intact. A configurable size cap rejects oversized uploads before any
upstream call.

**Binary outbound** (`/v1/audio/speech`) streams the response body through without parsing. It
bypasses body capture entirely — capturing audio into SQLite is never the right behavior — and the
request log records only content type, byte count, and duration.

Both are still subject to the full failover path, including the streaming commit rule from phase 3:
once the first byte of audio reaches the client, no re-route is possible.

## 7. Logging

Surface-specific fields are recorded where they exist and are meaningful: input item count and
dimensions for embeddings, image count and size for generations, audio duration and voice for speech,
document count for rerank. Token counts are recorded where the provider reports them; embeddings
usually report input tokens only, and images and audio often report nothing, in which case cost is
computed from per-call or per-unit pricing rather than tokens.

## 8. Testing

Fake fleet cases per surface: a successful call, an upstream failure that fails over to a second
provider, and a call where no candidate declares the surface — asserting the third returns the
distinguishable no-provider error rather than attempting anything.

A multipart test asserts the upload is streamed rather than buffered, and that an oversized upload is
rejected before any upstream connection is made.

A binary-response test asserts that body capture is skipped even when `capture.bodies` is on, and that
the log still records content type and size.

A routing test asserts an embedding request never selects a chat-only model even when the model name
matches.

## 9. Done criteria

- Each of the seven routes reaches a real provider and returns a correct response.
- An embedding request fails over between two embedding providers without the client noticing.
- A request for a surface no configured provider offers returns a clear error naming that fact.
- A speech response streams without buffering and is not captured into the database.
- `go test ./...` passes.
