import { Button } from "darkraise-ui"
import { Pips, ScaleBar } from "../shell/measures"
import { PathMark, RequestStatus } from "../shell/status-mark"
import { ColumnHeader, DataTable } from "darkraise-ui/data-table"
import type { RequestRow } from "../../lib/api-types"

/** The scalar shape a DataTable facet needs. `attempts` already reports the
 *  count; this is the same fact restated as a fixed string, because a facet
 *  filters on exact values and the API has no `attempts` filter to delegate
 *  it to. */
export type TableRow = RequestRow & { failover: "failover" | "single" }

export function facetRow(r: RequestRow): TableRow {
  return { ...r, failover: r.attempts > 1 ? "failover" : "single" }
}

export const CSV_COLUMNS: { key: keyof TableRow; header: string }[] = [
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
export type Columns = Parameters<typeof DataTable<TableRow, unknown>>[0]["columns"]

/** The bar's domain. Three decades: below the floor every request is simply
 *  fast, and above the ceiling it has already failed a timeout. */
const LATENCY_FLOOR_MS = 100
const LATENCY_CEILING_MS = 100_000

/** Seconds past the thousand, because 8100 ms makes the reader do the
 *  division — the same rule the overview's latency tile follows. */
export function formatLatency(ms: number): string {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`
}

export function buildColumns(onOpen: (id: string) => void): Columns {
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
        <span className="font-mono text-sm">
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
      cell: ({ row }) => <RequestStatus status={row.original.status} />,
    },
    {
      accessorKey: "attempts",
      header: ({ column }) => <ColumnHeader column={column} title="Attempts" />,
      // More than one attempt means a failover, which is the row an operator
      // is usually looking for. One mark per try, so a failover is a longer
      // run of dots rather than a number to read on every line.
      cell: ({ row }) => (
        <Pips
          count={row.original.attempts}
          title={
            row.original.attempts > 1
              ? `${row.original.attempts} attempts — this request failed over`
              : "served on the first attempt"
          }
        />
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
      // Log-scaled against a fixed domain rather than against the rows on
      // screen: latency runs over three orders of magnitude, and a scale that
      // moved with the filter would redraw a row whose value never changed.
      cell: ({ row }) => (
        <ScaleBar
          value={row.original.total_ms}
          min={LATENCY_FLOOR_MS}
          max={LATENCY_CEILING_MS}
          label={row.original.total_ms === null ? "—" : formatLatency(row.original.total_ms)}
          title="Bar is log-scaled, 100 ms to 100 s"
        />
      ),
    },
    {
      accessorKey: "path",
      header: "Path",
      // Neutral, not accent: which renderer served is not a request outcome,
      // so it does not earn a state colour.
      cell: ({ row }) => <PathMark path={row.original.path} />,
    },
    { accessorKey: "failover", header: "Failover" },
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
