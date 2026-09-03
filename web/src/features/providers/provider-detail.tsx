import { useState } from "react"
import { Link, useParams } from "@tanstack/react-router"
import { Plus, Settings2 } from "lucide-react"
import {
  Badge,
  Button,
  Card,
  Checkbox,
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui"
import { POLL, api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import {
  keys,
  useDiscoveryHealth,
  useModels,
  usePresets,
  useProviderHealth,
  useProviders,
  useUsage,
} from "../../lib/queries"
import type { Preset, Provider } from "../../lib/api-types"
import { ConfirmButton } from "../shell/confirm-button"
import { EmptyState, GhostRows } from "../shell/empty-state"
import { AddAccountsDialog } from "./add-accounts-dialog"
import { CredentialRow } from "./credential-row"
import { DiscoveryPanel } from "./discovery-panel"
import { ProbePanel } from "./probe-panel"
import { ProviderIcon } from "./provider-icon"
import { ProviderModels } from "./provider-models"
import { ProviderSettingsDialog } from "./provider-settings-dialog"
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
import { STATE_VARIANT, breakersFor, isKeyless, providerState } from "./provider-state"
import { isLocalPreset } from "./local-runtimes"
import { AddLocalDialog } from "./add-local-dialog"

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

/**
 * A provider the release supports and nobody has configured.
 *
 * The same page, minus everything that reads a database row: there are no
 * accounts to list, nothing to probe, no priority to set, and no discovery to
 * report, because none of those exist until the provider has a key. What the
 * release does know — where it connects and how it authenticates — is here,
 * because deciding whether to add a key is what an operator came to this page
 * to do.
 */
function UnconfiguredProvider({ preset }: { preset: Preset }) {
  const [addOpen, setAddOpen] = useState(false)
  const [localOpen, setLocalOpen] = useState(false)
  const [freeOnly, setFreeOnly] = useState(false)
  // A local runtime is keyless too, but adding it is a different act: what it
  // needs is the address it is listening on, and the import filter below asks
  // which of its models are cheap enough -- of a server whose models are all
  // free. So it takes the address form the providers list opens.
  const local = isLocalPreset(preset)
  const keyless = isKeyless({ auth_style: preset.auth_kind }) && !local
  // A base URL that names no host is a program on this box rather than an
  // endpoint, and what it needs from the operator is a different question
  // from "which credential": the program signs itself in.
  const localProgram = !/^https?:\/\//i.test(preset.base_url ?? "")

  // A keyless provider needs no secret, so the accounts dialog would be a form
  // with no fields — but the import filter is not a property of a key, and
  // skipping the dialog was skipping the one question that still applied. The
  // first sweep starts within seconds of this POST, so choosing afterwards is
  // choosing too late.
  const add = useApiMutation({
    mutationFn: () =>
      api.post("/api/providers", {
        id: preset.id,
        preset: preset.id,
        free_models_only: freeOnly,
      }),
    success: `${preset.name} added`,
    invalidates: [keys.providers, keys.health, keys.overview, keys.models],
  })

  return (
    <>
      <Link to="/providers" className="text-sm text-[hsl(var(--legend))] hover:underline">
        ← Providers
      </Link>

      <header className="mt-3 mb-6 flex flex-wrap items-center gap-4 border-b pb-5">
        <ProviderIcon preset={preset.id} id={preset.id} name={preset.name} size={44} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-2xl font-semibold tracking-tight">{preset.name}</h2>
            <Badge variant={STATE_VARIANT.unconfigured}>unconfigured</Badge>
            {preset.free_tier && <Badge variant="secondary">Free tier</Badge>}
          </div>
          <p className="mt-0.5 font-mono text-sm text-[hsl(var(--legend))]">
            {preset.id} · {preset.kind}
          </p>
        </div>
        {local ? (
          <Button size="sm" onClick={() => setLocalOpen(true)}>
            <Plus className="size-[var(--icon-size)]" />
            Add provider
          </Button>
        ) : keyless ? (
          <Button size="sm" disabled={add.isPending} onClick={() => add.mutate(undefined)}>
            <Plus className="size-[var(--icon-size)]" />
            Add provider
          </Button>
        ) : (
          // Nothing here. The panel below opens on "Add the first credential",
          // which is this same act with the explanation attached, and a header
          // button a few pixels above it was the choice offered twice.
          null
        )}
      </header>

      <AddAccountsDialog preset={preset} open={addOpen} onOpenChange={setAddOpen} />

      <AddLocalDialog
        preset={preset}
        open={localOpen}
        onOpenChange={setLocalOpen}
        onDone={() => setLocalOpen(false)}
      />

      <div className="grid gap-6 lg:grid-cols-[1fr_20rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <EmptyState
            title={`${preset.name} ships with this release, unconfigured`}
            hint={
              local
                ? "It is a model server on this machine, so what it needs is the address " +
                  "the gateway can reach it on rather than a credential. Its models are " +
                  "free, so there is no import filter to set."
                : localProgram
                ? "It runs a program on this machine, and that program holds its own login. " +
                  "Adding the provider is the whole of the setup if the program is already " +
                  "signed in; otherwise add a credential and paste its session, which the " +
                  "next screen explains."
                : keyless
                ? preset.auth_kind === "optional"
                  ? "It answers without a credential, so adding it is the whole of the setup. A credential can be added later — this provider serves more generously when it knows who is calling."
                  : preset.auth_kind === "anonymous"
                    ? "It needs a credential and publishes one, which this release ships, so adding it is the whole of the setup. A credential of your own can be added later — a registered key buys a shorter queue."
                    : "It asks for no credential, so adding it is the whole of the setup. The router can choose it as soon as it exists, and discovery lists its models on the next sweep."
                  : "The router cannot choose it until it has a key to send with. The first credential creates the provider and starts discovery."
            }
            action={
              local ? (
                <Button size="sm" onClick={() => setLocalOpen(true)}>
                  Add {preset.name}
                </Button>
              ) : keyless ? (
                <div className="flex flex-col items-center gap-3">
                  {/* Offered here because this is the last moment it is a
                      choice: the first sweep starts within seconds of the
                      provider existing, and a filter set afterwards only takes
                      effect on the next one. */}
                  <label className="flex items-start gap-2 text-left">
                    <Checkbox
                      id="keyless-free-only"
                      checked={freeOnly}
                      onCheckedChange={(next) => setFreeOnly(next === true)}
                    />
                    <span className="flex flex-col">
                      <span className="text-sm font-medium">Import free models only</span>
                      <span className="max-w-prose text-sm text-[hsl(var(--muted-foreground))]">
                        A sweep keeps a model this provider's free tier documents, one
                        priced at zero, or one tagged <span className="font-mono">:free</span>.
                        Changeable afterwards in settings.
                      </span>
                    </span>
                  </label>
                  <Button size="sm" disabled={add.isPending} onClick={() => add.mutate(undefined)}>
                    Add {preset.name}
                  </Button>
                </div>
              ) : (
                <Button size="sm" onClick={() => setAddOpen(true)}>
                  Add the first credential
                </Button>
              )
            }
            preview={<GhostRows rows={3} />}
          />

          <section>
            <h2 className="mb-2 text-sm font-medium">Models</h2>
            {/* Not "no models": nothing has asked this provider what it serves.
                A catalogue is the result of a discovery sweep, and a sweep
                needs a credential. */}
            <EmptyState
              title="Nothing has asked this provider what it serves"
              hint="Discovery lists a provider's models with one of its own keys, so the catalogue fills in once a credential exists."
            />
          </section>
        </div>

        <aside className="flex min-w-0 flex-col gap-4">
          <Card className="p-4">
            <h2 className="mb-3 text-sm font-medium">Connection</h2>
            <dl className="text-sm">
              <Fact term="Base URL">{preset.base_url}</Fact>
              <Fact term="Preset">{preset.id}</Fact>
              <Fact term="Auth style">{preset.auth_kind}</Fact>
              <Fact term="Kind">{preset.kind}</Fact>
              <Fact term="Surfaces">{preset.surfaces.join(", ") || "—"}</Fact>
            </dl>
            {preset.website && (
              <a
                href={preset.website}
                target="_blank"
                rel="noreferrer noopener"
                className="text-sm text-[hsl(var(--legend))] hover:underline"
              >
                {preset.website}
              </a>
            )}
          </Card>
        </aside>
      </div>
    </>
  )
}

/** How often to look while a sweep is expected. Three seconds is faster than
 *  a sweep can plausibly finish and slow enough that a page left open on a
 *  broken provider is not a poll every render. */
const SWEEP_POLL_MS = 3000

/** Whether this provider is waiting on a sweep: it has a key to sweep with,
 *  which is the one thing discovery cannot proceed without. */
export function awaitingModels(providers: Provider[], id: string): boolean {
  const p = providers.find((x) => x.id === id)
  if (p === undefined || !p.enabled) return false
  // Sweepable, not credentialled. A keyless provider is swept with no key at
  // all, so asking for one here left every local runtime and every free
  // gateway on the slow poll — the models landed and the page sat there.
  return p.credentials.length > 0 || isKeyless(p)
}

export function ProviderDetail() {
  const { id } = useParams({ from: "/providers/$id" })
  const providers = useProviders()
  const presets = usePresets()
  const health = useProviderHealth()
  const discovery = useDiscoveryHealth()
  // Polled faster while a sweep is expected, and only then.
  //
  // Adding the first account makes the provider discoverable and triggers a
  // sweep, but that sweep is asynchronous: a single refetch when the dialog
  // closes lands before the models are written, and the ordinary
  // thirty-second poll leaves an operator watching an empty panel long after
  // they exist. The condition is read off the response itself, so the fast
  // poll stops the moment the first model arrives rather than running for as
  // long as the page is open.
  const catalog = useModels({
    refetchInterval: (query) => {
      const served = (query.state.data?.models ?? []).some((m) => m.providers.includes(id))
      return awaitingModels(providers.data?.providers ?? [], id) && !served
        ? SWEEP_POLL_MS
        : POLL.slow
    },
  })
  const usage = useUsage("provider")
  const provider = providers.data?.providers.find((p) => p.id === id)

  const [addOpen, setAddOpen] = useState(false)
  const [settingsOpen, setSettingsOpen] = useState(false)

  const toggle = useApiMutation({
    mutationFn: (enabled: boolean) => api.patch(`/api/providers/${id}`, { enabled }),
    success: "Provider updated",
    invalidates: [keys.providers, keys.overview, keys.health, keys.discovery],
  })
  const allowUnsanctioned = useApiMutation({
    mutationFn: (allow: boolean) =>
      api.patch(`/api/providers/${id}`, { allow_unsanctioned_free: allow }),
    success: "Provider updated",
    invalidates: [keys.providers, keys.models],
  })
  // No row is not the same as no such provider: the list holds every provider
  // the release supports, and clicking one that nobody has configured has to
  // land somewhere that explains it rather than on a deletion notice.
  const preset = presets.data?.presets.find((p) => p.id === id)
  if (providers.isSuccess && !provider) {
    if (preset) return <UnconfiguredProvider preset={preset} />
    if (!presets.isSuccess) return null
    return (
      <>
        <Link to="/providers" className="text-sm text-[hsl(var(--legend))] hover:underline">
          ← Providers
        </Link>
        <Card className="mt-4 p-6">
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            No provider named <span className="font-mono">{id}</span>. It may have been
            deleted, and this release ships no preset by that name.
          </p>
        </Card>
      </>
    )
  }
  if (!provider) return null

  const state = providerState(provider)
  // Asked of the preset, not the row. A local runtime's row holds the address
  // the gateway can actually reach it on, and adding one rewrites the host —
  // inside a container "localhost" is the container, so a runtime on the
  // operator's desktop is stored as its LAN address and stops looking local.
  // The preset is the thing that says what kind of provider this is; the row
  // is only the fallback for one imported from config with no preset behind it.
  //
  // Deliberately narrower than "runs a program on this box", which does take a
  // credential when you want it to use a session of yours rather than its own.
  const localRuntime = isLocalPreset(preset ?? provider)
  const cooling = breakersFor(health.data ?? [], provider.id)
  const accountsSummary = accountSummary(provider, cooling)
  const models = modelsFor(catalog.data?.models ?? [], provider.id)
  const caps = capabilityCount(models)
  const series = requestsByDay(usage.data?.days ?? [], provider.id)
  const requests = totalRequests(usage.data?.days ?? [], provider.id)
  const discoveryRow = discovery.data?.providers.find((d) => d.provider_id === provider.id)
  const discovered = discoveryFraction(discoveryRow)

  return (
    <>
      <Link to="/providers" className="text-sm text-[hsl(var(--legend))] hover:underline">
        ← Providers
      </Link>

      {/* The identity and the two things done to a provider from outside it —
          switch it off, and everything else is per-credential below. An h2:
          the app header already holds the page's h1, and this is a section
          of that page. */}
      <header className="mt-3 mb-6 flex flex-wrap items-center gap-4 border-b pb-5">
        <ProviderIcon preset={provider.preset} id={provider.id} name={provider.name} size={44} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-2xl font-semibold tracking-tight">{provider.name}</h2>
            <Badge variant={STATE_VARIANT[state]}>{state}</Badge>
          </div>
          <p className="mt-0.5 font-mono text-sm text-[hsl(var(--legend))]">
            {provider.id} · {headline(provider)}
          </p>
        </div>
        <Button size="sm" variant="ghost" onClick={() => setSettingsOpen(true)}>
          <Settings2 className="size-[var(--icon-size)]" />
          Settings
        </Button>
        {/* Disabling is a routing decision, not a deletion: the provider and
            its accounts stay, and the router stops choosing it. Enabling one
            asks nothing — only the half that takes capacity away does. */}
        {provider.enabled ? (
          <ConfirmButton
            size="sm"
            variant="ghost"
            title={`Disable ${provider.name}?`}
            description={
              provider.credentials.length > 0
                ? `The router stops choosing it, and requests that would have gone to its ${provider.credentials.length} ${provider.credentials.length === 1 ? "credential" : "credentials"} fail over to whatever else can serve them. Nothing is deleted.`
                : "The router stops choosing it. Nothing is deleted."
            }
            confirmLabel="Disable"
            onConfirm={() => toggle.mutate(false)}
          >
            Disable
          </ConfirmButton>
        ) : (
          <Button size="sm" variant="default" onClick={() => toggle.mutate(true)}>
            Enable
          </Button>
        )}
      </header>

      <ProviderSettingsDialog
        provider={provider}
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
      />
      <AddAccountsDialog provider={provider} open={addOpen} onOpenChange={setAddOpen} />

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
          caption="credentials usable"
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
              <h2 className="text-sm font-medium">Credentials</h2>
              {/* Two conditions, two different reasons.
                  A local runtime holds no credential at all: what it needed was
                  the address it listens on, and that is what its row already is.
                  And while the list is empty the panel below carries its own
                  call to action, so this button was the same act offered twice
                  a few pixels apart. */}
              {provider.credentials.length > 0 && !localRuntime && (
                <Button size="sm" variant="secondary" onClick={() => setAddOpen(true)}>
                  <Plus className="size-[var(--icon-size)]" />
                  Add credentials
                </Button>
              )}
            </div>

            {provider.credentials.length === 0 ? (
              // An empty screen is an invitation to act: the one thing to do
              // here is the one thing that makes the provider routable.
              <EmptyState
                title={
                  isKeyless(provider)
                    ? "This provider needs no credential"
                    : "This provider has no credentials"
                }
                hint={
                  // First, because the branches below it all end by offering a
                  // credential, and this panel no longer has a button to do it
                  // with. A hint that promises an act the page does not offer
                  // is worse than no hint.
                  localRuntime
                    ? `${provider.name} is a model server on this machine, reached at the address it listens on rather than with a key. There is no account here to hold one, and it needs none to be routed to.`
                    : !/^https?:\/\//i.test(provider.base_url ?? "")
                    ? `${provider.name} runs a program on this machine, and that program holds its own login, so the router can choose it as it is. Add a credential only to hand it a session of your own instead of the one it keeps on disk.`
                    : provider.auth_style === "anonymous"
                      ? `${provider.name} is reached with the key it publishes, which this release ships, so the router can choose it as it is. A credential of your own can still be added — a registered key buys a shorter queue.`
                      : isKeyless(provider)
                        ? `${provider.name} is reached with no credential, so the router can choose it as it is. A credential can still be added if your endpoint sits behind one.`
                        : `The router cannot choose ${provider.name} until it has a key to send with. Its settings and priority are kept either way.`
                }
                action={
                  // A local runtime is reached by address and takes no key, so
                  // this panel explains the state and offers nothing to do.
                  localRuntime ? undefined : (
                    <Button size="sm" variant={isKeyless(provider) ? "secondary" : "default"} onClick={() => setAddOpen(true)}>
                      {isKeyless(provider) ? "Add a credential anyway" : "Add the first credential"}
                    </Button>
                  )
                }
              />
            ) : (
              // Scrolls rather than clips: the actions are three words wide and
              // this column is the narrow half of a two-column page on a laptop.
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
            )}
          </section>

          <section>
            <h2 className="mb-2 text-sm font-medium">Models</h2>
            <ProviderModels models={models} loading={catalog.isPending} />
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

          <Card className="p-4">
            {/* Off by default, on this provider, is the router's whole
                answer to a free tier OmniRoute grades "avoid": it will not
                pick that access for anyone until the operator says so here. */}
            <label className="flex items-start gap-2">
              <Checkbox
                id="allow-unsanctioned-free"
                checked={provider.allow_unsanctioned_free}
                onCheckedChange={(next) => allowUnsanctioned.mutate(next === true)}
              />
              <span className="flex flex-col">
                <span className="text-sm font-medium">
                  Use models the vendor hasn&apos;t sanctioned
                </span>
                <span className="text-sm text-[hsl(var(--muted-foreground))]">
                  Off by default. These free tiers may breach the provider&apos;s terms,
                  so nothing routes to them automatically until you allow it.
                </span>
              </span>
            </label>
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
                {cooling.length} {cooling.length === 1 ? "credential" : "credentials"} cooling
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
