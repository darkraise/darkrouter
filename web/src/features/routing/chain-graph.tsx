import { useEffect, useMemo, useState } from "react"
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
import { Ladder, type LadderRow, type PredictiveMark } from "../ladder/ladder"

/**
 * Wide enough for a typical `provider/model` id on one line. A longer one
 * wraps inside the node rather than being cut off: the target is the one fact
 * a node exists to show, and real ids such as
 * `openrouter/meta-llama/llama-3.3-70b-instruct` never fit any fixed width.
 * Row heights leave room for a title wrapped over three lines at the largest
 * step of the font-size axis.
 */
const GEOM = {
  x: 0,
  gap: 276,
  nodeW: 240,
  y: 16,
  /** Where the skipped targets sit, under the run rather than in it. */
  skipY: 168,
  height: 184,
  heightWithSkips: 336,
  /** Left inset of the starting viewport, so the request is never clipped. */
  inset: 16,
} as const

type ChainNodeData = {
  title: string
  note: string
  rank: number | null
  kind: "origin" | "candidate" | "skip"
} & Record<string, unknown>

/**
 * The preview as a left-to-right run, with what was passed over beneath it.
 *
 * The row order is the router's own candidate order and is never re-sorted:
 * the endpoint hands back the same ordered list a real request would produce,
 * and a graph that rearranged it would misreport failover order however much
 * prettier it looked.
 *
 * Skipped targets are not part of that order. Drawn at the end of the run
 * with an edge into them, a cooling target read as the next fallback --
 * "if mock-fast fails, mock-error is tried" -- when the router never tries it.
 * So they sit on a row of their own, unranked, with nothing leading in.
 */
export function buildChainGraph(
  request: string,
  rows: LadderRow<PredictiveMark>[],
): { nodes: Node[]; edges: Edge[] } {
  const candidates = rows.filter((r) => !r.terminated)
  const skipped = rows.filter((r) => r.terminated)

  const origin: Node = {
    id: "origin",
    type: "chain",
    position: { x: GEOM.x, y: GEOM.y },
    data: {
      title: request || "request",
      note: candidates.length === 0 ? "nothing to try" : "would be tried in this order",
      rank: null,
      kind: "origin",
    } satisfies ChainNodeData,
    draggable: false,
  }
  const run: Node[] = candidates.map((row, i) => ({
    id: `row:${row.rank}:${row.target}`,
    type: "chain",
    position: { x: GEOM.x + GEOM.gap * (i + 1), y: GEOM.y },
    data: {
      title: row.target,
      // The inferred note and the credential count, as the ladder shows them.
      note: [row.reasonCode, row.reasonProse].filter(Boolean).join(" · ") || "candidate",
      rank: row.rank,
      kind: "candidate",
    } satisfies ChainNodeData,
    draggable: false,
  }))
  const skips: Node[] = skipped.map((row, i) => ({
    id: `row:${row.rank}:${row.target}`,
    type: "chain",
    // From the first candidate's column: the origin's column stays the
    // request's alone.
    position: { x: GEOM.x + GEOM.gap * (i + 1), y: GEOM.skipY },
    data: {
      title: row.target,
      // The reason and the credential count: "skipped" alone repeats what the
      // dimmed node already says.
      note: `skipped — ${[row.reasonCode, row.reasonProse].filter(Boolean).join(" · ") || "not tried"}`,
      rank: null,
      kind: "skip",
    } satisfies ChainNodeData,
    draggable: false,
  }))

  const sequence = [origin, ...run]
  const edges: Edge[] = sequence.slice(0, -1).map((from, i) => {
    const to = sequence[i + 1]
    return {
      id: `${from.id}->${to?.id ?? ""}`,
      source: from.id,
      target: to?.id ?? "",
      // Dashed throughout: every step here is conditional. Nothing has been
      // sent, so no edge may be drawn as traffic that flowed.
      style: { strokeDasharray: "4 3" },
    }
  })

  return { nodes: [...sequence, ...skips], edges }
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

/** Narrower than the request and one candidate need, side by side. */
const NARROW_BELOW = GEOM.inset + GEOM.gap + GEOM.nodeW

/** Whether `el` is narrower than the graph can usefully draw in. Read from the
 *  element rather than the viewport, since the card's width is what the graph
 *  gets. False until measured, and wherever ResizeObserver is missing. */
function useNarrow(el: HTMLElement | null): boolean {
  const [narrow, setNarrow] = useState(false)
  useEffect(() => {
    if (!el || typeof ResizeObserver === "undefined") return
    const ro = new ResizeObserver(([entry]) => {
      if (entry) setNarrow(entry.contentRect.width < NARROW_BELOW)
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [el])
  return narrow
}

export function ChainGraph({
  request,
  rows,
}: {
  request: string
  rows: LadderRow<PredictiveMark>[]
}) {
  const { nodes, edges } = useMemo(() => buildChainGraph(request, rows), [request, rows])
  const hasSkips = rows.some((r) => r.terminated)
  const [box, setBox] = useState<HTMLDivElement | null>(null)
  const narrow = useNarrow(box)

  // Too narrow to show the request and its first candidate side by side, the
  // graph is one node and an edge running off the edge. The ladder says the
  // same thing and is built for a phone, so it stands in.
  if (narrow) {
    return (
      <div ref={setBox}>
        <p className="mb-2 text-sm text-[hsl(var(--legend))]">
          Shown as a ladder: the graph needs a wider screen.
        </p>
        <Ladder mode="predictive" rows={rows} />
      </div>
    )
  }

  return (
    <div
      ref={setBox}
      className="cg-wrap"
      style={{ height: hasSkips ? GEOM.heightWithSkips : GEOM.height }}
    >
      {/* Drawn at its own size and never scaled. fitView shrank a run wider
          than the card to half size -- 14px text at 7px on a phone, and out
          of step with the font-size axis everywhere -- while still clipping
          both ends. At a fixed zoom the run starts at the left edge and a
          longer one is panned to, by drag or sideways scroll. */}
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={NODE_TYPES}
        defaultViewport={{ x: GEOM.inset, y: 0, zoom: 1 }}
        minZoom={1}
        maxZoom={1}
        proOptions={{ hideAttribution: true }}
        nodesDraggable={false}
        nodesConnectable={false}
        elementsSelectable={false}
        panOnScroll
        zoomOnScroll={false}
        zoomOnPinch={false}
        zoomOnDoubleClick={false}
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
      </ReactFlow>
    </div>
  )
}
