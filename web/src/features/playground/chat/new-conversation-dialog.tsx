import { useState } from "react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "darkraise-ui"
import { ConfigPane } from "../config-pane/config-pane"
import { NestedDialogContext } from "../config-pane/nested-dialog"
import type { PlaygroundConfig } from "../config"

/**
 * What a conversation will be sent under, asked before it is.
 *
 * Chat fixes its settings at the first message, so there is exactly one moment
 * when every one of them is still open — and until now nothing marked it. An
 * operator who wanted a temperature had to know to go looking in a side panel
 * before typing, and one who did not simply sent at the provider's defaults
 * and found the pane refusing to answer afterwards. The dialog puts that
 * moment on screen: this is what the thread will carry, and it is now that you
 * say so.
 *
 * The body is the request pane itself rather than a chosen few of its
 * controls. A dialog that offered only the model would send anyone looking for
 * a reasoning budget to a panel that no longer edits one, which is the same
 * split this was meant to close.
 *
 * Cancel is not a way to refuse the conversation — the rail's own button has
 * already started one. It refuses these settings, and the blank chat behind
 * the dialog keeps the defaults it was given. Nothing here blocks an operator
 * who wants to close it and type.
 */
export function NewConversationDialog({
  open,
  onOpenChange,
  seed,
  onStart,
  amending = false,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** What the draft starts as. The conversation being left behind, so a model
   *  an operator has been working with carries into the next thread. */
  seed: PlaygroundConfig
  onStart: (config: PlaygroundConfig) => void
  /** Reopened on a conversation that exists and has no turns yet. Nothing is
   *  being started, so the button does not say it is. */
  amending?: boolean
}) {
  const [draft, setDraft] = useState<PlaygroundConfig>(seed)
  // While the pane has a dialog of its own open, Escape belongs to that one.
  // See NestedDialogContext for why this has to be said.
  const [nested, setNested] = useState(false)

  // Reseeded on opening rather than on every seed change: a draft that
  // tracked the seed would be rewritten under the operator's hands the
  // moment anything behind the dialog moved, and one that never reseeded
  // would hand back the edits a Cancel had just discarded.
  //
  // The seed is read at the moment of opening, which is what makes this an
  // adjustment during render rather than an effect: an effect would paint
  // the discarded draft for a frame before replacing it.
  const [wasOpen, setWasOpen] = useState(open)
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setDraft(seed)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      closeOnEscape={!nested}
    >
      <DialogContent className="flex max-h-[85vh] max-w-2xl flex-col overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{amending ? "Request settings" : "New conversation"}</DialogTitle>
          <DialogDescription>
            {amending
              ? "Nothing has been sent yet, so all of it is still open. The first message fixes it."
              : "What every message in this thread will be sent under. The first message fixes it — start another conversation to send under anything else."}
          </DialogDescription>
        </DialogHeader>

        <NestedDialogContext.Provider value={setNested}>
          <ConfigPane config={draft} onChange={setDraft} showHeading={false} />
        </NestedDialogContext.Provider>

        <div className="mt-2 flex items-center justify-end gap-2 border-t pt-3">
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            onClick={() => {
              onStart(draft)
              onOpenChange(false)
            }}
          >
            {amending ? "Apply" : "Start conversation"}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
