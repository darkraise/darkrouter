import { useEffect, useRef, useState } from "react"
import {
  Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
  Input, Label, Popover, PopoverContent, PopoverTrigger,
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue, Textarea,
} from "darkraise-ui"
import { MoreHorizontal } from "lucide-react"
import { ModelCombobox, useModelCandidates } from "../../shell/model-combobox"
import { DIALECTS, type PlaygroundConfig } from "../config"
import type { PlaygroundDialect } from "../../../lib/api-types"

/**
 * What a conversation is, above the conversation.
 *
 * Chat mode shows no config pane, so the two settings a conversation genuinely
 * needs are reachable from here instead: the model and the dialect from the
 * pill's popover, the system prompt from the overflow menu. Everything else a
 * request can carry belongs to Lab, and the menu's *open in Lab* is how a
 * conversation gets there without being retyped.
 */
export function ConversationHeader({
  config,
  onConfigChange,
  title,
  onTitleChange,
  onOpenInLab,
  onDelete,
  canDelete,
}: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
  title: string
  onTitleChange: (next: string) => void
  onOpenInLab: () => void
  onDelete: () => void
  canDelete: boolean
}) {
  const { candidates, loading } = useModelCandidates()
  const [draftTitle, setDraftTitle] = useState(title)
  const [systemOpen, setSystemOpen] = useState(false)
  const [draftSystem, setDraftSystem] = useState(config.system)
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
    <div className="flex shrink-0 items-center gap-2 border-b px-6 py-2">
      <Popover>
        <PopoverTrigger asChild>
          <Button variant="outline" size="sm" className="max-w-[18rem] truncate font-mono">
            {config.model === "" ? "Choose a model" : config.model}
          </Button>
        </PopoverTrigger>
        <PopoverContent className="flex w-80 flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pgc-model">Model</Label>
            <ModelCombobox
              label="Model or alias"
              value={config.model}
              candidates={candidates}
              loading={loading}
              onChange={(model) => onConfigChange({ ...config, model })}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pgc-dialect">Dialect</Label>
            <Select
              value={config.dialect}
              onValueChange={(dialect) =>
                onConfigChange({ ...config, dialect: dialect as PlaygroundDialect })
              }
            >
              <SelectTrigger id="pgc-dialect">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {DIALECTS.map((d) => (
                  <SelectItem key={d} value={d}>
                    {d}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </PopoverContent>
      </Popover>

      <Input
        aria-label="Conversation title"
        value={draftTitle}
        onChange={(e) => setDraftTitle(e.target.value)}
        onBlur={commitTitle}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            commitTitle()
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
          <Button variant="ghost" size="icon" aria-label="Conversation actions">
            <MoreHorizontal className="size-[var(--icon-size)]" aria-hidden="true" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end">
          <DropdownMenuItem
            onSelect={(e) => {
              // The menu returns focus to its trigger as it closes, which would
              // pull focus straight back out of the dialog it just opened.
              e.preventDefault()
              setDraftSystem(config.system)
              setSystemOpen(true)
            }}
          >
            Edit system prompt
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={onOpenInLab}>Open in Lab</DropdownMenuItem>
          <DropdownMenuItem disabled={!canDelete} onSelect={onDelete}>
            Delete conversation
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>

      <Dialog open={systemOpen} onOpenChange={setSystemOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Edit system prompt</DialogTitle>
            <DialogDescription>
              Sent ahead of every turn in this conversation, and stored with it.
            </DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pgc-system">System prompt</Label>
            <Textarea
              id="pgc-system"
              aria-label="System prompt"
              rows={6}
              value={draftSystem}
              onChange={(e) => setDraftSystem(e.target.value)}
            />
          </div>
          <div className="mt-2 flex items-center justify-end gap-2 border-t pt-3">
            <Button variant="ghost" onClick={() => setSystemOpen(false)}>
              Cancel
            </Button>
            <Button
              onClick={() => {
                onConfigChange({ ...config, system: draftSystem })
                setSystemOpen(false)
              }}
            >
              Save
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
