import { Button, Card } from "darkraise-ui"
import { MessageSquarePlus, Trash2 } from "lucide-react"
import { relativeTime } from "../../../lib/time"
import type { PlaygroundConversation } from "../../../lib/api-types"

/**
 * Every conversation, in the order the caller supplies.
 *
 * This is the whole reason Chat mode is a mode rather than a tab: a chat
 * surface without retrievable history is a scratchpad, and nobody keeps a
 * scratchpad. Each row carries the title, what the last turn was about, and
 * how long ago — enough to recognise a conversation without opening it.
 *
 * Always on screen, and with no collapse control. The rail is the retrieval;
 * a panel that has to be summoned is one an operator stops reaching for, and
 * the toggle that summoned it cost a control on every screen width to save
 * 260px on one.
 *
 * Rows are inset and rounded rather than full-bleed with a rule under each,
 * which is the console's own list idiom: the sidebar reads this way, and a
 * stack of ruled rows in a narrow column reads as a table that has been
 * squeezed. The one the operator is in carries the sidebar's selected
 * treatment too — tinted, bolder, and a primary bar down its left edge — so
 * "where I am" looks the same in both places.
 */
/**
 * The second line of a row, or nothing when there is no second line worth
 * drawing.
 *
 * A conversation's title is derived from its first prompt and its preview is
 * its most recent one, so a thread that has had one turn has both from the
 * same sentence — and the rail drew that sentence twice, once truncated and
 * once not. Two lines of the same words read as a rendering fault rather than
 * as a summary. Suppressed rather than fixed upstream because the duplication
 * is real: on the second turn the preview starts saying something the title
 * does not, and it comes back on its own.
 *
 * Compared with the ellipsis and the whitespace normalised out, since the two
 * are the same text cut at different lengths by different code.
 */
export function previewLine(title: string, preview: string): string | null {
  if (preview.trim() === "") return "No messages yet"
  const flatten = (s: string) => s.trim().replace(/\s+/g, " ").replace(/…+$/, "")
  const t = flatten(title)
  const p = flatten(preview)
  if (t !== "" && (p.startsWith(t) || t.startsWith(p))) return null
  return preview
}

export function HistoryRail({
  conversations,
  activeId,
  onSelect,
  onNew,
  onDelete,
  idPrefix = "conversation",
}: {
  conversations: PlaygroundConversation[]
  activeId: string
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (conversation: PlaygroundConversation) => void
  idPrefix?: string
}) {
  return (
    // Fills the panel it was given rather than holding a width of its own.
    // The 260px it used to be fixed at made the drag handle beside it inert:
    // the panel resized and the card inside it did not.
    <Card className="flex min-h-0 w-full flex-1 flex-col overflow-hidden p-0">
      {/* Starting a conversation is the one thing this panel does besides
          list, so it shares the list's own header row rather than taking a
          footer of its own. Icon only: the heading beside it already says
          what a new one would be. */}
      <div className="flex shrink-0 items-center justify-between gap-2 border-b px-4 py-2">
        <h2 className="text-sm font-medium">Conversations</h2>
        <Button variant="ghost" size="icon" aria-label="New conversation" onClick={onNew}>
          <MessageSquarePlus className="size-[var(--icon-size)]" aria-hidden="true" />
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {conversations.length === 0 ? (
          <p className="px-2 py-1 text-sm text-[hsl(var(--legend))]">
            No saved conversations yet. Send a message and this is where it stays.
          </p>
        ) : (
          <ul className="flex flex-col gap-0.5">
            {conversations.map((c) => (
              <li key={c.id} className="group relative">
                <button
                  type="button"
                  id={`${idPrefix}-${c.id}`}
                  onClick={() => onSelect(c.id)}
                  aria-current={c.id === activeId ? "true" : undefined}
                  className={`flex w-full flex-col gap-0.5 rounded-[var(--radius)] px-2 py-2 text-left text-sm transition-colors hover:bg-[hsl(var(--muted))] ${
                    c.id === activeId
                      ? "bg-[hsl(var(--muted))] font-semibold shadow-[inset_3px_0_0_0_hsl(var(--primary))]"
                      : ""
                  }`}
                >
                  <span className="flex items-baseline gap-2">
                    <span className="min-w-0 flex-1 truncate">{c.title}</span>
                    {/* Shares its box with the delete control, which takes
                        over on hover. Both are small right-hand marginalia,
                        and reserving room for the second one permanently
                        would indent the first away from the edge it reads
                        against. */}
                    <span className="shrink-0 font-normal text-[hsl(var(--legend))] group-hover:invisible group-focus-within:invisible">
                      {relativeTime(new Date(c.updated_at).getTime())}
                    </span>
                  </span>
                  {/* One line, and only one: the rail is for recognising a
                      conversation, not for reading it. */}
                  {previewLine(c.title, c.preview) === null ? null : (
                    <span className="truncate font-normal text-[hsl(var(--muted-foreground))]">
                      {previewLine(c.title, c.preview)}
                    </span>
                  )}
                </button>
                <button
                  type="button"
                  aria-label="Delete conversation"
                  aria-describedby={`${idPrefix}-${c.id}`}
                  onClick={() => onDelete(c)}
                  className="absolute top-2 right-2 rounded-[var(--radius)] text-[hsl(var(--legend))] opacity-0 group-hover:opacity-100 focus-visible:opacity-100 hover:text-[hsl(var(--destructive))]"
                >
                  <Trash2 className="size-[var(--icon-size)]" aria-hidden="true" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Card>
  )
}
