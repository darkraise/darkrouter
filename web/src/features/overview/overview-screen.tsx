import { useMemo, useState } from "react"
import { Banner, Card } from "darkraise-ui"
import { useConfig, useOverview, useProviders, useUsage } from "../../lib/queries"
import type { Overview, UsageRow } from "../../lib/api-types"
import { durationParts, money, percent } from "../../lib/format"
import { AddAccountsDialog } from "../providers/add-accounts-dialog"
import { FirstRunProviders } from "../shell/first-run-providers"
import { LoadError } from "../shell/screen-state"
import { FlowGraph, aliasesFromUsage, type FlowProvider } from "./flow-graph"
import { Failovers } from "./failovers"

/** The window `/api/overview` rolls its live readings over, and the window the
 *  daily series covers. Both are printed rather than implied: three of the
 *  four readouts describe five minutes while every sparkline under them
 *  describes thirty days, and a tile that says neither invites the reader to
 *  assume they match. */
const LIVE_WINDOW = "last 5 min"
const SERIES_WINDOW = "30d"

/** Today's spend as the tile reads it. Unpriced is unknown, not free; a
 *  priced zero is a day nothing has been spent on yet, and the tile says
 *  that in words rather than printing a "free" that reads as a price. */
export function spendReading(spend: Overview["today_spend"]): string {
  if (!spend.priced || spend.micros === null) return "—"
  if (spend.micros === 0) return "no spend yet"
  return money(spend.micros)
}

/** Daily totals in day order, so a sparkline's x-axis is time. */
function byDay(rows: UsageRow[], value: (r: UsageRow) => number): number[] {
  const acc = new Map<string, number>()
  for (const row of rows) acc.set(row.day, (acc.get(row.day) ?? 0) + value(row))
  return [...acc.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([, v]) => v)
}

export function requestSeries(rows: UsageRow[]): number[] {
  return byDay(rows, (r) => r.requests)
}

export function spendSeries(rows: UsageRow[]): number[] {
  // Null contributes nothing rather than removing the day: dropping it would
  // compress the shape and misreport when the spending happened.
  return byDay(rows, (r) => r.cost_micros ?? 0)
}

export function errorSeries(rows: UsageRow[]): number[] {
  // Attempts beyond requests are failovers. Floored, because a rollup that
  // straddles a day boundary can land one attempt short of its request.
  return byDay(rows, (r) => Math.max(0, r.attempts - r.requests))
}

/** A sparkline over the daily series. Decorative: the number beside it is the
 *  reading, this is only its shape. Neutral rather than branded — a metric
 *  trend is neither a provider state nor a router verdict. */
function Sparkline({ points }: { points: number[] }) {
  const max = Math.max(...points, 1)
  const step = 100 / Math.max(1, points.length - 1)
  const coords = points.map((p, i) => `${i * step},${40 - (p / max) * 34}`)
  return (
    <svg
      className="mt-auto block h-10 w-full"
      viewBox="0 0 100 40"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      {points.length > 1 && (
        <>
          {/* Closed back to the baseline in the markup rather than filled via
              CSS, so one points array draws both the stroke and its wash. */}
          <polygon
            points={`${coords.join(" ")} 100,40 0,40`}
            fill="hsl(var(--muted-foreground))"
            opacity="0.08"
            stroke="none"
          />
          <polyline
            points={coords.join(" ")}
            fill="none"
            stroke="hsl(var(--muted-foreground))"
            strokeWidth="1.5"
            vectorEffect="non-scaling-stroke"
          />
        </>
      )}
    </svg>
  )
}

type Reading = { value: string; unit?: string; label?: string }

/**
 * One tile of the live strip.
 *
 * Every tile has the same anatomy — wire field and window, reading, trend,
 * and what the trend is of — so a tile with nothing to plot reads as a fact
 * about the data rather than as a tile that failed to finish.
 */
function Tile({
  caption,
  window,
  readings,
  points,
  seriesLabel,
}: {
  caption: string
  window: string
  readings: Reading[]
  points?: number[]
  seriesLabel: string
}) {
  // No per-refresh indicator. The strip polls every three seconds and a
  // request that resolves in milliseconds turns any such mark into a flicker
  // -- movement the eye has to check and that never carries news. The
  // skeleton covers the one wait that is long enough to report, and a poll
  // that fails says so beside the heading.
  return (
    <Card className="flex flex-col gap-2 overflow-hidden p-4">
      {/* Two deliberate lines rather than one that wraps: at the 14px floor
          the label and its window do not fit a quarter-width tile together,
          and a ragged wrap made the four readouts sit at different heights. */}
      <p className="text-sm font-medium text-[hsl(var(--muted-foreground))]">{caption}</p>
      <p className="-mt-1 text-sm text-[hsl(var(--legend))]">{window}</p>
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
        {readings.map((r) => (
          <span key={r.label ?? r.value} className="inline-flex items-baseline gap-1">
            {r.label && (
              <span className="text-sm text-[hsl(var(--legend))]">{r.label}</span>
            )}
            <span className="text-3xl font-semibold leading-none tracking-tight tabular-nums">
              {r.value}
            </span>
            {r.unit && (
              <span
                className={
                  r.unit === "%"
                    ? "text-sm text-[hsl(var(--muted-foreground))] -ml-1"
                    : "text-sm text-[hsl(var(--muted-foreground))]"
                }
              >
                {r.unit}
              </span>
            )}
          </span>
        ))}
      </div>
      {points ? <Sparkline points={points} /> : <div className="mt-auto h-10" />}
      <p className="text-sm text-[hsl(var(--legend))]">{seriesLabel}</p>
    </Card>
  )
}

