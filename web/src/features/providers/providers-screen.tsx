import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react"
import { MessageSquare, Plus, Radio, RefreshCw, RotateCcw } from "lucide-react"
import { useSearchFilters } from "../../lib/search-filters"
import { Link, useNavigate } from "@tanstack/react-router"
import {
  Badge,
  Button,
  Card,
  Input,
  Label,
  Switch,
  ToggleGroup,
  ToggleGroupItem,
  Tooltip,
  TooltipContent,
  TooltipTrigger,
  toast,
} from "darkraise-ui"
import { ColumnHeader, DataTable } from "darkraise-ui/data-table"
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
import type { BreakerEntry, DiscoveryHealthRow, Preset, ProbeResult } from "../../lib/api-types"
import { FilterSelect } from "../requests/filter-select"
import { NoMatch } from "../shell/empty-state"
import { TestDrawer } from "./test-drawer"
import { AccountStrip, ShareMeter, type AccountMix } from "../shell/measures"
import { ProviderStateMark } from "../shell/status-mark"
import { AddAccountsDialog } from "./add-accounts-dialog"
import { AddLocalDialog } from "./add-local-dialog"
import { AddKeylessDialog } from "./add-keyless-dialog"
import { isLocalPreset } from "./local-runtimes"
import { ProviderCard } from "./provider-card"
import { ProviderIcon } from "./provider-icon"
import {
  filterProviderRows,
  filterSummary,
  CONNECTION_DESCRIPTION,
  CONNECTION_LABEL,
  connectionCounts,
  mergeProviderRows,
  type ConnectionType,
  type ProviderRow,
} from "./provider-rows"
import { breakersFor, discoveryLine, probeOutcome } from "./provider-state"
import { dateTime, zoneLabel } from "../../lib/format"
import "./providers-table.css"

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

/** Shortest the windowed list is allowed to get, and what it falls back to
 *  where nothing can be measured. */
const MIN_LIST_HEIGHT = 320
const STATES = ["healthy", "degraded", "disabled", "unconfigured"]

/**
 * How tall the windowed list may be: whatever is left between the top of the
 * table and the bottom of the pane that scrolls.
 *
 * Measured rather than fixed. A constant has to be short enough for the
 * shortest screen it will ever meet, which on a tall one leaves the list
 * ending in mid-air with a band of empty page under it. And the number has to
 * be true as well as tall: DataTable places its window from this figure, so a
 * height that disagrees with the box actually rendered shows the wrong rows.
 *
 * The observer watches the pane and the table, because the space left over
 * changes for two different reasons -- the window resizing, and the filters
 * above wrapping to another line and pushing the table down.
 */
function useFillHeight(ref: RefObject<HTMLDivElement | null>): number {
  const [height, setHeight] = useState(MIN_LIST_HEIGHT)

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    const pane = el.closest(".dr-sidebar-layout-content")
    const bottom = pane
      ? pane.getBoundingClientRect().bottom -
        parseFloat(getComputedStyle(pane).paddingBottom || "0")
      : window.innerHeight
    // From the scrolling box, not the container: DataTable puts its own
    // toolbar above the window, so measuring the outer element overshoots by
    // the height of that row and pushes the list past the bottom of the pane.
    // The window's top does not move when its height changes, so this is a
    // fixed point rather than a circular one.
    const box = el.querySelector(".dr-data-table-viewport") ?? el
    const next = Math.max(Math.round(bottom - box.getBoundingClientRect().top), MIN_LIST_HEIGHT)
    // Only a real change: the table's own height is one of the things being
    // observed, so an unguarded write would answer its own notification.
    setHeight((prev) => (Math.abs(prev - next) > 1 ? next : prev))
  }, [ref])

  useEffect(() => {
    measure()
    window.addEventListener("resize", measure)
    const observer = new ResizeObserver(measure)
    const pane = ref.current?.closest(".dr-sidebar-layout-content")
    if (pane) observer.observe(pane)
    if (ref.current) observer.observe(ref.current)
    return () => {
      window.removeEventListener("resize", measure)
      observer.disconnect()
    }
  }, [measure, ref])

  return height
}

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
export function accountMix(row: ProviderRow, cooling: BreakerEntry[]): AccountMix {
  const creds = row.provider?.credentials ?? []
  const coolingIds = new Set(cooling.map((c) => c.key_id))
  const disabled = creds.filter((c) => !c.enabled).length
  const cool = creds.filter((c) => c.enabled && (c.cooling || coolingIds.has(c.id))).length
  return { usable: creds.length - disabled - cool, cooling: cool, disabled }
}

