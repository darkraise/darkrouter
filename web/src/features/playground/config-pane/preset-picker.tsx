import { useState } from "react"
import {
  Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle,
  Input, Label, Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "darkraise-ui"
import { Settings2, Trash2 } from "lucide-react"
import { api } from "../../../lib/api"
import { useApiMutation } from "../../../lib/mutations"
import { keys, usePlaygroundPresets } from "../../../lib/queries"
import { mergeStoredConfig, toStoredConfig } from "../preset-config"
import type { PlaygroundConfig } from "../config"
import type { PlaygroundPreset } from "../../../lib/api-types"

/** What a save sends. model and dialect are columns; the rest is the blob. */
function bodyFor(name: string, config: PlaygroundConfig) {
  return {
    name,
    dialect: config.dialect,
    model: config.model,
    config: toStoredConfig(config),
  }
}

/**
 * Saved request configurations, above the fields they fill in.
 *
 * Loading replaces the pane wholesale rather than merging into what is already
 * typed: a half-loaded preset would produce a request neither the operator nor
 * the preset asked for. A name that is already taken is not an error path —
 * the picker finds the clash in the presets list already on screen and offers
 * to overwrite that row; the server's own unique index still answers 409
 * independently, as a backstop, but that response is never read here.
 *
 * Loading and deleting are kept apart on purpose: the Select is the only load
 * path, reachable in one click plus a selection, while delete sits behind a
 * Manage dialog. A load replaces the whole pane, so the easier of the two
 * mistakes to make by accident is the one that should take more than a single
 * stray click.
 */
export function PresetPicker({
  config,
  onChange,
}: {
  config: PlaygroundConfig
  onChange: (next: PlaygroundConfig) => void
}) {
  const { data: presets } = usePlaygroundPresets()
  const [open, setOpen] = useState(false)
  const [manageOpen, setManageOpen] = useState(false)
  const [name, setName] = useState("")

  const close = () => {
    setOpen(false)
    setName("")
  }

  // The clash is found in the list already on screen rather than by sending a
  // save and reading the rejection: ApiError carries a status and a message
  // and no body, so a 409's id could not be recovered from it anyway.
  const clash = (presets ?? []).find((preset) => preset.name === name.trim())

  const save = useApiMutation<PlaygroundPreset, { name: string }>({
    mutationFn: (vars) => api.post<PlaygroundPreset>("/api/playground/presets", bodyFor(vars.name, config)),
    success: (_data, vars) => `Saved ${vars.name}`,
    invalidates: [keys.playgroundPresets],
    onSuccess: () => close(),
  })

  const overwrite = useApiMutation<{ id: string }, { id: string; name: string }>({
    mutationFn: (vars) =>
      api.patch<{ id: string }>(`/api/playground/presets/${vars.id}`, bodyFor(vars.name, config)),
    success: (_data, vars) => `Overwrote ${vars.name}`,
    invalidates: [keys.playgroundPresets],
    onSuccess: () => close(),
  })

  const remove = useApiMutation<void, { id: string; name: string }>({
    mutationFn: (vars) => api.del<void>(`/api/playground/presets/${vars.id}`),
    success: (_data, vars) => `Deleted ${vars.name}`,
    invalidates: [keys.playgroundPresets],
  })

  function load(preset: PlaygroundPreset) {
    onChange(mergeStoredConfig(preset.config, preset.model, preset.dialect))
  }

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor="pg-preset">Preset</Label>
      <div className="flex items-center gap-2">
        <Select
          value=""
          onValueChange={(id) => {
            const found = (presets ?? []).find((p) => p.id === id)
            if (found) load(found)
          }}
        >
          <SelectTrigger id="pg-preset" className="flex-1">
            <SelectValue placeholder="Load a preset" />
          </SelectTrigger>
          <SelectContent>
            {(presets ?? []).map((preset) => (
              <SelectItem key={preset.id} value={preset.id}>
                {preset.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Manage presets"
          title="Manage presets"
          onClick={() => setManageOpen(true)}
        >
          <Settings2 className="size-[var(--icon-size)]" aria-hidden="true" />
        </Button>
        <Button variant="ghost" onClick={() => setOpen(true)}>
          Save
        </Button>
      </div>

      <Dialog open={open} onOpenChange={(next) => (next ? setOpen(true) : close())}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>Save this request</DialogTitle>
            <DialogDescription>
              The model, the dialect and every setting in this pane, under a name you can
              load them back with.
            </DialogDescription>
          </DialogHeader>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="pg-preset-name">Name</Label>
            <Input
              id="pg-preset-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="terse"
              autoFocus
            />
            {clash ? (
              <p className="text-sm text-[hsl(var(--legend))]">
                A preset called {clash.name} already exists. Overwrite it?
              </p>
            ) : null}
          </div>

          <div className="mt-2 flex items-center justify-end gap-2 border-t pt-3">
            <Button variant="ghost" onClick={close}>
              Cancel
            </Button>
            {clash ? (
              <Button
                disabled={overwrite.isPending}
                onClick={() => overwrite.mutate({ id: clash.id, name: clash.name })}
              >
                Overwrite
              </Button>
            ) : (
              <Button
                disabled={name.trim() === "" || save.isPending}
                onClick={() => save.mutate({ name: name.trim() })}
              >
                Save
              </Button>
            )}
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={manageOpen} onOpenChange={setManageOpen}>
        <DialogContent className="max-w-md">
          <DialogHeader>
            <DialogTitle>Manage presets</DialogTitle>
            <DialogDescription>Delete a saved preset. This cannot be undone.</DialogDescription>
          </DialogHeader>

          {(presets ?? []).length > 0 ? (
            <ul className="flex flex-col gap-1">
              {(presets ?? []).map((preset) => (
                <li key={preset.id} className="flex items-center gap-2 text-sm">
                  <span className="flex-1 truncate">{preset.name}</span>
                  <button
                    type="button"
                    aria-label={`Delete ${preset.name}`}
                    disabled={remove.isPending}
                    className="shrink-0 p-1 text-[hsl(var(--legend))] hover:text-[hsl(var(--destructive))]"
                    onClick={() => remove.mutate({ id: preset.id, name: preset.name })}
                  >
                    <Trash2 className="size-[var(--icon-size)]" aria-hidden="true" />
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-[hsl(var(--legend))]">No presets saved yet.</p>
          )}

          <div className="mt-2 flex items-center justify-end border-t pt-3">
            <Button variant="ghost" onClick={() => setManageOpen(false)}>
              Close
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
