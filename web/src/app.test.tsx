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
              providers: [],
              requests_per_min: 0,
              error_rate: 0,
              window_sec: 60,
              today_spend: { micros: 0, priced: true },
            }
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
    expect(await screen.findByRole("link", { name: /Catalog/i })).toBeInTheDocument()
  })
})
