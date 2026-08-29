import "./chart-scope.css"
import { Link } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import {
  Button,
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
import { useSearchFilters } from "../../lib/search-filters"
import type { UsageDimension, UsageRow } from "../../lib/api-types"
import { EmptyState, GhostChart } from "../shell/empty-state"
import { StackedAreaChart, CostLineChart } from "./usage-charts"

const DIMENSIONS: { value: UsageDimension | "day"; label: string }[] = [
  { value: "day", label: "Total" },
  { value: "provider", label: "Provider" },
  { value: "model", label: "Model" },
  { value: "alias", label: "Alias" },
]

// The endpoint serves 365 days and no more. Labelling the widest option "all"
// would claim a completeness the data does not have -- the same reason an
// unpriced total renders as "--" rather than "$0.00".
export const RANGES = [
  { value: "7", label: "7d", days: 7 },
  { value: "30", label: "30d", days: 30 },
  { value: "90", label: "90d", days: 90 },
  { value: "365", label: "365d", days: 365 },
] as const

// Five is the chart ramp's width (chart-scope.css). A sixth series would
// reuse a fill and two providers would render indistinguishably.
const MAX_SERIES = 5

// Dimension and range live in the URL rather than in component state, same
// as every other filtered screen: this task's whole point is a click-through
// that turns a chart into an investigation, and an investigation you cannot
// paste to yourself is a weaker version of that.
const FIELDS = ["dimension", "days"] as const

/** The busiest keys in the window, capped at the ramp's width. */
export function topKeys(rows: UsageRow[], n: number): string[] {
  const total = new Map<string, number>()
  for (const r of rows) {
    if (!r.key) continue
    total.set(r.key, (total.get(r.key) ?? 0) + r.requests)
  }
  return [...total.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, n)
    .map(([k]) => k)
}

/**
 * Rows pivoted into one column per key, zero-filled and day-ordered, for a
 * recharts stacked series.
 *
 * A key with no row at all on a day is a genuine zero. A key whose rows
 * exist but are all unpriced (`value` returns null) stays unknown rather
 * than collapsing to the same zero -- one priced row among them makes the
 * cell real, if partial, mirroring `summarise`'s null-preserving sum. The
 * two must not merge: a stacked area rendering a hole through the stack
 * would misreport a missing key as traffic stopping, and a cost chart
 * rendering unpriced as zero would misreport an unknown as free.
 */
export function stackByDay(
  rows: UsageRow[],
  keys: string[],
  value: (r: UsageRow) => number | null,
): Record<string, number | string | null>[] {
  const byDay = new Map<string, Record<string, number | string | null>>()
  // Which keys have had at least one row on a given day -- distinct from the
  // zero-fill placeholder, which marks "no row at all" rather than "a row
  // whose value we don't know yet".
  const seen = new Map<string, Set<string>>()

  for (const r of rows) {
    if (!r.key || !keys.includes(r.key)) continue
    let day = byDay.get(r.day)
    if (!day) {
      day = { day: r.day }
      for (const k of keys) day[k] = 0
      byDay.set(r.day, day)
      seen.set(r.day, new Set())
    }
    const seenKeys = seen.get(r.day)!
    const v = value(r)
    if (!seenKeys.has(r.key)) {
      day[r.key] = v
      seenKeys.add(r.key)
    } else if (v !== null) {
      const cur = day[r.key]
      day[r.key] = (typeof cur === "number" ? cur : 0) + v
    }
  }
  return [...byDay.values()].sort((a, b) => String(a.day).localeCompare(String(b.day)))
}

/**
 * The search object for a click-through into Requests, filtered on the
 * dimension and key that was clicked.
 *
 * Returns a plain object rather than a URL string: TanStack Router 1.170
 * types `<Link to>` against the registered route union, so a `to` carrying a
 * query string does not typecheck. `<Link to="/requests" search={...}>` is
 * the idiom the router supports -- the root route's `validateSearch` already
 * turns any string-keyed record into the URL's query params.
 *
 * `range` rides along so Requests' own time-range pills don't lie: a URL
 * carrying `since_ms` with no `range` shows that control as "All" while a
 * filter is actually active. "7d" happens to match Requests' own "7d" pill
 * exactly; wider spans (this screen goes to 365d, Requests tops out at 7d)
 * land on no pill rather than a false "All" -- still honest, just less
 * specific than a highlighted pill would be.
 */
export function requestsSearch(
  dimension: UsageDimension,
  key: string,
  days: number,
): Record<string, string> {
  const since = Date.now() - days * 24 * 60 * 60 * 1000
  return {
    [dimension]: key,
    since_ms: String(Math.round(since)),
    range: `${days}d`,
  }
}

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
          <span className="w-40 shrink-0 truncate font-mono text-sm">{r.key}</span>
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
          <span className="w-16 text-right font-mono text-sm tabular-nums">
            {r.requests}
          </span>
        </div>
      ))}
    </div>
  )
}

