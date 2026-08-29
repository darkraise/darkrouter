import { useRef, useState } from "react"
import { stream, type StreamStart } from "../../../lib/api"
import { chatBody, parseTools, type ChatState } from "./request"
import { drainSSE, extractUnaryText } from "./stream"
import {
  NO_METRICS,
  metricsFromTrace,
  traceWhenWritten,
  type StreamMetrics,
} from "../metrics"
import { routeFromTrace, type TurnRoute } from "../message"
import type { PlaygroundConfig } from "../config"
import type { PlaygroundMessage } from "../../../lib/api-types"

export type ChatRun = {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  error: string
  send: (prompt: string) => Promise<void>
  stop: () => void
  clear: () => void
}

/**
 * One chat turn, from send to the trace that explains it.
 *
 * Held as a hook rather than inside a component because every surface that
 * sends needs exactly this and none of them needs a second, slightly
 * different copy of it — two copies of a streaming loop drift, and the
 * drift shows up as one surface reporting timings the other does not.
 */
export function useChatRun(
  config: PlaygroundConfig,
  onMetrics: (m: StreamMetrics) => void,
): ChatRun {
  const [messages, setMessages] = useState<PlaygroundMessage[]>([])
  // Keyed by the index of the assistant turn it belongs to. Kept beside the
  // messages rather than inside them: `messages` is the wire body sent to the
  // gateway, and a field of ours in it would be a field the provider sees.
  const [routes, setRoutes] = useState<Record<number, TurnRoute>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const abort = useRef<AbortController | null>(null)
  const buffer = useRef("")

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

  async function send(prompt: string) {
    const state: ChatState = { ...config, messages }
    const toolsError = parseTools(state.toolsRaw).error
    if (busy || state.model === "" || prompt === "" || toolsError !== undefined) return
    const dialect = state.dialect
    const doStream = state.stream
    const turns = [...state.messages, { role: "user", content: prompt } satisfies PlaygroundMessage]
    // The assistant turn this run will fill in, and the index its route lands
    // under when the trace arrives.
    const answerAt = turns.length
    setMessages([...turns, { role: "assistant", content: "" }])
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

  function clear() {
    setMessages([])
    setRoutes({})
    setError("")
    onMetrics(NO_METRICS)
  }

  return { messages, routes, busy, error, send, stop, clear }
}
