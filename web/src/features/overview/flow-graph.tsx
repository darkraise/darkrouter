import { useEffect, useMemo, useRef, type RefObject } from "react"
import { useTheme } from "darkraise-ui/theme"
import { Link } from "@tanstack/react-router"
import {
  Background,
  BackgroundVariant,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  Handle,
  Position,
  ReactFlow,
  useReactFlow,
  type Edge,
  type EdgeProps,
  type Node,
  type NodeProps,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import "./flow-graph.css"
import type { ProviderTile, UsageRow } from "../../lib/api-types"

/** How much wider the columns have to be at each step of the font-size axis.
 *
 *  The node text is `--text-sm`, which the axis moves 12 → 18px, so geometry
 *  fixed in pixels truncates every alias at extra-large and lets the router's
 *  own label fall out of its box. These are the ratios of each step's
 *  `--text-sm` to the medium step's 14px. */
const TEXT_SCALE: Record<string, number> = {
  small: 12 / 14,
  medium: 1,
  large: 16 / 14,
  "extra-large": 18 / 14,
}

/** Column geometry at the medium step, scaled by TEXT_SCALE. Rows are laid out
 *  rather than force-directed: the provider column is priority order, and an
 *  ordering the router does not use would be a lie however pretty it looked. */
const GEOM = {
  aliasX: 0,
  routerX: 260,
  providerX: 470,
  aliasW: 196,
  routerW: 136,
  providerW: 424,
  rowGap: 68,
  top: 16,
  minHeight: 360,
  maxHeight: 620,
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
  credentials: number
  cooling: number
  needsReauth: boolean
  state: ProviderTile["state"]
}

/** Traffic that arrived at `to` because `from` refused it. */
export type FlowFailover = { from: string; to: string; count: number }

type AliasData = FlowAlias & { width: number } & Record<string, unknown>
type RouterData = { total: number; failoverCount: number; width: number } & Record<string, unknown>
type ProviderData = FlowProvider & { share: number; width: number } & Record<string, unknown>

function rowY(index: number): number {
  return GEOM.top + index * GEOM.rowGap
}

/** What a provider row says about itself when it is not simply serving.
 *
 *  The credential count is the denominator: "1 cooling" alone cannot say
 *  whether two working keys remain or none do. */
export function providerNote(p: FlowProvider): string {
  const creds = `${p.credentials} ${p.credentials === 1 ? "credential" : "credentials"}`
  if (p.state === "disabled") return "disabled"
  if (p.credentials === 0) return "no credentials"
  if (p.needsReauth) return `${creds} · needs reconnection`
  if (p.cooling > 0) return `${creds} · ${p.cooling} cooling`
  return creds
}

/**
 * The graph as data, separate from the canvas that draws it.
 *
 * Exported because every rule worth testing — who gets an edge, how thick, and
 * which returns can be placed — lives here rather than in the renderer.
 */
export function buildGraph(
  aliases: FlowAlias[],
  providers: FlowProvider[],
  failovers: FlowFailover[],
  failoverCount: number,
  scale = 1,
): { nodes: Node[]; edges: Edge[] } {
  const served = providers.reduce((n, p) => n + p.requests, 0)
  const at = (v: number) => Math.round(v * scale)
  const row = (i: number) => at(rowY(i))
  const routerY = at(rowY(Math.max(aliases.length, providers.length, 1) / 2) - 30)

  const nodes: Node[] = [
    ...aliases.map((a, i) => ({
      id: `alias:${a.name}`,
      type: "alias",
      position: { x: GEOM.aliasX, y: row(i) },
      data: { ...a, width: at(GEOM.aliasW) } as AliasData,
      draggable: false,
    })),
    {
      id: "router",
      type: "router",
      position: { x: at(GEOM.routerX), y: routerY },
      // The router total is the same sum the provider column is a breakdown
      // of, so the parts add up to the whole a reader can see beside them.
      data: { total: served, failoverCount, width: at(GEOM.routerW) } as RouterData,
      draggable: false,
    },
    ...providers.map((p, i) => ({
      id: `provider:${p.id}`,
      type: "provider",
      position: { x: at(GEOM.providerX), y: row(i) },
      data: {
        ...p,
        share: served > 0 ? p.requests / served : 0,
        width: at(GEOM.providerW),
      } as ProviderData,
      draggable: false,
    })),
  ]

  const edges: Edge[] = [
    ...aliases.map((a) => ({
      id: `in:${a.name}`,
      source: `alias:${a.name}`,
      target: "router",
      className: "rf-edge-in",
      // Inbound is plumbing, not a decision: the thinnest structural stroke.
      style: { strokeWidth: 1 },
    })),
    ...providers
      .filter((p) => p.candidate)
      .map((p) => ({
        id: `out:${p.id}`,
        source: "router",
        target: `provider:${p.id}`,
        className: "rf-edge-out",
        style: {
          strokeWidth: Math.max(1, (served > 0 ? p.requests / served : 0) * MAX_EDGE),
        },
      })),
    ...failovers
      // A provider deleted since the window has no row to draw between.
      .filter((f) => providers.some((p) => p.id === f.from) && providers.some((p) => p.id === f.to))
      .map((f) => ({
        id: `fo:${f.from}->${f.to}`,
        source: `provider:${f.from}`,
        target: `provider:${f.to}`,
        sourceHandle: "fo-out",
        targetHandle: "fo-in",
        type: "failover" as const,
        className: "rf-edge-fail",
        // The magnitude is the whole point of a return: "some traffic moved"
        // is not something an operator can act on.
        label: `${f.count}`,
        ariaLabel: `${f.count} requests failed over from ${f.from} to ${f.to}`,
      })),
  ]

  return { nodes, edges }
}

/**
 * A return: traffic that arrived at one provider because another refused it.
 *
 * Both ends leave the right-hand side of the column and the curve bows out
 * past it, so a return reads as a return rather than as a line drawn through
 * the rows between. The bow grows with the distance travelled, which keeps
 * two returns over the same rows from landing on top of each other.
 */
function FailoverEdge({
  sourceX,
  sourceY,
  targetX,
  targetY,
  label,
  markerEnd,
}: EdgeProps) {
  const bow = Math.min(96, 28 + Math.abs(targetY - sourceY) * 0.2)
  const path = `M${sourceX} ${sourceY} C ${sourceX + bow} ${sourceY}, ${targetX + bow} ${targetY}, ${targetX} ${targetY}`
  const labelX = Math.max(sourceX, targetX) + bow * 0.72
  const labelY = (sourceY + targetY) / 2
  return (
    <>
      <BaseEdge path={path} markerEnd={markerEnd} />
      <EdgeLabelRenderer>
        <div
          className="rf-fo-label nodrag nopan"
          style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
        >
          {label}
        </div>
      </EdgeLabelRenderer>
    </>
  )
}

function AliasNode({ data }: NodeProps) {
  const d = data as AliasData
  return (
    <div className="rf-node rf-alias" style={{ width: d.width }}>
      <span className="rf-name">{d.name}</span>
      <span className="rf-vol">{d.requests.toLocaleString()}</span>
      <Handle type="source" position={Position.Right} className="rf-handle" />
    </div>
  )
}

function RouterNode({ data }: NodeProps) {
  const d = data as RouterData
  return (
    <div className="rf-node rf-router" style={{ width: d.width }}>
      <Handle type="target" position={Position.Left} className="rf-handle" />
      <p className="rf-router-label">router</p>
      <span className="rf-router-total">{d.total.toLocaleString()}</span>
      <span className="rf-router-unit">requests</span>
      {d.failoverCount > 0 && (
        <span className="rf-router-fo">{d.failoverCount} failed over</span>
      )}
      <Handle type="source" position={Position.Right} className="rf-handle" />
    </div>
  )
}

function ProviderNode({ data }: NodeProps) {
  const p = data as ProviderData
  return (
    <div className={p.candidate ? "rf-node rf-provider" : "rf-node rf-provider rf-provider-idle"}>
      <Handle type="target" position={Position.Left} className="rf-handle" />
      <Link
        to="/providers/$id"
        params={{ id: p.id }}
        className="rf-provider-link"
        style={{ width: p.width }}
      >
        <span className="rf-prio">{p.priority}</span>
        <span className="rf-pip" data-state={p.state} aria-hidden="true" />
        <span className="rf-name">{p.name}</span>
        <span className="rf-note">{providerNote(p)}</span>
        <span className="rf-right">
          {/* Keyed on the volume rather than on candidacy: a provider taken
              out of the path still served whatever the window recorded, and
              "no traffic" over a row with requests behind it is false. */}
          {p.requests > 0 ? (
            <>
              <span className="rf-count">{p.requests.toLocaleString()}</span>
              <span className="rf-pct">{(p.share * 100).toFixed(1)}%</span>
            </>
          ) : (
            <span className="rf-out">no traffic</span>
          )}
        </span>
      </Link>
      <Handle type="source" position={Position.Right} id="fo-out" className="rf-handle" />
      <Handle type="target" position={Position.Right} id="fo-in" className="rf-handle" />
    </div>
  )
}

const nodeTypes = { alias: AliasNode, router: RouterNode, provider: ProviderNode }
const edgeTypes = { failover: FailoverEdge }

/** Refits when the canvas changes size. The `fitView` prop fits once, on
 *  mount, so without this the graph keeps a width it no longer has — a
 *  narrowed window leaves the provider column off the right-hand edge. */
function FitOnResize({ target }: { target: RefObject<HTMLDivElement | null> }) {
  const { fitView } = useReactFlow()
  useEffect(() => {
    const el = target.current
    if (!el) return
    let frame = 0
    const observer = new ResizeObserver(() => {
      cancelAnimationFrame(frame)
      frame = requestAnimationFrame(() => void fitView())
    })
    observer.observe(el)
    return () => {
      cancelAnimationFrame(frame)
      observer.disconnect()
    }
  }, [fitView, target])
  return null
}

export function FlowGraph({
  aliases,
  providers,
  failovers,
  failoverCount,
}: {
  aliases: FlowAlias[]
  providers: FlowProvider[]
  failovers: FlowFailover[]
  failoverCount: number
}) {
  const { fontSize } = useTheme()
  const scale = TEXT_SCALE[fontSize] ?? 1
  const { nodes, edges } = useMemo(
    () => buildGraph(aliases, providers, failovers, failoverCount, scale),
    [aliases, providers, failovers, failoverCount, scale],
  )
  const rows = Math.max(aliases.length, providers.length, 1)
  const height = Math.min(
    GEOM.maxHeight * scale,
    Math.max(GEOM.minHeight, rows * GEOM.rowGap * scale + 48),
  )
  const wrap = useRef<HTMLDivElement>(null)

  return (
    <div className="rf-wrap" style={{ height }} ref={wrap}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        fitView
        // Asymmetric: the returns bow out past the right-hand column, and a
        // uniform padding that made room for them would leave the same gap on
        // the left where nothing is drawn.
        fitViewOptions={{
          padding: { top: "4%", right: "14%", bottom: "4%", left: "2%" },
          minZoom: 0.4,
          maxZoom: 1,
        }}
        minZoom={0.4}
        maxZoom={1.5}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        // Scroll belongs to the page. A graph that eats the wheel traps an
        // operator halfway down the screen they were scrolling.
        zoomOnScroll={false}
        panOnScroll={false}
        preventScrolling={false}
        aria-label="Routing flow: aliases, the router, and providers in priority order"
      >
        <FitOnResize target={wrap} />
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} className="rf-bg" />
        <Controls showInteractive={false} position="bottom-right" />
      </ReactFlow>
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
