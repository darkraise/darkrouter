import type { PlaygroundDialect } from "../../../lib/api-types"

// One extractor per dialect's streamed wire shape, verified against the edge
// writers rather than guessed: internal/edge/{openai,anthropic,gemini}/stream.go.

/** OpenAI: `choices[0].delta.content` on every chunk. */
function openaiStreamDelta(obj: unknown): string {
  const o = obj as { choices?: { delta?: { content?: string } }[] }
  const delta = o?.choices?.[0]?.delta?.content
  return typeof delta === "string" ? delta : ""
}

/** Anthropic: a `content_block_delta` frame whose `delta.type` is
 *  `text_delta` carries `delta.text`. Every other event type — including the
 *  block-open and block-close frames for the same block — carries no text. */
function anthropicStreamDelta(obj: unknown): string {
  const o = obj as { type?: string; delta?: { type?: string; text?: string } }
  if (o?.type === "content_block_delta" && o.delta?.type === "text_delta") {
    return typeof o.delta.text === "string" ? o.delta.text : ""
  }
  return ""
}

/** Gemini: each chunk carries `candidates[0].content.parts[]`, text and
 *  thought parts side by side. Only non-thought text parts are the answer —
 *  a thought part carries `thought: true` alongside its own `text`. */
function geminiStreamDelta(obj: unknown): string {
  const o = obj as {
    candidates?: { content?: { parts?: { text?: string; thought?: boolean }[] } }[]
  }
  const parts = o?.candidates?.[0]?.content?.parts ?? []
  return parts
    .filter((p) => !p.thought && typeof p.text === "string")
    .map((p) => p.text as string)
    .join("")
}

const STREAM_DELTA: Record<PlaygroundDialect, (obj: unknown) => string> = {
  openai: openaiStreamDelta,
  anthropic: anthropicStreamDelta,
  gemini: geminiStreamDelta,
}

// The same three wire shapes again, for the part of the reply that is the
// model reasoning rather than answering. Each edge writes it under its own
// name -- internal/edge/openai/stream.go emits `reasoning_content`,
// anthropic/stream.go a `thinking_delta`, gemini/stream.go a part flagged
// `thought` -- and every one of them used to be dropped on the floor here.

/**
 * OpenAI-shaped reasoning, under either of the two names it arrives as.
 *
 * `reasoning_content` is what internal/edge/openai/stream.go writes when the
 * gateway translates a reply into the OpenAI wire. `reasoning` is what an
 * OpenAI-compatible upstream writes for itself, and on the passthrough path
 * the client sees the provider's own body rather than ours — verified against
 * Groq, which streams `{"delta":{"reasoning":"…","channel":"analysis"}}` and
 * never `reasoning_content`. Reading only the gateway's spelling meant every
 * reasoning model reached on passthrough looked like it had not reasoned.
 *
 * Both are read rather than one being preferred: no frame carries both, and
 * a reply that somehow did would have concatenated them in arrival order
 * anyway.
 */
function openaiStreamReasoning(obj: unknown): string {
  const o = obj as {
    choices?: { delta?: { reasoning_content?: string; reasoning?: string } }[] }
  const delta = o?.choices?.[0]?.delta
  const named = typeof delta?.reasoning_content === "string" ? delta.reasoning_content : ""
  const bare = typeof delta?.reasoning === "string" ? delta.reasoning : ""
  return named + bare
}

/** Anthropic: a `content_block_delta` whose `delta.type` is `thinking_delta`
 *  carries `delta.thinking`. A `redacted_thinking` block carries ciphertext
 *  the client is not meant to read, so it is deliberately not collected. */
function anthropicStreamReasoning(obj: unknown): string {
  const o = obj as { type?: string; delta?: { type?: string; thinking?: string } }
  if (o?.type === "content_block_delta" && o.delta?.type === "thinking_delta") {
    return typeof o.delta.thinking === "string" ? o.delta.thinking : ""
  }
  return ""
}

/** Gemini: the parts the answer extractor skips — `thought: true` beside the
 *  part's own `text`. */
function geminiStreamReasoning(obj: unknown): string {
  const o = obj as {
    candidates?: { content?: { parts?: { text?: string; thought?: boolean }[] } }[]
  }
  const parts = o?.candidates?.[0]?.content?.parts ?? []
  return parts
    .filter((p) => p.thought === true && typeof p.text === "string")
    .map((p) => p.text as string)
    .join("")
}

const STREAM_REASONING: Record<PlaygroundDialect, (obj: unknown) => string> = {
  openai: openaiStreamReasoning,
  anthropic: anthropicStreamReasoning,
  gemini: geminiStreamReasoning,
}

/**
 * The message carried by an `{"error": ...}` frame, in either of the shapes
 * the wires use: an object with a message, or a bare string.
 */
function errorOf(obj: unknown): string | undefined {
  const o = obj as { error?: unknown }
  if (o === null || typeof o !== "object" || !("error" in o)) return undefined
  const err = o.error
  if (typeof err === "string") return err
  if (err && typeof err === "object" && "message" in err) {
    const message = (err as { message?: unknown }).message
    if (typeof message === "string") return message
  }
  return "the provider returned an error"
}

/** Where the first complete frame ends: at a blank line, whichever line
 *  ending the writer used. Returns the boundary and its width. */
