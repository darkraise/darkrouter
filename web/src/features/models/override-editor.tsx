import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Badge, Button, Input, Label, Sheet, SheetContent, SheetFooter, SheetHeader, SheetTitle, Switch } from "darkraise-ui"
import { api, ApiError } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"
import type { ModelCapabilities, ModelOverride } from "../../lib/api-types"
import { ConfirmButton } from "../shell/confirm-button"

function overridePath(provider: string, model: string) {
  return `/api/models/${provider}/${model}/override`
}

async function fetchOverride(provider: string, model: string): Promise<ModelOverride | null> {
  try {
    return await api.get<ModelOverride>(overridePath(provider, model))
  } catch (err) {
    // No override is the ordinary case for most of the catalog, not a
    // failure — an error banner over the ordinary case teaches an operator
    // to stop reading banners.
    if (err instanceof ApiError && err.status === 404) return null
    throw err
  }
}

const CAPABILITIES = ["tools", "vision", "reasoning"] as const

/**
 * `model_overrides` sits at the top of the merge precedence for capabilities,
 * context window and surfaces, and until now nothing in the product could
 * write one.
 *
 * PUT replaces the whole row: a field the body omits is written NULL, not
 * left as it was. Save therefore resends every value the editor currently
 * shows, not just the one the operator touched. Each field still tracks its
 * own draft, separately from the loaded override, purely so an untouched
 * control keeps displaying the loaded value until it is edited.
 */
export function OverrideEditor({
  provider,
  model,
  onClose,
}: {
  provider: string
  model: string
  onClose: () => void
}) {
  const query = useQuery({
    queryKey: keys.override(provider, model),
    queryFn: () => fetchOverride(provider, model),
  })
  const existing = query.data ?? null

  const [draftContextWindow, setDraftContextWindow] = useState<string | null>(null)
  const [draftCapabilities, setDraftCapabilities] = useState<ModelCapabilities | null>(null)
  const [draftSurfaces, setDraftSurfaces] = useState<string | null>(null)

  function resetDrafts() {
    setDraftContextWindow(null)
    setDraftCapabilities(null)
    setDraftSurfaces(null)
  }

  const save = useApiMutation({
    mutationFn: (patch: Partial<ModelOverride>) =>
      api.put<ModelOverride>(overridePath(provider, model), patch),
    success: "Override saved",
    invalidates: [keys.models, keys.override(provider, model)],
    onSuccess: resetDrafts,
  })

  const remove = useApiMutation({
    mutationFn: () => api.del<void>(overridePath(provider, model)),
    success: "Override removed",
    invalidates: [keys.models, keys.override(provider, model)],
    onSuccess: resetDrafts,
  })

  const contextWindowValue =
    draftContextWindow ??
    (existing?.context_window !== undefined ? String(existing.context_window) : "")
  const surfacesValue = draftSurfaces ?? existing?.surfaces?.join(", ") ?? ""

  function capability(key: keyof ModelCapabilities): boolean {
    return draftCapabilities?.[key] ?? existing?.capabilities?.[key] ?? false
  }

  function setCapability(key: keyof ModelCapabilities, value: boolean) {
    setDraftCapabilities((d) => ({ ...(d ?? {}), [key]: value }))
  }

  // Built from the displayed values, not the drafts alone: PUT is a full
  // replace, so a field left at its loaded value still has to be resent or
  // the replace writes it away. This is also why capabilities is always sent
  // whole — capability() already merges a partial draft over the loaded
  // object one key at a time.
  function buildPatch(): Partial<ModelOverride> {
    const patch: Partial<ModelOverride> = {}
    if (contextWindowValue.trim() !== "") {
      patch.context_window = Number(contextWindowValue)
    }
    const capabilities: ModelCapabilities = {}
    for (const key of CAPABILITIES) capabilities[key] = capability(key)
    patch.capabilities = capabilities
    patch.surfaces = surfacesValue
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean)
    return patch
  }

  return (
    <Sheet open onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="overflow-y-auto">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-2 font-mono text-sm">
            {provider}/{model}
            {existing !== null && <Badge variant="secondary">overridden</Badge>}
          </SheetTitle>
        </SheetHeader>

        {query.isError && (
          <p role="alert" className="mt-4 text-sm text-[hsl(var(--destructive))]">
            Could not load the current override.
          </p>
        )}

        <div className="mt-4 flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="override-context-window">Context window</Label>
            <Input
              id="override-context-window"
              type="number"
              value={contextWindowValue}
              onChange={(e) => setDraftContextWindow(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            {CAPABILITIES.map((key) => (
              <div key={key} className="flex items-center gap-2">
                <Switch
                  id={`override-${key}`}
                  checked={capability(key)}
                  onCheckedChange={(checked) => setCapability(key, checked)}
                />
                <Label htmlFor={`override-${key}`} className="capitalize">
                  {key}
                </Label>
              </div>
            ))}
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="override-surfaces">Surfaces</Label>
            <Input
              id="override-surfaces"
              placeholder="llm, embedding, image"
              value={surfacesValue}
              onChange={(e) => setDraftSurfaces(e.target.value)}
            />
          </div>
        </div>

        <SheetFooter className="mt-6 flex-row justify-between">
          <ConfirmButton
            variant="destructive"
            disabled={existing === null}
            title={`Remove the override on ${model}?`}
            description="The model goes back to whatever the catalogue says about it, which is what discovery imported or what was inferred from the name."
            confirmLabel="Remove override"
            destructive
            onConfirm={() => remove.mutate(undefined)}
          >
            Remove override
          </ConfirmButton>
          <Button onClick={() => save.mutate(buildPatch())}>Save</Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
