import { useEffect, useRef } from "react"
import { Button } from "darkraise-ui"
import { AssistantTurn, UserTurn, type TurnRoute } from "./message"
import type { TurnThinking } from "./lib/use-chat-run"
import type { PlaygroundMessage } from "../../lib/api-types"

export function Transcript({
  messages,
  epoch,
  routes,
  thinking = {},
  busy,
  model,
  seedNote,
  quiet = false,
  onChooseModel,
}: {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  /** The model's own working per answer, where it sent any. */
  thinking?: Record<number, TurnThinking>
  busy: boolean
  /** From the chat run; part of every turn's key. */
  epoch?: number
  model: string
  seedNote?: string
  quiet?: boolean
  /** Opens wherever the model is chosen. Offered by the empty state, which is
   *  the moment an operator is looking for it. */
  onChooseModel?: () => void
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
        <EmptyChat model={model} onChooseModel={onChooseModel} />
      ) : (
        <div className="flex flex-col gap-6">
          {messages.map((m, i) =>
            m.role === "user" ? (
              <UserTurn key={`${epoch ?? 0}:${i}`} text={m.content} />
            ) : (
              <AssistantTurn
                key={`${epoch ?? 0}:${i}`}
                text={m.content}
                route={routes[i]}
                thinking={thinking[i]}
                // Only the last turn can still be arriving.
                streaming={busy && i === messages.length - 1}
                quiet={quiet}
              />
            ),
          )}
        </div>
      )}

      <div ref={foot} aria-hidden="true" />
    </div>
  )
}

/** How far from the bottom still counts as reading the newest text. Roughly a
 *  few lines: enough that one wheel notch does not detach the follow, small
 *  enough that scrolling up to re-read does. */
const FOLLOW_SLACK_PX = 160

/**
 * Whether the reader is at the bottom of the transcript.
 *
 * Read off the scrolling element, which is this component's own container.
 * The window is not it and never was: darkraise-ui's layout root is
 * `h-screen overflow-hidden`, so `window.scrollY` is pinned at 0 and the
 * previous window-based test was true on every render — the follow could not
 * decline, and reading something earlier while an answer streamed pulled you
 * straight back down.
 */
export function nearBottom(el: HTMLElement): boolean {
  return el.scrollTop + el.clientHeight >= el.scrollHeight - FOLLOW_SLACK_PX
}

/**
 * The chat before anything has been said.
 *
 * An empty panel cannot be told apart from one that failed to load, and the
 * one thing that has to be true before a prompt goes anywhere is that a model
 * is named. So the empty state says exactly that, and nothing else.
 */
function EmptyChat({ model, onChooseModel }: { model: string; onChooseModel?: () => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-2 text-center">
      <p className="text-base font-medium">
        {model === "" ? "Name a model to send to" : `Ready to send to ${model}`}
      </p>
      <p className="max-w-prose text-sm text-[hsl(var(--legend))]">
        {model === ""
          ? "The router resolves a model or alias the same way it resolves one from a client."
          : "Every answer records which provider served it, what it cost, and how long the first token took."}
      </p>
      {model === "" && onChooseModel ? (
        <Button size="sm" className="mt-2" onClick={onChooseModel}>
          Choose a model
        </Button>
      ) : null}
    </div>
  )
}
