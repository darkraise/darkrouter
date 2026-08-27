import { useState } from "react"
import {
  Badge,
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  Switch,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, usePresets, useProviders } from "../../lib/queries"
import type { Preset, Provider } from "../../lib/api-types"
import { FilterSelect } from "../requests/filter-select"
import { AccountFields, type AccountDraft, draftAccounts, emptyAccounts, maskSecret } from "./account-fields"
import { addCredentials, reportAdded } from "./accounts"
import { ProviderIcon } from "./provider-icon"

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

/**
 * What has to happen for the chosen provider to hold an account.
 *
 * A provider is defined in code, not created here: the preset catalogue ships
 * with the release. But a preset only becomes a row in the database the first
 * time someone gives it a key, so the first account on a provider carries that
 * one-time materialisation with it. Every account after it is just a key.
 */
export function planFor(
  preset: Preset,
  existing: Provider[],
): { needsProvider: boolean; provider?: Provider } {
  const provider = existing.find((p) => p.id === preset.id)
  return { needsProvider: provider === undefined, provider }
}

function distinctSorted(values: string[]): string[] {
  return [...new Set(values)].sort()
}

const STEPS = ["Provider", "Accounts", "Review"] as const
type Step = 0 | 1 | 2

function Stepper({ step }: { step: Step }) {
  return (
    <ol className="mb-4 flex items-center gap-2" aria-label="Progress">
      {STEPS.map((label, i) => (
        <li key={label} className="flex items-center gap-2">
          <span
            className={
              i === step
                ? "flex h-6 w-6 items-center justify-center rounded-full bg-[hsl(var(--primary))] font-mono text-sm text-[hsl(var(--primary-foreground))]"
                : i < step
                  ? "flex h-6 w-6 items-center justify-center rounded-full bg-[hsl(var(--muted))] font-mono text-sm"
                  : "flex h-6 w-6 items-center justify-center rounded-full border font-mono text-sm text-[hsl(var(--legend))]"
            }
            aria-current={i === step ? "step" : undefined}
          >
            {i < step ? "✓" : i + 1}
          </span>
          <span
            className={
              i === step ? "text-sm font-medium" : "text-sm text-[hsl(var(--legend))]"
            }
          >
            {label}
          </span>
          {i < STEPS.length - 1 && (
            <span className="mx-1 w-6 border-t border-dashed" aria-hidden="true" />
          )}
        </li>
      ))}
    </ol>
  )
}

/** One provider in the picker. Already-configured ones are not hidden: adding
 *  a second key to a provider that has one is the common case, not an edge. */
function PresetRow({
  preset,
  provider,
  selected,
  onSelect,
}: {
  preset: Preset
  provider?: Provider
  selected: boolean
  onSelect: () => void
}) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={selected}
      className={
        selected
          ? "flex w-full items-center gap-3 rounded-[var(--radius)] border border-[hsl(var(--primary))] p-3 text-left"
          : "flex w-full items-center gap-3 rounded-[var(--radius)] border p-3 text-left hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))]"
      }
    >
      <ProviderIcon preset={preset.id} id={preset.id} name={preset.name} size={28} />
      <span className="min-w-0 flex-1">
        <span className="block truncate font-medium">{preset.name}</span>
        <span className="block truncate font-mono text-sm text-[hsl(var(--legend))]">
          {preset.id}
        </span>
      </span>
      {preset.free_tier && <Badge variant="secondary">Free tier</Badge>}
      {provider && (
        <Badge variant="outline">
          {provider.credentials.length}{" "}
          {provider.credentials.length === 1 ? "account" : "accounts"}
        </Badge>
      )}
    </button>
  )
}

/**
 * Add accounts to a provider the release already supports.
 *
 * Three steps, because they are three different jobs: finding one provider
 * among two hundred, pasting keys, and checking what is about to be written.
 * Nothing is sent until the last one is confirmed.
 *
 * There is no provider id to type and no raw form. The set of providers is
 * whatever the release ships; an operator adds keys to one, they do not invent
 * one.
 */
