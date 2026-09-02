import { useState } from "react"
import { MessageSquare, Plus, Radio, RefreshCw, RotateCcw } from "lucide-react"
import { useSearchFilters } from "../../lib/search-filters"
import { useNavigate } from "@tanstack/react-router"
import {
  Badge,
  Button,
  Card,
  Input,
  Label,
  Switch,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  ToggleGroup,
  ToggleGroupItem,
  toast,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { ConfirmButton } from "../shell/confirm-button"
import {
  keys,
  useDiscoveryHealth,
  usePresets,
  useProviderHealth,
  useProviders,
  useUsage,
} from "../../lib/queries"
import type { BreakerEntry, Preset, ProbeResult } from "../../lib/api-types"
import { FilterSelect } from "../requests/filter-select"
import { NoMatch } from "../shell/empty-state"
import { TestDrawer } from "./test-drawer"
import { AccountStrip, ShareMeter, type AccountMix } from "../shell/measures"
import { ProviderStateMark } from "../shell/status-mark"
import { AddAccountsDialog } from "./add-accounts-dialog"
import { ProviderCard } from "./provider-card"
import { ProviderIcon } from "./provider-icon"
import {
  filterProviderRows,
  filterSummary,
  CONNECTION_LABEL,
  connectionCounts,
  mergeProviderRows,
  type ConnectionType,
  type ProviderRow,
} from "./provider-rows"
import { breakersFor, discoveryLine, probeOutcome } from "./provider-state"

export { breakersFor, discoveryLine, probeOutcome, providerState } from "./provider-state"

export type ProviderView = "list" | "grid"

/** The filters this screen keeps in the URL. */
const PROVIDER_FIELDS = ["q", "state", "connection", "configured", "free_tier"] as const

const VIEW_KEY = "providers-view"

/** Most of the catalogue first, then the handfuls. The order is stable so a
 *  chip does not move when a provider is added. */
const CONNECTION_ORDER: ConnectionType[] = ["key", "local", "none", "oauth", "signed"]

/** The pill shape only. Selected, hover and disabled are the group's, which
 *  is the point of moving to one. */
const CHIP_SHAPE = "gap-1.5 rounded-full px-3"
const STATES = ["healthy", "degraded", "disabled", "unconfigured"]

/** Which layout an operator last chose.
 *
 *  Persisted because it is a preference about how they read this screen, not
 *  a filter: coming back tomorrow to the other layout would be the console
 *  forgetting something it was told. */
export function readView(store: Pick<Storage, "getItem">): ProviderView {
  return store.getItem(VIEW_KEY) === "grid" ? "grid" : "list"
}

/** A row's accounts by what the router can do with them right now. The list
 *  has the credentials for a configured provider and nothing for one that has
 *  never been set up, which is why this reads the row rather than a Provider. */
function accountMix(row: ProviderRow, cooling: BreakerEntry[]): AccountMix {
  const creds = row.provider?.credentials ?? []
  const coolingIds = new Set(cooling.map((c) => c.key_id))
  const disabled = creds.filter((c) => !c.enabled).length
  const cool = creds.filter((c) => c.enabled && (c.cooling || coolingIds.has(c.id))).length
  return { usable: creds.length - disabled - cool, cooling: cool, disabled }
}

export function ProvidersScreen() {
  const providers = useProviders()
  const presets = usePresets()
  const byProvider = useUsage("provider")
  const health = useProviderHealth()
  const discovery = useDiscoveryHealth()
  const navigate = useNavigate()
  const [addOpen, setAddOpen] = useState(false)
  // Which provider the dialog opens on. Null is the picker, which is what the
  // header button means; a row's own button has already named one.
  const [addPreset, setAddPreset] = useState<Preset | null>(null)
  // Which provider the test drawer is aimed at. Null closes it, so the drawer
  // is mounted once rather than per row.
  const [testing, setTesting] = useState<ProviderRow | null>(null)
  const [view, setView] = useState<ProviderView>(() => readView(localStorage))
  // In the URL like every other filtered screen, so a narrowed list survives a
  // reload and can be sent to someone. The two switches travel as "1" or the
  // empty string: the hook's values are strings, and an absent key is the same
  // as an unticked box.
  const [filters, setFilter, clearFilters] = useSearchFilters(PROVIDER_FIELDS)
  const q = filters.q
  const state = filters.state
  const connection = filters.connection
  const configuredOnly = filters.configured === "1"
  const freeTier = filters.free_tier === "1"

  // Every row goes to the same place. Clicking a provider means "show me this
  // provider", and a click that opened a dialog for some rows and navigated
  // for others made the destination depend on database state the operator
  // cannot see. The detail page renders an unconfigured provider from its
  // preset and offers the accounts dialog there.
  function open(row: ProviderRow) {
    void navigate({ to: "/providers/$id", params: { id: row.id } })
  }

  // Requests served per provider over the window, and their sum. The share a
  // provider carries is the fact the list cannot otherwise show: two healthy
  // providers look identical until one of them turns out to be serving
  // everything.
  const share = new Map<string, number>()
  for (const day of byProvider.data?.days ?? []) {
    if (!day.key) continue
    share.set(day.key, (share.get(day.key) ?? 0) + day.requests)
  }
  const servedTotal = [...share.values()].reduce((n, v) => n + v, 0)

  const reset = useApiMutation({
    mutationFn: (id: string) => api.post(`/api/providers/${id}/breaker/reset`, {}),
    success: "Cooldown cleared",
    invalidates: [keys.health, keys.providers],
  })
  const discover = useApiMutation({
    mutationFn: (id: string) => api.post(`/api/providers/${id}/discover`, {}),
    success: "Discovery sweep queued",
    invalidates: [keys.models],
  })
  const probe = useApiMutation({
    mutationFn: (id: string) => api.post<ProbeResult>(`/api/providers/${id}/test`, {}),
    invalidates: [keys.providers, keys.health],
    onSuccess: (result) => {
      const verdict = probeOutcome(result)
      if (verdict.kind === "success") toast.success(verdict.message)
      else toast.error(verdict.message)
    },
  })

  const all = mergeProviderRows(presets.data?.presets ?? [], providers.data?.providers ?? [])
  const rows = filterProviderRows(all, { q, state, connection, configuredOnly, freeTier })
  // Counted over everything the other filters leave, not over the whole
  // catalogue: a chip reading 40 beside a list of 6 would be counting rows
  // the screen is not showing.
  const counts = connectionCounts(
    filterProviderRows(all, { q, state, configuredOnly, freeTier }),
  )

  return (
    <>
      {/* The page's own name is in the app header; this row is what the
          screen can do. */}
      <div className="mb-4 flex flex-wrap items-center justify-end gap-2">
        <ToggleGroup
          type="single"
          value={view}
          onValueChange={(next) => {
            if (next !== "list" && next !== "grid") return
            setView(next)
            localStorage.setItem(VIEW_KEY, next)
          }}
          aria-label="Provider layout"
          className="w-fit rounded-[var(--radius)] border bg-[hsl(var(--muted))] p-0.5"
        >
          <ToggleGroupItem value="list" aria-label="List view">
            <ListGlyph />
            List
          </ToggleGroupItem>
          <ToggleGroupItem value="grid" aria-label="Grid view">
            <GridGlyph />
            Grid
          </ToggleGroupItem>
        </ToggleGroup>
        <Button
          size="sm"
          onClick={() => {
            setAddPreset(null)
            setAddOpen(true)
          }}
        >
          <Plus className="size-[var(--icon-size)]" />
          Add accounts
        </Button>
      </div>

      <TestDrawer
        row={testing}
        open={testing !== null}
        onOpenChange={(next) => !next && setTesting(null)}
      />

      <AddAccountsDialog
        preset={addPreset ?? undefined}
        open={addOpen}
        onOpenChange={setAddOpen}
        onDone={(id) => void navigate({ to: "/providers/$id", params: { id } })}
      />

      {/* The list is every provider the release supports, so it needs a way
          back down to the handful that carry traffic. */}
      <Card className="mb-4 flex flex-wrap items-center gap-3 p-3">
        <Input
          placeholder="Search providers"
          value={q}
          onChange={(e) => setFilter("q", e.target.value)}
          className="w-56"
          aria-label="Search providers"
        />
        <FilterSelect
          label="State"
          value={state}
          options={STATES}
          onChange={(next) => setFilter("state", next)}
        />
        <div className="flex items-center gap-2">
          <Switch
            id="providers-configured"
            checked={configuredOnly}
            onCheckedChange={(next) => setFilter("configured", next ? "1" : "")}
          />
          <Label htmlFor="providers-configured">Configured only</Label>
        </div>
        <div className="flex items-center gap-2">
          <Switch
            id="providers-free-tier"
            checked={freeTier}
            onCheckedChange={(next) => setFilter("free_tier", next ? "1" : "")}
          />
          <Label htmlFor="providers-free-tier">Free tier</Label>
        </div>
        <span className="ml-auto text-sm text-[hsl(var(--legend))]">
          {filterSummary(rows.length, all.length)}
        </span>
      </Card>

      {/* One chip per way of connecting, with its count. A quick filter beats
          another dropdown here: "the ones I run myself" is a question an
          operator asks by pointing, and a count that reads zero answers it
          before the click. */}
      {/* A ToggleGroup, like the view switcher above and the time windows on
          Requests. Hand-rolled, this was one screen holding two idioms for
          "a row of buttons, exactly one active" — and a zero-count chip was
          disabled but still a tab stop, where the group's roving focus steps
          over it.

          "all" is a sentinel rather than the empty string the filter stores:
          an empty value cannot be held by a controlled group, which is the
          same trick requests-screen plays with its range. */}
      <ToggleGroup
        type="single"
        variant="outline"
        value={connection === "" ? "all" : connection}
        onValueChange={(v) => setFilter("connection", !v || v === "all" ? "" : v)}
        className="mb-3 flex-wrap justify-start gap-2"
      >
        <ToggleGroupItem value="all" className={CHIP_SHAPE}>
          All
          <span className="tabular-nums text-[hsl(var(--legend))]">{all.length}</span>
        </ToggleGroupItem>
        {CONNECTION_ORDER.map((type) => (
          <ToggleGroupItem
            key={type}
            value={type}
            disabled={counts[type] === 0}
            className={CHIP_SHAPE}
          >
            {CONNECTION_LABEL[type]}
            <span className="tabular-nums text-[hsl(var(--legend))]">{counts[type]}</span>
          </ToggleGroupItem>
        ))}
      </ToggleGroup>

      {rows.length === 0 ? (
        // Only ever a filter miss: the list is every provider the release
        // supports, so it is never empty on its own.
        <NoMatch what="providers" onClear={clearFilters} />
      ) : view === "grid" ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {rows.map((row) => (
            <ProviderCard
              key={row.id}
              row={row}
              cooling={breakersFor(health.data ?? [], row.id)}
              onTest={() => setTesting(row)}
              share={
                share.get(row.id)
                  ? (share.get(row.id) ?? 0) / Math.max(servedTotal, 1)
                  : undefined
              }
              onOpen={() => open(row)}
            />
          ))}
        </div>
      ) : (
        // Scrolls rather than clips: the provider column carries two lines and
        // a mark, and the actions are three words wide.
        <Card className="overflow-x-auto p-0">
          {/* A minimum width so the table overflows the card and scrolls,
              rather than squeezing the actions column until its labels clip. */}
          <Table className="min-w-[56rem]">
          <TableHeader>
            <TableRow>
              <TableHead>Provider</TableHead>
              <TableHead>Priority</TableHead>
              <TableHead>Accounts</TableHead>
              <TableHead title="Share of the requests served in the last 30 days">
                Traffic
              </TableHead>
              <TableHead>Discovery</TableHead>
              <TableHead>State</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => {
              const cooling = breakersFor(health.data ?? [], row.id)
              const discoveryRow = discovery.data?.providers.find((d) => d.provider_id === row.id)
              return (
                <TableRow
                  key={row.id}
                  onClick={() => open(row)}
                  tabIndex={0}
                  role="button"
                  onKeyDown={(e) => {
                    // Only the row itself. Enter on a button inside the row —
                    // Probe, Discover, the breaker-reset confirm and its own
                    // buttons in the portal — bubbles up here, and
                    // preventDefault would suppress that button's activation
                    // and navigate away instead. stopPropagation on the cell
                    // guards clicks; it does not guard keys.
                    if (e.target !== e.currentTarget) return
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      open(row)
                    }
                  }}
                  className="cursor-pointer hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))] focus-visible:-outline-offset-2"
                >
                  {/* Two lines and a 36px mark: the row is the primary way into
                      a provider, and a single line of small text is a harder
                      target than it is a denser list. */}
                  <TableCell className="py-3">
                    <span className="flex items-center gap-3">
                      <ProviderIcon preset={row.preset} id={row.id} name={row.name} size={36} />
                      <span className="flex min-w-0 flex-col">
                        <span className="truncate font-medium">{row.name}</span>
                        <span className="truncate font-mono text-sm text-[hsl(var(--legend))]">
                          {row.id} · {row.kind}
                        </span>
                      </span>
                      {row.freeTier && <Badge variant="secondary">Free tier</Badge>}
                    </span>
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {row.priority ?? <span className="text-[hsl(var(--legend))]">—</span>}
                  </TableCell>
                  <TableCell>
                    {row.accounts > 0 ? (
                      <AccountStrip
                        mix={accountMix(row, cooling)}
                        label={`${accountMix(row, cooling).usable}/${row.accounts}`}
                      />
                    ) : (
                      <span className="text-[hsl(var(--legend))]">none</span>
                    )}
                  </TableCell>
                  <TableCell>
                    {share.get(row.id) ? (
                      <ShareMeter
                        fraction={(share.get(row.id) ?? 0) / Math.max(servedTotal, 1)}
                        label={`${Math.round(((share.get(row.id) ?? 0) / Math.max(servedTotal, 1)) * 100)}%`}
                      />
                    ) : (
                      <span className="text-[hsl(var(--legend))]">—</span>
                    )}
                  </TableCell>
                  <TableCell
                    title={row.provider ? discoveryLine(discoveryRow) : undefined}
                    className={
                      discoveryRow && discoveryRow.max_missing_streak > 0
                        ? "max-w-[11rem] truncate text-sm text-[hsl(var(--warning))]"
                        : "max-w-[11rem] truncate text-sm text-[hsl(var(--legend))]"
                    }
                  >
                    {row.provider ? discoveryLine(discoveryRow) : "—"}
                  </TableCell>
                  <TableCell>
                    {/* A mark, not the word: most of two hundred rows are
                        unconfigured, and a column of that word repeated is
                        text the eye reads to learn it says nothing new. */}
                    <ProviderStateMark state={row.state} />
                  </TableCell>
                  {/* The row opens the provider; these act on it in place, so
                      they must not also open it — by pointer or by keyboard.
                      The row's own handler ignores anything that did not start
                      on the row, which covers the confirm dialog's buttons too:
                      those portal to the body but stay React children of this
                      cell, so their events bubble here. */}
                  <TableCell className="flex gap-2 py-3" onClick={(e) => e.stopPropagation()}>
                    {/* A keyless provider is testable with nothing set up:
                        there is no account to add, so offering "Add accounts"
                        there is a button whose whole job is unavailable. */}
                    {row.keyless && !row.configured ? (
                      // Nothing else applies yet: probing and sweeping act on
                      // a database row this provider does not have, and Test
                      // is the one action that can make it.
                      <Button
                        size="icon"
                        variant="ghost"
                        title={`Test — send a message through ${row.name}`}
                        onClick={() => setTesting(row)}
                      >
                        <MessageSquare className="size-[var(--icon-size)]" />
                        <span className="sr-only">Test</span>
                      </Button>
                    ) : row.configured ? (
                      <>
                        {/* Icon-only, and not for fashion: four labelled
                            actions made the row 1358px wide inside a 1294px
                            column, which put Discover past the right edge of a
                            1600px window. The name survives as the tooltip and
                            the accessible name.
                            Probe asks whether the credential is accepted; Test
                            asks whether a real completion comes back. Both,
                            because a key can be valid on a provider that
                            serves nothing an operator asked for. */}
                        <Button
                          size="icon"
                          variant="ghost"
                          title="Test — send a message through this provider"
                          onClick={() => setTesting(row)}
                        >
                          <MessageSquare className="size-[var(--icon-size)]" />
                          <span className="sr-only">Test</span>
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          title="Probe — check the credential is accepted"
                          onClick={() => probe.mutate(row.id)}
                        >
                          <Radio className="size-[var(--icon-size)]" />
                          <span className="sr-only">Probe</span>
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          title="Discover — sweep this provider's models now"
                          onClick={() => discover.mutate(row.id)}
                        >
                          <RefreshCw className="size-[var(--icon-size)]" />
                          <span className="sr-only">Discover</span>
                        </Button>
                        {/* Only offered when there is something to clear: a
                            reset on a healthy provider invites a click that
                            does nothing and teaches the operator to distrust
                            it. */}
                        {cooling.length > 0 && (
                          <ConfirmButton
                            size="icon"
                            variant="ghost"
                            title={`Reset the breaker on ${row.name}?`}
                            description="The cooldown is cleared and the router starts dispatching here again immediately. If whatever tripped it has not been fixed, it trips straight back — with the backoff starting over from the beginning."
                            confirmLabel="Reset breaker"
                            onConfirm={() => reset.mutate(row.id)}
                          >
                            <RotateCcw className="size-[var(--icon-size)]" />
                            <span className="sr-only">Reset breaker</span>
                          </ConfirmButton>
                        )}
                      </>
                    ) : (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => {
                          // The row already names the provider. Opening the
                          // picker here would ask an operator to find, among
                          // two hundred, the one whose button they just
                          // pressed.
                          setAddPreset(
                            presets.data?.presets.find((p) => p.id === row.id) ?? null,
                          )
                          setAddOpen(true)
                        }}
                      >
                        <Plus className="size-[var(--icon-size)]" />
                        Add accounts
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
            </TableBody>
          </Table>
        </Card>
      )}

      {(health.data ?? []).some((e) => e.cooling_until) && (
        <Card className="mt-6 p-4">
          <h2 className="mb-2 text-sm font-medium">Cooling credentials</h2>
          <ul className="flex flex-col gap-1 font-mono text-sm">
            {(health.data ?? [])
              .filter((e) => e.cooling_until)
              .map((e) => (
                <li key={`${e.provider_id}/${e.key_id}/${e.model}`}>
                  {e.provider_id}/{e.key_id || "—"} · backoff {e.backoff_level} ·{" "}
                  {e.consecutive_failures} consecutive failures · until{" "}
                  {new Date(e.cooling_until as string).toLocaleTimeString()}
                </li>
              ))}
          </ul>
        </Card>
      )}
    </>
  )
}

function ListGlyph() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M2 4h12M2 8h12M2 12h12"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  )
}

function GridGlyph() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="2" y="2" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="9" y="2" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="2" y="9" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="9" y="9" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  )
}
