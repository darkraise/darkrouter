import { useRef, useState } from "react"
import { Button, Textarea } from "darkraise-ui"
import { stream, type StreamStart } from "../../lib/api"
import { chatBody } from "./lib/request"
import { drainSSE } from "./lib/stream"
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
  update: (fn: (c: Column) => Column) => void,
): Promise<void> {
  const started = performance.now()
  update((c) => ({ ...emptyColumn(c.id), model: c.model, status: "streaming" }))
  let buffer = ""
  try {
    const turns: PlaygroundMessage[] = [{ role: "user", content: prompt }]
    for await (const chunk of stream(
      "/api/playground",
      // The shared settings, with only the model differing between columns:
      // comparing models under two system prompts would answer a question
      // nobody asked.
      chatBody({ ...config, model, stream: true, messages: turns }),
      (s: StreamStart) => update((c) => ({ ...c, requestId: s.requestId })),
    )) {
      buffer += chunk
      const { text, rest } = drainSSE(buffer, config.dialect)
      buffer = rest
      if (text) update((c) => ({ ...c, text: c.text + text }))
    }
    update((c) => ({ ...c, status: "done" }))
  } catch (err) {
    update((c) => ({ ...c, error: (err as Error).message, status: "error" }))
  } finally {
    update((c) => ({ ...c, latencyMs: performance.now() - started }))
  }
}

/** Up to four models against the same prompt, run concurrently through the
 *  exact request chat sends — chatBody is shared rather than rebuilt, so a
 *  difference in the transcripts reflects the models, not a second, slightly
 *  different request shape. */
export function Compare({ config }: { config: PlaygroundConfig }) {
  const counter = useRef(MIN_COLUMNS)
  const [prompt, setPrompt] = useState("")
  const [columns, setColumns] = useState<Column[]>(() => [emptyColumn("c0"), emptyColumn("c1")])

  const { candidates, loading } = useModelCandidates()
  const busy = columns.some((c) => c.status === "streaming")
  const canRun = !busy && prompt !== "" && columns.every((c) => c.model !== "")

  const updateColumn = (id: string, fn: (c: Column) => Column) =>
    setColumns((cs) => cs.map((c) => (c.id === id ? fn(c) : c)))

  function run() {
    if (!canRun) return
    // Started in one pass so they overlap: run sequentially and the latency
    // readings beside them would measure the queue, not the providers.
    for (const column of columns) {
      void runColumn(column.model, prompt, config, (fn) => updateColumn(column.id, fn))
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-6">
      <Textarea placeholder="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
      <div className="flex items-center gap-2">
        <Button onClick={run} disabled={!canRun}>
          {busy ? "Running…" : "Run"}
        </Button>
        <Button
          variant="ghost"
          disabled={columns.length >= MAX_COLUMNS}
          onClick={() => setColumns((cs) => [...cs, emptyColumn(`c${counter.current++}`)])}
        >
          Add a column
        </Button>
      </div>
      <div
        className="grid gap-4"
        style={{ gridTemplateColumns: `repeat(${columns.length}, minmax(0, 1fr))` }}
      >
        {columns.map((column, index) => (
          <CompareColumn
            key={column.id}
            column={column}
            index={index}
            candidates={candidates}
            loading={loading}
            removable={columns.length > MIN_COLUMNS}
            onModel={(model) => updateColumn(column.id, (c) => ({ ...c, model }))}
            onRemove={() => setColumns((cs) => cs.filter((c) => c.id !== column.id))}
          />
        ))}
      </div>
    </div>
  )
}
