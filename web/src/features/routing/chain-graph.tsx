import { useMemo } from "react"
import {
  Background,
  BackgroundVariant,
  Handle,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react"
import "@xyflow/react/dist/style.css"
import "./chain-graph.css"
import type { LadderRow, PredictiveMark } from "../ladder/ladder"

const GEOM = {
  x: 0,
  gap: 236,
  nodeW: 200,
  y: 40,
  height: 200,
} as const

type ChainNodeData = {
  title: string
  note: string
  rank: number | null
  kind: "origin" | "candidate" | "skip"
} & Record<string, unknown>

/**
 * The preview as a left-to-right run.
 *
 * The row order is the router's own candidate order and is never re-sorted:
 * the endpoint hands back the same ordered list a real request would produce,
 * and a graph that rearranged it would misreport failover order however much
 * prettier it looked.
 */
export function buildChainGraph(
  request: string,
  rows: LadderRow<PredictiveMark>[],
): { nodes: Node[]; edges: Edge[] } {
  const nodes: Node[] = [
    {
      id: "origin",
      type: "chain",
      position: { x: GEOM.x, y: GEOM.y },
      data: {
        title: request || "request",
        note: rows.length === 0 ? "nothing to try" : "would be tried in this order",
        rank: null,
        kind: "origin",
      } satisfies ChainNodeData,
      draggable: false,
    },
    ...rows.map((row, i) => ({
      id: `row:${row.rank}:${row.target}`,
      type: "chain",
      position: { x: GEOM.x + GEOM.gap * (i + 1), y: GEOM.y },
      data: {
        title: row.target,
        note: row.reasonProse ?? (row.terminated ? "skipped" : "candidate"),
        rank: row.rank,
        kind: row.terminated ? "skip" : "candidate",
      } satisfies ChainNodeData,
      draggable: false,
    })),
  ]

  const edges: Edge[] = nodes.slice(0, -1).map((from, i) => {
    const to = nodes[i + 1]
    return {
      id: `${from.id}->${to?.id ?? ""}`,
      source: from.id,
      target: to?.id ?? "",
      // Dashed throughout: every step here is conditional. Nothing has been
      // sent, so no edge may be drawn as traffic that flowed.
      style: { strokeDasharray: "4 3" },
    }
  })

  return { nodes, edges }
}

function ChainNode({ data }: NodeProps) {
  const d = data as ChainNodeData
  return (
    <div className="cg-node" data-kind={d.kind} style={{ width: GEOM.nodeW }}>
      <Handle type="target" position={Position.Left} />
      <span className="cg-title">
        {d.rank !== null && <span className="cg-rank">{d.rank}. </span>}
        {d.title}
      </span>
      <span className="cg-note">{d.note}</span>
      <Handle type="source" position={Position.Right} />
    </div>
  )
}

const NODE_TYPES = { chain: ChainNode }

export function ChainGraph({
  request,
  rows,
}: {
  request: string
  rows: LadderRow<PredictiveMark>[]
}) {
  const { nodes, edges } = useMemo(() => buildChainGraph(request, rows), [request, rows])
  return (
    <div className="cg-wrap" style={{ height: GEOM.height }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        fitView
        fitViewOptions={{ padding: 0.15 }}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnScroll
        zoomOnScroll={false}
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
      </ReactFlow>
    </div>
  )
}
