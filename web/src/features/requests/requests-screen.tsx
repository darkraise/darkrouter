import { useEffect, useMemo, useState } from "react"
import { useRouter, useRouterState } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import { Button, ToggleGroup, ToggleGroupItem } from "darkraise-ui"
import { DataTable, exportToCsv } from "darkraise-ui/data-table"
import { api } from "../../lib/api"
import { useRequests } from "../../lib/queries"
import { useSearchFilters, filterQuery } from "../../lib/search-filters"
import type { RequestPage, RequestRow } from "../../lib/api-types"
import { EmptyLegend } from "../shell/empty-legend"
import { TraceDrawer } from "./trace-drawer"
import { FilterSelect } from "./filter-select"
import { SavedViewsBar } from "./saved-views-bar"
import { buildColumns, facetRow, CSV_COLUMNS } from "./requests-columns"

const FIELDS = [
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

/** Distinct values for a combobox, drawn from what the log actually holds. */
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
  const pathname = useRouterState({ select: (s) => s.location.pathname })
  const search = useRouterState({ select: (s) => s.location.searchStr })

  // Pages accumulate: the operator is scrolling a log, and a "next page" that
  // swapped the table would lose their place and make the cursor pointless.
  const [older, setOlder] = useState<RequestRow[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  // The first page the reader is currently looking at, frozen. `null` means
  // "not yet loaded" — distinct from an empty result set, which would
  // otherwise look identical and re-freeze forever on every poll.
  const [held, setHeld] = useState<RequestRow[] | null>(null)

  const first = useRequests({ ...apiFilters(filters), limit: "50" })

  useEffect(() => {
    // An empty first load must keep re-freezing on every poll, same as
    // `null`: freezing `[]` once would leave the very first row that ever
    // arrives uncounted and undisplayed until something else forced a reload.
    if (first.data && (held === null || held.length === 0)) setHeld(first.data.requests)
  }, [first.data, held])

  function resetPaging() {
    // The cursor is rejected under different filters by design; resetting it
    // here is what keeps that rejection invisible in normal use.
    setOlder([])
    setCursor(null)
    setHeld(null)
  }

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
    resetPaging()
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
    const from = cursor ?? first.data?.next_cursor
    if (!from) return
    const page = await api.get<RequestPage>(
      `/api/requests${filterQuery({ ...apiFilters(filters), limit: "50", cursor: from })}`,
    )
    setOlder((p) => [...p, ...page.requests])
    setCursor(page.next_cursor ?? null)
  }

  const pageRows = [...(held ?? []), ...older]
  const rows = pageRows.map(facetRow)
  const columns = useMemo(() => buildColumns(setSelected), [])
  const more = cursor ?? first.data?.next_cursor
  const filtered = Object.values(filters).some((v) => v !== "")
  const newer = newerCount(first.data?.requests ?? [], held?.[0]?.id ?? "")

  return (
    <>
      <PageHeader
        title="Requests"
        description="What it just did, and which provider actually served"
        actions={
          <Button variant="outline" size="sm" onClick={() => exportToCsv(rows, "requests.csv", CSV_COLUMNS)}>
            Export CSV
          </Button>
        }
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        <FilterSelect
          label="Provider"
          value={filters.provider}
          options={optionsFrom(pageRows, "provider")}
          onChange={(v) => onFilter("provider", v)}
        />
        <FilterSelect
          label="Model"
          value={filters.model}
          options={optionsFrom(pageRows, "model")}
          onChange={(v) => onFilter("model", v)}
        />
        <FilterSelect
          label="Alias"
          value={filters.alias}
          options={optionsFrom(pageRows, "alias")}
          onChange={(v) => onFilter("alias", v)}
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
          <Button variant="secondary" size="sm" onClick={() => first.data && setHeld(first.data.requests)}>
            {newer} newer
          </Button>
        )}
      </div>

      <SavedViewsBar fields={FIELDS} filters={filters} onApply={writeFilters} />

      <DataTable
        columns={columns}
        data={rows}
        facets={["surface", "status", "failover"]}
        virtualize={{ rowHeight: 36, height: 640 }}
      />

      {rows.length === 0 &&
        (filtered ? (
          <p className="mt-4 text-sm text-[hsl(var(--muted-foreground))]">
            No requests match these filters.
          </p>
        ) : (
          <EmptyLegend
            what="Requests appear here as clients call the gateway."
            hint="Point a client at Connect to see the first one."
          />
        ))}

      {more && (
        <Button variant="secondary" className="mt-4" onClick={() => void loadMore()}>
          Load more
        </Button>
      )}

      <TraceDrawer id={selected} onClose={() => setSelected(null)} />
    </>
  )
}
