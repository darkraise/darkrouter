import { Button } from "darkraise-ui"
import { MessageSquarePlus, PanelLeftClose, PanelLeftOpen, Trash2 } from "lucide-react"
import { relativeTime } from "../../providers/test-log-tab"
import type { PlaygroundConversation } from "../../../lib/api-types"

/**
 * Every conversation, in the order the caller supplies.
 *
 * This is the whole reason Chat mode is a mode rather than a tab: a chat
 * surface without retrievable history is a scratchpad, and nobody keeps a
 * scratchpad. Each row carries the title, what the last turn was about, and
 * how long ago — enough to recognise a conversation without opening it.
 *
 * Collapsible to nothing because a 260px rail and a transcript are two columns
 * an operator sometimes wants to be one.
 */
export function HistoryRail({
  conversations,
  activeId,
  onSelect,
  onNew,
  onDelete,
  collapsed,
  onToggleCollapsed,
}: {
  conversations: PlaygroundConversation[]
  activeId: string
  onSelect: (id: string) => void
  onNew: () => void
  onDelete: (conversation: PlaygroundConversation) => void
  collapsed: boolean
  onToggleCollapsed: () => void
}) {
  if (collapsed) {
    return (
      <div className="shrink-0 border-r p-2">
        <Button variant="ghost" size="icon" aria-label="Show conversations" onClick={onToggleCollapsed}>
          <PanelLeftOpen className="size-[var(--icon-size)]" aria-hidden="true" />
        </Button>
      </div>
    )
  }

  return (
    <aside className="flex w-[260px] shrink-0 flex-col border-r">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <span className="text-sm font-medium">Conversations</span>
        <Button variant="ghost" size="icon" aria-label="Hide conversations" onClick={onToggleCollapsed}>
          <PanelLeftClose className="size-[var(--icon-size)]" aria-hidden="true" />
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {conversations.length === 0 ? (
          <p className="p-3 text-sm text-[hsl(var(--legend))]">
            No saved conversations yet. Send a message and this is where it stays.
          </p>
        ) : (
          <ul className="flex flex-col">
            {conversations.map((c) => (
              <li key={c.id} className="group relative border-b last:border-b-0">
                <button
                  type="button"
                  id={`conversation-${c.id}`}
                  onClick={() => onSelect(c.id)}
                  aria-current={c.id === activeId ? "true" : undefined}
                  className={`flex w-full flex-col gap-0.5 px-3 py-2 pr-9 text-left text-sm hover:bg-[hsl(var(--muted))] ${
                    c.id === activeId ? "bg-[hsl(var(--muted))]" : ""
                  }`}
                >
                  <span className="truncate font-medium">{c.title}</span>
                  {/* One line, and only one: the rail is for recognising a
                      conversation, not for reading it. */}
                  <span className="truncate text-[hsl(var(--muted-foreground))]">
                    {c.preview === "" ? "No messages yet" : c.preview}
                  </span>
                  <span className="text-[hsl(var(--legend))]">
                    {relativeTime(new Date(c.updated_at).getTime())}
                  </span>
                </button>
                <button
                  type="button"
                  aria-label="Delete conversation"
                  aria-describedby={`conversation-${c.id}`}
                  onClick={() => onDelete(c)}
                  className="absolute top-2 right-2 p-1 text-[hsl(var(--legend))] opacity-0 group-hover:opacity-100 focus-visible:opacity-100 hover:text-[hsl(var(--destructive))]"
                >
                  <Trash2 className="size-[var(--icon-size)]" aria-hidden="true" />
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="shrink-0 border-t p-2">
        <Button variant="ghost" className="w-full justify-start gap-2" onClick={onNew}>
          <MessageSquarePlus className="size-[var(--icon-size)]" aria-hidden="true" />
          New
        </Button>
      </div>
    </aside>
  )
}
