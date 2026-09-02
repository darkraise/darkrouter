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
  PasswordInput,
  PasswordInputControl,
  PasswordInputField,
} from "darkraise-ui"
import { useQueryClient } from "@tanstack/react-query"
import { api } from "../../lib/api"
import { keys, usePresets, useProviders } from "../../lib/queries"
import type { Preset } from "../../lib/api-types"
import { PasswordToggle } from "../shell/password-toggle"
import { ProviderIcon } from "./provider-icon"
import { isLoopbackUrl, localRuntimes, validBaseUrl, withHost } from "./local-runtimes"
import { addLocalRuntime, testLocalRuntime, type LocalOutcome } from "./local-add"

/**
 * Where a runtime on this machine is reached from inside the container.
 *
 * Every local preset ships a localhost base URL, and localhost inside the
 * container is the container. compose.yml maps this name to the host gateway,
 * so it is the address that works on a default install; an operator running
 * darkrouter outside a container keeps localhost instead. The form starts from
 * the preset's own address and offers this, rather than starting here, so what
 * it shows first is the address the runtime's own documentation gives.
 */
const CONTAINER_HOST = "host.docker.internal"

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
 *
 * It is also what a local row's own Add button opens. The keyless dialog would
 * otherwise ask the one question that cannot apply here — which models to
 * import by price, of a server whose models are all free — and none of the one
 * that does, which is where the server is listening.
 */
export function AddLocalDialog({
  preset,
  open,
  onOpenChange,
  onDone,
}: {
  /** Preselects a runtime, for the row that already named one. Null opens the
   *  picker, which is what the header button means. */
  preset?: Preset | null
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone: (id: string) => void
}) {
  const presets = usePresets()
  const providers = useProviders()
  const queryClient = useQueryClient()

  const [selectedId, setSelectedId] = useState("")
  // The whole address, not a host and a port composed into one. Those two were
  // a way to build a URL back when the URL itself was not editable; now that it
  // is, they would be a second way to say the same thing.
  const [baseUrl, setBaseUrl] = useState("")
  const [apiKey, setApiKey] = useState("")
  const [outcome, setOutcome] = useState<LocalOutcome | null>(null)
  const [pending, setPending] = useState<"test" | "add" | null>(null)
  // False rather than `open`, so a dialog that mounts already open still runs
  // the reset below and picks up the preset it was mounted on.
  const [wasOpen, setWasOpen] = useState(false)
  const [wasPreset, setWasPreset] = useState<string | null>(null)

  const runtimes = localRuntimes(presets.data?.presets ?? [])

  function choose(p: Preset) {
    setSelectedId(p.id)
    setBaseUrl(p.base_url)
    setApiKey("")
    setOutcome(null)
  }

  function clear() {
    setSelectedId("")
    setBaseUrl("")
    setApiKey("")
    setOutcome(null)
  }

  // Each visit starts from the preset it was opened on, or from nothing. The
  // dialog outlives any one visit, so an address left over from a failed
  // attempt would be reused silently by the next one, which reads as the add
  // failing for no reason. A swapped preset counts as a new visit too: the
  // caller may aim the dialog at another row without closing it in between.
  const presetId = preset?.id ?? null
  if (open !== wasOpen || (open && presetId !== wasPreset)) {
    setWasOpen(open)
    setWasPreset(presetId)
    if (open) {
      if (preset) choose(preset)
      else clear()
    }
  }

  const selected = runtimes.find((p) => p.id === selectedId) ?? (preset ?? undefined)
  const existing = providers.data?.providers?.find((p) => p.id === selectedId)
  const endpoint = validBaseUrl(baseUrl) ? baseUrl.trim() : null
  const loopback = endpoint !== null && isLoopbackUrl(endpoint)

  async function run(action: "test" | "add") {
    if (!selected || !endpoint) return
    setPending(action)
    setOutcome(null)
    const secret = apiKey.trim()
    const draft = {
      presetId: selected.id,
      baseUrl: endpoint,
      ...(secret === "" ? {} : { apiKey: secret }),
    }
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
            A model server running on this machine. What it needs is the address the
            gateway can reach it on — and a key only if you started it behind one.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          {preset ? (
            // The row already named the runtime. A picker here would ask an
            // operator to find, among a dozen, the one they just pressed.
            <div className="flex items-center gap-3 rounded-[var(--radius)] border p-3">
              <ProviderIcon preset={preset.id} id={preset.id} name={preset.name} size={28} />
              <span className="min-w-0 flex-1">
                <span className="block truncate font-medium">{preset.name}</span>
                <span className="block truncate font-mono text-sm text-[hsl(var(--legend))]">
                  {preset.id} · {preset.kind}
                </span>
              </span>
            </div>
          ) : (
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
          )}

          {selected && (
            <>
              <div className="flex flex-col gap-1.5 border-t pt-4">
                <Label htmlFor="local-base-url">Base URL</Label>
                <Input
                  id="local-base-url"
                  value={baseUrl}
                  onChange={(e) => setBaseUrl(e.target.value)}
                  spellCheck={false}
                  placeholder="http://localhost:11434/v1"
                  className="font-mono"
                />
                {baseUrl.trim() !== "" && endpoint === null ? (
                  <p className="text-sm text-[hsl(var(--destructive))]">
                    Not an http or https URL yet.
                  </p>
                ) : (
                  loopback && (
                    <p className="max-w-prose text-sm text-[hsl(var(--legend))]">
                      If darkrouter runs in a container, this address is the container
                      itself rather than your machine.{" "}
                      <button
                        type="button"
                        className="underline underline-offset-2"
                        onClick={() => setBaseUrl(withHost(baseUrl, CONTAINER_HOST))}
                      >
                        Use {CONTAINER_HOST}
                      </button>
                    </p>
                  )
                )}
              </div>

              <div className="flex flex-col gap-1.5 border-t pt-4">
                <Label htmlFor="local-api-key">API key</Label>
                <PasswordInput className="w-72">
                  <PasswordInputControl>
                    <PasswordInputField
                      id="local-api-key"
                      value={apiKey}
                      onChange={(e) => setApiKey(e.target.value)}
                      placeholder="Leave empty — most runtimes need none"
                    />
                    <PasswordToggle />
                  </PasswordInputControl>
                </PasswordInput>
                <p className="max-w-prose text-sm text-[hsl(var(--legend))]">
                  A runtime started with a token — vLLM or llama.cpp behind{" "}
                  <span className="font-mono">--api-key</span> — needs it here. It goes
                  out as an <span className="font-mono">Authorization: Bearer</span>{" "}
                  header, which is the only form a local server reads. Without one the
                  provider is added keyless.
                </p>
              </div>

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
            disabled={!endpoint || existing !== undefined || pending !== null}
            onClick={() => void run("test")}
          >
            {pending === "test" ? "Testing…" : "Test connection"}
          </Button>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={pending !== null}>
              Cancel
            </Button>
            <Button
              disabled={!endpoint || existing !== undefined || pending !== null}
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