/**
 * One line of the list, with every reading its cells need already taken.
 *
 * Computed once over the row set rather than once per cell: the breaker
 * filter and the account mix were each being run several times per row on
 * every render of a two-hundred-row table.
 */
export type ListRow = {
  row: ProviderRow
  cooling: BreakerEntry[]
  mix: AccountMix
  /** Share of the window's requests, or undefined for a provider that served
   *  none — an empty meter and no meter say different things. */
  share?: number
  discovery?: DiscoveryHealthRow
}

export function listRows(
  rows: ProviderRow[],
  health: BreakerEntry[],
  share: Map<string, number>,
  discovery: DiscoveryHealthRow[],
): ListRow[] {
  const servedTotal = Math.max([...share.values()].reduce((n, v) => n + v, 0), 1)
  const discoveryById = new Map(discovery.map((d) => [d.provider_id, d]))
  return rows.map((row) => {
    const cooling = breakersFor(health, row.id)
    const served = share.get(row.id)
    return {
      row,
      cooling,
      mix: accountMix(row, cooling),
      share: served ? served / servedTotal : undefined,
      discovery: discoveryById.get(row.id),
    }
  })
}

/** What the cells can do to a row. Passed in rather than closed over so the
 *  column set can be built once and kept across renders. */
type RowActions = {
  onTest: (row: ProviderRow) => void
  onProbe: (id: string) => void
  onDiscover: (id: string) => void
  onReset: (id: string) => void
  onAdd: (row: ProviderRow) => void
  onAddKeyless: (row: ProviderRow) => void
}

// `darkraise-ui` bundles its own tanstack/react-table and does not re-export
// its column types, so the shape is pulled from the component's own signature
// rather than from a second install of the same package.
type Columns = Parameters<typeof DataTable<ListRow, unknown>>[0]["columns"]