/** The strip's shape while the first response is outstanding. A blank screen
 *  cannot be told apart from a gateway that is down. */
function TileSkeleton() {
  return (
    <Card className="flex flex-col gap-3 p-4" aria-hidden="true">
      <div className="h-3 w-28 animate-pulse rounded bg-[hsl(var(--muted))]" />
      <div className="h-7 w-20 animate-pulse rounded bg-[hsl(var(--muted))]" />
      <div className="mt-auto h-10 w-full animate-pulse rounded bg-[hsl(var(--muted))]" />
    </Card>
  )
}

/** Providers in priority order, annotated with what the window actually sent
 *  them. A provider the router cannot currently choose is drawn as idle.
 *
 *  A provider with no account is not in the routing path at all and is left
 *  out: it keeps its database row after its last credential is deleted, so
 *  drawing every such row would put a column of providers nobody has set up
 *  beside the handful that carry traffic. It stays only while the window
 *  still holds requests it served, because that is history the graph would
 *  otherwise erase — along with any return arc drawn to or from it. */
export function flowProviders(
  overview: Overview,
  byProvider: UsageRow[],
): FlowProvider[] {
  const volume = new Map<string, number>()
  for (const row of byProvider) {
    if (!row.key) continue
    volume.set(row.key, (volume.get(row.key) ?? 0) + row.requests)
  }
  return overview.providers
    .filter((tile) => tile.state !== "unconfigured" || (volume.get(tile.id) ?? 0) > 0)
    .map((tile, i) => ({
      id: tile.id,
      name: tile.name,
      requests: volume.get(tile.id) ?? 0,
      // The tile order is the priority order the API already sorts by, so the
      // graph's column reads top-to-bottom the way the router walks it.
      priority: i + 1,
      // Disabled or unconfigured means the router will not pick it, whatever
      // the window happened to contain. `degraded` still routes.
      candidate: tile.state !== "disabled" && tile.state !== "unconfigured",
      credentials: tile.credentials,
      cooling: tile.cooling,
      needsReauth: tile.needs_reauth,
      state: tile.state,
    }))
}