export function UsageScreen() {
  const [filters, setFilter] = useSearchFilters(FIELDS)
  // "day" (Total) and 30 days are each the default, so they are dropped from
  // the URL rather than written explicitly -- the same convention every
  // other screen's useSearchFilters caller uses for its own default.
  const dimension = (filters.dimension || "day") as UsageDimension | "day"
  const rangeValue = (filters.days || "30") as (typeof RANGES)[number]["value"]
  const days = RANGES.find((r) => r.value === rangeValue)?.days ?? 30
  const usage = useUsage({ dimension: dimension === "day" ? undefined : dimension, days })
  const usageRows = usage.data?.days ?? []
  const rows = summarise(usageRows)
  const keys = topKeys(usageRows, MAX_SERIES)
  // Nothing to filter by on the day view -- there is no dimension key, only
  // the day itself, and Requests has no "day" field to filter on.
  const clickable = dimension !== "day"

  return (
    <>
      <PageHeader title="Usage" description="What it cost, and where it went" />
      {/* Tokens burned by an attempt that failed before commit never reach
          usage_daily, so every figure here understates reality exactly when
          failover fires. Fixing the underlying gap is its own project; until
          then this is the honest caveat. */}
      <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">
        Tokens spent on failed attempts before a request committed are not counted here.
      </p>

      <div className="mb-4 flex flex-wrap items-center justify-between gap-2">
        <ToggleGroup
          type="single"
          value={dimension}
          onValueChange={(v) => v && setFilter("dimension", v === "day" ? "" : v)}
          variant="outline"
          size="sm"
        >
          {DIMENSIONS.map((d) => (
            <ToggleGroupItem key={d.value} value={d.value}>
              {d.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>

        <ToggleGroup
          type="single"
          value={rangeValue}
          onValueChange={(v) => v && setFilter("days", v === "30" ? "" : v)}
          variant="outline"
          size="sm"
        >
          {RANGES.map((r) => (
            <ToggleGroupItem key={r.value} value={r.value}>
              {r.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>

      {usageRows.length === 0 ? (
        <EmptyState
          title="Usage rolls up once a day, once requests start arriving"
          hint="Every served request lands in the day's totals. Spend needs a priced model — a model nobody has priced shows an em dash rather than a zero."
          action={
            <Button asChild size="sm">
              <Link to="/connect">Get a client connected</Link>
            </Button>
          }
          preview={<GhostChart />}
        />
      ) : (
        <>
          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">Requests</h2>
            <StackedAreaChart data={stackByDay(usageRows, keys, (r) => r.requests)} keys={keys} />
          </Card>

          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">Tokens</h2>
            <StackedAreaChart
              data={stackByDay(usageRows, keys, (r) => r.tokens_in + r.tokens_out)}
              keys={keys}
            />
          </Card>

          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">Cost</h2>
            <CostLineChart
              data={stackByDay(usageRows, keys, (r) => r.cost_micros)}
              keys={keys}
              formatValue={formatCost}
            />
          </Card>

          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">Ranked by requests</h2>
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
                  <TableCell className="font-mono text-sm">
                    {clickable ? (
                      <Link
                        to="/requests"
                        search={requestsSearch(dimension, r.key, days)}
                        className="underline"
                      >
                        {r.key}
                      </Link>
                    ) : (
                      r.key
                    )}
                  </TableCell>
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
      )}
    </>
  )
}
