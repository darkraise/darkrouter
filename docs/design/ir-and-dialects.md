# The IR and the dialect contract

The intermediate representation is the pivot. Every inbound dialect parses
into it; every outbound adapter renders from it. Nothing in `internal/ir`
performs I/O or imports another internal package.

## Vocabulary

**Surfaces** — `llm`, `embedding`, `image`, `tts`, `stt`, `rerank`,
`moderation`. Filtered twice: once against the catalogue's declaration, once
against the adapter's own set.

**Block types** — `text`, `image`, `audio`, `document`, `thinking`,
`redacted_thinking`, `tool_use`, `tool_result`.

**Stop reasons** — `end_turn`, `max_tokens`, `stop_sequence`, `tool_use`,
`content_filter`.

**Error types** — nine transport and semantic classes plus
`payload_too_large` (413) and `unsupported_media` (415).

`ir.MediaKind` maps a bare MIME type to a block type. Dialects that use one
part shape for every medium have no other signal, and the router's vision
capability check reads the result — so the mapping is defined once rather than
per direction.

## Media

`Media` carries `MIME`, `Data` (base64), `URL`, and `FileID`. `FileID` is a
provider-side handle — an OpenAI file id, a Gemini `fileData.fileUri`, an
Anthropic file source. **It is not interchangeable with `URL`**: a target that
accepts its own handle will reject a public address, and the reverse.

A data URI arriving in an OpenAI `image_url` is split into `MIME` plus `Data`
at parse time. Without that, Anthropic would receive `source.type: "url"` for
base64 content and Gemini would try to fetch `data:` over HTTP.

## Usage

Eight fields. `InputTokens` means input **excluding** cache reads, repository
wide, and `PromptTokens()` re-adds them when rendering back to a dialect that
reports an inclusive count.

This normalisation is load-bearing: Anthropic's `input_tokens` excludes cache
tokens while OpenAI's `prompt_tokens` and Gemini's `promptTokenCount` include
them. Any cost formula written before normalisation is wrong for at least one
family. `cache_read_tokens` is exposed on the request list and trace so an
operator can still reconstruct a full prompt count against an invoice.

`CacheWrite5mTokens` and `CacheWrite1hTokens` are **included in**
`CacheWriteTokens`, never additional, because Anthropic prices the two TTLs
differently.

## Translation rules

**Warn, do not drop silently.** Where a field cannot be expressed against the
target, the request record carries a warning naming the field and the target.
Where a shape can be converted, it is converted rather than refused.

**Never send a shape the catalogue says the model refuses.** Three per-model
traits drive real reshaping: `NoPrefill`, `ThinkingAlwaysOn` and
`NoForcedToolChoice`. A forced tool choice downgrades to `auto` with a
warning; the thinking off-switch is withheld from a model that always thinks.

**The catalogue decides, not the model name.** A name-fragment table needs a
new entry every time a vendor ships a generation, and is wrong for an aliased
or proxied model whose name says nothing about its generation. A model the
catalogue does not know gets the permissive fallback — shape the request the
way the client asked, and warn that the shape was guessed.

## Thinking

Four modes: none, adaptive, manual budget, disabled. The client's own spelling
says what it wants; the model's traits say what it can have.

An explicit token budget is evidence the client targets a budget-taking model;
an effort or an adaptive config is evidence of the opposite. Where the target
cannot honour the client's shape, the request is converted rather than failed.

Manual thinking and a forced tool choice are mutually exclusive on Anthropic,
and thinking loses: the forced tool is the client's explicit instruction and
an agentic loop depends on it, while reasoning depth is the softer ask.
Adaptive thinking has no such conflict.

Anthropic requires `max_tokens` to be strictly greater than the budget, so the
budget is clamped to `max_tokens - 1`; if that lands under the 1024 floor,
thinking is disabled with a warning. Raising `max_tokens` instead would
silently multiply the bill on the one control the client actually set.

Gemini 3 takes a `thinkingLevel` rather than a token budget.

Bedrock's Converse cannot express the adaptive shape at all, so a model the
catalogue knows to be adaptive-only has its reasoning request dropped with a
warning rather than sent as a manual budget it refuses.

## Streaming

Block indices in the IR are not wire indices — the OpenAI and Anthropic stream
writers renumber densely from zero for the client.

The OpenAI adapter keeps text, reasoning and tool calls in disjoint index
spaces and leaves them open concurrently; blocks close on `finish_reason`,
`[DONE]` or EOF, not on a kind change.

The Anthropic edge holds back `message_start` until the first usage update or
the first content, so an Anthropic-served route reports real input tokens
inside `message_start` without delaying a route whose dialect reports usage
last.

Three places sort explicitly to defeat Go's random map iteration — the IR's
own block close, and the Anthropic and Gemini stream writers' flushes.

An in-stream error under a 200 — Anthropic's `overloaded_error`, Gemini's
`RESOURCE_EXHAUSTED` — is classified into a typed IR error so the attempt loop
can reason about a failure the status line cannot describe.

## Transport metadata

State the Anthropic edge needs its own adapter to echo — the version header,
the beta list, the inbound thinking type — is parked in `Metadata` under keys
prefixed `anthropic_`. Every other adapter strips that prefix. A key that
misses the prefix is forwarded to an upstream that has no idea what it is.

`gemini_cached_content` is the one provider-specific handle parked in
`Metadata`, read back only by the Gemini adapter.

`Request.Extra` carries fields the IR does not model, and never overwrites a
field the IR already rendered.

## Auxiliary surfaces

The six non-LLM surfaces share the router, the attempt loop, the health model
and the request log. They do **not** share the fast path: passthrough is
structurally impossible for them, and only the `llm` surface is ever eligible.

Each has its own narrow request type. Not all have a response type: speech
forwards audio, and transcription has neither — it is a multipart form whose
body is forwarded verbatim so `verbose_json` timings survive.

A transcription body is re-rendered per attempt, because the model name lives
inside the form. That is why an operation builds per attempt rather than per
request.

Moderation categories are maps rather than structs, so a provider-added
category is never silently dropped.

Every auxiliary surface writes a metadata map onto the request row — an
embedding's input count and dimensions, a rerank's document count, a speech
request's voice, a transcription's file name.
