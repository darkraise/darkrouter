import { render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "./app"

// The authenticated shell is the first thing that renders darkraise-ui's
// SidebarLayout, and SidebarItem reaches for the router adapter. Everything
// else here is only enough to get past the auth gate.
beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const body = url.includes("/api/auth/status")
        ? { authenticated: true, csrf_token: "test-token" }
          : url.includes("/api/overview")
          ? {
              // The whole shape the handler emits. A partial mock here hid a
              // screen that threw on a missing collection, and the shell went
              // with it.
              providers: [],
              requests_per_min: 0,
              error_rate: 0,
              window_sec: 60,
              today_spend: { micros: 0, priced: true },
              latency: { p50_ms: 0, p95_ms: 0 },
              series: [],
              failovers: [],
              failover_edges: [],
            }
          : url.includes("/api/usage")
            ? []
            : {}
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
})

describe("the authenticated shell", () => {
  it("renders its navigation instead of throwing for want of a router adapter", async () => {
    // darkraise-ui's SidebarItem calls useRouterAdapter(), which throws rather
    // than degrading when no RouterAdapterProvider is above it. src/lib/router
    // exports an adapter built on TanStack Router; nothing used to mount it, so
    // the whole dashboard went blank the moment a login succeeded.
    render(<App />)

    expect(await screen.findByRole("link", { name: /Requests/i })).toBeInTheDocument()
    // One item from each of §5's three groups, so a rail that lost a group
    // fails here rather than at a reader wondering where Routing went.
    expect(await screen.findByRole("link", { name: /Models/i })).toBeInTheDocument()
    expect(await screen.findByRole("link", { name: /Connect/i })).toBeInTheDocument()
  })
})
