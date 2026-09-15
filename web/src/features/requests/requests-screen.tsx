import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react"
import { ChevronDown } from "lucide-react"
import { Link, useNavigate, useParams, useRouter, useRouterState } from "@tanstack/react-router"
import { Banner, Button, ToggleGroup, ToggleGroupItem } from "darkraise-ui"
import { DataTable, exportToCsv } from "darkraise-ui/data-table"
import { api } from "../../lib/api"
import { useAliases, useModels, useProviders, useRequests } from "../../lib/queries"
import { useSearchFilters, filterQuery } from "../../lib/search-filters"
import type { RequestPage, RequestRow } from "../../lib/api-types"
import { ModelCombobox } from "../shell/model-combobox"
import { EmptyState, GhostRows, NoMatch } from "../shell/empty-state"
import { LoadError } from "../shell/screen-state"
import { TrafficStrip } from "./traffic-strip"
import { TraceDrawer } from "./trace-drawer"
import { FilterSelect } from "./filter-select"
import { SavedViewsBar } from "./saved-views-bar"
import { buildColumns, facetRow, CSV_COLUMNS } from "./requests-columns"

const FIELDS = [
  "source",
  "provider",
  "model",
  "status",
  "alias",
  "surface",
  "error_code",
  "since_ms",
  "until_ms",
  // Which preset (1h/24h/7d/all) produced `since_ms`, so the time-range
  // control can show the truth after a reload or a pasted link instead of
  // going blank. UI bookkeeping only — see apiFilters below.
  "range",
] as const

const STATUS_OPTIONS = ["success", "error"]

const TIME_WINDOWS = [
  { value: "1h", label: "1h", ms: 60 * 60 * 1000 },
  { value: "24h", label: "24h", ms: 24 * 60 * 60 * 1000 },
  { value: "7d", label: "7d", ms: 7 * 24 * 60 * 60 * 1000 },
] as const

/**
 * How many rows arrived ahead of the one the reader is anchored to.
 *
 * Counted rather than inserted: rows appearing at the top would shift the
 * scroll position out from under someone reading, which is the one thing a
 * three-second poll must not do.
 */
export function newerCount(firstPage: RequestRow[], heldNewestId: string): number {
  if (heldNewestId === "") return 0
  const i = firstPage.findIndex((r) => r.id === heldNewestId)
  // Not on the page at all: retention or a long absence carried it away, and
  // reporting zero would be the one answer that is certainly wrong.
  return i === -1 ? firstPage.length : i
}

/** `next` with anything already in `existing` dropped, by id.
 *
 * A page fetched from a cursor minted against one snapshot of `held` can
 * still overlap it when the log moved between the two -- the frozen page and
 * the cursor it ends on come from different polls whenever a newer request
 * lands before Load more is first clicked. Dropping the overlap here is a
 * second line of defence behind pinning that cursor to the frozen page: it
 * also covers a retention sweep or any other way the same id could reappear.
 */
export function dedupeAppend(existing: RequestRow[], next: RequestRow[]): RequestRow[] {
  const seen = new Set(existing.map((r) => r.id))
  return next.filter((r) => !seen.has(r.id))
}

/** Distinct values for a combobox, drawn from what the log actually holds. */
/** What a filter offers: the values on this page, then everything else the
 *  gateway knows, each once. Page values lead because they are the ones with
 *  traffic behind them right now. */
export function mergedOptions(fromPage: string[], known: string[]): string[] {
  const seen = new Set(fromPage)
  return [...fromPage, ...known.filter((k) => k !== "" && !seen.has(k)).sort()]
}

export function optionsFrom(rows: RequestRow[], field: keyof RequestRow): string[] {
  const seen = new Set<string>()
  for (const row of rows) {
    const value = row[field]
    if (typeof value === "string" && value !== "") seen.add(value)
  }
  return [...seen].sort()
}

/** The filter set as the API understands it. `range` is display-only — the
 *  handler has no such parameter, and forwarding it anyway would ride a
 *  meaningless query param on every request and vary the react-query cache
 *  key for no reason. */
export function apiFilters(filters: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(filters)) {
    if (k !== "range") out[k] = v
  }
  return out
}

