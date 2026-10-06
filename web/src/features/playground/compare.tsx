import { useEffect, useRef, useState } from "react"
import { Button, Textarea } from "darkraise-ui"
import { Plus, Square } from "lucide-react"
import { ConfigPane } from "./config-pane/config-pane"
import { stream, type StreamStart } from "../../lib/api"
import { chatBody, requestProblem } from "./lib/request"
import { drainSSE, extractUnaryText } from "./lib/stream"
import { traceWhenWritten } from "./metrics"
import { attemptFailure, routeFromTrace } from "./message"
import type { PlaygroundConfig } from "./config"
import { CompareColumn, emptyColumn, type Column } from "./compare-column"
import { useModelCandidates } from "../shell/model-combobox"
import type { PlaygroundMessage } from "../../lib/api-types"

/** Past four, the answers stop being something a reader can hold against
 *  each other, and the comparison the screen exists for stops being possible.
 *  Not a promise that four fit on one row: on a narrow screen they wrap. */
export const MAX_COLUMNS = 4

/** Two is the comparison the screen is named for. */
const MIN_COLUMNS = 2

async function runColumn(
  model: string,
  prompt: string,
  config: PlaygroundConfig,
  signal: AbortSignal,
  update: (fn: (c: Column) => Column) => void,
): Promise<void> {
  const started = performance.now()
  update((c) => ({ ...emptyColumn(c.id), model, status: "streaming" }))
  let buffer = ""
  let requestId = ""
  try {
    const turns: PlaygroundMessage[] = [{ role: "user", content: prompt }]
    for await (const chunk of stream(
      "/api/playground",
      // The shared settings, with only the model differing between columns:
      // comparing models under two system prompts would answer a question
      // nobody asked. That includes "Stream the reply", which the pane
      // offers here as it does in Chat; a switch that reads off while every
      // column streams is a control that does nothing.
      chatBody({ ...config, model, messages: turns }),
      (s: StreamStart) => {
        requestId = s.requestId
        update((c) => ({ ...c, requestId: s.requestId }))
      },
      signal,
    )) {
      buffer += chunk
      // A unary reply is one JSON document with no SSE framing, so there is
      // nothing to drain until it is complete -- handled after the loop, as
      // useChatRun does.
      if (!config.stream) continue
      const { text, rest, error } = drainSSE(buffer, config.dialect)
      buffer = rest
      if (text) update((c) => ({ ...c, text: c.text + text }))
      if (error !== undefined) throw new Error(error)
    }
    if (!config.stream) {
      const text = extractUnaryText(config.dialect, buffer)
      update((c) => ({ ...c, text }))
    }
    update((c) => ({ ...c, status: "done", latencyMs: performance.now() - started }))
    // The counts are the gateway's, read off the trace once the log writer
    // has it, the same way a chat turn's are. Priced across every attempt,
    // so a column that failed over reports what the whole run cost.
    if (requestId !== "") {
      const trace = await traceWhenWritten(requestId, signal)
      if (trace && !signal.aborted) {
        const route = routeFromTrace(trace)
        update((c) => ({
          ...c,
          tokensIn: route.tokensIn,
          tokensOut: route.tokensOut,
          costMicros: route.costMicros,
        }))
      }
    }
  } catch (err) {
    // An abort is the operator stopping, the column being removed or the run
    // being replaced -- not a provider failing.
    const aborted = (err as Error).name === "AbortError"
    update((c) => ({
      ...c,
      ...(aborted ? {} : { error: (err as Error).message }),
      status: aborted ? "stopped" : "error",
      latencyMs: performance.now() - started,
    }))
    // A stopped or failed column still has a trace, and its counts and the
    // status its provider sent back are the reading that explains it. Waited
    // for apart from `signal`, which is already aborted after a stop. Written
    // only while the column still shows this request: a rerun has reset it,
    // and a removed column is simply not found.
    if (requestId === "") return
    const trace = await traceWhenWritten(requestId)
    if (!trace) return
    const route = routeFromTrace(trace)
    const why = aborted ? undefined : attemptFailure(trace)
    update((c) =>
      c.requestId !== requestId
        ? c
        : {
            ...c,
            tokensIn: route.tokensIn,
            tokensOut: route.tokensOut,
            costMicros: route.costMicros,
            error: why && !c.error.includes(why) ? `${c.error} (${why})` : c.error,
          },
    )
  }
}

