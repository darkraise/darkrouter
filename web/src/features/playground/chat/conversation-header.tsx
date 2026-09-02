import { useEffect, useRef, useState } from "react"
import {
  Button, Card,
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
  Input,
} from "darkraise-ui"
import { Lock, MoreHorizontal } from "lucide-react"
import type { PlaygroundConfig } from "../config"

/**
 * What a conversation is, above the conversation.
 *
 * The model it answers under, and the conversation's name. Everything else a
 * request can carry is in the request pane beside the transcript; the model is
 * here because it is what an operator checks before every send, and a pane is
 * somewhere you look rather than something you see.
 *
 * Its own island at the top of the chat column, because what model is
 * answering is a property of the conversation rather than of the message being
 * typed — and a strip fused to the transcript read as part of it.
 *
 * The model is a reading and not a control. It used to be a popover that
 * edited the model and the dialect, which made three places to change one
 * value once the new-conversation dialog existed — and three places that each
 * had to agree about when the settings close. They close at the first message;
 * until then the dialog is where they are set, reachable from the actions menu
 * beside this. The name stays editable throughout: what a thread is called is
 * not part of what was sent.
 */
export function ConversationHeader({
  config,
  title,
  onTitleChange,
  onDelete,
  onOpenSettings,
  canDelete,
  locked = false,
  disabled = false,
}: {
  config: PlaygroundConfig
  title: string
  onTitleChange: (next: string) => void
  onDelete: () => void
  /** Reopens the request settings. Offered only while nothing has been sent. */
  onOpenSettings: () => void
  canDelete: boolean
  /** Set once a turn has been sent under these settings. */
  locked?: boolean
  /** The selected conversation has not loaded, so the readings still belong
   *  to the conversation being left and must not be written to the new one. */
  disabled?: boolean
}) {
  const [draftTitle, setDraftTitle] = useState(title)
  // Escape blurs the field, and blur commits; without this the abandoned draft
  // would be saved by the very keystroke that discards it.
  const abandoning = useRef(false)

  // The field follows the conversation, not the keystroke: selecting another
  // conversation in the rail must not leave the previous one's name in it.
  useEffect(() => setDraftTitle(title), [title])

  function commitTitle() {
    if (abandoning.current) {
      abandoning.current = false
      return
    }
    const next = draftTitle.trim()
    // An empty title would draw a blank row in the rail, so it is refused
    // rather than accepted and papered over with a placeholder.
    if (next === "" || next === title) {
      setDraftTitle(title)
      return
    }
    onTitleChange(next)
  }

  return (
    <Card className="flex shrink-0 items-center gap-2 p-3">
      {/* A plain reading rather than a disabled button: a control that cannot
          be operated is still a control, and an operator will click it before
          reading why it did nothing. The padlock is drawn only once the
          settings are actually shut, so it marks the moment rather than
          decorating the pill. */}
      <span
        className="flex max-w-[18rem] items-center gap-1.5 rounded-[var(--radius)] border border-dashed px-2.5 py-1.5 text-sm text-[hsl(var(--muted-foreground))]"
        title={
          locked
            ? `Fixed by the first message: ${config.model} on the ${config.dialect} dialect`
            : `${config.model === "" ? "No model chosen" : config.model} on the ${config.dialect} dialect`
        }
      >
        {locked ? (
          <Lock className="size-[var(--icon-size,1rem)] shrink-0" aria-hidden="true" />
        ) : null}
        <span className="truncate font-mono">
          {config.model === "" ? "No model" : config.model}
        </span>
      </span>

      <Input
        aria-label="Conversation title"
        value={draftTitle}
        disabled={disabled}
        onChange={(e) => setDraftTitle(e.target.value)}
        onBlur={commitTitle}
        onKeyDown={(e) => {
          if (e.nativeEvent.isComposing) return
          if (e.key === "Enter") {
            e.preventDefault()
            // Blur is what commits. Committing here as well would fire the
            // change twice for one rename.
            e.currentTarget.blur()
          }
          if (e.key === "Escape") {
            abandoning.current = true
            setDraftTitle(title)
            e.currentTarget.blur()
          }
        }}
        className="flex-1 border-transparent bg-transparent px-2 hover:border-[hsl(var(--border))] focus:border-[hsl(var(--border))]"
      />

      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            aria-label="Conversation actions"
            disabled={disabled}
          >
            <MoreHorizontal className="size-[var(--icon-size)]" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          {/* Disabled rather than hidden once a turn has been sent. An item
              that disappears reads as a menu that has lost something; one
              that stays and refuses says the settings are shut, which is the
              fact the operator is looking for. */}
          <DropdownMenuItem disabled={locked} onSelect={onOpenSettings}>
            Request settings…
          </DropdownMenuItem>
          {/* The system prompt used to be edited from here. It is in the
              request settings now, beside the rest of what a request carries
              and under the same lock, rather than in a dialog that could
              change it after the turns it shaped had already been answered. */}
          <DropdownMenuItem disabled={!canDelete} onSelect={onDelete}>
            Delete conversation
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </Card>
  )
}
