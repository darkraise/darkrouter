import { useState } from "react"
import { Link, useParams } from "@tanstack/react-router"
import {
  Badge,
  Button,
  Card,
  Checkbox,
  Input,
  Label,
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import {
  keys,
  useDiscoveryHealth,
  useModels,
  useProviderHealth,
  useProviders,
  useUsage,
} from "../../lib/queries"
import type { Provider } from "../../lib/api-types"
import { AccountFields, type AccountDraft, emptyAccounts } from "./account-fields"
import { addAccountsLabel, addCredentials, countAccounts, reportAdded } from "./accounts"
import { CredentialRow } from "./credential-row"
import { DiscoveryPanel } from "./discovery-panel"
import { ProbePanel } from "./probe-panel"
import { ProviderIcon } from "./provider-icon"
import { ProviderModels } from "./provider-models"
import { CapabilityBars, Sparkline, Stat } from "./provider-summary"
import {
  accountSummary,
  capabilityCount,
  discoveryFraction,
  discoveryNote,
  modelsFor,
  requestsByDay,
  totalRequests,
} from "./provider-stats"
import { STATE_VARIANT, breakersFor, providerState } from "./provider-state"

/**
 * Only the touched half of the region/project patch.
 *
 * Both are pointer fields on the backend (`store.ProviderPatch.Region` /
 * `.Project`): a key present with value "" means "set this to empty", not
 * "leave alone". `GET /api/providers` never returns either field, so the
 * inputs here start with nothing to prefill — null distinguishes "never
 * touched" from "touched and cleared", which "" alone cannot.
 */
export function locationPatch(
  region: string | null,
  project: string | null,
): Record<string, string> {
  const patch: Record<string, string> = {}
  if (region !== null) patch.region = region
  if (project !== null) patch.project = project
  return patch
}

/** The line under the name. Kind and priority only: everything else about
 *  this provider is a reading in the strip below, and repeating it here would
 *  make the two disagree the moment one of them changed. */
export function headline(p: Provider): string {
  return `${p.kind} · priority ${p.priority}`
}

function Fact({ term, children }: { term: string; children: React.ReactNode }) {
  return (
    <>
      <dt className="text-sm text-[hsl(var(--legend))]">{term}</dt>
      <dd className="mb-2 truncate font-mono text-sm" title={String(children)}>
        {children}
      </dd>
    </>
  )
}

export function ProviderDetail() {
  const { id } = useParams({ from: "/providers/$id" })
  const providers = useProviders()
  const health = useProviderHealth()
  const discovery = useDiscoveryHealth()
  const catalog = useModels()
  const usage = useUsage("provider")
  const provider = providers.data?.providers.find((p) => p.id === id)

  const [draftPriority, setDraftPriority] = useState<string | null>(null)
  const [draftRegion, setDraftRegion] = useState<string | null>(null)
  const [draftProject, setDraftProject] = useState<string | null>(null)
  const [accounts, setAccounts] = useState<AccountDraft>(emptyAccounts)
  const [addOpen, setAddOpen] = useState(false)

  const saveFreeOnly = useApiMutation({
    mutationFn: (next: boolean) =>
      api.patch(`/api/providers/${id}`, { free_models_only: next }),
    success: "Import filter updated",
    invalidates: [keys.providers],
  })
  const savePriority = useApiMutation({
    mutationFn: () =>
      api.patch(`/api/providers/${id}`, {
        priority: Number(draftPriority ?? provider?.priority) || 0,
      }),
    success: "Priority updated",
    invalidates: [keys.providers, keys.overview],
    onSuccess: () => setDraftPriority(null),
  })
  const toggle = useApiMutation({
    mutationFn: (enabled: boolean) => api.patch(`/api/providers/${id}`, { enabled }),
    success: "Provider updated",
    invalidates: [keys.providers, keys.overview],
  })
  const saveLocation = useApiMutation({
    mutationFn: (vars: Record<string, string>) => api.patch(`/api/providers/${id}`, vars),
    success: "Provider updated",
    invalidates: [keys.providers],
    onSuccess: () => {
      setDraftRegion(null)
      setDraftProject(null)
    },
  })
  const addAccounts = useApiMutation({
    mutationFn: (draft: AccountDraft) => addCredentials(id, draft),
    invalidates: [keys.providers, keys.health],
    onSuccess: (result) => {
      reportAdded(result)
      setAccounts(emptyAccounts)
      setAddOpen(false)
    },
  })
  if (providers.isSuccess && !provider) {
    return (
      <>
        <Link to="/providers" className="text-sm text-[hsl(var(--legend))] hover:underline">
          ← Providers
        </Link>
        <Card className="mt-4 p-6">
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            No provider named <span className="font-mono">{id}</span>. It may have been
            deleted.
          </p>
        </Card>
      </>
    )
  }
  if (!provider) return null

  const state = providerState(provider)
  const cooling = breakersFor(health.data ?? [], provider.id)
  const accountsSummary = accountSummary(provider, cooling)
  const models = modelsFor(catalog.data?.models ?? [], provider.id)
  const caps = capabilityCount(models)
  const series = requestsByDay(usage.data?.days ?? [], provider.id)
  const requests = totalRequests(usage.data?.days ?? [], provider.id)
  const discoveryRow = discovery.data?.providers.find((d) => d.provider_id === provider.id)
  const discovered = discoveryFraction(discoveryRow)
  const dirtySettings = draftPriority !== null
  const dirtyLocation = draftRegion !== null || draftProject !== null

  return (
    <>
      <Link to="/providers" className="text-sm text-[hsl(var(--legend))] hover:underline">
        ← Providers
      </Link>

      {/* The identity and the two things done to a provider from outside it —
          switch it off, and everything else is per-account below. */}
      <header className="mt-3 mb-6 flex flex-wrap items-center gap-4 border-b pb-5">
        <ProviderIcon preset={provider.preset} id={provider.id} name={provider.name} size={44} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h1 className="truncate text-2xl font-semibold tracking-tight">{provider.name}</h1>
            <Badge variant={STATE_VARIANT[state]}>{state}</Badge>
          </div>
          <p className="mt-0.5 font-mono text-sm text-[hsl(var(--legend))]">
            {provider.id} · {headline(provider)}
          </p>
        </div>
        <Button
          size="sm"
          variant={provider.enabled ? "ghost" : "default"}
          onClick={() => toggle.mutate(!provider.enabled)}
        >
          {/* Disabling is a routing decision, not a deletion: the provider and
              its accounts stay, and the router stops choosing it. */}
          {provider.enabled ? "Disable" : "Enable"}
        </Button>
      </header>

      {/* Four readings, in the order an operator asks them: is it carrying
          traffic, can it be sent to, what can it serve, and does the catalogue
          still agree with the vendor. */}
      <div className="mb-6 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          caption="requests · 30d"
          value={requests.toLocaleString()}
          note={series.length < 2 ? "no daily series yet" : undefined}
        >
          <Sparkline points={series} />
        </Stat>
        <Stat
          caption="accounts usable"
          value={`${accountsSummary.usable}/${accountsSummary.total}`}
          note={
            accountsSummary.cooling > 0
              ? `${accountsSummary.cooling} cooling`
              : accountsSummary.disabled > 0
                ? `${accountsSummary.disabled} disabled`
                : accountsSummary.total === 0
                  ? "none configured"
                  : "all available"
          }
          tone={accountsSummary.cooling > 0 ? "warning" : "muted"}
        />
        <Stat
          caption="models offered"
          value={String(models.length)}
          note={caps.total > 0 ? `${caps.tools} with tools` : "catalogue empty"}
        />
        <Stat
          caption="discovery"
          value={discovered ?? "—"}
          note={discoveryNote(discoveryRow)}
          tone={
            discoveryRow &&
            (discoveryRow.max_missing_streak > 0 || discoveryRow.total === 0)
              ? "warning"
              : "muted"
          }
        />
      </div>

      <div className="grid gap-6 lg:grid-cols-[1fr_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <section>
            <div className="mb-2 flex items-center justify-between gap-2">
              <h2 className="text-sm font-medium">Accounts</h2>
              {!addOpen && (
                <Button size="sm" variant="secondary" onClick={() => setAddOpen(true)}>
                  Add accounts
                </Button>
              )}
            </div>

            {provider.credentials.length === 0 && !addOpen ? (
              // An empty screen is an invitation to act: the one thing to do
              // here is the one thing that makes the provider routable.
              <Card className="flex flex-col items-start gap-2 p-6">
                <p className="text-sm font-medium">No accounts yet</p>
                <p className="text-sm text-[hsl(var(--muted-foreground))]">
                  The router cannot choose {provider.name} until it has a key to send
                  with.
                </p>
                <Button size="sm" className="mt-1" onClick={() => setAddOpen(true)}>
                  Add the first account
                </Button>
              </Card>
            ) : (
              // Scrolls rather than clips: the actions are three words wide and
              // this column is the narrow half of a two-column page on a laptop.
              provider.credentials.length > 0 && (
                <Card className="overflow-x-auto p-0">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Label</TableHead>
                        <TableHead>Secret</TableHead>
                        <TableHead>State</TableHead>
                        <TableHead />
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      {provider.credentials.map((c) => (
                        <CredentialRow key={c.id} providerId={provider.id} credential={c} />
                      ))}
                    </TableBody>
                  </Table>
                </Card>
              )
            )}

            {addOpen && (
              <Card className="mt-3 p-4">
                <AccountFields value={accounts} onChange={setAccounts} autoFocus />
                <div className="mt-3 flex items-center gap-2">
                  <Button
                    size="sm"
                    disabled={countAccounts(accounts) === 0 || addAccounts.isPending}
                    onClick={() => addAccounts.mutate(accounts)}
                  >
                    {addAccountsLabel(countAccounts(accounts))}
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setAccounts(emptyAccounts)
                      setAddOpen(false)
                    }}
                  >
                    Cancel
                  </Button>
                </div>
              </Card>
            )}
          </section>

          <section>
            <h2 className="mb-2 text-sm font-medium">Models</h2>
            <ProviderModels models={models} loading={catalog.isPending} />
          </section>

          <section>
            <h2 className="mb-2 text-sm font-medium">Routing</h2>
            <Card className="flex flex-col gap-4 p-4">
              <div className="flex flex-wrap items-end gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="provider-priority">Priority</Label>
                  <Input
                    id="provider-priority"
                    value={draftPriority ?? String(provider.priority)}
                    onChange={(e) => setDraftPriority(e.target.value)}
                    className="w-24"
                    inputMode="numeric"
                  />
                </div>
                <p className="max-w-xs text-sm text-[hsl(var(--legend))]">
                  The order the router walks providers in. Its name and its
                  connection come from the release.
                </p>
                {/* Only once something has changed: a Save that is always
                    live invites a click that writes what is already there. */}
                {dirtySettings && (
                  <div className="flex items-center gap-2">
                    <Button size="sm" onClick={() => savePriority.mutate(undefined)}>
                      Save
                    </Button>
                    <Button size="sm" variant="ghost" onClick={() => setDraftPriority(null)}>
                      Discard
                    </Button>
                  </div>
                )}
              </div>

              {/* The wizard asks this before the first sweep; this is where an
                  operator changes their mind. It takes effect on the next
                  sweep -- models already imported stay until then. */}
              <div className="flex items-start gap-2 border-t pt-4">
                <Checkbox
                  id="provider-free-only"
                  checked={provider.free_models_only}
                  onCheckedChange={(next) => saveFreeOnly.mutate(next === true)}
                />
                <div className="flex flex-col">
                  <Label htmlFor="provider-free-only">Import free models only</Label>
                  <span className="text-sm text-[hsl(var(--legend))]">
                    The next discovery sweep keeps only models priced at zero or
                    tagged <span className="font-mono">:free</span>.
                  </span>
                </div>
              </div>

              <div className="flex flex-wrap items-end gap-3 border-t pt-4">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="provider-region">Region</Label>
                  {/* Blank rather than prefilled: GET /api/providers does not
                      return either field, so there is no current value to
                      show. */}
                  <Input
                    id="provider-region"
                    value={draftRegion ?? ""}
                    onChange={(e) => setDraftRegion(e.target.value)}
                    placeholder="unset"
                    className="w-40"
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="provider-project">Project</Label>
                  <Input
                    id="provider-project"
                    value={draftProject ?? ""}
                    onChange={(e) => setDraftProject(e.target.value)}
                    placeholder="unset"
                    className="w-40"
                  />
                </div>
                {dirtyLocation && (
                  <Button
                    size="sm"
                    onClick={() => saveLocation.mutate(locationPatch(draftRegion, draftProject))}
                  >
                    Save
                  </Button>
                )}
              </div>
            </Card>
          </section>

          <section>
            <h2 className="mb-2 text-sm font-medium">Health</h2>
            <div className="flex flex-col gap-3">
              <ProbePanel providerId={provider.id} />
              <DiscoveryPanel providerId={provider.id} />
            </div>
          </section>
        </div>

        <aside className="flex min-w-0 flex-col gap-4">
          <Card className="p-4">
            <h2 className="mb-3 text-sm font-medium">Connection</h2>
            <dl className="text-sm">
              <Fact term="Base URL">{provider.base_url}</Fact>
              <Fact term="Preset">{provider.preset || "—"}</Fact>
              <Fact term="Auth style">{provider.auth_style}</Fact>
              <Fact term="Kind">{provider.kind}</Fact>
            </dl>
          </Card>

          {models.length > 0 && (
            <Card className="p-4">
              <h2 className="mb-3 text-sm font-medium">Capabilities</h2>
              <CapabilityBars caps={caps} />
            </Card>
          )}

          {cooling.length > 0 && (
            <Card className="border-[hsl(var(--warning))] p-4">
              <h2 className="mb-2 text-sm font-medium">
                {cooling.length} {cooling.length === 1 ? "account" : "accounts"} cooling
              </h2>
              <ul className="flex flex-col gap-1 font-mono text-sm text-[hsl(var(--legend))]">
                {cooling.map((e) => (
                  <li key={`${e.key_id}/${e.model}`}>
                    {e.key_id || "—"} · backoff {e.backoff_level} · {e.consecutive_failures}{" "}
                    consecutive failures
                  </li>
                ))}
              </ul>
            </Card>
          )}

        </aside>
      </div>
    </>
  )
}
