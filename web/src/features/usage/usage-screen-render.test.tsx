import { render, screen } from "@testing-library/react"
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from "@tanstack/react-router"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { RouterAdapterProvider } from "darkraise-ui/router"
import type { RouterAdapter } from "darkraise-ui/router"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { UsageScreen } from "./usage-screen"
import type { RequestPage, UsageResponse } from "../../lib/api-types"

const stubRouterAdapter: RouterAdapter = {
  Link: ({ children }) => <>{children}</>,
  useNavigate: () => () => {},
  usePathname: () => "/usage",
  useBack: () => () => {},
  useInvalidate: () => () => {},
}

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } })

function serve(usage: UsageResponse, requests: RequestPage = { requests: [] }) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("/api/requests") ? json(requests) : json(usage),
    ),
  )
}

async function renderAt(path: string) {
  const rootRoute = createRootRoute({
    component: () => (
      <RouterAdapterProvider value={stubRouterAdapter}>
        <Outlet />
      </RouterAdapterProvider>
    ),
  })
  const usage = createRoute({ getParentRoute: () => rootRoute, path: "/usage", component: UsageScreen })
  const requests = createRoute({ getParentRoute: () => rootRoute, path: "/requests" })
  const connect = createRoute({ getParentRoute: () => rootRoute, path: "/connect" })
  const router = createRouter({
    routeTree: rootRoute.addChildren([usage, requests, connect]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

const span = { first_day: "2026-09-07", last_day: "2026-10-06" }

beforeEach(() => vi.unstubAllGlobals())

describe("an empty usage window", () => {
  it("does not send an operator whose requests are arriving off to connect a client", async () => {
    serve(
      { days: [], priced: false, ...span },
      {
        requests: [
          {
            id: "r1", ts_ms: Date.now(), dialect: "openai", surface: "llm", model: "no-such-model",
            status: "error", source: "proxy", tokens_in: 0, tokens_out: 0, cache_read_tokens: 0,
            cost_micros: null, ttft_ms: null, total_ms: 0, attempts: 0,
          },
        ],
      },
    )
    await renderAt("/usage")
    expect(await screen.findByText(/no request in this window reached a provider/i)).toBeInTheDocument()
    expect(screen.queryByText(/get a client connected/i)).toBeNull()
  })

  it("offers to connect a client when the log is empty too", async () => {
    serve({ days: [], priced: false, ...span })
    await renderAt("/usage")
    expect(await screen.findByText(/usage appears here as requests are served/i)).toBeInTheDocument()
    expect(screen.queryByText(/once a day/i)).toBeNull()
  })
})

describe("a usage window with traffic", () => {
  it("says cost is unknown rather than drawing a $0 line when nothing was priced", async () => {
    serve({
      days: [
        { day: "2026-10-06", key: "mock-fast", requests: 3, attempts: 3, tokens_in: 1, tokens_out: 1, cost_micros: null },
      ],
      priced: false,
      group_by: "model",
      ...span,
    })
    await renderAt("/usage?dimension=model")
    expect(await screen.findByText(/no priced model served traffic/i)).toBeInTheDocument()
  })

  it("names both toggle groups", async () => {
    serve({ days: [], priced: false, ...span })
    await renderAt("/usage")
    expect(await screen.findByRole("radiogroup", { name: "Group by" })).toBeInTheDocument()
    expect(screen.getByRole("radiogroup", { name: "Range" })).toBeInTheDocument()
  })
})
