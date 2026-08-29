import { useEffect, useRef, useState } from "react"
import { useSearch } from "@tanstack/react-router"
import { Button } from "darkraise-ui/components/button"
import { Textarea } from "darkraise-ui"
import { MessageSquarePlus, Send, Square } from "lucide-react"
import { stream, type StreamStart } from "../../lib/api"
import { useTrace } from "../../lib/queries"
import { DIALECTS, type PlaygroundConfig } from "./config"
import { NO_METRICS, metricsFromTrace, traceWhenWritten, type StreamMetrics } from "./metrics"
import { AssistantTurn, UserTurn, routeFromTrace, type TurnRoute } from "./message"
import type {
  PlaygroundChatBody,
  PlaygroundDialect,
  PlaygroundMessage,
  RequestTrace,
} from "../../lib/api-types"

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

/** The shared request settings plus this surface's own conversation. The
 *  settings live beside the tabs now; the turns belong to the chat. */
export type ChatState = PlaygroundConfig & {
  messages: PlaygroundMessage[]
}

export function parseTools(raw: string): { tools?: Record<string, unknown>[]; error?: string } {
  const trimmed = raw.trim()
  if (trimmed === "") return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch (err) {
    // Named rather than dropped: sending nothing would answer a different
    // question and read as the model ignoring the tools.
    return { error: `tools must be JSON: ${(err as Error).message}` }
  }
  if (!Array.isArray(parsed)) return { error: "tools must be a JSON array" }
  return { tools: parsed as Record<string, unknown>[] }
}

export function chatBody(state: ChatState): PlaygroundChatBody {
  const body: PlaygroundChatBody = {
    model: state.model,
    messages: state.messages,
    stream: state.stream,
    dialect: state.dialect,
  }
  if (state.system !== "") body.system = state.system
  if (state.temperature !== "") body.temperature = Number(state.temperature)
  if (state.maxTokens !== "") body.max_tokens = Number(state.maxTokens)
  const { tools } = parseTools(state.toolsRaw)
  if (tools) body.tools = tools
  return body
}

export function seedFromTrace(trace: RequestTrace): Partial<ChatState> {
  // The model the client asked for, not the one that served: replaying
  // against the serving provider would skip the routing decision, which is
  // usually the thing under investigation.
  const dialect = trace.dialect as PlaygroundDialect
  return {
    model: trace.alias || trace.model,
    // The log records inbound dialects this screen has no control for, the
    // OpenAI Responses wire among them.
    dialect: DIALECTS.includes(dialect) ? dialect : "openai",
  }
}

