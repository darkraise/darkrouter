import { useEffect, useRef } from "react"
import { AssistantTurn, UserTurn, type TurnRoute } from "./message"
import type { PlaygroundMessage } from "../../lib/api-types"

export function Transcript({
  messages,
  routes,
  busy,
  model,
  seedNote,
}: {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  model: string
  seedNote?: string
}) {
  const scroller = useRef<HTMLDivElement | null>(null)
  const foot = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!busy) return
    const el = scroller.current
    if (!el) return
    if (nearBottom(el)) foot.current?.scrollIntoView({ block: "end" })
  }, [messages, busy])

  return (
    <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
      {seedNote ? (
        <p className="pb-4 text-sm text-[hsl(var(--muted-foreground))]">{seedNote}</p>
      ) : null}

      {messages.length === 0 ? (
        <EmptyChat model={model} />
      ) : (
        <div className="flex flex-col gap-6">
          {messages.map((m, i) =>
            m.role === "user" ? (
              <UserTurn key={i} text={m.content} />
            ) : (
              <AssistantTurn
                key={i}
                text={m.content}
                route={routes[i]}
                // Only the last turn can still be arriving.
                streaming={busy && i === messages.length - 1}
              />
            ),
          )}
        </div>
      )}

      <div ref={foot} aria-hidden="true" />
    </div>
  )
}

export function nearBottom(_el: HTMLElement): boolean {
  return true
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
    <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
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