export function RequestsScreen() {
  const [filters, , clear] = useSearchFilters(FIELDS)
  const router = useRouter()
  const navigate = useNavigate()
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const search = useRouterState({ select: (s) => s.location.searchStr })
  // The open trace is the URL, not local state, so a reload or a shared link
  // lands on the same drawer. The drawer fetches by id, so the request need
  // not be on the loaded page — or in the log at all.
  const { id: selected = null } = useParams({ strict: false })

  // Pages accumulate: the operator is scrolling a log, and a "next page" that
  // swapped the table would lose their place and make the cursor pointless.
  const [older, setOlder] = useState<RequestRow[]>([])
  // undefined: no page has been fetched past the frozen first one, so paging
  // follows that first page's own cursor (held.nextCursor) rather than
  // whatever the live, still-polling `first` query holds by the time Load
  // more is clicked. null is that first page's cursor once fetched and found
  // to carry no further page; a string is a page fetched with another one
  // after it. Collapsing "not yet paged" and "exhausted" into one null, as a
  // plain `cursor ?? first.data?.next_cursor` fallback does, is what let the
  // exhausted state fall back to the first page's cursor forever.
  const [cursor, setCursor] = useState<string | null | undefined>(undefined)
  const [loadingMore, setLoadingMore] = useState(false)
  const [loadMoreError, setLoadMoreError] = useState<string | null>(null)
  // The first page the reader is currently looking at, frozen together with
  // the cursor it ends on. `null` means "not yet loaded" — distinct from an
  // empty result set, which would otherwise look identical and re-freeze
  // forever on every poll. Freezing the cursor alongside the rows is what
  // keeps the first Load more paging from the boundary those rows actually
  // end at, rather than from wherever a live poll has since moved it.
  const [held, setHeld] = useState<{ requests: RequestRow[]; nextCursor: string | null } | null>(
    null,
  )

  const first = useRequests({ ...apiFilters(filters), limit: "50" })
  // The filter vocabularies. Cheap: all three are already cached by the
  // screens that own them, and none refetches for this.
  const providers = useProviders()
  const catalog = useModels()
  const aliases = useAliases()

  // Paging follows the URL rather than the control that changed it: Back and
  // a pasted link change the filters without passing through any handler
  // here, and pages accumulated under the old filters would otherwise sit
  // under the new ones. The cursor is rejected under different filters by
  // design; resetting it here is what keeps that rejection invisible.
  const filterKey = JSON.stringify(apiFilters(filters))
  // The filters in force right now, readable from an async callback that was
  // started under earlier ones. A page requested under the previous filters
  // is thrown away when it lands rather than appended.
  const latestFilterKey = useRef(filterKey)
  // Bumped when the loaded pages are thrown away for a new first page, so an
  // older page still in flight does not land after rows it no longer follows.
  const pagesReset = useRef(0)
  const [pagedUnder, setPagedUnder] = useState(filterKey)

  // Both adjustments run during render rather than after it. An effect would
  // paint one frame of the previous filters' pages under the new filters,
  // and a second frame of the empty state before the first row it already
  // has in hand.
  if (filterKey !== pagedUnder) {
    setPagedUnder(filterKey)
    setOlder([])
    setCursor(undefined)
    setHeld(null)
    setLoadingMore(false)
    setLoadMoreError(null)
  } else if (
    first.data &&
    (held === null || held.requests.length === 0) &&
    held?.requests !== first.data.requests
  ) {
    // An empty first load must keep re-freezing on every poll, same as
    // `null`: freezing `[]` once would leave the very first row that ever
    // arrives uncounted and undisplayed until something else forced a reload.
    // Each poll brings a new array, which is what stops this repeating
    // against the one already held.
    setHeld({ requests: first.data.requests, nextCursor: first.data.next_cursor ?? null })
  }

  useEffect(() => {
    latestFilterKey.current = filterKey
  }, [filterKey])

  /**
   * The one place a filter change reaches the URL, for every control on this
   * screen — a single Select, the time range, a saved view. Written as a
   * direct URLSearchParams merge (mirroring useSearchFilters's own internal
   * logic) rather than as repeated calls to that hook's single-key setter:
   * each of those replaces the whole URL from the same stale search string
   * captured at render time, so a handler needing more than one field at
   * once would only ever see its last call stick.
   */
  function writeFilters(next: Record<string, string>) {
    const params = new URLSearchParams(search)
    for (const [k, v] of Object.entries(next)) {
      if (v === "") params.delete(k)
      else params.set(k, v)
    }
    const str = params.toString()
    router.history.replace(`${pathname}${str ? `?${str}` : ""}`)
  }

  function onFilter(key: (typeof FIELDS)[number], value: string) {
    writeFilters({ [key]: value })
  }

  async function loadMore() {
    // Before the first page is fetched, page from where the frozen first
    // page ends -- not from `first.data?.next_cursor`, which keeps moving as
    // the live query polls and can by now sit past rows `held` never showed.
    const from = cursor === undefined ? held?.nextCursor : cursor
    if (!from || loadingMore) return
    const requestedUnder = filterKey
    const requestedAfter = pagesReset.current
    const current = () =>
      latestFilterKey.current === requestedUnder && pagesReset.current === requestedAfter
    setLoadingMore(true)
    setLoadMoreError(null)
    try {
      const page = await api.get<RequestPage>(
        `/api/requests${filterQuery({ ...apiFilters(filters), limit: "50", cursor: from })}`,
      )
      if (!current()) return
      setOlder((p) => [...p, ...dedupeAppend(p, page.requests)])
      setCursor(page.next_cursor ?? null)
    } catch (err) {
      if (!current()) return
      setLoadMoreError((err as Error).message)
    } finally {
      if (current()) setLoadingMore(false)
    }
  }

  function showNewer() {
    const page = first.data
    const newest = held?.requests[0]?.id
    if (!page || newest === undefined) return
    const at = page.requests.findIndex((r) => r.id === newest)
    if (at !== -1) {
      const newer = page.requests.slice(0, at)
      setHeld((prev) => prev && { ...prev, requests: [...newer, ...prev.requests] })
      return
    }
    // The new first page does not reach the rows on screen, so appending
    // those after it would hide the gap between them. Paging starts over.
    pagesReset.current += 1
    setHeld({ requests: page.requests, nextCursor: page.next_cursor ?? null })
    setOlder([])
    setCursor(undefined)
    setLoadingMore(false)
    setLoadMoreError(null)
  }

  function openRowUnderPointer(e: MouseEvent<HTMLDivElement>) {
    const target = e.target as HTMLElement
    if (target.closest("button, a, input, [role='menu']")) return
    const id = target.closest("tr")?.querySelector<HTMLElement>("[data-request-id]")?.dataset.requestId
    if (id) openTrace(id)
  }

  function openTrace(id: string) {
    void navigate({ to: "/requests/$id", params: { id }, search: true })
  }

  function closeTrace() {
    void navigate({ to: "/requests", search: true })
  }

  // Deduplicated here rather than when a page lands: `held` can gain newer
  // rows while an older page is in flight.
  const heldRows = held?.requests ?? []
  const pageRows = [...heldRows, ...dedupeAppend(heldRows, older)]
  const rows = pageRows.map(facetRow)
  // navigate is stable for the router's life, so the columns build once.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const columns = useMemo(() => buildColumns(openTrace), [])
  const more = cursor === undefined ? held?.nextCursor : cursor
  const filtered = Object.values(filters).some((v) => v !== "")
  // No page has ever loaded, and the one just attempted failed: the empty
  // state below would otherwise read as "no requests yet" rather than as the
  // query failure it is.
  const initialFailure = first.isError && held === null

  // The page's own values first — those are the ones with traffic behind them
  // right now — then everything else the gateway knows about.
  const providerOptions = mergedOptions(
    optionsFrom(pageRows, "provider"),
    (providers.data?.providers ?? []).map((p) => p.id),
  )
  const modelOptions = mergedOptions(
    optionsFrom(pageRows, "model"),
    (catalog.data?.models ?? []).map((m) => m.model),
  )
  const aliasOptions = mergedOptions(
    optionsFrom(pageRows, "alias"),
    aliases.data ? Object.keys(aliases.data) : [],
  )
  const newer = newerCount(first.data?.requests ?? [], held?.requests?.[0]?.id ?? "")

  return (
    <>
      <div className="mb-4 flex flex-wrap items-center gap-2">
        {/* Suggested from the whole catalogue, not just the page in hand: a
            menu built from the loaded rows can only offer what is already on
            screen, so filtering to a provider whose traffic is older than the
            first page was impossible. These filter server-side, so free text
            has to keep working — a model retired from the catalogue is still
            in the log. */}
        <ModelCombobox
          label="Filter by provider"
          placeholder="Any provider"
          value={filters.provider}
          onChange={(v) => onFilter("provider", v)}
          candidates={providerOptions}
          loading={providers.isPending}
          emptyText="No provider by that name. The filter still applies."
          className="w-44"
        />
        <ModelCombobox
          label="Filter by model"
          placeholder="Any model"
          value={filters.model}
          onChange={(v) => onFilter("model", v)}
          candidates={modelOptions}
          loading={catalog.isPending}
          emptyText="No model by that name. The filter still applies."
          className="w-52"
        />
        <ModelCombobox
          label="Filter by alias"
          placeholder="Any alias"
          value={filters.alias}
          onChange={(v) => onFilter("alias", v)}
          candidates={aliasOptions}
          loading={aliases.isPending}
          emptyText="No alias by that name. The filter still applies."
          className="w-40"
        />
        {/* Console traffic is real traffic — the playground and the provider
            test drawer go through the same executor — so it is in this log
            too. Separating them is a filter rather than an exclusion, because
            "did my test work" and "what are my clients doing" are both
            questions this screen answers. */}
        <FilterSelect
          label="Source"
          value={filters.source}
          options={["proxy", "console"]}
          onChange={(v) => onFilter("source", v)}
        />
        <FilterSelect
          label="Surface"
          value={filters.surface}
          options={optionsFrom(pageRows, "surface")}
          onChange={(v) => onFilter("surface", v)}
        />
        <FilterSelect
          label="Error code"
          value={filters.error_code}
          options={optionsFrom(pageRows, "error_code")}
          onChange={(v) => onFilter("error_code", v)}
        />
        <FilterSelect
          label="Status"
          value={filters.status}
          options={STATUS_OPTIONS}
          onChange={(v) => onFilter("status", v)}
        />
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          // Controlled by the URL, not by click history: a reload, a pasted
          // link, or an applied saved view must show the truth about
          // since_ms rather than going blank while a window is still active.
          value={filters.range || "all"}
          onValueChange={(v) => {
            if (!v || v === "all") {
              writeFilters({ range: "", since_ms: "" })
              return
            }
            const window = TIME_WINDOWS.find((w) => w.value === v)
            if (window) writeFilters({ range: v, since_ms: String(Date.now() - window.ms) })
          }}
        >
          {TIME_WINDOWS.map((w) => (
            <ToggleGroupItem key={w.value} value={w.value}>
              {w.label}
            </ToggleGroupItem>
          ))}
          <ToggleGroupItem value="all">All</ToggleGroupItem>
        </ToggleGroup>
        {filtered && (
          <Button variant="ghost" size="sm" onClick={clear}>
            Clear
          </Button>
        )}
        {newer > 0 && (
          <Button
            variant="secondary"
            size="sm"
            onClick={showNewer}
          >
            {newer} newer
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          onClick={() => exportToCsv(rows, "requests.csv", CSV_COLUMNS)}
        >
          Export CSV
        </Button>
      </div>

      <SavedViewsBar fields={FIELDS} filters={filters} onApply={writeFilters} />

      <TrafficStrip rows={rows} />

      {initialFailure ? (
        // Not the table: without a page to show, "no requests yet" is a
        // claim about the log that a query failure never earned.
        <LoadError
          what="The requests"
          error={first.error}
          onRetry={() => void first.refetch()}
          className="mt-4"
        />
      ) : (
        <>
          {/* A failed poll on a screen that has already loaded is a staleness
              note, not an alarm: the rows below are real, just older than
              they look, and an empty log may have filled since. */}
          {first.isError && held && (
            <p className="mb-2 text-sm text-[hsl(var(--warning))]">
              {held.requests.length > 0
                ? "last refresh failed — rows may be stale"
                : "last refresh failed — requests may have arrived since"}
            </p>
          )}
          {/* Scrolls sideways inside its own box rather than pushing the page
              wider: ten columns do not fit a laptop, and the Path column at
              the far end is the one that used to fall off. */}
          {/* A click anywhere on a row opens it; the Open button stays for
              the keyboard and for screen readers, since the row itself is a
              row and not a button. The table renders its own cells, so the
              id rides on the first cell and the click is delegated from
              here. */}
          <div className="overflow-x-auto [&_tbody_tr]:cursor-pointer" onClick={openRowUnderPointer}>
            <DataTable
              columns={columns}
              data={rows}
              facets={["surface", "status", "failover"]}
              virtualize={{ rowHeight: 36, height: 640 }}
            />
          </div>

          {rows.length === 0 && (
            <div className="mt-4">
              {filtered ? (
                <NoMatch what="requests" onClear={clear} />
              ) : (
                <EmptyState
                  title="Every request the gateway serves is logged here"
                  hint="Point a client at the proxy and the first one appears within seconds, with the full attempt trail behind it."
                  action={
                    <Button asChild size="sm">
                      <Link to="/connect">Get a client connected</Link>
                    </Button>
                  }
                  preview={<GhostRows />}
                />
              )}
            </div>
          )}
        </>
      )}

      {loadMoreError !== null && (
        <Banner
          variant="destructive"
          className="mt-4"
          action={
            <Button variant="outline" size="sm" onClick={() => void loadMore()}>
              Try again
            </Button>
          }
        >
          Could not load older requests: {loadMoreError}
        </Banner>
      )}

      {more && (
        <Button
          variant="secondary"
          className="mt-4"
          disabled={loadingMore}
          onClick={() => void loadMore()}
        >
          <ChevronDown className="size-[var(--icon-size)]" />
          {loadingMore ? "Loading…" : "Load more"}
        </Button>
      )}

      <TraceDrawer id={selected} onClose={closeTrace} />
    </>
  )
}
