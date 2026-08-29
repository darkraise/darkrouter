import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Textarea } from "darkraise-ui/components/textarea"
import { stream, type StreamStart } from "../../lib/api"
import { chatBody } from "./lib/request"
import { drainSSE } from "./lib/stream"
import type { PlaygroundConfig } from "./config"
import { ModelCombobox, useModelCandidates } from "../shell/model-combobox"
import type { PlaygroundMessage } from "../../lib/api-types"

type Side = {
  model: string
  text: string
  requestId: string
  error: string
  busy: boolean
  latencyMs: number | undefined
}

function emptySide(model: string): Side {
  return { model, text: "", requestId: "", error: "", busy: false, latencyMs: undefined }
}

async function runSide(
  model: string,
  prompt: string,
  config: PlaygroundConfig,
  setSide: (fn: (s: Side) => Side) => void,
): Promise<void> {
  const started = performance.now()
  setSide((s) => ({ ...emptySide(s.model), busy: true }))
  let buffer = ""
  try {
    const turns: PlaygroundMessage[] = [{ role: "user", content: prompt }]
    for await (const chunk of stream(
      "/api/playground",
      // The shared settings, with only the model differing between sides:
      // comparing two models under two system prompts would answer a question
      // nobody asked.
      chatBody({ ...config, model, stream: true, messages: turns }),
      (s: StreamStart) => setSide((cur) => ({ ...cur, requestId: s.requestId })),
    )) {
      buffer += chunk
      const { text, rest } = drainSSE(buffer, config.dialect)
      buffer = rest
      if (text) setSide((cur) => ({ ...cur, text: cur.text + text }))
    }
  } catch (err) {
    setSide((cur) => ({ ...cur, error: (err as Error).message }))
  } finally {
    setSide((cur) => ({ ...cur, busy: false, latencyMs: performance.now() - started }))
  }
}

function SidePanel({
  side,
  onModel,
  candidates,
  loading,
  label,
}: {
  side: Side
  onModel: (model: string) => void
  candidates: string[]
  loading?: boolean
  label: string
}) {
  return (
    <Card className="flex flex-col gap-3 p-4">
      <ModelCombobox
        label={label}
        value={side.model}
        onChange={onModel}
        candidates={candidates}
        loading={loading}
        className="w-full"
      />
      <div className="min-h-24 rounded border p-3 text-sm font-mono whitespace-pre-wrap">{side.text}</div>
      {side.error ? <p className="text-destructive text-sm">{side.error}</p> : null}
      <div className="flex items-center gap-3 text-sm text-[hsl(var(--muted-foreground))]">
        {side.latencyMs !== undefined ? <span>{Math.round(side.latencyMs)} ms</span> : null}
        {side.requestId ? (
          <Link to="/requests/$id" params={{ id: side.requestId }} className="underline">
            View the trace for this request
          </Link>
        ) : null}
      </div>
    </Card>
  )
}

/** Two models against the same prompt, run concurrently through the exact
 *  request chat sends — chatBody is shared rather than rebuilt, so a
 *  difference in the transcripts reflects the models, not a second,
 *  slightly different request shape. */
export function Compare({ config }: { config: PlaygroundConfig }) {
  const [prompt, setPrompt] = useState("")
  const [left, setLeft] = useState<Side>(() => emptySide(""))
  const [right, setRight] = useState<Side>(() => emptySide(""))

  const { candidates, loading } = useModelCandidates()
  const busy = left.busy || right.busy
  const canRun = !busy && prompt !== "" && left.model !== "" && right.model !== ""

  function run() {
    if (!canRun) return
    void runSide(left.model, prompt, config, setLeft)
    void runSide(right.model, prompt, config, setRight)
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-6">
      <Textarea placeholder="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
      <Button onClick={run} disabled={!canRun}>
        {busy ? "Running…" : "Run"}
      </Button>
      <div className="grid gap-4 sm:grid-cols-2">
        <SidePanel
          side={left}
          label="Left model or alias"
          candidates={candidates}
          loading={loading}
          onModel={(model) => setLeft((s) => ({ ...s, model }))}
        />
        <SidePanel
          side={right}
          label="Right model or alias"
          candidates={candidates}
          loading={loading}
          onModel={(model) => setRight((s) => ({ ...s, model }))}
        />
      </div>
    </div>
  )
}
