import { Link } from "@tanstack/react-router"
import "./flow-graph.css"
import type { ProviderTile, UsageRow } from "../../lib/api-types"

/** Fixed pixel geometry. The viewBox matches 1:1 so the graph scrolls rather
 *  than scales — a scaled graph distorts stroke widths, and stroke width is
 *  data here. */
const GEOM = {
  aliasX: 24,
  aliasW: 140,
  routerX: 232,
  routerW: 104,
  providerX: 398,
  providerW: 396,
  nodeH: 34,
  rowGap: 58,
  top: 30,
  minHeight: 320,
} as const

/** The widest an edge may be drawn. A share of one maps here; everything else
 *  is proportional, so two providers at 50% look equal rather than both maxed. */
const MAX_EDGE = 10

export type FlowAlias = { name: string; requests: number }
export type FlowProvider = {
  id: string
  name: string
  requests: number
  priority: number
  /** No edge is drawn for a provider the router cannot currently choose. */
  candidate: boolean
  note?: string
  state: ProviderTile["state"]
}

/** Traffic that arrived at `to` because `from` refused it. */
export type FlowFailover = { from: string; to: string; count: number }

function centreY(index: number): number {
  return GEOM.top + index * GEOM.rowGap + GEOM.nodeH / 2
}

/** A cubic with horizontal control points, so an edge leaves and arrives flat
 *  and the eye reads the column rather than the curve. */
function edge(x1: number, y1: number, x2: number, y2: number): string {
  const dx = (x2 - x1) / 2
  return `M${x1} ${y1} C ${x1 + dx} ${y1}, ${x2 - dx} ${y2}, ${x2} ${y2}`
}

export function FlowGraph({
  aliases,
  providers,
  failovers,
  totalRequests,
  failoverCount,
}: {
  aliases: FlowAlias[]
  providers: FlowProvider[]
  failovers: FlowFailover[]
  totalRequests: number
  failoverCount: number
}) {
  const rows = Math.max(aliases.length, providers.length, 1)
  const height = Math.max(GEOM.minHeight, GEOM.top * 2 + rows * GEOM.rowGap)
  const width = GEOM.providerX + GEOM.providerW + 40
  const routerY = height / 2

  const served = providers.reduce((n, p) => n + p.requests, 0) || 1
  const providerY = new Map(providers.map((p, i) => [p.id, centreY(i)]))

  return (
    <div className="rf-wrap">
      <div className="rf-graph" style={{ width, height }}>
        <svg
          className="rf-edges"
          width={width}
          height={height}
          viewBox={`0 0 ${width} ${height}`}
          fill="none"
          aria-hidden="true"
        >
          {aliases.map((a, i) => (
            <path
              key={a.name}
              className="rf-edge rf-edge-in"
              d={edge(
                GEOM.aliasX + GEOM.aliasW,
                centreY(i),
                GEOM.routerX,
                routerY,
              )}
            />
          ))}

          {providers
            .filter((p) => p.candidate)
            .map((p) => (
              <path
                key={p.id}
                className="rf-edge rf-edge-out"
                // Share of the window, so a provider taking most of the
                // traffic is visibly the one taking it.
                style={{
                  strokeWidth: Math.max(1, (p.requests / served) * MAX_EDGE),
                }}
                d={edge(
                  GEOM.routerX + GEOM.routerW,
                  routerY,
                  GEOM.providerX,
                  providerY.get(p.id) ?? routerY,
                )}
              />
            ))}

          {failovers.map((f) => {
            const y1 = providerY.get(f.from)
            const y2 = providerY.get(f.to)
            if (y1 === undefined || y2 === undefined) return null
            const x = GEOM.providerX + GEOM.providerW
            return (
              <path
                key={`${f.from}->${f.to}`}
                className="rf-edge rf-edge-fail"
                // Bowed out past the column so a return is visible as a
                // return rather than as a line through the nodes.
                d={`M${x} ${y1} C ${x + 14} ${y1}, ${x + 14} ${y2}, ${x} ${y2}`}
              />
            )
          })}
        </svg>

        {aliases.map((a, i) => (
          <div
            key={a.name}
            className="rf-node rf-alias"
            style={{
              left: GEOM.aliasX,
              top: centreY(i) - GEOM.nodeH / 2,
              width: GEOM.aliasW,
              height: GEOM.nodeH,
            }}
          >
            <span className="mono rf-name">{a.name}</span>
            <span className="micro rf-vol">{a.requests}</span>
          </div>
        ))}

        <div
          className="rf-node rf-router"
          style={{
            left: GEOM.routerX,
            top: routerY - 44,
            width: GEOM.routerW,
            height: 88,
          }}
        >
          <p className="legend-caps rf-router-label">router</p>
          <span className="mono rf-router-total">{totalRequests}</span>
          <span className="micro rf-router-unit">requests</span>
          {failoverCount > 0 && (
            <span className="micro rf-router-fo">{failoverCount} failed over</span>
          )}
        </div>

        {providers.map((p, i) => (
          // display: contents keeps the Link out of the box tree, so the
          // absolutely-positioned node beneath it still measures against
          // .rf-graph rather than against the anchor's own box.
          <Link
            key={p.id}
            to="/providers/$id"
            params={{ id: p.id }}
            style={{ display: "contents" }}
          >
            <div
              className={
                p.candidate
                  ? "rf-node rf-provider"
                  : "rf-node rf-provider rf-provider-idle"
              }
              style={{
                left: GEOM.providerX,
                top: centreY(i) - GEOM.nodeH / 2,
                width: GEOM.providerW,
                height: GEOM.nodeH,
              }}
            >
              <span className="micro rf-prio">{p.priority}</span>
              <span className="mono rf-name">{p.name}</span>
              {p.note && <span className="micro rf-note">{p.note}</span>}
              <span className="rf-right">
                <span className="mono rf-count">{p.requests}</span>
                <span className="micro rf-pct">
                  {p.candidate
                    ? `${Math.round((p.requests / served) * 100)}%`
                    : "not a candidate"}
                </span>
              </span>
            </div>
          </Link>
        ))}
      </div>
    </div>
  )
}

/** Alias volumes come from usage grouped by alias — §6.1: the graph reads its
 *  whole left-hand column from that dimension. */
export function aliasesFromUsage(rows: UsageRow[]): FlowAlias[] {
  const totals = new Map<string, number>()
  for (const row of rows) {
    if (!row.key) continue
    totals.set(row.key, (totals.get(row.key) ?? 0) + row.requests)
  }
  return [...totals.entries()]
    .map(([name, requests]) => ({ name, requests }))
    .sort((a, b) => b.requests - a.requests)
}
