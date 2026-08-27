import { Badge, Button } from "darkraise-ui"
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
