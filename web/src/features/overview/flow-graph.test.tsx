import { render } from "@testing-library/react"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import type { ReactElement } from "react"
import { describe, it, expect } from "vitest"
import { FlowGraph, aliasesFromUsage } from "./flow-graph"
import type { FlowProvider } from "./flow-graph"

const provider = (over: Partial<FlowProvider> & { id: string }): FlowProvider => ({
  name: over.id,
  requests: 0,
  priority: 1,
  candidate: true,
  state: "healthy",
  ...over,
})

/** FlowGraph now links each provider node to its detail page, which needs a
 *  router in context. A standalone route tree keeps this from pulling in the
 *  app's real router, which would import every screen it renders. The first
 *  render is a pending match until the router resolves, so this awaits
 *  `router.load()` before handing back the container. */
async function renderFlowGraph(ui: ReactElement) {
  const rootRoute = createRootRoute()
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => ui,
  })
  const providerRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/providers/$id",
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute, providerRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  await router.load()
  return render(<RouterProvider router={router} />)
}

describe("the routing flow graph", () => {
  it("draws no edge for a provider that is not a candidate", async () => {
    // §6.1: the fastest way to see that a disabled or unconfigured provider is
    // not quietly absorbing work. An edge at zero width would still read as a
    // connection.
    const { container } = await renderFlowGraph(
      <FlowGraph
        aliases={[{ name: "fast", requests: 10 }]}
        providers={[
          provider({ id: "groq", requests: 8 }),
          provider({ id: "cold", requests: 0, candidate: false }),
        ]}
        failovers={[]}
        totalRequests={10}
        failoverCount={0}
      />,
    )
    expect(container.querySelectorAll(".rf-edge-out")).toHaveLength(1)
    expect(container.querySelectorAll(".rf-provider-idle")).toHaveLength(1)
  })

  it("scales edge thickness by share", async () => {
    const { container } = await renderFlowGraph(
      <FlowGraph
        aliases={[]}
        providers={[
          provider({ id: "a", requests: 75 }),
          provider({ id: "b", requests: 25 }),
        ]}
        failovers={[]}
        totalRequests={100}
        failoverCount={0}
      />,
    )
    const widths = [...container.querySelectorAll(".rf-edge-out")].map((e) =>
      parseFloat((e as SVGElement).style.strokeWidth),
    )
    expect(widths[0]).toBeGreaterThan(widths[1] as number)
    expect(widths[0]).toBeCloseTo(7.5, 1)
  })

  it("draws a dashed return only between providers it can place", async () => {
    const { container } = await renderFlowGraph(
      <FlowGraph
        aliases={[]}
        providers={[provider({ id: "a", requests: 5 }), provider({ id: "b", requests: 5 })]}
        failovers={[
          { from: "a", to: "b", count: 2 },
          // A provider deleted since the window: nothing to draw between.
          { from: "a", to: "gone", count: 1 },
        ]}
        totalRequests={10}
        failoverCount={3}
      />,
    )
    expect(container.querySelectorAll(".rf-edge-fail")).toHaveLength(1)
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
