import { useState } from "react"
import {
  Button,
  Checkbox,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"
import type { Provider } from "../../lib/api-types"

/**
 * The touched half of a provider's settings.
 *
 * Region and project are pointer fields on the backend
 * (`store.ProviderPatch.Region` / `.Project`): a key present with value ""
 * means "set this to empty", not "leave alone". `GET /api/providers` never
 * returns either field, so the inputs start with nothing to prefill — null
 * distinguishes "never touched" from "touched and cleared", which "" alone
 * cannot.
 *
 * The others are compared against the provider they came from, so a dialog
 * opened and saved without an edit sends `{}` rather than rewriting values
 * that never changed.
 */
export type SettingsDraft = {
  priority: string
  freeModelsOnly: boolean
  region: string | null
  project: string | null
}

export function draftOf(p: Provider): SettingsDraft {
  return {
    priority: String(p.priority),
    freeModelsOnly: p.free_models_only,
    region: null,
    project: null,
  }
}

export function settingsPatch(draft: SettingsDraft, p: Provider): Record<string, unknown> {
  const patch: Record<string, unknown> = {}
  const typed = draft.priority.trim()
  const priority = Number(typed)
  // A field that will not parse is left out rather than sent. NaN serialises
  // to null, and an emptied field is worse than that: `Number("")` is 0, so a
  // cleared box would quietly write the highest priority there is.
  //
  // Whole numbers only. The backend field is a Go `*int`, so `10.5` comes back
  // as a raw `cannot unmarshal number 10.5` toast, and `Number` would read
  // `0x10` as 16 — a value nobody typed.
  if (typed !== "" && Number.isInteger(priority) && priority !== p.priority) {
    patch.priority = priority
  }
  if (draft.freeModelsOnly !== p.free_models_only) patch.free_models_only = draft.freeModelsOnly
  if (draft.region !== null) patch.region = draft.region
  if (draft.project !== null) patch.project = draft.project
  return patch
}

/**
 * Everything about a provider an operator sets, in one place.
 *
 * One draft and one Save rather than a save button per field: these are four
 * settings on one provider, and three independent writes for one visit is a
 * way to leave two of them applied and the third not.
 */
export function ProviderSettingsDialog({
  provider,
  open,
  onOpenChange,
}: {
  provider: Provider
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [draft, setDraft] = useState<SettingsDraft>(() => draftOf(provider))
  const [wasOpen, setWasOpen] = useState(open)

  // Each visit starts from what the provider says now. The dialog outlives any
  // one visit, so a draft seeded at mount would still be showing — and would
  // write back — whatever the provider held before someone else changed it.
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setDraft(draftOf(provider))
  }

  const save = useApiMutation({
    mutationFn: (patch: Record<string, unknown>) =>
      api.patch(`/api/providers/${provider.id}`, patch),
    success: "Provider settings saved",
    invalidates: [keys.providers, keys.overview],
    onSuccess: () => onOpenChange(false),
  })

  const patch = settingsPatch(draft, provider)
  const dirty = Object.keys(patch).length > 0

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
    >
      <DialogContent className="flex max-h-[85vh] max-w-xl flex-col overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{provider.name} settings</DialogTitle>
          <DialogDescription>
            How the router treats this provider, and what discovery imports from it.
            Its name and its connection come from the release.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap items-end gap-3">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="provider-priority">Priority</Label>
              <Input
                id="provider-priority"
                value={draft.priority}
                onChange={(e) => setDraft({ ...draft, priority: e.target.value })}
                className="w-24"
                inputMode="numeric"
              />
            </div>
            <p className="max-w-sm text-sm text-[hsl(var(--legend))]">
              The order the router walks providers in when a bare model name could be
              served by more than one.
            </p>
          </div>

          {/* The wizard asks this before the first sweep; this is where an
              operator changes their mind. It takes effect on the next sweep --
              models already imported stay until then. */}
          <div className="flex items-start gap-2 border-t pt-4">
            <Checkbox
              id="provider-free-only"
              checked={draft.freeModelsOnly}
              onCheckedChange={(next) =>
                setDraft({ ...draft, freeModelsOnly: next === true })
              }
            />
            <div className="flex flex-col">
              <Label htmlFor="provider-free-only">Import free models only</Label>
              <span className="text-sm text-[hsl(var(--legend))]">
                The next discovery sweep keeps a model this provider's own free tier
                documents, one priced at zero, or one tagged{" "}
                <span className="font-mono">:free</span>.
              </span>
            </div>
          </div>

          <div className="flex flex-wrap items-end gap-3 border-t pt-4">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="provider-region">Region</Label>
              {/* Blank rather than prefilled: GET /api/providers does not
                  return either field, so there is no current value to show. */}
              <Input
                id="provider-region"
                value={draft.region ?? ""}
                onChange={(e) => setDraft({ ...draft, region: e.target.value })}
                placeholder="unset"
                className="w-40"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="provider-project">Project</Label>
              <Input
                id="provider-project"
                value={draft.project ?? ""}
                onChange={(e) => setDraft({ ...draft, project: e.target.value })}
                placeholder="unset"
                className="w-40"
              />
            </div>
            <p className="max-w-xs text-sm text-[hsl(var(--legend))]">
              Only the ones you type are written. Leave them alone and they keep
              whatever they hold.
            </p>
          </div>
        </div>

        <div className="mt-2 flex items-center gap-2 border-t pt-3">
          <div className="ml-auto flex items-center gap-2">
            <Button
              variant="ghost"
              onClick={() => onOpenChange(false)}
              disabled={save.isPending}
            >
              Cancel
            </Button>
            <Button disabled={!dirty || save.isPending} onClick={() => save.mutate(patch)}>
              Save changes
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
