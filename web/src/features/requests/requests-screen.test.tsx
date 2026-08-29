import { render, screen, waitFor } from "@testing-library/react"
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
import { RequestsScreen } from "./requests-screen"
import type { RequestPage, RequestRow } from "../../lib/api-types"

// PageHeader calls useRouterAdapter unconditionally, and this screen never
// navigates through it, so a stub satisfying the interface is enough — the
// same shortcut models-screen.test.tsx takes.
const stubRouterAdapter: RouterAdapter = {
  Link: ({ children }) => <>{children}</>,
  useNavigate: () => () => {},
  usePathname: () => "/requests",
  useBack: () => () => {},
  useInvalidate: () => () => {},
}

/**
 * useSearchFilters reads the URL through TanStack Router's own hooks, so the
 * screen needs a real router in context, the same way models-screen.test.tsx
 * does. The QueryClient is returned too, so a test can force the poll's
 * refetch directly rather than waiting on a real 3s timer.
 */
async function renderScreen() {
  const rootRoute = createRootRoute({
    component: () => (
      <RouterAdapterProvider value={stubRouterAdapter}>
        <Outlet />
      </RouterAdapterProvider>
    ),
  })
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: RequestsScreen,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  await router.load()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const utils = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { ...utils, client }
}

const row = (over: Partial<RequestRow> & { id: string }): RequestRow => ({
  source: "proxy",
  ts_ms: Date.now(),
  dialect: "openai",
  surface: "llm",
  model: "m",
  status: "success",
  tokens_in: 0,
  tokens_out: 0,
  cache_read_tokens: 0,
  cost_micros: null,
  ttft_ms: null,
  total_ms: null,
  attempts: 1,
  ...over,
})

function mockRequests(pages: RequestPage[]) {
  let call = 0
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => {
      const page = pages[Math.min(call, pages.length - 1)]
      call += 1
      return new Response(JSON.stringify(page), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
}

beforeEach(() => vi.unstubAllGlobals())

describe("a requests screen opened while empty", () => {
  it("shows the first row once the poll finds one, without a filter change or reload", async () => {
    // The anchor mechanism freezes the first page into `held`. Freezing an
    // empty first load forever — rather than re-freezing while it stays
    // empty — would leave the very first request that ever arrives both
    // undisplayed and uncounted.
    mockRequests([{ requests: [] }, { requests: [row({ id: "r1", model: "distinctive-model" })] }])
    const { client } = await renderScreen()

    // Waited for explicitly, rather than inferred from the empty state
    // alone: the legend already shows on the very first render, before the
    // first fetch has actually settled, and refetching too early would race
    // the two responses instead of exercising "held is frozen empty, then a
    // row arrives".
    await waitFor(() => {
      const query = client.getQueryCache().findAll({ queryKey: ["requests"] })[0]
      expect(query?.state.status).toBe("success")
      expect(query?.state.data).toEqual({ requests: [] })
    })
    expect(screen.getByText(/point a client at the proxy/i)).toBeInTheDocument()

    await client.refetchQueries({ queryKey: ["requests"] })

    await waitFor(() =>
      expect(screen.queryByText(/point a client at the proxy/i)).not.toBeInTheDocument(),
    )
    expect(screen.getByText("distinctive-model")).toBeInTheDocument()
  })

  it("keeps showing the empty state while the poll keeps finding nothing", async () => {
    mockRequests([{ requests: [] }])
    await renderScreen()

    await waitFor(() =>
      expect(screen.getByText(/point a client at the proxy/i)).toBeInTheDocument(),
    )
    expect(screen.getByText(/point a client at the proxy/i)).toBeInTheDocument()
  })
})

describe("what a filter offers", () => {
  it("puts the page's own values first, then everything else known", async () => {
    // A menu built from the loaded rows can only offer what is already on
    // screen, so filtering to a provider whose traffic is older than the
    // first page used to be impossible.
    const { mergedOptions } = await import("./requests-screen")
    expect(mergedOptions(["groq"], ["cerebras", "groq", "nebius"])).toEqual([
      "groq",
      "cerebras",
      "nebius",
    ])
  })

  it("offers each value once, and never the empty one", async () => {
    const { mergedOptions } = await import("./requests-screen")
    expect(mergedOptions(["groq", "groq"], ["", "groq"])).toEqual(["groq", "groq"])
    expect(mergedOptions([], ["", "a"])).toEqual(["a"])
  })
})
