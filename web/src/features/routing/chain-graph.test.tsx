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
})
