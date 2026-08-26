import { PageHeader } from "darkraise-ui/layout"
import { Card } from "darkraise-ui"
import { useOverview, useUsage } from "../../lib/queries"
import type { Overview, UsageRow } from "../../lib/api-types"
import { FlowGraph, aliasesFromUsage, type FlowProvider } from "./flow-graph"

/** Micro-dollars to a readable amount. Unpriced is not zero: a model with no
 *  catalog price has an unknown cost, and showing $0.00 would claim it was
 *  free. */
function money(micros: number, priced: boolean): string {
  if (!priced) return "—"
  return `$${(micros / 1_000_000).toFixed(2)}`
}

/** A sparkline over the daily series. Decorative: the number beside it is the
 *  reading, this is only its shape. */
function Sparkline({ points }: { points: number[] }) {
  if (points.length < 2) return null
  const max = Math.max(...points, 1)
  const step = 100 / (points.length - 1)
  const coords = points.map((p, i) => `${i * step},${40 - (p / max) * 34}`)
  return (
    <svg
      className="mt-2 h-10 w-full"
      viewBox="0 0 100 40"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <polyline
        points={coords.join(" ")}
        fill="none"
        stroke="hsl(var(--primary))"
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}

function Tile({
  caption,
  value,
  children,
}: {
  caption: string
  value: string
  children?: React.ReactNode
}) {
  return (
    <Card className="p-4">
      <p className="text-xs text-[hsl(var(--legend))] font-mono">{caption}</p>
      <div className="mt-1 text-3xl font-semibold tracking-tight">{value}</div>
      {children}
    </Card>
  )
}

/** Providers in priority order, annotated with what the window actually sent
 *  them. A provider the router cannot currently choose is drawn as idle. */
export function flowProviders(
  overview: Overview,
  byProvider: UsageRow[],
): FlowProvider[] {
  const volume = new Map<string, number>()
  for (const row of byProvider) {
    if (!row.key) continue
    volume.set(row.key, (volume.get(row.key) ?? 0) + row.requests)
  }
  return overview.providers.map((tile, i) => ({
    id: tile.id,
    name: tile.name,
    requests: volume.get(tile.id) ?? 0,
    // The tile order is the priority order the API already sorts by, so the
    // graph's column reads top-to-bottom the way the router walks it.
    priority: i + 1,
    // Disabled or unconfigured means the router will not pick it, whatever
    // the window happened to contain. `degraded` still routes.
    candidate: tile.state !== "disabled" && tile.state !== "unconfigured",
    note:
      tile.cooling > 0
        ? `${tile.cooling} cooling`
        : tile.needs_reauth
          ? "needs reauth"
          : undefined,
    state: tile.state,
  }))
}

export function OverviewScreen() {
  const overview = useOverview()
  const byAlias = useUsage("alias")
  const byProvider = useUsage("provider")

  if (!overview.data) return null
  const o = overview.data

  return (
    <>
      <PageHeader
        title="Overview"
        description="Is it working, and what did it just do"
      />

      <div className="grid gap-4 md:grid-cols-4">
        <Tile
          caption="requests_per_min"
          value={o.requests_per_min.toFixed(1)}
        >
          <Sparkline points={o.series.map((s) => s.requests)} />
        </Tile>
        <Tile
          caption="error_rate"
          value={`${(o.error_rate * 100).toFixed(1)}%`}
        />
        <Tile caption="latency_p95" value={`${o.latency.p95_ms}ms`} />
        <Tile
          caption="today_spend"
          value={money(o.today_spend.micros, o.today_spend.priced)}
        />
      </div>

      <section className="mt-6">
        <h2 className="text-sm font-medium">Routing</h2>
        <p className="mt-1 mb-4 max-w-prose text-sm text-[hsl(var(--muted-foreground))]">
          Aliases on the left, providers on the right in priority order. Edge
          thickness is share of the window; a dashed return is traffic that
          arrived somewhere because somewhere else refused it. A provider that
          is not a candidate has no edge at all.
        </p>
        <FlowGraph
          aliases={aliasesFromUsage(byAlias.data ?? [])}
          providers={flowProviders(o, byProvider.data ?? [])}
          failovers={o.failover_edges.map((e) => ({
            from: e.from_provider_id,
            to: e.to_provider_id,
            count: e.requests,
          }))}
          totalRequests={Math.round((o.requests_per_min * o.window_sec) / 60)}
          failoverCount={o.failovers.length}
        />
      </section>
    </>
  )
}
