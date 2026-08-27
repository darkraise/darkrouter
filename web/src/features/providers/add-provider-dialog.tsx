import { useState } from "react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Switch,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, usePresets } from "../../lib/queries"
import type { Preset } from "../../lib/api-types"
import { FilterSelect } from "../requests/filter-select"
import { PresetCard } from "./preset-card"
import { RawProviderForm } from "./raw-provider-form"

export function filterPresets(
  presets: Preset[],
  f: { q?: string; surface?: string; authKind?: string; freeTier?: boolean },
): Preset[] {
  const q = (f.q ?? "").toLowerCase()
  return presets.filter((p) => {
    if (q && !p.id.toLowerCase().includes(q) && !p.name.toLowerCase().includes(q)) return false
    if (f.surface && !p.surfaces.includes(f.surface)) return false
    if (f.authKind && p.auth_kind !== f.authKind) return false
    // Narrowing rather than a two-way toggle: false would hide free-tier
    // providers and leave the filter impossible to clear.
    if (f.freeTier && !p.free_tier) return false
    return true
  })
}

export function createBodyFromPreset(
  preset: Preset,
  form: { id: string },
): Record<string, unknown> {
  // The preset carries kind, base_url and auth_style already. Echoing them
  // back would freeze this provider against a later preset correction.
  return { id: form.id, preset: preset.id }
}

function distinctSorted(values: string[]): string[] {
  return [...new Set(values)].sort()
}

/**
 * The Browse tab's search and facet state is transient interaction state,
 * not a filtered view: closing the dialog and reopening it is not a
 * navigation an operator expects to survive, unlike the Requests screen's
 * URL-backed filters.
 */
export function AddProviderDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onCreated?: (id: string) => void
}) {
  const presets = usePresets()
  const [q, setQ] = useState("")
  const [surface, setSurface] = useState("")
  const [authKind, setAuthKind] = useState("")
  const [freeTier, setFreeTier] = useState(false)
  const [selected, setSelected] = useState<Preset | null>(null)
  const [providerId, setProviderId] = useState("")

  const create = useApiMutation({
    mutationFn: (body: Record<string, unknown>) => api.post<{ id: string }>("/api/providers", body),
    success: "Provider created",
    invalidates: [keys.providers],
    onSuccess: (data) => {
      onOpenChange(false)
      onCreated?.(data.id)
    },
  })

  const all = presets.data?.presets ?? []
  const filtered = filterPresets(all, { q, surface, authKind, freeTier })
  const surfaceOptions = distinctSorted(all.flatMap((p) => p.surfaces))
  const authKindOptions = distinctSorted(all.map((p) => p.auth_kind))

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setSelected(null)
          setProviderId("")
        }
        onOpenChange(next)
      }}
    >
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>Add provider</DialogTitle>
        </DialogHeader>

        <Tabs defaultValue="browse">
          <TabsList>
            <TabsTrigger value="browse">Browse</TabsTrigger>
            <TabsTrigger value="raw">Raw</TabsTrigger>
          </TabsList>

          <TabsContent value="browse" className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <Input
                placeholder="Search presets"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                className="w-48"
              />
              <FilterSelect label="Surface" value={surface} options={surfaceOptions} onChange={setSurface} />
              <FilterSelect label="Auth kind" value={authKind} options={authKindOptions} onChange={setAuthKind} />
              <div className="flex items-center gap-2">
                <Switch id="preset-free-tier" checked={freeTier} onCheckedChange={setFreeTier} />
                <Label htmlFor="preset-free-tier">Free tier only</Label>
              </div>
            </div>

            <div className="grid max-h-80 grid-cols-2 gap-3 overflow-y-auto">
              {filtered.map((p) => (
                <PresetCard key={p.id} preset={p} onSelect={() => setSelected(p)} />
              ))}
            </div>

            {selected && (
              <div className="flex items-end gap-2 border-t border-[hsl(var(--border))] pt-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="preset-provider-id">Provider ID</Label>
                  <Input
                    id="preset-provider-id"
                    value={providerId}
                    onChange={(e) => setProviderId(e.target.value)}
                    className="w-48"
                  />
                </div>
                <Button
                  disabled={providerId === ""}
                  onClick={() => create.mutate(createBodyFromPreset(selected, { id: providerId }))}
                >
                  Create
                </Button>
              </div>
            )}
          </TabsContent>

          <TabsContent value="raw">
            <RawProviderForm onSubmit={(body) => create.mutate(body)} />
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}