function buildColumns(actions: RowActions): Columns {
  return [
    {
      id: "name",
      accessorFn: (r) => r.row.name,
      header: ({ column }) => <ColumnHeader column={column} title="Provider" />,
      cell: ({ row: { original: r } }) => (
        // Two lines and a 36px mark: the name is the way into a provider, and
        // a single line of small text is a harder target than it is a denser
        // list.
        <span className="flex min-w-[14rem] items-center gap-3">
          <ProviderIcon preset={r.row.preset} id={r.row.id} name={r.row.name} size={36} />
          <span className="flex min-w-0 flex-col">
            <Link
              to="/providers/$id"
              params={{ id: r.row.id }}
              className="truncate font-medium hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))]"
            >
              {r.row.name}
            </Link>
            <span className="truncate font-mono text-sm text-[hsl(var(--legend))]">
              {r.row.id} · {r.row.kind}
            </span>
          </span>
          {r.row.freeTier && <Badge variant="secondary">Free tier</Badge>}
        </span>
      ),
    },
    {
      id: "priority",
      accessorFn: (r) => r.row.priority ?? -1,
      header: ({ column }) => <ColumnHeader column={column} title="Priority" />,
      cell: ({ row: { original: r } }) => (
        <span className="tabular-nums">
          {r.row.priority ?? <span className="text-[hsl(var(--legend))]">—</span>}
        </span>
      ),
    },
    {
      id: "credentials",
      header: "Credentials",
      cell: ({ row: { original: r } }) =>
        r.row.accounts > 0 ? (
          <AccountStrip mix={r.mix} label={`${r.mix.usable}/${r.row.accounts}`} />
        ) : (
          <span className="text-[hsl(var(--legend))]">none</span>
        ),
    },
    {
      id: "traffic",
      // The window is in the header because it is the one thing the meter
      // cannot say about itself.
      header: "Traffic · 30d",
      cell: ({ row: { original: r } }) =>
        r.share !== undefined ? (
          <ShareMeter fraction={r.share} label={`${Math.round(r.share * 100)}%`} />
        ) : (
          <span className="text-[hsl(var(--legend))]">—</span>
        ),
    },
    {
      id: "state",
      header: "State",
      // A mark, not the word: most of two hundred rows are unconfigured, and
      // a column of that word repeated is text the eye reads to learn it
      // says nothing new.
      cell: ({ row: { original: r } }) => <ProviderStateMark state={r.row.state} />,
    },
    {
      id: "discovery",
      header: "Discovery",
      cell: ({ row: { original: r } }) => {
        if (!r.row.provider) return <span className="text-[hsl(var(--legend))]">—</span>
        const line = discoveryLine(r.discovery)
        const warn = r.discovery !== undefined && r.discovery.max_missing_streak > 0
        // A tooltip rather than a title: the line is longer than the cell as
        // soon as a provider has anything to report, and a title never shows
        // on touch or on keyboard focus.
        return (
          <Tooltip>
            <TooltipTrigger asChild>
              <span
                tabIndex={0}
                className={
                  warn
                    ? "block max-w-[11rem] truncate text-sm text-[hsl(var(--warning))]"
                    : "block max-w-[11rem] truncate text-sm text-[hsl(var(--legend))]"
                }
              >
                {line}
              </span>
            </TooltipTrigger>
            <TooltipContent>
              <span className="text-sm">{line}</span>
            </TooltipContent>
          </Tooltip>
        )
      },
    },
    {
      id: "actions",
      header: "",
      // Not data, so not something to hide — and the sticky column below
      // relies on it always being the last one rendered.
      enableHiding: false,
      cell: ({ row: { original: r } }) => <RowActionCell r={r} actions={actions} />,
    },
  ]
}

