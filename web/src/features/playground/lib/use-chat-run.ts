import { useEffect, useRef, useState } from "react"
import { api, stream, type StreamStart } from "../../../lib/api"
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
import type { PlaygroundMessage, RequestTrace } from "../../../lib/api-types"

/**
 * One turn that finished, reported to whoever wants to keep it.
 *
 * The hook does not store anything itself. Lab's Single tab and Chat mode
 * share this loop and disagree about persistence -- section 8.2 says Lab's
 * tabs persist nothing -- so the decision belongs at the call site, and a
 * callback is the smallest thing that can carry it.
 */
export type CompletedTurn = {
  prompt: string
  answer: string
  /** Empty when the response carried no id. */
  requestId: string
}

export type ChatRun = {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  error: string
  send: (prompt: string) => Promise<void>
  stop: () => void
  clear: () => void
  /** Replaces the transcript wholesale, for a conversation reopened from the
   *  history rail. */
  load: (messages: PlaygroundMessage[], routes: Record<number, TurnRoute>) => void
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
  onTurn?: (turn: CompletedTurn) => void,
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
  // Bumped by anything that replaces the transcript, so a run that is still
  // waiting on its trace cannot write into the state that succeeded it.
  // stop() deliberately does not bump: a stopped run keeps its half answer
  // and still reports it.
  const generation = useRef(0)

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
    const myGeneration = generation.current
    const superseded = () => generation.current !== myGeneration
    // The rendered text lives in a functional setState, so it is never in a
    // variable this scope can read. A completed turn has to be reported with
    // its whole answer, so it is accumulated here as well as appended.
    let answer = ""
    let failed = false
    let aborted = false
    const emit = (text: string) => {
      if (superseded()) return
      if (!text) return
      answer += text
      appendToLastMessage(text)
    }
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
          emit(text)
        }
      }
      if (!doStream) {
        emit(extractUnaryText(dialect, buffer.current))
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
        if (trace && !superseded()) {
          onMetrics(metricsFromTrace(measured, trace))
          // The route lands under the turn it served, so a transcript of six
          // answers says which provider produced each rather than only the
          // last.
          setRoutes((prev) => ({ ...prev, [answerAt]: routeFromTrace(trace) }))
        }
      }
    } catch (err) {
      // Stopping is the operator's decision, not a failure to report at them.
      if ((err as Error).name === "AbortError") {
        aborted = true
      } else {
        setError((err as Error).message)
        failed = true
      }
    } finally {
      abort.current = null
      setBusy(false)
    }
    // After the finally, so a stopped run still reports: the turns already
    // written stay, and a half answer is what the tokens were spent on.
    //
    // Reported when there is something the operator can see, or when the run
    // simply finished. A hard failure with nothing streamed has no turn worth
    // keeping; a failure part-way through has the text it already spent
    // tokens on, and the transcript still shows it.
    //
    // A stop before the first token is neither: nothing was streamed and
    // nothing failed, and storing it puts an assistant row with no content
    // into the conversation, which is re-rendered as an empty bubble every
    // time it is reopened. A run that finished on its own with an empty
    // answer is still kept -- that is the provider's answer, not an absence.
    if (!superseded() && (answer !== "" || (!failed && !aborted))) {
      onTurn?.({ prompt, answer, requestId: liveRequestId })
    }
  }

  // Leaving the surface is as much an end to the run as pressing Stop. Both
  // callers -- Chat mode and Lab's Single tab -- can be navigated away from
  // mid-answer, and the stream would otherwise keep arriving into state that
  // has been unmounted.
  //
  // It inherits Stop's semantics too, which is a behaviour change worth
  // naming: onTurn fires with whatever had streamed, so navigating away
  // mid-answer persists the partial turn rather than discarding it. A half
  // answer is still an answer, and the tokens were spent either way.
  useEffect(() => () => abort.current?.abort(), [])

  /** Ends the run in flight. The turns already written stay: a half answer is
   *  still an answer, and it is what the tokens were spent on. */
  function stop() {
    abort.current?.abort()
  }

  function clear() {
    generation.current++
    setMessages([])
    setRoutes({})
    setError("")
    onMetrics(NO_METRICS)
  }

  /** Aborts anything in flight first: a stream left running would append its
   *  next chunk into the transcript that has just replaced it. */
  /**
   * Fills in what a restored turn's stored request id already points at.
   *
   * A turn read back from the store knows only its request id, so without
   * this a reopened conversation cannot say who served it, how long it took
   * or what it cost -- the operator has to follow the trace link out to
   * Requests for every answer. The trace is long written by now, so this is a
   * plain fetch rather than traceWhenWritten's wait-and-retry, which exists
   * for a run whose record is still in the log writer's batch.
   */
  async function hydrate(nextRoutes: Record<number, TurnRoute>, mine: number) {
    const unresolved = Object.entries(nextRoutes).filter(
      ([, r]) => r.requestId !== "" && r.provider === "",
    )
    await Promise.all(
      unresolved.map(async ([index, r]) => {
        try {
          const trace = await api.get<RequestTrace>(`/api/requests/${r.requestId}`)
          if (!trace) return
          // The transcript has been replaced since this went out, so this
          // trace belongs to a conversation the operator has already left.
          if (generation.current !== mine) return
          setRoutes((prev) => ({ ...prev, [Number(index)]: routeFromTrace(trace) }))
        } catch {
          // Swept by log retention, or never written. The turn keeps the mark
          // it has rather than inventing numbers for it.
        }
      }),
    )
  }

  function load(next: PlaygroundMessage[], nextRoutes: Record<number, TurnRoute>) {
    generation.current++
    abort.current?.abort()
    const mine = generation.current
    setMessages(next)
    setRoutes(nextRoutes)
    setError("")
    onMetrics(NO_METRICS)
    void hydrate(nextRoutes, mine)
  }

  return { messages, routes, busy, error, send, stop, clear, load }
}
