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

/** Reads the assistant text out of whatever complete SSE frames have
 *  arrived, in the wire shape the request's own dialect streams in. */
export function drainSSE(
  buffer: string,
  dialect: PlaygroundDialect = "openai",
): { text: string; rest: string } {
  let text = ""
  let rest = buffer
  const extract = STREAM_DELTA[dialect]
  for (;;) {
    const i = rest.indexOf("\n\n")
    if (i < 0) break
    const frame = rest.slice(0, i)
    rest = rest.slice(i + 2)
    for (const line of frame.split("\n")) {
      if (!line.startsWith("data: ")) continue
      const payload = line.slice(6)
      if (payload === "[DONE]") continue
      try {
        text += extract(JSON.parse(payload))
      } catch {
        // A frame that is not JSON is a provider quirk, not a client error.
        // Skipping it beats aborting a stream that is otherwise fine.
      }
    }
  }
  return { text, rest }
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

/** Reads the assistant text out of a complete, non-streamed response body. */
export function extractUnaryText(dialect: PlaygroundDialect, body: string): string {
  try {
    return UNARY_TEXT[dialect](JSON.parse(body))
  } catch {
    return ""
  }
}