function RowActionCell({ r, actions }: { r: ListRow; actions: RowActions }) {
  const { row, cooling } = r
  // A keyless provider is testable with nothing set up: there is no account
  // to add, so offering "Add credentials" there is a button whose whole job
  // is unavailable.
  if (row.keyless && !row.configured) {
    return (
      <span className="flex gap-2">
        {/* Not "Add credentials": there is no credential. Adding it is still a
            deliberate act, and one question -- what the first sweep imports --
            has to be answered before the row exists, which is why this opens a
            dialog rather than writing straight away. */}
        <Button size="sm" variant="ghost" onClick={() => actions.onAddKeyless(row)}>
          <Plus className="size-[var(--icon-size)]" />
          Add provider
        </Button>
        <Button
          size="icon"
          variant="ghost"
          title={`Test — send a message through ${row.name}`}
          onClick={() => actions.onTest(row)}
        >
          <MessageSquare className="size-[var(--icon-size)]" />
          <span className="sr-only">Test</span>
        </Button>
      </span>
    )
  }
  if (!row.configured) {
    return (
      <span className="flex gap-2">
        <Button size="sm" variant="ghost" onClick={() => actions.onAdd(row)}>
          <Plus className="size-[var(--icon-size)]" />
          Add credentials
        </Button>
      </span>
    )
  }
  return (
    // Icon-only, and not for fashion: four labelled actions made the row
    // wider than the column that holds it. The name survives as the tooltip
    // and the accessible name.
    // Probe asks whether the credential is accepted; Test asks whether a real
    // completion comes back. Both, because a key can be valid on a provider
    // that serves nothing an operator asked for.
    <span className="flex gap-2">
      {/* A second key on a working provider is ordinary, and it opens the same
          dialog the unconfigured row does -- the preset is already settled, so
          there is no picker to walk. Offered on a keyless provider too: its
          endpoint can still sit behind a key, which is the case the detail
          page's "add a credential anyway" already covers. */}
      <Button
        size="icon"
        variant="ghost"
        title={`Add credentials — add another key to ${row.name}`}
        onClick={() => actions.onAdd(row)}
      >
        <Plus className="size-[var(--icon-size)]" />
        <span className="sr-only">Add credentials</span>
      </Button>
      <Button
        size="icon"
        variant="ghost"
        title="Test — send a message through this provider"
        onClick={() => actions.onTest(row)}
      >
        <MessageSquare className="size-[var(--icon-size)]" />
        <span className="sr-only">Test</span>
      </Button>
      <Button
        size="icon"
        variant="ghost"
        title="Probe — check the credential is accepted"
        onClick={() => actions.onProbe(row.id)}
      >
        <Radio className="size-[var(--icon-size)]" />
        <span className="sr-only">Probe</span>
      </Button>
      <Button
        size="icon"
        variant="ghost"
        title="Discover — sweep this provider's models now"
        onClick={() => actions.onDiscover(row.id)}
      >
        <RefreshCw className="size-[var(--icon-size)]" />
        <span className="sr-only">Discover</span>
      </Button>
      {/* Only offered when there is something to clear: a reset on a healthy
          provider invites a click that does nothing and teaches the operator
          to distrust it. */}
      {cooling.length > 0 && (
        <ConfirmButton
          size="icon"
          variant="ghost"
          title={`Reset the breaker on ${row.name}?`}
          description="The cooldown is cleared and the router starts dispatching here again immediately. If whatever tripped it has not been fixed, it trips straight back — with the backoff starting over from the beginning."
          confirmLabel="Reset breaker"
          onConfirm={() => actions.onReset(row.id)}
        >
          <RotateCcw className="size-[var(--icon-size)]" />
          <span className="sr-only">Reset breaker</span>
        </ConfirmButton>
      )}
    </span>
  )
}

