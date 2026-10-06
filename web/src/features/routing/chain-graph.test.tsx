import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { ChainGraph, buildChainGraph } from "./chain-graph"
import type { LadderRow, PredictiveMark } from "../ladder/ladder"

const row = (over: Partial<LadderRow<PredictiveMark>> & { rank: number; target: string }):
  LadderRow<PredictiveMark> => ({ mark: "skipped", ...over })

describe("buildChainGraph", () => {
  it("draws the request, then every candidate in the router's own order", () => {
    // The endpoint hands back the order a real request would produce. A graph
    // that rearranged it would misreport failover order.
    const { nodes } = buildChainGraph("sonnet", [
      row({ rank: 1, target: "groq/a" }),
      row({ rank: 2, target: "nebius/b" }),
    ])
    expect(nodes.map((n) => n.id)).toEqual(["origin", "row:1:groq/a", "row:2:nebius/b"])
  })

  it("lays the run out left to right, one step per node", () => {
    const { nodes } = buildChainGraph("sonnet", [row({ rank: 1, target: "groq/a" })])
    const xs = nodes.map((n) => n.position.x)
    expect(xs[1]).toBeGreaterThan(xs[0] ?? 0)
  })

  it("connects each node to the next and nothing else", () => {
    const { edges } = buildChainGraph("sonnet", [
      row({ rank: 1, target: "groq/a" }),
      row({ rank: 2, target: "nebius/b" }),
    ])
    expect(edges).toHaveLength(2)
    expect(edges.map((e) => [e.source, e.target])).toEqual([
      ["origin", "row:1:groq/a"],
      ["row:1:groq/a", "row:2:nebius/b"],
    ])
  })

  it("marks a skipped candidate apart from one that would be tried", () => {
    const { nodes } = buildChainGraph("sonnet", [
      row({ rank: 1, target: "groq/a" }),
      row({ rank: 2, target: "nebius/b: cooling", terminated: true }),
    ])
    expect(nodes.map((n) => n.data.kind)).toEqual(["origin", "candidate", "skip"])
  })

  it("says why a rung was skipped, and how many credentials it stands for", () => {
    // The ladder shows the skip reason; the graph said only "skipped", which
    // is the one word the dimmed node already conveys.
    const { nodes } = buildChainGraph("sonnet", [
      row({ rank: 1, target: "groq/a", reasonProse: "× 2 credentials" }),
      row({ rank: 2, target: "nebius/b", reasonCode: "cooling", terminated: true }),
    ])
    expect(nodes.map((n) => n.data.note)).toEqual([
      "would be tried in this order",
      "× 2 credentials",
      "skipped — cooling",
    ])
  })

  it("draws a skipped target off the run, unranked and with nothing leading in", () => {
    // Joined to the end of the run, a cooling target read as the next
    // fallback after the last candidate -- which the router never tries.
    const { nodes, edges } = buildChainGraph("errfb", [
      row({ rank: 1, target: "lmstudio/mock-fast" }),
      row({ rank: 2, target: "lmstudio/mock-error", reasonCode: "cooling", terminated: true, unranked: true }),
    ])
    const skip = nodes.find((n) => n.data.kind === "skip")
    const fast = nodes.find((n) => n.data.title === "lmstudio/mock-fast")
    expect(skip?.data.rank).toBeNull()
    expect(edges.some((e) => e.target === skip?.id || e.source === skip?.id)).toBe(false)
    expect(skip?.position.y).toBeGreaterThan(fast?.position.y ?? 0)
    expect(edges.map((e) => [e.source, e.target])).toEqual([["origin", "row:1:lmstudio/mock-fast"]])
  })

  it("says there is nothing to try when every target was skipped", () => {
    const { nodes, edges } = buildChainGraph("errfb", [
      row({ rank: 1, target: "lmstudio/mock-error", reasonCode: "cooling", terminated: true }),
    ])
    expect(nodes[0]?.data.note).toBe("nothing to try")
    expect(edges).toHaveLength(0)
  })

  it("still draws the request when nothing routed", () => {
    // The skips are the only account of why nothing routed, and an empty
    // canvas would say less than the error does.
    const { nodes, edges } = buildChainGraph("sonnet", [])
    expect(nodes).toHaveLength(1)
    expect(edges).toHaveLength(0)
    expect(nodes[0]?.data.note).toBe("nothing to try")
  })
})

describe("the chain graph on a canvas", () => {
  it("renders the request and its candidates", () => {
    // buildChainGraph is tested on its own; this is the part that would fail
    // only in a browser — a custom node type the canvas cannot resolve draws
    // nothing and throws nowhere.
    render(
      <ChainGraph
        request="sonnet"
        rows={[
          { rank: 1, mark: "skipped", target: "groq/a" },
          { rank: 2, mark: "cooling", target: "nebius/b", terminated: true },
        ]}
      />,
    )
    expect(screen.getByText("sonnet")).toBeInTheDocument()
    expect(screen.getByText("groq/a")).toBeInTheDocument()
    expect(screen.getByText("nebius/b")).toBeInTheDocument()
  })

  it("draws the run at its own size rather than shrinking it to fit", () => {
    // fitView scaled a run wider than the card to half size: 14px text drawn
    // at 7px on a phone, with both ends still clipped.
    const { container } = render(
      <ChainGraph request="sonnet" rows={[{ rank: 1, mark: "skipped", target: "groq/a" }]} />,
    )
    const viewport = container.querySelector<HTMLElement>(".react-flow__viewport")
    expect(viewport?.style.transform).toMatch(/scale\(1\)/)
  })
})
