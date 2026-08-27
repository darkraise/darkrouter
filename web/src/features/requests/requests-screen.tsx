import { useEffect, useMemo, useState } from "react"
import { useRouter, useRouterState } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import {
  Badge,
  Button,
  Input,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  ToggleGroup,
  ToggleGroupItem,
} from "darkraise-ui"
import { ColumnHeader, DataTable, exportToCsv } from "darkraise-ui/data-table"
import { api } from "../../lib/api"
import { useRequests } from "../../lib/queries"
import { useSearchFilters, filterQuery } from "../../lib/search-filters"
import type { RequestPage, RequestRow, SavedView } from "../../lib/api-types"
import { TraceDrawer } from "./trace-drawer"
import { deleteView, loadSavedViews, saveView } from "./saved-views"

const FIELDS = [
  "provider",
  "model",
  "status",
  "alias",
  "surface",
  "error_code",
  "since_ms",
  "until_ms",
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

/** The scalar shape a DataTable facet needs. `attempts` already reports the
 *  count; this is the same fact restated as a fixed string, because a facet
 *  filters on exact values and the API has no `attempts` filter to delegate
 *  it to. */
type TableRow = RequestRow & { failover: "failover" | "single" }

function facetRow(r: RequestRow): TableRow {
  return { ...r, failover: r.attempts > 1 ? "failover" : "single" }
}

const CSV_COLUMNS: { key: keyof TableRow; header: string }[] = [
  { key: "ts_ms", header: "Time" },
  { key: "surface", header: "Surface" },
  { key: "model", header: "Model" },
  { key: "provider", header: "Provider" },
  { key: "status", header: "Status" },
  { key: "attempts", header: "Attempts" },
  { key: "tokens_in", header: "Tokens in" },
  { key: "tokens_out", header: "Tokens out" },
  { key: "total_ms", header: "Latency ms" },
  { key: "path", header: "Path" },
  { key: "failover", header: "Failover" },
]

// `darkraise-ui` bundles its own tanstack/react-table internally and does not
// re-export its column types, so the shape is pulled from the component's own
// signature rather than from a second, independently-versioned install of the
// same package — the two do not agree on what a ColumnDef looks like.
type Columns = Parameters<typeof DataTable<TableRow, unknown>>[0]["columns"]

function buildColumns(onOpen: (id: string) => void): Columns {
  return [
    {
      accessorKey: "ts_ms",
      header: ({ column }) => <ColumnHeader column={column} title="Time" />,
      cell: ({ row }) => (
        <span className="whitespace-nowrap">
          {new Date(row.original.ts_ms).toLocaleTimeString()}
        </span>
      ),
    },
    { accessorKey: "surface", header: "Surface" },
    {
      accessorKey: "model",
      header: ({ column }) => <ColumnHeader column={column} title="Model" />,
      cell: ({ row }) => (
        <span className="font-mono text-xs">
          {row.original.alias ? `${row.original.alias} → ${row.original.model}` : row.original.model}
        </span>
      ),
    },
    {
      accessorKey: "provider",
      header: ({ column }) => <ColumnHeader column={column} title="Provider" />,
      cell: ({ row }) => row.original.provider || "—",
    },
    {
      accessorKey: "status",
      header: ({ column }) => <ColumnHeader column={column} title="Status" />,
      cell: ({ row }) => (
        <Badge variant={row.original.status === "success" ? "green" : "destructive"}>
          {row.original.status}
        </Badge>
      ),
    },
    {
      accessorKey: "attempts",
      header: ({ column }) => <ColumnHeader column={column} title="Attempts" />,
      // More than one attempt means a failover, which is the row an operator
      // is usually looking for.
      cell: ({ row }) =>
        row.original.attempts > 1 ? (
          <Badge variant="amber">{row.original.attempts}</Badge>
        ) : (
          row.original.attempts
        ),
    },
    {
      id: "tokens",
      accessorFn: (r) => r.tokens_in + r.tokens_out,
      header: ({ column }) => <ColumnHeader column={column} title="Tokens" />,
      cell: ({ row }) => `${row.original.tokens_in}/${row.original.tokens_out}`,
    },
    {
      accessorKey: "total_ms",
      header: ({ column }) => <ColumnHeader column={column} title="Latency" />,
      cell: ({ row }) => `${row.original.total_ms ?? "—"} ms`,
    },
    {
      accessorKey: "path",
      header: "Path",
      cell: ({ row }) => {
        const path = row.original.path
        if (!path) return "—"
        // Neutral, not accent: which renderer served is not a request
        // outcome, so it does not earn a state colour.
        return (
          <Badge variant={path === "passthrough" ? "outline" : "secondary"}>
            {path === "passthrough" ? "passthrough" : "translated"}
          </Badge>
        )
      },
    },
    { accessorKey: "failover", header: "Failover" },
    {
      id: "actions",
      header: "",
      // The library has no row-click prop, so opening the trace lives here
      // instead of on the row.
      cell: ({ row }) => (
        <Button variant="ghost" size="sm" onClick={() => onOpen(row.original.id)}>
          Open
        </Button>
      ),
    },
  ]
}

function FilterSelect({
  label,
  value,
  options,
  onChange,
}: {
  label: string
  value: string
  options: string[]
  onChange: (v: string) => void
}) {
  return (
    <Select value={value === "" ? "any" : value} onValueChange={(v) => onChange(v === "any" ? "" : v)}>
      <SelectTrigger className="w-36">
        <SelectValue placeholder={label} />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="any">Any {label.toLowerCase()}</SelectItem>
        {options.map((o) => (
          <SelectItem key={o} value={o}>
            {o}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

export function RequestsScreen() {
  const [filters, setFilter, clear] = useSearchFilters(FIELDS)
  const router = useRouter()
  const pathname = useRouterState({ select: (s) => s.location.pathname })

  // Pages accumulate: the operator is scrolling a log, and a "next page" that
  // swapped the table would lose their place and make the cursor pointless.
  const [older, setOlder] = useState<RequestRow[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  // The first page the reader is currently looking at, frozen. `null` means
  // "not yet loaded" — distinct from an empty result set, which would
  // otherwise look identical and re-freeze forever on every poll.
  const [held, setHeld] = useState<RequestRow[] | null>(null)
  const [views, setViews] = useState<SavedView[]>(() => loadSavedViews())
  const [savingName, setSavingName] = useState<string | null>(null)

  const first = useRequests({ ...filters, limit: "50" })

  useEffect(() => {
    if (first.data && held === null) setHeld(first.data.requests)
  }, [first.data, held])

  function onFilter(key: (typeof FIELDS)[number], value: string) {
    // The cursor is rejected under different filters by design; resetting it
    // here is what keeps that rejection invisible in normal use.
    setOlder([])
    setCursor(null)
    setHeld(null)
    setFilter(key, value)
  }

  function applyView(view: SavedView) {
    setOlder([])
    setCursor(null)
    setHeld(null)
    // One write, not a loop of setFilter calls: each of those replaces the
    // whole URL from the same stale search string, so only the last field in
    // a loop would ever stick.
    const merged = Object.fromEntries(FIELDS.map((f) => [f, view.filters[f] ?? ""]))
    router.history.replace(`${pathname}${filterQuery(merged)}`)
  }

  function confirmSave() {
    if (!savingName) return
    setViews(saveView(savingName, filters))
    setSavingName(null)
  }

  async function loadMore() {
    const from = cursor ?? first.data?.next_cursor
    if (!from) return
    const page = await api.get<RequestPage>(
      `/api/requests${filterQuery({ ...filters, limit: "50", cursor: from })}`,
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
          onValueChange={(v) => {
            if (!v) return
            if (v === "all") {
              onFilter("since_ms", "")
              return
            }
            const window = TIME_WINDOWS.find((w) => w.value === v)
            if (window) onFilter("since_ms", String(Date.now() - window.ms))
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

      <div className="mb-4 flex flex-wrap items-center gap-2">
        {views.map((v) => (
          <div key={v.name} className="flex items-center gap-1">
            <Button variant="outline" size="sm" onClick={() => applyView(v)}>
              {v.name}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              aria-label={`Delete saved view ${v.name}`}
              onClick={() => setViews(deleteView(v.name))}
            >
              ×
            </Button>
          </div>
        ))}
        {savingName === null ? (
          <Button variant="ghost" size="sm" onClick={() => setSavingName("")}>
            Save this view
          </Button>
        ) : (
          <>
            <Input
              autoFocus
              placeholder="View name"
              value={savingName}
              onChange={(e) => setSavingName(e.target.value)}
              className="w-40"
            />
            <Button size="sm" onClick={confirmSave}>
              Save
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setSavingName(null)}>
              Cancel
            </Button>
          </>
        )}
      </div>

      <DataTable
        columns={columns}
        data={rows}
        facets={["surface", "status", "failover"]}
        virtualize={{ rowHeight: 36, height: 640 }}
      />

      {more && (
        <Button variant="secondary" className="mt-4" onClick={() => void loadMore()}>
          Load more
        </Button>
      )}

      <TraceDrawer id={selected} onClose={() => setSelected(null)} />
    </>
  )
}
