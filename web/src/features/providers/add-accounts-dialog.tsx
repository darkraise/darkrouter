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
  Listbox,
  ListboxItem,
  Progress,
  Switch,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, usePresets, useProviders } from "../../lib/queries"
import type { Preset, Provider } from "../../lib/api-types"
import { FilterSelect } from "../requests/filter-select"
import { isLocalPreset } from "./local-runtimes"
import {
  AccountFields,
  emptyAccounts,
  needsAccount,
  secretFieldFor,
  type AccountDraft,
} from "./account-fields"
import {
  addAccountsLabel,
  addCredentials,
  countAccounts,
  progressLabel,
  reportAdded,
  retryDraft,
  type AddFailure,
  type AddProgress,
} from "./accounts"
import { ProviderIcon } from "./provider-icon"

export function filterPresets(
  presets: Preset[],
  f: {
    q?: string
    surface?: string
    authKind?: string
    freeTier?: boolean
    /** Ids the picker must not offer at all. Applied before the search term,
     *  so an excluded preset cannot be typed back into view either. */
    exclude?: ReadonlySet<string>
  },
): Preset[] {
  const q = (f.q ?? "").toLowerCase()
  return presets.filter((p) => {
    if (f.exclude?.has(p.id)) return false
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

/**
 * Whether submitting also has to write the provider's free-models setting.
 *
 * A provider that is about to be created carries the flag in its POST, and one
 * whose setting the operator left alone needs no write at all — so this is
 * true only for a change to a provider that already exists.
 */
export function freeOnlyChange(
  draft: AccountDraft,
  plan: { needsProvider: boolean; provider?: Provider } | null,
): boolean {
  if (!plan || plan.needsProvider || !plan.provider) return false
  return draft.freeModelsOnly !== plan.provider.free_models_only
}

/** What a fresh visit starts from. The free-models box is a provider setting,
 *  so against a provider that already exists it opens on what that provider
 *  holds: an unticked box over a free-only provider would misreport it, and
 *  saving would then quietly turn the setting off. */
function draftFor(provider?: Provider): AccountDraft {
  return provider
    ? { ...emptyAccounts, freeModelsOnly: provider.free_models_only }
    : emptyAccounts
}

/** Where a cloud provider's endpoint lives. The release cannot ship these:
 *  Bedrock's host is derived from the region, and Vertex puts the project and
 *  location in every request path, so a row created without them can reach
 *  nothing. */
export type EndpointDraft = { region: string; project: string; location: string }

const emptyEndpoint: EndpointDraft = { region: "", project: "", location: "" }

export function endpointFieldsFor(kind?: string): (keyof EndpointDraft)[] {
  if (kind === "bedrock") return ["region"]
  if (kind === "vertex") return ["project", "location"]
  return []
}

const ENDPOINT_FIELD: Record<keyof EndpointDraft, { label: string; placeholder: string }> = {
  region: { label: "Region", placeholder: "us-east-1" },
  project: { label: "Project", placeholder: "my-project" },
  location: { label: "Location", placeholder: "us-central1" },
}

function distinctSorted(values: string[]): string[] {
  return [...new Set(values)].sort()
}

/** The screens, in order. Opened from a provider that already exists there is
 *  nothing to pick, so the same step index means a different screen — which is
 *  why the sequence is a value rather than a constant with fixed indices.
 *
 *  There is no review screen. Everything it restated is on the accounts step
 *  and still editable there — the parsed accounts, the two settings and what
 *  each does — so it asked the operator to read the form back to itself before
 *  letting them submit the form. */
export type Phase = "provider" | "accounts"

export function phases(locked: boolean): Phase[] {
  return locked ? ["accounts"] : ["provider", "accounts"]
}

const PHASE_LABEL: Record<Phase, string> = {
  provider: "Provider",
  accounts: "Credentials",
}

/** What the accounts are being added to. A preset and a provider disagree
 *  about most things and agree about these, which is all the summary
 *  strips and the write path need. */
type Chosen = {
  id: string
  name: string
  kind: string
  base_url: string
  /** Which brand mark to draw. A preset's own id is its preset, but a provider
   *  imported from config can carry a different one — reading `id` there shows
   *  an anonymous monogram beside a detail page showing the real mark. */
  preset?: string
  /** How it authenticates — a provider's own, or the preset's — which decides
   *  what shape of secret the gateway will parse. */
  auth_style?: string
  auth_kind?: string
}

/* Hand-rolled on purpose. darkraise-ui ships `Steps`, and it was tried here:
   it is a wizard indicator, so the shape is right. But `StepsIndicator`
   renders no content of its own, so the step numbers and the completed tick
   disappear unless you pass them back in, and the component sets no
   `aria-current` — which is the one thing this display-only strip has to say,
   and the one thing the markup below already gets right. Swapping would have
   traded an <ol> with aria-current for a div stack that needs the numbers
   re-supplied. Verified in the running console before reverting. */
function Stepper({ steps, step }: { steps: Phase[]; step: number }) {
  return (
    <ol className="mb-4 flex items-center gap-2" aria-label="Progress">
      {steps.map((phase, i) => (
        <li key={phase} className="flex items-center gap-2">
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
            {PHASE_LABEL[phase]}
          </span>
          {i < steps.length - 1 && (
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
}: {
  preset: Preset
  provider?: Provider
  selected: boolean
}) {
  // An option in a single-choice list, which is what it always was.
  // `aria-pressed` announced it as a toggle button, and a list of two hundred
  // toggle buttons costs a Tab per row to walk; a listbox is one Tab in and
  // then arrows, with typeahead over the provider name.
  return (
    <ListboxItem
      value={preset.id}
      textValue={preset.name}
      className={
        selected
          ? "flex w-full items-center gap-3 rounded-[var(--radius)] border border-[hsl(var(--primary))] p-3 text-left"
          : "flex w-full items-center gap-3 rounded-[var(--radius)] border p-3 text-left"
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
          {provider.credentials.length === 1 ? "credential" : "credentials"}
        </Badge>
      )}
    </ListboxItem>
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
 *
 * Opened from a provider's own page there is one step rather than two: the
 * provider is settled, and asking again for something the operator has already
 * navigated to would be a step that can only be answered one way.
 */
export function AddAccountsDialog({
  open,
  onOpenChange,
  onDone,
  provider,
  preset,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onDone?: (providerId: string) => void
  /** Set when the dialog opens from a provider that already exists: the picker
   *  is skipped and no provider row is created. */
  provider?: Provider
  /** Set when it opens from a provider the release supports but nobody has
   *  configured. The picker is skipped for the same reason — the operator
   *  named the provider by navigating to it — and the first account creates
   *  its row. */
  preset?: Preset
}) {
  const presets = usePresets()
  const providers = useProviders()
  const [step, setStep] = useState(0)
  const [q, setQ] = useState("")
  const [surface, setSurface] = useState("")
  const [authKind, setAuthKind] = useState("")
  const [freeTier, setFreeTier] = useState(false)
  const [selected, setSelected] = useState<Preset | null>(null)
  const [accounts, setAccounts] = useState<AccountDraft>(() => draftFor(provider))
  const [endpoint, setEndpoint] = useState<EndpointDraft>(emptyEndpoint)
  const [progress, setProgress] = useState<AddProgress | null>(null)
  // What the last run could not store, and why. Held in the dialog as well as
  // toasted, because the form stays open for a retry and a toast does not.
  const [notAdded, setNotAdded] = useState<AddFailure[]>([])
  // The provider row this visit created. The providers list does not show it
  // until its refetch lands, and a retry sent before then must not POST it
  // again: that 409s and abandons the keys being retried.
  const [createdId, setCreatedId] = useState<string | null>(null)
  const [wasOpen, setWasOpen] = useState(open)

  // The row the free-models box reads its setting from. A preset usually has
  // none — that is what makes it a preset — but one can appear between the
  // page loading and the dialog opening, and the box has to show what that row
  // holds rather than write a default over it.
  const settled =
    provider ?? providers.data?.providers?.find((p) => p.id === preset?.id)

  // Each visit re-reads the provider's free-models setting. The dialog outlives
  // any one visit, so a draft seeded when it mounted would still be showing --
  // and would write back -- a value that has since changed elsewhere.
  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) setAccounts(draftFor(settled))
  }

  const locked = provider !== undefined || preset !== undefined
  const steps = phases(locked)
  const phase = steps[Math.min(step, steps.length - 1)] ?? "accounts"
  const chosen: Chosen | null = provider ?? preset ?? selected

  function reset() {
    setStep(0)
    setSelected(null)
    setAccounts(draftFor(settled))
    setEndpoint(emptyEndpoint)
    setProgress(null)
    setNotAdded([])
    setCreatedId(null)
    setQ("")
  }

  const existing = providers.data?.providers ?? []
  // A preset goes through planFor rather than straight to "create it": the row
  // may have appeared since the page loaded, and a second POST would 409
  // against it.
  const target = preset ?? selected
  const planned = provider
    ? { needsProvider: false, provider }
    : target
      ? planFor(target, existing)
      : null
  const plan =
    planned?.needsProvider && createdId !== null && createdId === target?.id
      ? { needsProvider: false }
      : planned
  // Asked only of a row about to be created: an existing one already holds
  // them, and its settings are where they change.
  const endpointFields = plan?.needsProvider ? endpointFieldsFor(chosen?.kind) : []
  const endpointMissing = endpointFields.some((f) => endpoint[f].trim() === "")

  const submit = useApiMutation({
    mutationFn: async () => {
      if (!chosen) throw new Error("no provider chosen")
      setProgress(null)
      setNotAdded([])
      // The provider row is created only when it does not exist yet, and from
      // the preset alone — id, kind, base URL and auth style all come from the
      // release rather than from anything typed here.
      if (plan?.needsProvider) {
        await api.post<{ id: string }>("/api/providers", {
          id: chosen.id,
          preset: chosen.id,
          free_models_only: accounts.freeModelsOnly,
          ...Object.fromEntries(endpointFields.map((f) => [f, endpoint[f].trim()])),
        })
        setCreatedId(chosen.id)
      } else if (freeOnlyChange(accounts, plan)) {
        // Against a provider that already exists the flag is a setting to be
        // written, not part of the POST that creates the row. Sent on its own
        // so the box means the same thing here as it does on the provider's
        // settings, rather than being a control that looks applied and is not.
        await api.patch(`/api/providers/${chosen.id}`, {
          free_models_only: accounts.freeModelsOnly,
        })
      }
      return addCredentials(chosen.id, accounts, needsAccount(chosen.base_url), setProgress)
    },
    // The catalogue too: the first credential makes the provider
    // discoverable, and a sweep lands models the screen that opened this
    // dialog is showing.
    invalidates: [keys.providers, keys.health, keys.overview, keys.models, keys.discovery],
    onSuccess: (result) => {
      reportAdded(result)
      if (result.retry.length > 0) {
        // Closing would throw away the keys that did not go in along with the
        // reason, and the operator may have them nowhere else.
        setProgress(null)
        setNotAdded(result.retry)
        setAccounts((a) => retryDraft(a, result.retry, needsAccount(chosen?.base_url)))
        return
      }
      const id = chosen?.id
      onOpenChange(false)
      reset()
      if (id) onDone?.(id)
    },
  })

  const all = presets.data?.presets ?? []
  // A local runtime that already has a row is not a candidate for a
  // credential: what it needed was the address it listens on, and that is what
  // the row is. Ordinary providers stay listed however many credentials they
  // already hold, because a second key is a real thing to want.
  const configuredIds = new Set((providers.data?.providers ?? []).map((p) => p.id))
  const exclude = new Set(
    all.filter((p) => isLocalPreset(p) && configuredIds.has(p.id)).map((p) => p.id),
  )
  const filtered = filterPresets(all, { q, surface, authKind, freeTier, exclude })
  const surfaceOptions = distinctSorted(all.flatMap((p) => p.surfaces))
  const authKindOptions = distinctSorted(all.map((p) => p.auth_kind))
  const count = countAccounts(accounts, needsAccount(chosen?.base_url))

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
          <DialogTitle>Add credentials</DialogTitle>
        </DialogHeader>

        {/* A one-step progress indicator is a claim that there is a sequence
            to follow. Opened from a provider there is not: it is a form. */}
        {steps.length > 1 && <Stepper steps={steps} step={step} />}

        {phase === "provider" && (
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

            <Listbox
              mode="single"
              variant="outline"
              value={selected?.id ?? ""}
              onValueChange={(next) => {
                const id = Array.isArray(next) ? next[0] : next
                const p = filtered.find((f) => f.id === id)
                // Re-reporting the row already chosen must not advance the
                // wizard a second time, which a click on a selected row does.
                if (!p || p.id === selected?.id) return
                // A fresh draft, not the last one with its setting swapped: a
                // key typed for the provider left behind would otherwise be
                // stored on, and probed against, this one. A provider that
                // already exists brings its own free-models setting.
                setAccounts(draftFor(existing.find((e) => e.id === p.id)))
                setEndpoint(emptyEndpoint)
                setSelected(p)
                setStep(step + 1)
              }}
              className="flex max-h-96 flex-col gap-2 overflow-y-auto"
            >
              {filtered.map((p) => (
                <PresetRow
                  key={p.id}
                  preset={p}
                  provider={existing.find((e) => e.id === p.id)}
                  selected={selected?.id === p.id}
                />
              ))}
              {filtered.length === 0 && (
                <p className="text-sm text-[hsl(var(--muted-foreground))]">
                  No provider here matches those filters. The catalogue ships with the
                  release — a provider that is missing arrives in a new version, not from
                  this screen.
                </p>
              )}
            </Listbox>
          </div>
        )}

        {phase === "accounts" && chosen && (
          <div className="flex flex-col gap-4">
            <div className="flex items-center gap-3 rounded-[var(--radius)] border p-3">
              <ProviderIcon preset={chosen.preset ?? chosen.id} id={chosen.id} name={chosen.name} size={32} />
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{chosen.name}</span>
                <span className="block truncate font-mono text-sm text-[hsl(var(--legend))]">
                  {chosen.kind} · {chosen.base_url}
                </span>
              </span>
            </div>

            {/* Inset rather than full width: it marks a break inside one panel
                -- which provider, then what to give it -- rather than the edge
                between two. */}
            <div className="ml-12 border-t" aria-hidden="true" />

            <AccountFields
              needsAccount={needsAccount(chosen?.base_url)}
              value={accounts}
              onChange={setAccounts}
              autoFocus
              field={secretFieldFor(
                chosen.preset ?? chosen.id,
                chosen.auth_style || chosen.auth_kind,
              )}
            />

            {endpointFields.length > 0 && (
              <div className="flex flex-wrap items-end gap-3 border-t pt-4">
                {endpointFields.map((f) => (
                  <div key={f} className="flex flex-col gap-1.5">
                    <Label htmlFor={`endpoint-${f}`}>{ENDPOINT_FIELD[f].label}</Label>
                    <Input
                      id={`endpoint-${f}`}
                      value={endpoint[f]}
                      onChange={(e) => setEndpoint({ ...endpoint, [f]: e.target.value })}
                      placeholder={ENDPOINT_FIELD[f].placeholder}
                      spellCheck={false}
                      className="w-40 font-mono"
                    />
                  </div>
                ))}
                <p className="max-w-xs text-sm text-[hsl(var(--legend))]">
                  Where this provider's endpoint is. Its requests go nowhere without it.
                </p>
              </div>
            )}
          </div>
        )}

        <div className="mt-2 flex flex-col gap-2 border-t pt-3">
          {/* Checking a key is a round trip to the provider per account, so a
              paste of twenty is a wait long enough that silence reads as a
              hang. The line names the account it is on, because the one it
              stops at is the one an operator needs to look at. */}
          {submit.isPending && progress && (
            <div className="flex flex-col gap-1">
              <div className="flex items-baseline justify-between gap-2">
                <span className="text-sm">{progressLabel(progress)}</span>
                <span className="text-sm tabular-nums text-[hsl(var(--legend))]">
                  {Math.round((progress.done / progress.total) * 100)}%
                </span>
              </div>
              <Progress value={progress.done} max={progress.total} />
            </div>
          )}

          {notAdded.length > 0 && (
            <div role="alert" className="flex flex-col gap-1 text-sm text-[hsl(var(--destructive))]">
              <span className="font-medium">
                Not added — still in the form to fix and send again
              </span>
              <ul className="flex flex-col gap-0.5">
                {notAdded.map((f, i) => (
                  <li key={i}>
                    <span className="font-mono">{f.label}</span>: {f.error}
                  </li>
                ))}
              </ul>
            </div>
          )}

          <div className="flex items-center gap-2">
            {step > 0 && (
              <Button
                variant="ghost"
                onClick={() => setStep(step - 1)}
                disabled={submit.isPending}
              >
                Back
              </Button>
            )}
            <div className="ml-auto flex items-center gap-2">
              {phase === "accounts" && (
                <>
                  {count === 0 ? (
                    <span className="text-sm text-[hsl(var(--legend))]">
                      Add at least one key to continue
                    </span>
                  ) : (
                    endpointMissing && (
                      <span className="text-sm text-[hsl(var(--legend))]">
                        Fill in {endpointFields.map((f) => ENDPOINT_FIELD[f].label.toLowerCase()).join(" and ")} to continue
                      </span>
                    )
                  )}
                  <Button
                    disabled={count === 0 || endpointMissing || submit.isPending}
                    onClick={() => submit.mutate(undefined)}
                  >
                    {submit.isPending ? "Adding…" : addAccountsLabel(count)}
                  </Button>
                </>
              )}
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