function frameEnd(buffer: string): { at: number; width: number } | null {
  const lf = buffer.indexOf("\n\n")
  const crlf = buffer.indexOf("\r\n\r\n")
  if (lf < 0 && crlf < 0) return null
  if (crlf >= 0 && (lf < 0 || crlf < lf)) return { at: crlf, width: 4 }
  return { at: lf, width: 2 }
}

/** The payload of one frame: its `data` lines joined with newlines, as the
 *  SSE spec says. `data:` with no space is the same field. */
function framePayload(frame: string): string | null {
  const data: string[] = []
  for (const raw of frame.split(/\r?\n/)) {
    if (!raw.startsWith("data:")) continue
    const value = raw.slice(5)
    data.push(value.startsWith(" ") ? value.slice(1) : value)
  }
  return data.length === 0 ? null : data.join("\n")
}

/**
 * Reads the assistant's reply out of whatever complete SSE frames have
 * arrived, in the wire shape the request's own dialect streams in.
 *
 * `text` is the answer and `reasoning` is the model's own working, kept apart
 * because they are different things to read: splicing the two into one blob
 * makes a transcript where the answer begins several paragraphs in.
 *
 * `error` is set when a frame carried `{"error": ...}` — a provider failing
 * mid-stream — so the run can end as a failure rather than as a short answer.
 */
export function drainSSE(
  buffer: string,
  dialect: PlaygroundDialect = "openai",
): { text: string; reasoning: string; rest: string; error?: string } {
  let text = ""
  let reasoning = ""
  let error: string | undefined
  let rest = buffer
  const extract = STREAM_DELTA[dialect]
  const extractReasoning = STREAM_REASONING[dialect]
  for (;;) {
    const end = frameEnd(rest)
    if (end === null) break
    const frame = rest.slice(0, end.at)
    rest = rest.slice(end.at + end.width)
    const payload = framePayload(frame)
    if (payload === null || payload === "[DONE]") continue
    try {
      const frameBody: unknown = JSON.parse(payload)
      error ??= errorOf(frameBody)
      text += extract(frameBody)
      reasoning += extractReasoning(frameBody)
    } catch {
      // A frame that is not JSON is a provider quirk, not a client error.
      // Skipping it beats aborting a stream that is otherwise fine.
    }
  }
  return error === undefined ? { text, reasoning, rest } : { text, reasoning, rest, error }
}

// The unary (stream: false) counterpart: one complete JSON document rather
// than SSE frames, in each dialect's non-streaming WriteResponse shape.

/** OpenAI: `choices[0].message.content`, `null` when the turn had no text. */
function openaiUnaryText(obj: unknown): string {
  const o = obj as { choices?: { message?: { content?: string | null } }[] }
  const content = o?.choices?.[0]?.message?.content
  return typeof content === "string" ? content : ""
}

/** Anthropic: `content` is a flat array of typed blocks; only `text` blocks
 *  answer the prompt. */
function anthropicUnaryText(obj: unknown): string {
  const o = obj as { content?: { type?: string; text?: string }[] }
  return (o?.content ?? [])
    .filter((b) => b.type === "text" && typeof b.text === "string")
    .map((b) => b.text as string)
    .join("")
}

/** Gemini: the same `candidates[0].content.parts[]` shape as the streamed
 *  form, just delivered whole instead of one chunk at a time. */
function geminiUnaryText(obj: unknown): string {
  return geminiStreamDelta(obj)
}

const UNARY_TEXT: Record<PlaygroundDialect, (obj: unknown) => string> = {
  openai: openaiUnaryText,
  anthropic: anthropicUnaryText,
  gemini: geminiUnaryText,
}

/** OpenAI: the same two spellings as the streamed form, written whole. */
function openaiUnaryReasoning(obj: unknown): string {
  const o = obj as {
    choices?: { message?: { reasoning_content?: string | null; reasoning?: string | null } }[]
  }
  const message = o?.choices?.[0]?.message
  const named = typeof message?.reasoning_content === "string" ? message.reasoning_content : ""
  const bare = typeof message?.reasoning === "string" ? message.reasoning : ""
  return named + bare
}

/** Anthropic: the `thinking` blocks among the flat typed content array. */
function anthropicUnaryReasoning(obj: unknown): string {
  const o = obj as { content?: { type?: string; thinking?: string }[] }
  return (o?.content ?? [])
    .filter((b) => b.type === "thinking" && typeof b.thinking === "string")
    .map((b) => b.thinking as string)
    .join("")
}

/** Gemini: the same thought parts as the streamed form, delivered whole. */
function geminiUnaryReasoning(obj: unknown): string {
  return geminiStreamReasoning(obj)
}

const UNARY_REASONING: Record<PlaygroundDialect, (obj: unknown) => string> = {
  openai: openaiUnaryReasoning,
  anthropic: anthropicUnaryReasoning,
  gemini: geminiUnaryReasoning,
}

/** Reads the assistant text out of a complete, non-streamed response body. */
export function extractUnaryText(dialect: PlaygroundDialect, body: string): string {
  try {
    return UNARY_TEXT[dialect](JSON.parse(body))
  } catch {
    return ""
  }
}

/** The model's own working out of a complete, non-streamed response body. */
export function extractUnaryReasoning(dialect: PlaygroundDialect, body: string): string {
  try {
    return UNARY_REASONING[dialect](JSON.parse(body))
  } catch {
    return ""
  }
}
