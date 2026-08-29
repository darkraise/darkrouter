import { useEffect, useState } from "react"
import { useSearch } from "@tanstack/react-router"
import { useTrace } from "../../lib/queries"
import type { PlaygroundConfig } from "./config"
import type { StreamMetrics } from "./metrics"
import { parseTools, seedFromTrace } from "./lib/request"
import { useChatRun } from "./lib/use-chat-run"
import type { RequestTrace } from "../../lib/api-types"
import { Transcript } from "./transcript"
import { Composer } from "./composer"

export function ChatTab({
  config,
  onConfigChange,
  onMetrics,
}: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
  onMetrics: (m: StreamMetrics) => void
}) {
  const { messages, routes, busy, error, send, stop, clear } = useChatRun(config, onMetrics)
  const [seededFrom, setSeededFrom] = useState<string | undefined>(undefined)

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

  const seedNote =
    seed !== undefined
      ? // capture.bodies has a retention sweep and no writer, so a trace
        // carries no prompt text — the model and dialect are all a seeded
        // run can restore. Stated here rather than left for the operator to
        // discover from a transcript that is silently empty. The three
        // states are worded separately so the note stays true while the
        // trace is still loading or failed to load.
        seededFrom === seed
        ? `Seeded from trace ${seed}: model and dialect carried over. The original prompt was not retained and is not recoverable.`
        : trace.isError
          ? `Trace ${seed} could not be loaded, so nothing was seeded.`
          : `Loading trace ${seed}…`
      : undefined

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Transcript
        messages={messages} routes={routes} busy={busy}
        model={config.model} seedNote={seedNote}
      />
      <Composer
        model={config.model} busy={busy} error={error}
        toolsError={parseTools(config.toolsRaw).error}
        canClear={messages.length > 0}
        onSend={(p) => void send(p)} onStop={stop} onClear={clear}
      />
    </div>
  )
}