export function Chat({
  config,
  onConfigChange,
  onMetrics,
}: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
  onMetrics: (m: StreamMetrics) => void
}) {
  const [messages, setMessages] = useState<PlaygroundMessage[]>([])
  const state: ChatState = { ...config, messages }
  const [draft, setDraft] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  // Keyed by the index of the assistant turn it belongs to. Kept beside the
  // messages rather than inside them: `messages` is the wire body sent to the
  // gateway, and a field of ours in it would be a field the provider sees.
  const [routes, setRoutes] = useState<Record<number, TurnRoute>>({})
  const abort = useRef<AbortController | null>(null)
  const [seededFrom, setSeededFrom] = useState<string | undefined>(undefined)
  const buffer = useRef("")
  const foot = useRef<HTMLDivElement | null>(null)

  // Follows a streaming answer down, unless the operator has scrolled up to
  // read something earlier — yanking them back to the bottom mid-sentence is
  // the thing every chat surface gets wrong.
  useEffect(() => {
    if (!busy) return
    const nearBottom =
      window.innerHeight + window.scrollY >= document.body.offsetHeight - 160
    if (nearBottom) foot.current?.scrollIntoView({ block: "end" })
  }, [messages, busy])

  const search = useSearch({ strict: false })
  const seed = search.seed
  const trace = useTrace(seed ?? "", { enabled: seed !== undefined })

  // Applied once per seed: a seed that already produced this run's state
  // must not re-fire and stomp on turns the operator has since typed.
  useEffect(() => {
    if (!trace.data || seed === undefined || seededFrom === seed) return
    onConfigChange({ ...config, ...seedFromTrace(trace.data as RequestTrace) })
    setSeededFrom(seed)
  }, [trace.data, seed, seededFrom])

  const toolsError = parseTools(state.toolsRaw).error

  // A functional update, and it has to be: a stream appends many times inside
  // one render, and a version that read the turns this render closed over
  // would append every chunk to the same stale array — which renders as a
  // transcript that stays empty while the request plainly succeeds.
  function appendToLastMessage(text: string) {
    if (!text) return
    setMessages((prev) => {
      const next = prev.slice()
      const lastIndex = next.length - 1
      const last = next[lastIndex]
      if (!last) return prev
      next[lastIndex] = { ...last, content: last.content + text }
      return next
    })
  }

  async function send() {
    if (busy || state.model === "" || draft === "" || toolsError !== undefined) return
    const dialect = state.dialect
    const doStream = state.stream
    const turns = [...state.messages, { role: "user", content: draft } satisfies PlaygroundMessage]
    // The assistant turn this run will fill in, and the index its route lands
    // under when the trace arrives.
    const answerAt = turns.length
    setMessages([...turns, { role: "assistant", content: "" }])
    setDraft("")
    setError("")
    setBusy(true)
    onMetrics(NO_METRICS)
    buffer.current = ""
    const controller = new AbortController()
    abort.current = controller
    const startedAt = performance.now()
    let ttftMs: number | null = null
    let liveRequestId = ""
    try {
      for await (const chunk of stream(
        "/api/playground",
        chatBody({ ...state, messages: turns }),
        // The id arrives with the headers, before the body this is rendering.
        (s: StreamStart) => {
          liveRequestId = s.requestId
        },
        controller.signal,
      )) {
        buffer.current += chunk
        // With streaming off the executor answers with one JSON document and
        // no SSE framing at all, so there is nothing to drain until the body
        // is complete — handled after the loop instead.
        if (doStream) {
          const { text, rest } = drainSSE(buffer.current, dialect)
          buffer.current = rest
          // Measured on the first text, not the first chunk: a keep-alive or
          // a role-only frame is not the model answering.
          if (text && ttftMs === null) ttftMs = performance.now() - startedAt
          appendToLastMessage(text)
        }
      }
      if (!doStream) {
        appendToLastMessage(extractUnaryText(dialect, buffer.current))
      }
      const totalMs = performance.now() - startedAt
      const measured: StreamMetrics = { ...NO_METRICS, ttftMs, totalMs }
      onMetrics(measured)
      // The token counts are the gateway's, fetched after the fact rather
      // than guessed here: a client-side tokenisation would be a number that
      // looks authoritative and is not. A trace that has not landed yet
      // simply leaves the counts unknown.
      if (liveRequestId) {
        const trace = await traceWhenWritten(liveRequestId)
        if (trace) {
          onMetrics(metricsFromTrace(measured, trace))
          // The route lands under the turn it served, so a transcript of six
          // answers says which provider produced each rather than only the
          // last.
          setRoutes((prev) => ({ ...prev, [answerAt]: routeFromTrace(trace) }))
        }
      }
    } catch (err) {
      // Stopping is the operator's decision, not a failure to report at them.
      if ((err as Error).name !== "AbortError") setError((err as Error).message)
    } finally {
      abort.current = null
      setBusy(false)
    }
  }

  /** Ends the run in flight. The turns already written stay: a half answer is
   *  still an answer, and it is what the tokens were spent on. */
  function stop() {
    abort.current?.abort()
  }

  function clearConversation() {
    setMessages([])
    setRoutes({})
    setError("")
    onMetrics(NO_METRICS)
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      {seed !== undefined ? (
        // capture.bodies has a retention sweep and no writer, so a trace
        // carries no prompt text — the model and dialect are all a seeded
        // run can restore. Stated here rather than left for the operator to
        // discover from a transcript that is silently empty. The three
        // states are worded separately so the note stays true while the
        // trace is still loading or failed to load.
        <p className="text-sm text-[hsl(var(--muted-foreground))]">
          {seededFrom === seed
            ? `Seeded from trace ${seed}: model and dialect carried over. The original prompt was not retained and is not recoverable.`
            : trace.isError
              ? `Trace ${seed} could not be loaded, so nothing was seeded.`
              : `Loading trace ${seed}…`}
        </p>
      ) : null}

      {state.messages.length === 0 ? (
        <EmptyChat model={state.model} />
      ) : (
        <div className="flex flex-col gap-6">
          {state.messages.map((m, i) =>
            m.role === "user" ? (
              <UserTurn key={i} text={m.content} />
            ) : (
              <AssistantTurn
                key={i}
                text={m.content}
                route={routes[i]}
                // Only the last turn can still be arriving.
                streaming={busy && i === state.messages.length - 1}
              />
            ),
          )}
        </div>
      )}

      <div ref={foot} aria-hidden="true" />

      {/* The negative margins pull the background out to the panel's padding
          so the composer covers the page edge. Without them the transcript
          scrolls into the gap beneath it and reads as text behind glass. */}
      <div className="sticky bottom-0 -mx-6 -mb-6 flex flex-col gap-2 bg-[hsl(var(--background))] px-6 pt-2 pb-4">
        <div className="flex items-end gap-2">
          <Textarea
            aria-label="Message"
            placeholder={state.model === "" ? "Choose a model first" : "Ask the router something"}
            value={draft}
            rows={1}
            onChange={(e) => setDraft(e.target.value)}
            // Grows with the prompt instead of scrolling a two-line box: a
            // system prompt pasted in here used to be typed blind. Capped so
            // the transcript never disappears behind the composer.
            ref={(el) => {
              if (!el) return
              el.style.height = "auto"
              el.style.height = `${Math.min(el.scrollHeight, 240)}px`
            }}
            // Enter sends, Shift+Enter makes a newline — which is why this is
            // a Textarea now and not a single-line Input.
            onKeyDown={(e) => {
              if (e.key !== "Enter" || e.shiftKey) return
              if (busy || state.model === "" || draft.trim() === "" || toolsError !== undefined) return
              e.preventDefault()
              void send()
            }}
            className="max-h-60 min-h-10 flex-1 resize-none"
          />
          {busy ? (
            <Button variant="secondary" onClick={stop} aria-label="Stop generating">
              <Square className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
              Stop
            </Button>
          ) : (
            <Button
              onClick={() => void send()}
              disabled={state.model === "" || draft.trim() === "" || toolsError !== undefined}
            >
              <Send className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
              Send
            </Button>
          )}
        </div>

        <div className="flex min-h-5 items-center justify-between gap-3">
          <span className="text-sm text-[hsl(var(--legend))]">
            {toolsError ? <span className="text-destructive">{toolsError}</span> : null}
            {error ? <span className="text-destructive">{error}</span> : null}
          </span>
          {state.messages.length > 0 && !busy ? (
            <button
              type="button"
              onClick={clearConversation}
              className="flex items-center gap-1.5 text-sm text-[hsl(var(--legend))] underline-offset-2 hover:underline"
            >
              <MessageSquarePlus className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
              New conversation
            </button>
          ) : null}
        </div>
      </div>
    </div>
  )
}

/**
 * The chat before anything has been said.
 *
 * An empty panel cannot be told apart from one that failed to load, and the
 * one thing that has to be true before a prompt goes anywhere is that a model
 * is named. So the empty state says exactly that, and nothing else.
 */
function EmptyChat({ model }: { model: string }) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 py-16 text-center">
      <p className="text-base font-medium">
        {model === "" ? "Name a model to send to" : `Ready to send to ${model}`}
      </p>
      <p className="max-w-prose text-sm text-[hsl(var(--legend))]">
        {model === ""
          ? "Pick a model or alias in the request settings. The router resolves it the same way it resolves one from a client."
          : "Every answer records which provider served it, what it cost, and how long the first token took."}
      </p>
    </div>
  )
}
