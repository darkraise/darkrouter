import { useState } from "react"
import { Button } from "darkraise-ui/components/button"
import { Textarea } from "darkraise-ui"
import { MessageSquarePlus, Send, Square } from "lucide-react"

export function Composer({
  model,
  busy,
  error,
  toolsError,
  canClear,
  onSend,
  onStop,
  onClear,
}: {
  model: string
  busy: boolean
  error: string
  toolsError?: string
  canClear: boolean
  onSend: (prompt: string) => void
  onStop: () => void
  onClear: () => void
}) {
  const [draft, setDraft] = useState("")

  return (
    <div className="flex shrink-0 flex-col gap-2 border-t bg-[hsl(var(--background))] px-6 pt-2 pb-4">
      <div className="flex items-end gap-2">
        <Textarea
          aria-label="Message"
          placeholder={model === "" ? "Choose a model first" : "Ask the router something"}
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
            if (busy || model === "" || draft.trim() === "" || toolsError !== undefined) return
            e.preventDefault()
            onSend(draft)
            setDraft("")
          }}
          className="max-h-60 min-h-10 flex-1 resize-none"
        />
        {busy ? (
          <Button variant="secondary" onClick={onStop} aria-label="Stop generating">
            <Square className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
            Stop
          </Button>
        ) : (
          <Button
            onClick={() => {
              onSend(draft)
              setDraft("")
            }}
            disabled={model === "" || draft.trim() === "" || toolsError !== undefined}
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
        {canClear && !busy ? (
          <button
            type="button"
            onClick={onClear}
            className="flex items-center gap-1.5 text-sm text-[hsl(var(--legend))] underline-offset-2 hover:underline"
          >
            <MessageSquarePlus className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
            New conversation
          </button>
        ) : null}
      </div>
    </div>
  )
}
