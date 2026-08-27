import { describe, it, expect } from "vitest"
import { aliasesFromUsage, buildGraph, providerNote } from "./flow-graph"
import type { FlowProvider } from "./flow-graph"

const provider = (over: Partial<FlowProvider> & { id: string }): FlowProvider => ({
  name: over.id,
  requests: 0,
  priority: 1,
  candidate: true,
  credentials: 1,
  cooling: 0,
  needsReauth: false,
  state: "healthy",
  ...over,
})

const widthOf = (edges: ReturnType<typeof buildGraph>["edges"], id: string) =>
  edges.find((e) => e.id === id)?.style?.strokeWidth as number

describe("the routing flow graph", () => {
  it("draws no edge for a provider that is not a candidate", () => {
    // §6.1: the fastest way to see that a disabled or unconfigured provider is
    // not quietly absorbing work. An edge at zero width would still read as a
    // connection.
    const { edges } = buildGraph(
      [{ name: "fast", requests: 10 }],
      [
        provider({ id: "groq", requests: 8 }),
        provider({ id: "cold", requests: 0, candidate: false }),
      ],
      [],
      0,
    )
    const out = edges.filter((e) => e.className === "rf-edge-out")
    expect(out.map((e) => e.id)).toEqual(["out:groq"])
  })

  it("scales edge thickness by share", () => {
    const { edges } = buildGraph(
      [],
      [provider({ id: "a", requests: 75 }), provider({ id: "b", requests: 25 })],
      [],
      0,
    )
    expect(widthOf(edges, "out:a")).toBeCloseTo(7.5, 1)
    expect(widthOf(edges, "out:b")).toBeCloseTo(2.5, 1)
  })

  it("draws a dashed return only between providers it can place", () => {
    const { edges } = buildGraph(
      [],
      [provider({ id: "a", requests: 5 }), provider({ id: "b", requests: 5 })],
      [
        { from: "a", to: "b", count: 2 },
        // A provider deleted since the window: nothing to draw between.
        { from: "a", to: "gone", count: 1 },
      ],
      3,
    )
    const fail = edges.filter((e) => e.className === "rf-edge-fail")
    expect(fail).toHaveLength(1)
    // The magnitude is the whole point of a return: "some traffic moved" is
    // not something an operator can act on.
    expect(fail[0]?.label).toBe("2")
  })

  it("totals the router from the same sum the provider column breaks down", () => {
    const { nodes } = buildGraph(
      [],
      [provider({ id: "a", requests: 75 }), provider({ id: "b", requests: 25 })],
      [],
      4,
    )
    const router = nodes.find((n) => n.id === "router")
    expect(router?.data).toMatchObject({ total: 100, failoverCount: 4 })
  })

  it("gives every provider a share of the window", () => {
    const { nodes } = buildGraph(
      [],
      [provider({ id: "a", requests: 3 }), provider({ id: "b", requests: 1 })],
      [],
      0,
    )
    expect(nodes.find((n) => n.id === "provider:a")?.data.share).toBe(0.75)
  })

  it("survives a window in which nothing was routed", () => {
    // A gateway that has served nothing yet must not divide by zero and
    // render NaN% on every row.
    const { nodes, edges } = buildGraph([], [provider({ id: "a" })], [], 0)
    expect(nodes.find((n) => n.id === "provider:a")?.data.share).toBe(0)
    expect(widthOf(edges, "out:a")).toBe(1)
  })

  it("reads its left column from usage grouped by alias", () => {
    const rows = aliasesFromUsage([
      { day: "2026-08-26", key: "fast", requests: 3, attempts: 3, tokens_in: 0, tokens_out: 0, cost_micros: null },
      { day: "2026-08-25", key: "fast", requests: 4, attempts: 4, tokens_in: 0, tokens_out: 0, cost_micros: null },
      { day: "2026-08-26", key: "smart", requests: 9, attempts: 9, tokens_in: 0, tokens_out: 0, cost_micros: null },
    ])
    // Summed across days and ordered by volume, so the busiest alias leads.
    expect(rows).toEqual([
      { name: "smart", requests: 9 },
      { name: "fast", requests: 7 },
    ])
  })
})

describe("what a provider row says about itself", () => {
  it("gives a cooling count its denominator", () => {
    // "1 cooling" alone cannot say whether two working keys remain or none.
    expect(providerNote(provider({ id: "a", credentials: 3, cooling: 1 }))).toBe(
      "3 credentials · 1 cooling",
    )
  })

  it("names the one state only the operator can fix", () => {
    expect(
      providerNote(provider({ id: "a", credentials: 1, needsReauth: true })),
    ).toBe("1 credential · needs reconnection")
  })

  it("distinguishes switched off from never configured", () => {
    expect(providerNote(provider({ id: "a", state: "disabled" }))).toBe("disabled")
    expect(
      providerNote(provider({ id: "a", state: "unconfigured", credentials: 0 })),
    ).toBe("no credentials")
  })
})