export function AddAccountsDialog({
  open,
  onOpenChange,
  onDone,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone?: (providerId: string) => void
}) {
  const presets = usePresets()
  const providers = useProviders()
  const [step, setStep] = useState<Step>(0)
  const [q, setQ] = useState("")
  const [surface, setSurface] = useState("")
  const [authKind, setAuthKind] = useState("")
  const [freeTier, setFreeTier] = useState(false)
  const [selected, setSelected] = useState<Preset | null>(null)
  const [accounts, setAccounts] = useState<AccountDraft>(emptyAccounts)

  function reset() {
    setStep(0)
    setSelected(null)
    setAccounts(emptyAccounts)
    setQ("")
  }

  const existing = providers.data?.providers ?? []
  const plan = selected ? planFor(selected, existing) : null

  const submit = useApiMutation({
    mutationFn: async () => {
      if (!selected) throw new Error("no provider chosen")
      // The provider row is created only when it does not exist yet, and from
      // the preset alone — id, kind, base URL and auth style all come from the
      // release rather than from anything typed here.
      if (plan?.needsProvider) {
        await api.post<{ id: string }>("/api/providers", {
          id: selected.id,
          preset: selected.id,
          free_models_only: accounts.freeModelsOnly,
        })
      }
      return addCredentials(selected.id, accounts)
    },
    invalidates: [keys.providers, keys.health, keys.overview],
    onSuccess: (result) => {
      reportAdded(result)
      const id = selected?.id
      onOpenChange(false)
      reset()
      if (id) onDone?.(id)
    },
  })

  const all = presets.data?.presets ?? []
  const filtered = filterPresets(all, { q, surface, authKind, freeTier })
  const surfaceOptions = distinctSorted(all.flatMap((p) => p.surfaces))
  const authKindOptions = distinctSorted(all.map((p) => p.auth_kind))
  const planned = draftAccounts(accounts)
  const count = planned.length

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="flex max-h-[85vh] max-w-3xl flex-col overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Add accounts</DialogTitle>
        </DialogHeader>

        <Stepper step={step} />

        {step === 0 && (
          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <Input
                placeholder="Search providers"
                value={q}
                onChange={(e) => setQ(e.target.value)}
                className="w-56"
                aria-label="Search providers"
              />
              <FilterSelect label="Surface" value={surface} options={surfaceOptions} onChange={setSurface} />
              <FilterSelect label="Auth kind" value={authKind} options={authKindOptions} onChange={setAuthKind} />
              <div className="flex items-center gap-2">
                <Switch id="preset-free-tier" checked={freeTier} onCheckedChange={setFreeTier} />
                <Label htmlFor="preset-free-tier">Free tier only</Label>
              </div>
              <span className="ml-auto text-sm text-[hsl(var(--legend))]">
                {filtered.length} of {all.length}
              </span>
            </div>

            <div className="flex max-h-96 flex-col gap-2 overflow-y-auto">
              {filtered.map((p) => (
                <PresetRow
                  key={p.id}
                  preset={p}
                  provider={existing.find((e) => e.id === p.id)}
                  selected={selected?.id === p.id}
                  onSelect={() => {
                    setSelected(p)
                    setStep(1)
                  }}
                />
              ))}
              {filtered.length === 0 && (
                <p className="text-sm text-[hsl(var(--muted-foreground))]">
                  No provider here matches those filters. The catalogue ships with the
                  release — a provider that is missing arrives in a new version, not from
                  this screen.
                </p>
              )}
            </div>
          </div>
        )}

        {step === 1 && selected && (
          <div className="flex flex-col gap-4">
            <div className="flex items-center gap-3 rounded-[var(--radius)] border p-3">
              <ProviderIcon preset={selected.id} id={selected.id} name={selected.name} size={32} />
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{selected.name}</span>
                <span className="block truncate font-mono text-sm text-[hsl(var(--legend))]">
                  {selected.kind} · {selected.base_url}
                </span>
              </span>
            </div>

            {/* Inset rather than full width: it marks a break inside one panel
                -- which provider, then what to give it -- rather than the edge
                between two. */}
            <div className="ml-12 border-t" aria-hidden="true" />

            <AccountFields value={accounts} onChange={setAccounts} autoFocus />
          </div>
        )}

        {step === 2 && selected && (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-3 rounded-[var(--radius)] border p-3">
              <ProviderIcon preset={selected.id} id={selected.id} name={selected.name} size={32} />
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{selected.name}</span>
                <span className="block font-mono text-sm text-[hsl(var(--legend))]">
                  {plan?.needsProvider
                    ? "not configured yet — the first account sets it up"
                    : `${plan?.provider?.credentials.length ?? 0} already configured`}
                </span>
              </span>
              <Badge variant="outline">
                +{count} {count === 1 ? "account" : "accounts"}
              </Badge>
            </div>

            {/* The masked keys, in the order they will be written. This is the
                last point at which a stray line is cheap to notice. */}
            <ul className="flex max-h-48 flex-col gap-1 overflow-y-auto rounded-[var(--radius)] border p-3 font-mono text-sm">
              {planned.map((a) => (
                <li key={a.secret}>
                  {a.label} · {maskSecret(a.secret)}
                </li>
              ))}
            </ul>

            <ul className="flex flex-col gap-1 text-sm text-[hsl(var(--legend))]">
              <li>
                {accounts.freeModelsOnly
                  ? "Discovery will import only models it can show are free."
                  : "Discovery will import every model the provider lists."}
              </li>
              <li>
                {accounts.verifyKeys
                  ? "Each key is probed, and any the provider refuses is removed again."
                  : "Keys are stored without being checked."}
              </li>
            </ul>
          </div>
        )}

        <div className="mt-2 flex items-center gap-2 border-t pt-3">
          {step > 0 && (
            <Button
              variant="ghost"
              onClick={() => setStep((step - 1) as Step)}
              disabled={submit.isPending}
            >
              Back
            </Button>
          )}
          <div className="ml-auto flex items-center gap-2">
            {step === 1 && (
              <>
                {count === 0 && (
                  <span className="text-sm text-[hsl(var(--legend))]">
                    Add at least one key to continue
                  </span>
                )}
                <Button disabled={count === 0} onClick={() => setStep(2)}>
                  Review
                </Button>
              </>
            )}
            {step === 2 && (
              <Button disabled={submit.isPending} onClick={() => submit.mutate(undefined)}>
                {count === 1 ? "Add account" : `Add ${count} accounts`}
              </Button>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
