import { Button } from "darkraise-ui"
import { ScaleBar } from "../shell/measures"
import { PathMark, RequestStatus } from "../shell/status-mark"
import { ColumnHeader, DataTable } from "darkraise-ui/data-table"
import { count, dateTime, duration, zoneLabel } from "../../lib/format"
import type { RequestRow } from "../../lib/api-types"

/** The scalar shape a DataTable facet needs. `attempts` already reports the
 *  count; this is the same fact restated as a fixed string, because a facet
 *  filters on exact values and the API has no `attempts` filter to delegate
 *  it to. */
export type RequestTableRow = RequestRow & { failover: "failover" | "single" }

export function facetRow(r: RequestRow): RequestTableRow {
  return { ...r, failover: r.attempts > 1 ? "failover" : "single" }
}

export const CSV_COLUMNS: { key: keyof RequestTableRow; header: string }[] = [
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
export type Columns = Parameters<typeof DataTable<RequestTableRow, unknown>>[0]["columns"]

/** The bar's domain. Three decades: below the floor every request is simply
 *  fast, and above the ceiling it has already failed a timeout. */
const LATENCY_FLOOR_MS = 100
const LATENCY_CEILING_MS = 100_000

/** What the client asked for and what answered, when they differ. An alias
 *  with no final model is a request nothing served, and an arrow pointing at
 *  nothing would read as a rendering fault. */
export function modelLabel(row: RequestRow): string {
  if (row.alias && row.final_model) return `${row.alias} → ${row.final_model}`
  return row.model
}

export function buildColumns(onOpen: (id: string) => void): Columns {
  return [
    {
      accessorKey: "ts_ms",
      // The zone is named once, here, rather than on every row.
      header: ({ column }) => <ColumnHeader column={column} title={`Time (${zoneLabel()})`} />,
      cell: ({ row }) => <span className="whitespace-nowrap">{dateTime(row.original.ts_ms)}</span>,
    },
    { accessorKey: "surface", header: "Surface" },
    {
      accessorKey: "model",
      header: ({ column }) => <ColumnHeader column={column} title="Model" />,
      cell: ({ row }) => <span className="font-mono text-sm">{modelLabel(row.original)}</span>,
    },
    {
      accessorKey: "provider",
      header: ({ column }) => <ColumnHeader column={column} title="Provider" />,
      cell: ({ row }) => row.original.provider || "—",
    },
    {
      accessorKey: "status",
      // A string header, because the facet button takes its name from it and
      // falls back to the column id otherwise.
      header: "Status",
      cell: ({ row }) => (
        <span className="inline-flex items-center gap-1.5">
          <RequestStatus status={row.original.status} />
          <span>{row.original.status}</span>
        </span>
      ),
    },
    {
      // The facet filters on the fixed string; the cell shows the count.
      accessorKey: "failover",
      header: "Attempts",
      cell: ({ row }) => (
        <span
          className="tabular-nums"
          title={
            row.original.attempts > 1
              ? `${row.original.attempts} attempts — this request failed over`
              : "served on the first attempt"
          }
        >
          {row.original.attempts}
        </span>
      ),
    },
    {
      id: "tokens",
      accessorFn: (r) => r.tokens_in + r.tokens_out,
      header: ({ column }) => <ColumnHeader column={column} title="Tokens" />,
      cell: ({ row }) => (
        <span className="whitespace-nowrap tabular-nums">
          {count(row.original.tokens_in)}/{count(row.original.tokens_out)}
        </span>
      ),
    },
    {
      accessorKey: "total_ms",
      header: ({ column }) => <ColumnHeader column={column} title="Latency" />,
      // Log-scaled against a fixed domain rather than against the rows on
      // screen: latency runs over three orders of magnitude, and a scale that
      // moved with the filter would redraw a row whose value never changed.
      cell: ({ row }) => (
        <ScaleBar
          value={row.original.total_ms}
          min={LATENCY_FLOOR_MS}
          max={LATENCY_CEILING_MS}
          label={duration(row.original.total_ms)}
          title="Bar is log-scaled, 100 ms to 100 s"
        />
      ),
    },
    {
      accessorKey: "path",
      header: "Path",
      // Neutral, not accent: which renderer served is not a request outcome,
      // so it does not earn a state colour.
      cell: ({ row }) => (
        <span className="inline-flex items-center gap-1.5">
          <PathMark path={row.original.path} />
          {row.original.path ? <span>{row.original.path}</span> : null}
        </span>
      ),
    },
    {
      id: "actions",
      header: "",
      // Not data, so not something to hide: the column-visibility menu labels
      // each entry with its header, and an empty header would sit in that list
      // as a checkbox with no name.
      enableHiding: false,
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