export function OverviewScreen() {
  const overview = useOverview()
  const byAlias = useUsage("alias")
  const byProvider = useUsage("provider")
  const config = useConfig()
  const providers = useProviders()
  const [addOpen, setAddOpen] = useState(false)

  const o = overview.data
  const providerDays = byProvider.data?.days
  const aliasDays = byAlias.data?.days
  const days = o?.series ?? []
  // Guards the array itself, not just the response object: a caller that
  // returns a response shaped differently than expected must read as "not
  // known yet" rather than crash the whole screen on a missing collection.
  const noProviders = providers.data?.providers?.length === 0
  const p95 = o ? durationParts(o.latency.p95_ms) : null
  const p50 = o ? durationParts(o.latency.p50_ms) : null

  // The graph's inputs, rebuilt only when the responses that feed them
  // change. Every three-second poll re-rendered this screen, and fresh
  // arrays on each pass made the canvas lay itself out again for nothing.
  const graphAliases = useMemo(() => aliasesFromUsage(aliasDays ?? []), [aliasDays])
  const graphProviders = useMemo(
    () => (o ? flowProviders(o, providerDays ?? []) : []),
    [o, providerDays],
  )
  const failoverEdges = o?.failover_edges
  const graphFailovers = useMemo(
    () =>
      (failoverEdges ?? []).map((e) => ({
        from: e.from_provider_id,
        to: e.to_provider_id,
        count: e.requests,
      })),
    [failoverEdges],
  )

  return (
    <>
      {config.data && !config.data.valid && (
        <Banner variant="destructive" className="mb-6">
          <p className="text-sm font-medium">Config invalid</p>
          <p className="mt-1 font-mono text-sm break-words">{config.data.error}</p>
          {config.data.serving && (
            <p className="mt-1 text-sm">{config.data.serving}</p>
          )}
        </Banner>
      )}

      {overview.isError && !o && (
        <LoadError
          what="The overview"
          error={overview.error}
          onRetry={() => void overview.refetch()}
          className="mb-6"
        />
      )}

      {noProviders ? (
        <FirstRunProviders onAdd={() => setAddOpen(true)} />
      ) : (
        <>
          <section>
            <div className="mb-2 flex items-baseline gap-2">
              <h2 className="text-sm font-medium">Live</h2>
              {/* A failed poll on a screen that still has readings is a
                  staleness note, not an alarm: the numbers below are real,
                  they are just older than they look. */}
              {overview.isError && o && (
                <span className="text-sm text-[hsl(var(--warning))]">
                  last refresh failed — readings may be stale
                </span>
              )}
            </div>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              {!o ? (
                <>
                  <TileSkeleton />
                  <TileSkeleton />
                  <TileSkeleton />
                  <TileSkeleton />
                </>
              ) : (
                <>
                  <Tile
                    caption="Requests / min"
                    window={LIVE_WINDOW}
                    readings={[{ value: o.requests_per_min.toFixed(1), unit: "req/min" }]}
                    points={requestSeries(days)}
                    seriesLabel={`${SERIES_WINDOW} · requests per day`}
                  />
                  <Tile
                    caption="Error rate"
                    window={LIVE_WINDOW}
                    readings={[{ value: percent(o.error_rate) }]}
                    points={errorSeries(days)}
                    // Named for what it plots. The daily rollup has no error
                    // column, so the trend under an error rate is the closest
                    // thing it does have, and saying so is the difference
                    // between a proxy and a mislabelled number.
                    seriesLabel={`${SERIES_WINDOW} · failovers per day`}
                  />
                  <Tile
                    caption="p50 latency"
                    window={LIVE_WINDOW}
                    readings={[
                      { label: "p50", value: p50!.value, unit: p50!.unit },
                      { label: "p95", value: p95!.value, unit: p95!.unit },
                    ]}
                    // usage_daily has no per-day latency column, so there is
                    // no series to plot here.
                    seriesLabel="live window only"
                  />
                  <Tile
                    caption="Today's spend (UTC day)"
                    window="since 00:00 UTC"
                    readings={[{ value: spendReading(o.today_spend) }]}
                    points={spendSeries(days)}
                    seriesLabel={`${SERIES_WINDOW} · spend per day`}
                  />
                </>
              )}
            </div>
          </section>

          <section className="mt-6">
            <h2 className="text-sm font-medium">
              Routing flow{" "}
              <span className="font-normal text-[hsl(var(--legend))]">
                · {SERIES_WINDOW}
              </span>
            </h2>
            {/* A legend, not a paragraph. Four sentences explaining a picture
                sit above the picture and are read once; the same facts beside
                the marks they name are read whenever the graph is. */}
            <ul className="mt-1 mb-4 flex flex-wrap gap-x-5 gap-y-1 text-sm text-[hsl(var(--muted-foreground))]">
              {/* The swatches take the edges' own colour, --info, so the
                  legend names what is drawn rather than a grey stand-in. */}
              <li className="flex items-center gap-1.5">
                <span className="h-0.5 w-6 rounded bg-[hsl(var(--info))]" aria-hidden="true" />
                thicker edge, larger share
              </li>
              <li className="flex items-center gap-1.5">
                <span
                  className="h-0.5 w-6 rounded border-t-2 border-dashed border-[hsl(var(--info))] bg-transparent"
                  aria-hidden="true"
                />
                failed over from somewhere else
              </li>
              <li className="flex items-center gap-1.5">
                <span className="h-px w-6 rounded bg-[hsl(var(--border))]" aria-hidden="true" />
                inbound, before routing
              </li>
              <li className="flex items-center gap-1.5">
                <span className="h-3 w-3 rounded border border-dashed" aria-hidden="true" />
                no edge, not a candidate
              </li>
            </ul>
            {o ? (
              <FlowGraph
                aliases={graphAliases}
                providers={graphProviders}
                failovers={graphFailovers}
                // Every return the graph draws, summed — not the five rows
                // /api/overview caps its recent list at, which would report a
                // busy window as five.
                failoverCount={o.failover_edges.reduce((n, e) => n + e.requests, 0)}
              />
            ) : (
              // The heading and its explanation without a canvas beneath them
              // read as a graph that failed to draw. This is the same box,
              // waiting.
              <div
                className="h-[360px] animate-pulse rounded-[var(--radius)] border bg-[hsl(var(--muted))]"
                aria-hidden="true"
              />
            )}
          </section>
        </>
      )}

      {o && <Failovers rows={o.failovers.slice(0, 5)} />}

      <AddAccountsDialog open={addOpen} onOpenChange={setAddOpen} />
    </>
  )
}