export function ProvidersScreen() {
  const providers = useProviders()
  const presets = usePresets()
  const byProvider = useUsage("provider")
  const health = useProviderHealth()
  const discovery = useDiscoveryHealth()
  const navigate = useNavigate()
  const [addOpen, setAddOpen] = useState(false)
  // Which runtime the local dialog opens on. The dialog is reached only by
  // naming one -- a local preset's own card -- so the preset is both the
  // subject and the open flag.
  const [localPreset, setLocalPreset] = useState<Preset | null>(null)
  const [keylessPreset, setKeylessPreset] = useState<Preset | null>(null)
  const tableRef = useRef<HTMLDivElement | null>(null)
  const listHeight = useFillHeight(tableRef)
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

  // Requests served per provider over the window. The share a provider
  // carries is the fact the list cannot otherwise show: two healthy providers
  // look identical until one of them turns out to be serving everything.
  const share = useMemo(() => {
    const out = new Map<string, number>()
    for (const day of byProvider.data?.days ?? []) {
      if (!day.key) continue
      out.set(day.key, (out.get(day.key) ?? 0) + day.requests)
    }
    return out
  }, [byProvider.data])

  const reset = useApiMutation({
    mutationFn: (id: string) => api.post(`/api/providers/${id}/breaker/reset`, {}),
    success: "Cooldown cleared",
    invalidates: [keys.health, keys.providers],
  })
  const discover = useApiMutation({
    mutationFn: (id: string) => api.post(`/api/providers/${id}/discover`, {}),
    success: "Discovery sweep queued",
    invalidates: [keys.models, keys.discovery],
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

  // Memoised on the query results, not on the `?? []` defaults: those build a
  // fresh array on every render while the data is undefined, and every memo
  // below them would recompute each time.
  const presetRows = useMemo(() => presets.data?.presets ?? [], [presets.data])
  const providerRows = useMemo(() => providers.data?.providers ?? [], [providers.data])
  const healthRows = useMemo(() => health.data ?? [], [health.data])
  const discoveryRows = useMemo(() => discovery.data?.providers ?? [], [discovery.data])
  const all = useMemo(() => mergeProviderRows(presetRows, providerRows), [presetRows, providerRows])
  const rows = useMemo(
    () => filterProviderRows(all, { q, state, connection, configuredOnly, freeTier }),
    [all, q, state, connection, configuredOnly, freeTier],
  )
  const list = useMemo(
    () => listRows(rows, healthRows, share, discoveryRows),
    [rows, healthRows, share, discoveryRows],
  )
  // Counted over everything the other filters leave, not over the whole
  // catalogue: a chip reading 40 beside a list of 6 would be counting rows
  // the screen is not showing.
  const counts = useMemo(
    () => connectionCounts(filterProviderRows(all, { q, state, configuredOnly, freeTier })),
    [all, q, state, configuredOnly, freeTier],
  )

  // The mutation triggers are stable across renders, so the column set is
  // built once and DataTable is not handed a new table definition per poll.
  const rowActions: RowActions = useMemo(
    () => ({
      onTest: setTesting,
      onProbe: probe.mutate,
      onDiscover: discover.mutate,
      onReset: reset.mutate,
      onAdd: (row) => {
        // The row already names the provider. Opening the picker here would
        // ask an operator to find, among two hundred, the one whose button
        // they just pressed.
        setAddPreset(presetRows.find((p) => p.id === row.id) ?? null)
        setAddOpen(true)
      },
      onAddKeyless: (row) => {
        const p = presetRows.find((q) => q.id === row.id) ?? null
        // A local runtime is keyless too, but the keyless dialog asks the one
        // question that cannot apply to it -- which models to import by price,
        // of a server whose models are all free -- and not the one that can,
        // which is the address it is listening on.
        if (p && isLocalPreset(p)) setLocalPreset(p)
        else setKeylessPreset(p)
      },
    }),
    [probe.mutate, discover.mutate, reset.mutate, presetRows],
  )

  const columns = useMemo(
    () =>
      buildColumns(rowActions),
    [rowActions],
  )

  return (
    <>
      {/* Everything above the results stays put while they scroll.

          The pane that scrolls is the layout's content column, not the
          window, so this sticks to that. The pane is padded, which the block
          has to cover in two different ways: sideways a negative margin
          reaches into it, but upwards it cannot, because a sticky element is
          clamped to its containing block and the containing block starts
          below the padding. That band is painted by a shadow instead --
          see providers-table.css. */}
      <div className="providers-sticky-panel sticky top-0 z-20 -mx-6 bg-[hsl(var(--background))] px-6">
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
            {/* Glyph only. The label is a tooltip rather than a word beside the
                mark: two words of chrome sat where the screen's own actions are,
                and the aria-label is what carries the name either way.

                The trigger is the span, not the item. Both components write
                `data-state` -- the tooltip its open/closed, the item its on/off
                -- and Slot merges the child's props over the trigger's, so an
                item made the trigger receives "closed" and is spread over its
                own "on". The selected chip then matches no rule and the group
                renders with nothing chosen. A wrapper keeps each state on the
                element that owns it, and the tooltip still opens on keyboard
                focus because onFocus and onBlur bubble. */}
            <ViewToggle value="list" label="List view">
              <ListGlyph />
            </ViewToggle>
            <ViewToggle value="grid" label="Grid view">
              <GridGlyph />
            </ViewToggle>
          </ToggleGroup>
          <Button
            size="sm"
            onClick={() => {
              setAddPreset(null)
              setAddOpen(true)
            }}
          >
            <Plus className="size-[var(--icon-size)]" />
            Add credentials
          </Button>
        </div>

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
              title={CONNECTION_DESCRIPTION[type]}
            >
              {CONNECTION_LABEL[type]}
              <span className="tabular-nums text-[hsl(var(--legend))]">{counts[type]}</span>
            </ToggleGroupItem>
          ))}
        </ToggleGroup>

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

      <AddKeylessDialog
        preset={keylessPreset}
        open={keylessPreset !== null}
        onOpenChange={(next) => !next && setKeylessPreset(null)}
      />

      <AddLocalDialog
        preset={localPreset}
        open={localPreset !== null}
        onOpenChange={(next) => {
          if (next) return
          setLocalPreset(null)
        }}
        onDone={(id) => {
          setLocalPreset(null)
          void navigate({ to: "/providers/$id", params: { id } })
        }}
      />


      {presets.isSuccess && providers.isSuccess && rows.length === 0 ? (
        // Only ever a filter miss: the list is every provider the release
        // supports, so it is never empty on its own.
        <NoMatch what="providers" onClear={clearFilters} />
      ) : view === "grid" ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((r) => (
            <ProviderCard
              key={r.row.id}
              row={r.row}
              mix={r.mix}
              onTest={() => setTesting(r.row)}
              onAdd={() =>
                r.row.keyless && !r.row.configured
                  ? rowActions.onAddKeyless(r.row)
                  : rowActions.onAdd(r.row)
              }
              share={r.share}
              onOpen={() => void navigate({ to: "/providers/$id", params: { id: r.row.id } })}
            />
          ))}
        </div>
      ) : (
        // Windowed rather than paged. The catalogue is the whole list of
        // providers the release supports, and a pager over it turned "does
        // this release ship X" into a hunt through twenty pages. Setting
        // `virtualize` is what drops the pager: the component swaps its
        // pagination row model for a scrolling window, so every row is
        // reachable by scrolling and only the visible ones are rendered.
        //
        // The row height is declared, not measured, so providers-table.css
        // pins every row to it. The two numbers have to agree.
        <div className="providers-table" ref={tableRef}>
          <DataTable
            data={list}
            columns={columns}
            isLoading={presets.isPending || providers.isPending}
            virtualize={{ rowHeight: 60, height: listHeight }}
          />
        </div>
      )}

      {healthRows.some((e) => e.cooling_until) && (
        <Card className="mt-6 p-4">
          <h2 className="mb-2 text-sm font-medium">
            Cooling credentials
            <span className="ml-2 font-normal text-[hsl(var(--legend))]">{zoneLabel()}</span>
          </h2>
          <ul className="flex flex-col gap-1 font-mono text-sm">
            {healthRows
              .filter((e) => e.cooling_until)
              .map((e) => (
                <li key={`${e.provider_id}/${e.key_id}/${e.model}`}>
                  {e.provider_id}/{e.key_id || "—"} · backoff {e.backoff_level} ·{" "}
                  {e.consecutive_failures} consecutive failures · until{" "}
                  {dateTime(e.cooling_until as string)}
                </li>
              ))}
          </ul>
        </Card>
      )}
    </>
  )
}

/** One glyph in the view switcher, with its name in a tooltip.
 *
 *  The span exists to carry the tooltip. See the note at the call site: the
 *  item cannot be the trigger without losing the `data-state` its own selected
 *  styling is keyed on. `flex` keeps it a transparent flex item, so the group
 *  measures and spaces the buttons exactly as it did without it. */
function ViewToggle({
  value,
  label,
  children,
}: {
  value: ProviderView
  label: string
  children: ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="flex">
          <ToggleGroupItem value={value} aria-label={label}>
            {children}
          </ToggleGroupItem>
        </span>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
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
