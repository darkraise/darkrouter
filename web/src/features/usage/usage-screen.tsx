import "./chart-scope.css"
import { Link } from "@tanstack/react-router"
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
import { count, money } from "../../lib/format"
import type { UsageDimension, UsageRow } from "../../lib/api-types"
import { EmptyState, GhostChart } from "../shell/empty-state"
import { LoadError, LoadingRows } from "../shell/screen-state"
import { StackedAreaChart, CostLineChart } from "./usage-charts"

type Dimension = UsageDimension | "day"

const DIMENSIONS: { value: Dimension; label: string }[] = [
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
// reuse a hue and two providers would render indistinguishably.
const MAX_SERIES = 5

/** The one series the Total view plots. Rows on that view carry no key, so
 *  they are given this one; the charts then have a column to stack. */
const TOTAL = "total"

// Dimension and range live in the URL rather than in component state, same
// as every other filtered screen: this task's whole point is a click-through
// that turns a chart into an investigation, and an investigation you cannot
// paste to yourself is a weaker version of that.
const FIELDS = ["dimension", "days"] as const

/** A URL's dimension, or the default when it names one this screen has not
 *  got. A pasted `?dimension=anything` used to reach the API as a group_by. */
export function readDimension(raw: string): Dimension {
  return DIMENSIONS.some((d) => d.value === raw) ? (raw as Dimension) : "day"
}

export function readRange(raw: string): (typeof RANGES)[number] {
  return RANGES.find((r) => r.value === raw) ?? RANGES[1]
}

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

/** What the charts plot for a dimension: its top keys, or on the Total view
 *  the rows themselves under one key, since they have none of their own. */
export function chartSeries(
  rows: UsageRow[],
  dimension: Dimension,
): { rows: UsageRow[]; keys: string[] } {
  if (dimension !== "day") return { rows, keys: topKeys(rows, MAX_SERIES) }
  return { rows: rows.map((r) => ({ ...r, key: TOTAL })), keys: [TOTAL] }
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

/** A cost axis tick. `money` says "free" for zero, which is a price and
 *  reads oddly at the origin of an axis; the tick says $0 instead. */
export function costTick(micros: number | null): string {
  if (micros === 0) return "$0"
  return money(micros)
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

/** What the ranking card is a ranking of. "Ranked by requests" alone left the
 *  Total view's list of dates unexplained. */
export function rankingHeading(dimension: Dimension): string {
  switch (dimension) {
    case "day":
      return "Busiest days"
    case "provider":
      return "Providers ranked by requests"
    case "model":
      return "Models ranked by requests"
    case "alias":
      return "Aliases ranked by requests"
  }
}

const COLUMN_HEADING: Record<Dimension, string> = {
  day: "UTC day",
  provider: "Provider",
  model: "Model",
  alias: "Alias",
}

function Bars({ rows }: { rows: ReturnType<typeof summarise> }) {
  const max = Math.max(...rows.map((r) => r.requests), 1)
  return (
    <div className="chart-scope flex flex-col gap-2">
      {rows.slice(0, 10).map((r, i) => (
        <div key={r.key} className="flex items-center gap-3">
          <span className="w-40 shrink-0 truncate font-mono text-sm">{r.key}</span>
          <div className="h-4 min-w-0 flex-1 rounded-sm bg-[hsl(var(--muted))]">
            <div
              className="h-full rounded-sm"
              style={{
                width: `${(r.requests / max) * 100}%`,
                background: `hsl(var(--chart-${(i % 5) + 1}))`,
              }}
            />
          </div>
          <span className="w-16 shrink-0 text-right font-mono text-sm tabular-nums">
            {count(r.requests)}
          </span>
        </div>
      ))}
    </div>
  )
}

function ChartSkeleton() {
  return (
    <>
      {["Requests", "Tokens", "Cost"].map((title) => (
        <Card key={title} className="mb-6 p-4">
          <h2 className="mb-2 text-sm font-medium">{title}</h2>
          <LoadingRows rows={1} className="h-56" />
        </Card>
      ))}
    </>
  )
}

export function UsageScreen() {
  const [filters, setFilter] = useSearchFilters(FIELDS)
  // "day" (Total) and 30 days are each the default, so they are dropped from
  // the URL rather than written explicitly -- the same convention every
  // other screen's useSearchFilters caller uses for its own default.
  const dimension = readDimension(filters.dimension)
  const range = readRange(filters.days)
  const days = range.days
  const usage = useUsage({ dimension: dimension === "day" ? undefined : dimension, days })
  const usageRows = usage.data?.days ?? []
  const rows = summarise(usageRows)
  const series = chartSeries(usageRows, dimension)
  const legend = dimension !== "day"
  // Nothing to filter by on the day view -- there is no dimension key, only
  // the day itself, and Requests has no "day" field to filter on.
  const clickable = dimension !== "day"

  return (
    <>
      {/* Tokens burned by an attempt that failed before commit never reach
          usage_daily, so every figure here understates reality exactly when
          failover fires. Fixing the underlying gap is its own project; until
          then this is the honest caveat. */}
      <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">
        Tokens spent on failed attempts before a request committed are not counted here.
        Days are UTC days.
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
          value={range.value}
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

      {usage.isError && (
        <LoadError
          what="The usage"
          error={usage.error}
          onRetry={() => void usage.refetch()}
          className="mb-6"
        />
      )}

      {usage.isPending ? (
        <ChartSkeleton />
      ) : usageRows.length === 0 ? (
        !usage.isError && (
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
        )
      ) : (
        <>
          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">Requests</h2>
            <StackedAreaChart
              data={stackByDay(series.rows, series.keys, (r) => r.requests)}
              keys={series.keys}
              legend={legend}
            />
          </Card>

          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">Tokens</h2>
            <StackedAreaChart
              data={stackByDay(series.rows, series.keys, (r) => r.tokens_in + r.tokens_out)}
              keys={series.keys}
              legend={legend}
            />
          </Card>

          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">Cost</h2>
            <CostLineChart
              data={stackByDay(series.rows, series.keys, (r) => r.cost_micros)}
              keys={series.keys}
              formatValue={costTick}
              legend={legend}
            />
          </Card>

          <Card className="mb-6 p-4">
            <h2 className="mb-2 text-sm font-medium">{rankingHeading(dimension)}</h2>
            <Bars rows={rows} />
          </Card>

          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{COLUMN_HEADING[dimension]}</TableHead>
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
                    <TableCell className="tabular-nums">{count(r.requests)}</TableCell>
                    {/* Attempts exceed requests exactly when something failed over,
                        which is the column that explains a cost the request count
                        does not. */}
                    <TableCell className="tabular-nums">{count(r.attempts)}</TableCell>
                    <TableCell className="tabular-nums">{count(r.tokensIn)}</TableCell>
                    <TableCell className="tabular-nums">{count(r.tokensOut)}</TableCell>
                    <TableCell className="tabular-nums">{money(r.cost)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </>
      )}
    </>
  )
}