/**
 * Up to four models against the same prompt, run concurrently through the
 * exact request chat sends — chatBody is shared rather than rebuilt, so a
 * difference in the transcripts reflects the models, not a second, slightly
 * different request shape.
 *
 * The count of models is stated above the control that changes it. Adding a
 * third model was always possible and never visible: "Add a column" sat as a
 * ghost button beside Run, said nothing about how many were already being
 * compared, and gave no reason when it stopped working at four. The cap is
 * the readable width of a column, so it is worth saying rather than enforcing
 * silently.
 */
export function Compare({
  config,
  onConfigChange,
  active = true,
}: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
  active?: boolean
}) {
  const counter = useRef(MIN_COLUMNS)
  // One per column, so removing a column can stop the request it started.
  // Without this the orphan keeps arriving into state nothing renders, and
  // `busy` — which counts streaming columns — drops while it is still coming.
  const controllers = useRef(new Map<string, AbortController>())
  const [prompt, setPrompt] = useState("")
  const [columns, setColumns] = useState<Column[]>(() => [emptyColumn("c0"), emptyColumn("c1")])

  const { candidates, loading } = useModelCandidates()
  const busy = columns.some((c) => c.status === "streaming")
  // Gated on busy: a second run starting on top of a live one would append
  // two runs' output into the same columns and time both at once.
  const problem = requestProblem(config)
  const canRun =
    !busy && prompt !== "" && columns.every((c) => c.model !== "") && problem === undefined

  const updateColumn = (id: string, fn: (c: Column) => Column) =>
    setColumns((cs) => cs.map((c) => (c.id === id ? fn(c) : c)))

  function abortColumn(id: string) {
    controllers.current.get(id)?.abort()
    controllers.current.delete(id)
  }

  // Removing a column already stops its request; navigating away did not, and
  // an operator who leaves mid-run left four streams arriving into state
  // nothing renders. The ref holds one Map for the component's whole life, so
  // capturing it here is the same Map the cleanup drains.
  useEffect(() => {
    const live = controllers.current
    return () => {
      for (const controller of live.values()) controller.abort()
      live.clear()
    }
  }, [])

  useEffect(() => {
    if (active) return
    for (const id of [...controllers.current.keys()]) abortColumn(id)
  }, [active])

  function stopAll() {
    for (const id of [...controllers.current.keys()]) abortColumn(id)
  }

  function run() {
    if (!canRun) return
    // Defensive: the busy guard means Run cannot fire while a controller is
    // live, so this loop should never have anything to abort. It is what
    // keeps that true if the busy count ever stops covering a case -- which
    // is exactly how the removed-column orphan got loose in the first place.
    for (const id of [...controllers.current.keys()]) abortColumn(id)
    // Started in one pass so they overlap: run sequentially and the latency
    // readings beside them would measure the queue, not the providers.
    for (const column of columns) {
      const controller = new AbortController()
      controllers.current.set(column.id, controller)
      void runColumn(column.model, prompt, config, controller.signal, (fn) =>
        updateColumn(column.id, fn),
      ).finally(() => {
        // Only if it is still this column's controller: a rerun has already
        // replaced the entry, and deleting it would leave the new run
        // unstoppable.
        if (controllers.current.get(column.id) === controller) {
          controllers.current.delete(column.id)
        }
      })
    }
  }

  const atCap = columns.length >= MAX_COLUMNS

  return (
    // One scrolling column below lg, with the settings under the results:
    // two side-by-side scrollers stacked in a fixed height left the answers
    // a 52px window on a phone, behind a request pane that never shrank.
    <div className="flex min-h-0 flex-1 flex-col overflow-y-auto lg:flex-row lg:overflow-hidden">
      <div className="flex min-w-0 shrink-0 flex-col gap-4 p-6 lg:min-h-0 lg:flex-1 lg:shrink lg:overflow-y-auto">
        <Textarea
          aria-label="Prompt"
          placeholder="Prompt"
          rows={3}
          value={prompt}
          disabled={busy}
          onChange={(e) => setPrompt(e.target.value)}
        />

        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm text-[hsl(var(--legend))]">
            Models{" "}
            <span className="tabular-nums text-[hsl(var(--foreground))]">
              {columns.length} / {MAX_COLUMNS}
            </span>
          </span>
          <Button
            variant="outline"
            size="sm"
            disabled={busy || atCap}
            // Said on the control rather than left to be inferred from a
            // button that has quietly stopped responding.
            title={
              atCap
                ? "Four is the most compared at once"
                : "Compare another model against the same prompt"
            }
            onClick={() => setColumns((cs) => [...cs, emptyColumn(`c${counter.current++}`)])}
          >
            <Plus className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
            Add model
          </Button>
          {atCap ? (
            <span className="text-sm text-[hsl(var(--legend))]">
              Four is the most compared at once.
            </span>
          ) : null}
          {/* Beside Run, which it is holding: the pane that shows the same
              message can be scrolled out of view. */}
          {problem !== undefined ? (
            <span className="text-sm text-[hsl(var(--destructive))]">{problem}</span>
          ) : null}
          {/* Stop takes Run's place while it runs, as it does in Chat's
              composer. Without it a column waiting on a hung provider could
              only be abandoned by leaving the tab. */}
          {busy ? (
            <Button className="ml-auto" variant="secondary" onClick={stopAll}>
              <Square className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
              Stop
            </Button>
          ) : (
            <Button className="ml-auto" onClick={run} disabled={!canRun}>
              Run
            </Button>
          )}
        </div>

        {/* Columns have a floor and wrap onto another row past it. Left to
            shrink freely, a fourth column squeezed the model name down to
            eight characters, and two columns both read "lmstudio"; scrolled
            sideways instead, the last column and its error were cut off at
            the edge. A comparison whose columns no longer say which model
            they ran is worse than one on two rows. */}
        <div
          className="grid gap-4"
          style={{ gridTemplateColumns: "repeat(auto-fit, minmax(min(18rem, 100%), 1fr))" }}
        >
          {columns.map((column, index) => (
            <CompareColumn
              key={column.id}
              column={column}
              index={index}
              candidates={candidates}
              loading={loading}
              removable={columns.length > MIN_COLUMNS}
              disabled={busy}
              onModel={(model) => updateColumn(column.id, (c) => ({ ...c, model }))}
              onRemove={() => {
                abortColumn(column.id)
                setColumns((cs) => cs.filter((c) => c.id !== column.id))
              }}
            />
          ))}
        </div>
      </div>

      {/* Every column is sent under these, which is what makes the comparison
          one. The model field is off: naming a single model here would be a
          control contradicting the four beside it.

          The column is drawn here rather than by the pane. The pane is three
          screens' worth of fields and one screen's worth of chrome would have
          to be wrong on two of them. */}
      <aside className="flex w-full shrink-0 flex-col gap-4 border-t p-4 lg:w-80 lg:overflow-y-auto lg:border-t-0 lg:border-l">
        {/* The default lock note is Chat's, about a first message; Compare
            has no conversation, and its lock lifts when the run ends. */}
        <ConfigPane
          config={config}
          onChange={onConfigChange}
          showModel={false}
          locked={busy}
          lockNote="Locked while this comparison runs."
        />
      </aside>
    </div>
  )
}
