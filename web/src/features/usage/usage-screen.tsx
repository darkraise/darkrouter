import "./chart-scope.css"
import { useState } from "react"
import { PageHeader } from "darkraise-ui/layout"
import {
  Card,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  ToggleGroup,
  ToggleGroupItem,
} from "darkraise-ui"
import { useUsage } from "../../lib/queries"
import type { UsageDimension, UsageRow } from "../../lib/api-types"

const DIMENSIONS: { value: UsageDimension | "day"; label: string }[] = [
  { value: "day", label: "Total" },
  { value: "provider", label: "Provider" },
  { value: "model", label: "Model" },
  { value: "alias", label: "Alias" },
]

/** Unpriced is not zero. A day whose models had no catalog price has an
 *  unknown cost, and $0.00 would claim it was free. */
export function formatCost(micros: number | null): string {
  if (micros === null) return "—"
  return `$${(micros / 1_000_000).toFixed(4)}`
}

/** Rows summed per key, so a dimension reads as totals rather than as one
 *  line per key per day. */
export function summarise(rows: UsageRow[]): {
  key: string
  requests: number
  attempts: number
  tokensIn: number
  tokensOut: number
  cost: number | null
}[] {
  const acc = new Map<string, ReturnType<typeof summarise>[number]>()
  for (const row of rows) {
    const key = row.key || row.day
    const cur = acc.get(key) ?? {
      key,
      requests: 0,
      attempts: 0,
      tokensIn: 0,
      tokensOut: 0,
      cost: null as number | null,
    }
    cur.requests += row.requests
    cur.attempts += row.attempts
    cur.tokensIn += row.tokens_in
    cur.tokensOut += row.tokens_out
    // A null stays null only while every contributing row is null: one priced
    // row makes the total a real, if partial, number.
    if (row.cost_micros !== null) cur.cost = (cur.cost ?? 0) + row.cost_micros
    acc.set(key, cur)
  }
  return [...acc.values()].sort((a, b) => b.requests - a.requests)
}

function Bars({ rows }: { rows: ReturnType<typeof summarise> }) {
  const max = Math.max(...rows.map((r) => r.requests), 1)
  return (
    <div className="chart-scope flex flex-col gap-2">
      {rows.slice(0, 10).map((r, i) => (
        <div key={r.key} className="flex items-center gap-3">
          <span className="w-40 shrink-0 truncate font-mono text-xs">{r.key}</span>
          <div className="h-4 flex-1 rounded-sm bg-[hsl(var(--muted))]">
            <div
              className="h-full rounded-sm"
              style={{
                width: `${(r.requests / max) * 100}%`,
                background: `hsl(var(--chart-${(i % 5) + 1}))`,
                // Fill, not hue, is what separates the series.
                opacity: 1 - (i % 5) * 0.15,
              }}
            />
          </div>
          <span className="w-16 text-right font-mono text-xs tabular-nums">
            {r.requests}
          </span>
        </div>
      ))}
    </div>
  )
}

export function UsageScreen() {
  const [dimension, setDimension] = useState<UsageDimension | "day">("day")
  const usage = useUsage(dimension === "day" ? undefined : dimension)
  const rows = summarise(usage.data ?? [])

  return (
    <>
      <PageHeader title="Usage" description="What it cost, and where it went" />

      <ToggleGroup
        type="single"
        value={dimension}
        onValueChange={(v) => v && setDimension(v as UsageDimension | "day")}
        variant="outline"
        size="sm"
        className="mb-4"
      >
        {DIMENSIONS.map((d) => (
          <ToggleGroupItem key={d.value} value={d.value}>
            {d.label}
          </ToggleGroupItem>
        ))}
      </ToggleGroup>

      <Card className="mb-6 p-4">
        <Bars rows={rows} />
      </Card>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{dimension === "day" ? "Day" : dimension}</TableHead>
            <TableHead>Requests</TableHead>
            <TableHead>Attempts</TableHead>
            <TableHead>Tokens in</TableHead>
            <TableHead>Tokens out</TableHead>
            <TableHead>Cost</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow key={r.key}>
              <TableCell className="font-mono text-xs">{r.key}</TableCell>
              <TableCell className="tabular-nums">{r.requests}</TableCell>
              {/* Attempts exceed requests exactly when something failed over,
                  which is the column that explains a cost the request count
                  does not. */}
              <TableCell className="tabular-nums">{r.attempts}</TableCell>
              <TableCell className="tabular-nums">{r.tokensIn}</TableCell>
              <TableCell className="tabular-nums">{r.tokensOut}</TableCell>
              <TableCell className="tabular-nums">{formatCost(r.cost)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </>
  )
}
