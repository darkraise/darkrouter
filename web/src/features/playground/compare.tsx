import { useEffect, useRef, useState } from "react"
import { Button, Textarea } from "darkraise-ui"
import { Plus } from "lucide-react"
import { ConfigPane } from "./config-pane/config-pane"
import { stream, type StreamStart } from "../../lib/api"
import { chatBody } from "./lib/request"
import { drainSSE } from "./lib/stream"
import { traceWhenWritten } from "./metrics"
import { routeFromTrace } from "./message"
import type { PlaygroundConfig } from "./config"
import { CompareColumn, emptyColumn, type Column } from "./compare-column"
import { useModelCandidates } from "../shell/model-combobox"
import type { PlaygroundMessage } from "../../lib/api-types"

/** Past four, no column is wide enough to read a wrapped answer in, and the
 *  comparison the screen exists for stops being possible. */
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
      // nobody asked.
      chatBody({ ...config, model, stream: true, messages: turns }),
      (s: StreamStart) => {
        requestId = s.requestId
        update((c) => ({ ...c, requestId: s.requestId }))
      },
      signal,
    )) {
      buffer += chunk
      const { text, rest, error } = drainSSE(buffer, config.dialect)
      buffer = rest
      if (text) update((c) => ({ ...c, text: c.text + text }))
      if (error !== undefined) throw new Error(error)
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
    // An abort is the column being removed or the run being replaced, not a
    // provider failing. It leaves nothing behind: the column is either gone
    // or about to be reset by the run that replaced this one.
    if ((err as Error).name === "AbortError") {
      update((c) => ({
        ...c,
        status: "stopped",
        latencyMs: performance.now() - started,
      }))
      return
    }
    update((c) => ({
      ...c,
      error: (err as Error).message,
      status: "error",
      latencyMs: performance.now() - started,
    }))
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
  const canRun = !busy && prompt !== "" && columns.every((c) => c.model !== "")

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
    <div className="flex min-h-0 flex-1 flex-col lg:flex-row">
      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-4 overflow-y-auto p-6">
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
                ? "Four is the most that stays readable side by side"
                : "Compare another model against the same prompt"
            }
            onClick={() => setColumns((cs) => [...cs, emptyColumn(`c${counter.current++}`)])}
          >
            <Plus className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
            Add model
          </Button>
          {atCap ? (
            <span className="text-sm text-[hsl(var(--legend))]">
              Four is the most that stays readable side by side.
            </span>
          ) : null}
          <Button className="ml-auto" onClick={run} disabled={!canRun}>
            {busy ? "Running…" : "Run"}
          </Button>
        </div>

        {/* Columns have a floor and the row scrolls past it. Left to shrink
            freely, a fourth column on a narrow screen squeezes the model
            combobox down to its chevron, and a comparison whose columns no
            longer say which model they ran is worse than one that scrolls. */}
        <div className="overflow-x-auto">
          <div
            className="grid gap-4"
            style={{ gridTemplateColumns: `repeat(${columns.length}, minmax(14rem, 1fr))` }}
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
      </div>

      {/* Every column is sent under these, which is what makes the comparison
          one. The model field is off: naming a single model here would be a
          control contradicting the four beside it.

          The column is drawn here rather than by the pane. The pane is three
          screens' worth of fields and one screen's worth of chrome would have
          to be wrong on two of them. */}
      <aside className="flex w-full shrink-0 flex-col gap-4 overflow-y-auto border-l p-4 lg:w-80">
        <ConfigPane config={config} onChange={onConfigChange} showModel={false} locked={busy} />
      </aside>
    </div>
  )
}
