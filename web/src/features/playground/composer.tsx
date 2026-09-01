import { useState } from "react"
import { Button } from "darkraise-ui/components/button"
import { Textarea } from "darkraise-ui"
import { Send, Square } from "lucide-react"

/** How tall the field is with nothing in it, and how tall it is allowed to
 *  get. Three lines because a prompt is rarely one, and a box that starts at
 *  one line asks for a sentence when the answer is a paragraph. Twelve
 *  because past that the transcript disappears behind the thing writing to
 *  it. */
const MIN_ROWS = 3
const MAX_ROWS = 12

/** Grow the field with its content, between MIN_ROWS and MAX_ROWS.
 *
 *  Measured off the element rather than hardcoded in pixels: the font-size
 *  axis rebinds the text scale, so twelve lines is a different height for an
 *  operator who set the console to extra-large. jsdom reports no computed
 *  metrics at all, hence a fallback for each reading. */
function autosize(el: HTMLTextAreaElement) {
  const style = getComputedStyle(el)
  const line = parseFloat(style.lineHeight) || 20
  const frame =
    (parseFloat(style.paddingTop) || 0) +
    (parseFloat(style.paddingBottom) || 0) +
    (parseFloat(style.borderTopWidth) || 0) +
    (parseFloat(style.borderBottomWidth) || 0)
  el.style.height = "auto"
  const wanted = el.scrollHeight + (parseFloat(style.borderTopWidth) || 0) +
    (parseFloat(style.borderBottomWidth) || 0)
  const min = line * MIN_ROWS + frame
  const max = line * MAX_ROWS + frame
  el.style.height = `${Math.min(Math.max(wanted, min), max)}px`
}

export function Composer({
  model,
  busy,
  error,
  toolsError,
  disabled = false,
  onSend,
  onStop,
}: {
  model: string
  busy: boolean
  error: string
  toolsError?: string
  disabled?: boolean
  onSend: (prompt: string) => void
  onStop: () => void
}) {
  const [draft, setDraft] = useState("")
  const blocked = disabled || model === "" || draft.trim() === "" || toolsError !== undefined

  return (
    <div className="flex shrink-0 flex-col gap-2">
      {/* The send control sits inside the field rather than beside it: the
          field is now three lines tall by default, and a button aligned to
          the foot of a growing box drifts further from the text it sends the
          longer the prompt gets. */}
      <div className="relative">
        <Textarea
          aria-label="Message"
          placeholder={model === "" ? "Choose a model first" : "Ask the router something"}
          value={draft}
          disabled={disabled}
          rows={MIN_ROWS}
          onChange={(e) => setDraft(e.target.value)}
          ref={(el) => {
            if (el) autosize(el)
          }}
          // Enter sends, Shift+Enter makes a newline — which is why this is
          // a Textarea now and not a single-line Input.
          onKeyDown={(e) => {
            if (e.key !== "Enter" || e.shiftKey) return
            if (busy || blocked) return
            e.preventDefault()
            onSend(draft)
            setDraft("")
          }}
          className="w-full resize-none pr-14"
        />
        <div className="absolute right-2 bottom-2">
          {busy ? (
            <Button variant="secondary" size="icon" onClick={onStop} aria-label="Stop generating">
              <Square className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
            </Button>
          ) : (
            <Button
              size="icon"
              aria-label="Send"
              onClick={() => {
                onSend(draft)
                setDraft("")
              }}
              disabled={blocked}
            >
              <Send className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
            </Button>
          )}
        </div>
      </div>

      {/* Drawn only when it has something to say. The row used to hold a
          reserved blank line under every composer, which reads as an
          unfinished panel rather than as space kept for a failure that has
          not happened. Starting a new conversation used to live here too;
          it is the icon on the conversations panel's own header now. */}
      {(toolsError || error) && (
        <p className="text-sm text-destructive">{toolsError || error}</p>
      )}
    </div>
  )
}
