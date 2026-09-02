import { useState } from "react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Listbox,
  ListboxItem,
} from "darkraise-ui"
import { useQueryClient } from "@tanstack/react-query"
import { api } from "../../lib/api"
import { keys, usePresets, useProviders } from "../../lib/queries"
import type { Preset } from "../../lib/api-types"
import { ProviderIcon } from "./provider-icon"
import { composeBaseUrl, localRuntimes, portOf } from "./local-runtimes"
import { addLocalRuntime, testLocalRuntime, type LocalOutcome } from "./local-add"

/**
 * Where a runtime on this machine is reached from inside the container.
 *
 * Every local preset ships a localhost base URL, and localhost inside the
 * container is the container. compose.yml maps this name to the host gateway,
 * so it is the address that works on a default install; an operator running
 * darkrouter outside a container replaces it with localhost.
 */
const DEFAULT_HOST = "host.docker.internal"

function outcomeMessage(o: LocalOutcome): string {
  if (!o.ok) return o.error
  return o.modelCount === undefined
    ? "The endpoint answered."
    : `The endpoint answered with ${o.modelCount} models.`
}

/**
 * Adds a model runtime running on the operator's own machine.
 *
 * Separate from the credential dialog because the two ask opposite questions.
 * That one exists to collect a secret, and a preset only becomes a provider
 * row when someone supplies one; a local runtime has no secret at all, so the
 * thing that materialises the row here is the address instead.
 */
export function AddLocalDialog({
  open,
  onOpenChange,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: (id: string) => void
}) {
  const presets = usePresets()
  const providers = useProviders()
  const queryClient = useQueryClient()

  const [selectedId, setSelectedId] = useState("")
  const [host, setHost] = useState(DEFAULT_HOST)
  const [port, setPort] = useState("")
  const [outcome, setOutcome] = useState<LocalOutcome | null>(null)
  const [pending, setPending] = useState<"test" | "add" | null>(null)
  const [wasOpen, setWasOpen] = useState(open)

  // Each visit starts empty. The dialog outlives any one visit, so a port left
  // over from a failed attempt would be reused silently by the next one, which
  // reads as the add failing for no reason.
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setSelectedId("")
      setHost(DEFAULT_HOST)
      setPort("")
      setOutcome(null)
    }
  }

  const runtimes = localRuntimes(presets.data?.presets ?? [])
  const selected = runtimes.find((p) => p.id === selectedId)
  const existing = providers.data?.providers?.find((p) => p.id === selectedId)
  const baseUrl = selected ? composeBaseUrl(selected, host, port) : null

  function choose(p: Preset) {
    setSelectedId(p.id)
    setPort(portOf(p))
    setOutcome(null)
  }

  async function run(action: "test" | "add") {
    if (!selected || !baseUrl) return
    setPending(action)
    setOutcome(null)
    const draft = { presetId: selected.id, baseUrl }
    const result =
      action === "add"
        ? await addLocalRuntime(api, draft)
        : await testLocalRuntime(api, draft)
    setPending(null)
    setOutcome(result)
    // Both paths create and may delete a provider, so the list is stale
    // either way — including after a rollback, which removed one.
    void queryClient.invalidateQueries({ queryKey: keys.providers })
    if (action === "add" && result.ok) onDone(selected.id)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85vh] max-w-xl flex-col overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Add a local runtime</DialogTitle>
          <DialogDescription>
            A model server running on this machine. There is no key to paste — what
            it needs is the address the gateway can reach it on.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label id="local-runtime-label">Runtime</Label>
            <Listbox
              mode="single"
              variant="outline"
              aria-labelledby="local-runtime-label"
              value={selectedId}
              onValueChange={(next) => {
                const id = Array.isArray(next) ? next[0] : next
                const p = runtimes.find((r) => r.id === id)
                if (p && p.id !== selectedId) choose(p)
              }}
              className="max-h-64 overflow-y-auto"
            >
              {runtimes.map((p) => (
                <ListboxItem
                  key={p.id}
                  value={p.id}
                  textValue={p.name}
                  className={
                    p.id === selectedId
                      ? "flex w-full items-center gap-3 rounded-[var(--radius)] border border-[hsl(var(--primary))] p-3 text-left"
                      : "flex w-full items-center gap-3 rounded-[var(--radius)] border p-3 text-left"
                  }
                >
                  <ProviderIcon preset={p.id} id={p.id} name={p.name} size={28} />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{p.name}</span>
                    <span className="block truncate font-mono text-sm text-[hsl(var(--legend))]">
                      {p.id}
                    </span>
                  </span>
                </ListboxItem>
              ))}
            </Listbox>
          </div>

          {selected && (
            <>
              <div className="flex flex-wrap items-end gap-3 border-t pt-4">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="local-host">Host</Label>
                  <Input
                    id="local-host"
                    value={host}
                    onChange={(e) => setHost(e.target.value)}
                    spellCheck={false}
                    className="w-64 font-mono"
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="local-port">Port</Label>
                  <Input
                    id="local-port"
                    value={port}
                    onChange={(e) => setPort(e.target.value)}
                    inputMode="numeric"
                    className="w-28 font-mono"
                  />
                </div>
              </div>

              <p className="text-sm text-[hsl(var(--legend))]">
                {baseUrl ? (
                  <span className="font-mono text-[hsl(var(--foreground))]">{baseUrl}</span>
                ) : (
                  "Fill in a host and a port to see the endpoint."
                )}
              </p>

              {existing && (
                <p className="text-sm text-[hsl(var(--legend))]">
                  {selected.name} is already added. Change its address in the provider's
                  own settings rather than adding it twice.
                </p>
              )}

              {outcome && (
                <p
                  className={
                    outcome.ok
                      ? "text-sm text-[hsl(var(--foreground))]"
                      : "text-sm text-[hsl(var(--destructive))]"
                  }
                  role="status"
                >
                  {outcomeMessage(outcome)}
                </p>
              )}
            </>
          )}
        </div>

        <div className="mt-2 flex items-center gap-2 border-t pt-3">
          <Button
            variant="outline"
            disabled={!baseUrl || existing !== undefined || pending !== null}
            onClick={() => void run("test")}
          >
            {pending === "test" ? "Testing…" : "Test connection"}
          </Button>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={pending !== null}>
              Cancel
            </Button>
            <Button
              disabled={!baseUrl || existing !== undefined || pending !== null}
              onClick={() => void run("add")}
            >
              {pending === "add" ? "Adding…" : "Add runtime"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
