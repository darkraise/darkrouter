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
import { api, routingNotUpdated } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"
import type { Provider } from "../../lib/api-types"
import { endpointFieldsFor } from "./add-accounts-dialog"
import { endpointOf } from "./provider-state"

/**
 * A provider's settings as the dialog edits them.
 *
 * Every field opens on what the provider holds and is compared against it, so
 * a dialog opened and saved without an edit sends `{}` rather than rewriting
 * values that never changed.
 *
 * Region, project and location are pointer fields on the backend
 * (`store.ProviderPatch.Region` / `.Project` / `.Location`): a key present with
 * value "" means "set this to empty". Emptying a box that held a value is
 * therefore a deliberate clear, and is sent.
 */
export type SettingsDraft = {
  priority: string
  baseUrl: string
  freeModelsOnly: boolean
  region: string
  project: string
  location: string
}

export function draftOf(p: Provider): SettingsDraft {
  return {
    priority: String(p.priority),
    baseUrl: p.base_url,
    freeModelsOnly: p.free_models_only,
    region: p.region ?? "",
    project: p.project ?? "",
    location: p.location ?? "",
  }
}

/** Why the typed priority cannot be saved, or null when it can.
 *
 *  Whole numbers in plain decimal only. The backend field is a Go `*int`, so
 *  `10.5` comes back as a raw `cannot unmarshal number 10.5` toast, and
 *  `Number` would read `0x10` as 16 — a value nobody typed. And an empty box is
 *  not zero: `Number("")` is 0, the highest priority there is. */
export function priorityError(typed: string): string | null {
  return /^-?\d+$/.test(typed.trim()) ? null : "Priority must be a whole number"
}

export function settingsPatch(draft: SettingsDraft, p: Provider): Record<string, unknown> {
  const patch: Record<string, unknown> = {}
  const typed = draft.priority.trim()
  // A field that will not parse is left out rather than sent; the dialog says
  // why beside the box and holds Save until it is fixed.
  if (priorityError(typed) === null && Number(typed) !== p.priority) {
    patch.priority = Number(typed)
  }
  // An emptied box leaves the endpoint alone rather than clearing it: a
  // provider with no base URL is unreachable, and the backend rejects the
  // write anyway, so a slip would cost a 400 rather than mean anything.
  const baseUrl = draft.baseUrl.trim()
  if (baseUrl !== "" && baseUrl !== p.base_url) patch.base_url = baseUrl
  if (draft.freeModelsOnly !== p.free_models_only) patch.free_models_only = draft.freeModelsOnly
  if (draft.region !== (p.region ?? "")) patch.region = draft.region
  if (draft.project !== (p.project ?? "")) patch.project = draft.project
  // Vertex alone reads a location, and the backend refuses changing one that
  // is set, so a stray space or an empty value would be stored for good.
  const location = draft.location.trim()
  if (p.kind === "vertex" && location !== "" && location !== (p.location ?? "")) {
    patch.location = location
  }
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
      routingNotUpdated(api.patch(`/api/providers/${provider.id}`, patch)),
    success: "Provider settings saved",
    warning: (notRouted) => notRouted,
    invalidates: [keys.providers, keys.overview],
    onSuccess: () => onOpenChange(false),
  })

  const patch = settingsPatch(draft, provider)
  const dirty = Object.keys(patch).length > 0
  // Shown rather than silently dropped: before, Save just went grey on "abc",
  // and with another field edited it stayed live and dropped the priority.
  const priorityProblem = priorityError(draft.priority)
  // Only the fields this kind reads. Region on an openaicompat provider means
  // nothing, and offering it invited a value that would be stored and ignored.
  const endpointFields = endpointFieldsFor(provider.kind)
  // What an empty box means for a provider whose host is built from its other
  // fields, read off the draft so the preview follows a region being typed.
  const derived = provider.base_url
    ? undefined
    : endpointOf({ ...draft, base_url: "", kind: provider.kind })

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
    >
      <DialogContent className="flex max-h-[85vh] max-w-xl flex-col overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{provider.name} settings</DialogTitle>
          <DialogDescription>
            How the router treats this provider, what discovery imports from it, and
            where it is reached. Its name comes from the release.
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
                aria-invalid={priorityProblem !== null || undefined}
                aria-describedby={priorityProblem ? "provider-priority-error" : undefined}
              />
            </div>
            <p className="max-w-sm text-sm text-[hsl(var(--legend))]">
              The order the router walks providers in when a bare model name could be
              served by more than one.
            </p>
            {priorityProblem && (
              <p
                id="provider-priority-error"
                role="alert"
                className="basis-full text-sm text-[hsl(var(--destructive))]"
              >
                {priorityProblem}
              </p>
            )}
          </div>

          {/* Editable because a local runtime's address is the operator's, not
              the release's: the shipped presets all name localhost, which from
              inside the container is the container. A wrong host would
              otherwise mean deleting the provider and starting again. */}
          <div className="flex flex-col gap-1.5 border-t pt-4">
            <Label htmlFor="provider-base-url">Base URL</Label>
            <Input
              id="provider-base-url"
              value={draft.baseUrl}
              onChange={(e) => setDraft({ ...draft, baseUrl: e.target.value })}
              placeholder={derived?.url || undefined}
              spellCheck={false}
              className="font-mono"
            />
            <span className="text-sm text-[hsl(var(--legend))]">
              {derived?.derivedFrom
                ? `Left empty, the gateway derives the endpoint from ${derived.derivedFrom}. ` +
                  "A URL here replaces it, for a private endpoint."
                : "The endpoint the gateway calls. Emptying the box leaves it unchanged."}
            </span>
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

          {endpointFields.length > 0 && (
            <div className="flex flex-wrap items-end gap-3 border-t pt-4">
              {endpointFields.includes("region") && (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="provider-region">Region</Label>
                  <Input
                    id="provider-region"
                    value={draft.region}
                    onChange={(e) => setDraft({ ...draft, region: e.target.value })}
                    placeholder="us-east-1"
                    className="w-40"
                  />
                </div>
              )}
              {endpointFields.includes("project") && (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="provider-project">Project</Label>
                  <Input
                    id="provider-project"
                    value={draft.project}
                    onChange={(e) => setDraft({ ...draft, project: e.target.value })}
                    placeholder="my-project"
                    className="w-40"
                  />
                </div>
              )}
              {/* Fills a location the provider was created without. The
                  backend refuses moving one that is set, so a set one is
                  shown and not offered for editing. */}
              {endpointFields.includes("location") && (
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="provider-location">Location</Label>
                  <Input
                    id="provider-location"
                    value={draft.location}
                    onChange={(e) => setDraft({ ...draft, location: e.target.value })}
                    placeholder="us-central1"
                    disabled={Boolean(provider.location)}
                    className="w-40"
                  />
                </div>
              )}
              <p className="max-w-xs text-sm text-[hsl(var(--legend))]">
                Where the endpoint is. A changed value moves every request this provider
                serves.
                {endpointFields.includes("location") &&
                  " The location can be set once: a provider that already has one keeps it."}
              </p>
            </div>
          )}
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
            <Button
              disabled={!dirty || priorityProblem !== null || save.isPending}
              onClick={() => save.mutate(patch)}
            >
              Save changes
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
