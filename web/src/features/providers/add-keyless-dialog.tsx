import { useState } from "react"
import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Label,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"
import type { Preset } from "../../lib/api-types"
import { ProviderIcon } from "./provider-icon"

/**
 * Adds a provider that is reached with no credential.
 *
 * The accounts dialog cannot serve this: there is no secret to collect. But a
 * bare button cannot either, because the import filter is not a property of a
 * key and the first discovery sweep starts within seconds of the POST — a
 * choice offered afterwards is offered too late. So this asks the one question
 * that still applies, and nothing else.
 */
export function AddKeylessDialog({
  preset,
  open,
  onOpenChange,
  onDone,
}: {
  preset: Preset | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone?: (id: string) => void
}) {
  const [freeOnly, setFreeOnly] = useState(false)
  const [wasOpen, setWasOpen] = useState(open)

  // Each visit starts from the default. The dialog outlives any one visit, so
  // a box ticked for a provider the operator then backed out of would still be
  // ticked for the next one.
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setFreeOnly(false)
  }

  const add = useApiMutation({
    mutationFn: (p: Preset) =>
      api.post("/api/providers", {
        id: p.id,
        preset: p.id,
        free_models_only: freeOnly,
      }),
    success: preset ? `${preset.name} added` : "Provider added",
    invalidates: [keys.providers, keys.health, keys.overview, keys.models],
    onSuccess: () => {
      if (!preset) return
      onOpenChange(false)
      onDone?.(preset.id)
    },
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-w-lg flex-col">
        <DialogHeader>
          <DialogTitle>Add a keyless provider</DialogTitle>
          <DialogDescription>
            This provider answers an unauthenticated request, so there is nothing to
            paste. One choice applies before it is added.
          </DialogDescription>
        </DialogHeader>

        {preset && (
          <>
            <div className="flex items-center gap-3 rounded-[var(--radius)] border p-3">
              <ProviderIcon preset={preset.id} id={preset.id} name={preset.name} size={28} />
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">{preset.name}</span>
                <span className="block truncate font-mono text-sm text-[hsl(var(--legend))]">
                  {preset.id} · {preset.kind}
                </span>
              </span>
            </div>

            <div className="flex items-start gap-2">
              <Checkbox
                id="keyless-add-free-only"
                checked={freeOnly}
                onCheckedChange={(next) => setFreeOnly(next === true)}
              />
              <div className="flex flex-col">
                <Label htmlFor="keyless-add-free-only">Import free models only</Label>
                <span className="max-w-prose text-sm text-[hsl(var(--legend))]">
                  A sweep keeps a model this provider's free tier documents, one priced
                  at zero, or one tagged <span className="font-mono">:free</span>.
                  Changeable afterwards in settings, but the first sweep starts now.
                </span>
              </div>
            </div>

            <div className="mt-2 flex items-center gap-2 border-t pt-3">
              <div className="ml-auto flex items-center gap-2">
                <Button
                  variant="ghost"
                  onClick={() => onOpenChange(false)}
                  disabled={add.isPending}
                >
                  Cancel
                </Button>
                <Button disabled={add.isPending} onClick={() => add.mutate(preset)}>
                  Add {preset.name}
                </Button>
              </div>
            </div>
          </>
        )}
      </DialogContent>
    </Dialog>
  )
}
