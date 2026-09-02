import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
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

function trace(id: string) {
  return {
    id, ts_ms: 0, dialect: "openai", surface: "llm", model: "m", provider: "groq",
    status: "success", tokens_in: 1, tokens_out: 2, cache_read_tokens: 0,
    cost_micros: 0, ttft_ms: 10, total_ms: 20, attempts: [], candidates: [], skips: [],
  }
}

/** Serves the list and the trace by path, so a screen test can open a trace
 *  the list does not hold and settle deferred pages in an order it controls. */
function mockByPath(handler: (url: string) => Promise<Response> | Response) {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => handler(String(input))))
}

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })

async function renderAt(path: string) {
  const rootRoute = createRootRoute({
    component: () => (
      <RouterAdapterProvider value={stubRouterAdapter}>
        <Outlet />
      </RouterAdapterProvider>
    ),
  })
  const list = createRoute({ getParentRoute: () => rootRoute, path: "/requests", component: RequestsScreen })
  const one = createRoute({ getParentRoute: () => rootRoute, path: "/requests/$id", component: RequestsScreen })
  const router = createRouter({
    routeTree: rootRoute.addChildren([list, one]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, client }
}

describe("a trace deep link", () => {
  it("opens the drawer for a request the loaded page does not hold", async () => {
    // A reloaded or shared /requests/<id> has to land on the trace itself,
    // not on a list that happens not to contain it any more.
    mockByPath((url) =>
      url.includes("/api/requests/01DEEP") ? json(trace("01DEEP")) : json({ requests: [] }),
    )
    await renderAt("/requests/01DEEP")

    const dialog = await screen.findByRole("dialog", { name: "01DEEP" })
    expect(await within(dialog).findByText("groq")).toBeInTheDocument()
  })

  it("says when there is no request with that id", async () => {
    mockByPath((url) =>
      url.includes("/api/requests/01GONE") ? json({ error: "not found" }, 404) : json({ requests: [] }),
    )
    await renderAt("/requests/01GONE")

    expect(await screen.findByText(/no request with that id/i)).toBeInTheDocument()
  })

  it("moves the URL to the trace when one is opened, and back when it closes", async () => {
    mockByPath((url) =>
      url.includes("/api/requests/r1")
        ? json(trace("r1"))
        : json({ requests: [row({ id: "r1", model: "distinctive-model" })] }),
    )
    const { router } = await renderAt("/requests?provider=groq")

    await userEvent.click(await screen.findByRole("button", { name: "Open" }))
    await waitFor(() => expect(router.state.location.pathname).toBe("/requests/r1"))
    // The filters survive the trip: the drawer is a detail of the list, not
    // a different screen.
    expect(router.state.location.search).toEqual({ provider: "groq" })

    await userEvent.keyboard("{Escape}")
    await waitFor(() => expect(router.state.location.pathname).toBe("/requests"))
    expect(router.state.location.search).toEqual({ provider: "groq" })
  })
})

describe("loading older requests", () => {
  it("sends one request for the next page however often the button is pressed", async () => {
    let release: (() => void) | undefined
    const calls: string[] = []
    mockByPath((url) => {
      calls.push(url)
      if (!url.includes("cursor=")) return json({ requests: [row({ id: "r1" })], next_cursor: "c1" })
      return new Promise<Response>((resolve) => {
        release = () => resolve(json({ requests: [row({ id: "r0" })] }))
      })
    })
    await renderAt("/requests")

    const more = await screen.findByRole("button", { name: /load more/i })
    await userEvent.click(more)
    await userEvent.click(more)
    await userEvent.click(more)

    await waitFor(() => expect(release).toBeDefined())
    expect(calls.filter((u) => u.includes("cursor=c1"))).toHaveLength(1)
    release!()
    await waitFor(() => expect(screen.queryByRole("button", { name: /load more/i })).toBeNull())
  })

  it("reports a page that failed to load, and offers to try again", async () => {
    let fail = true
    mockByPath((url) => {
      if (!url.includes("cursor=")) return json({ requests: [row({ id: "r1" })], next_cursor: "c1" })
      if (fail) return json({ error: "log locked" }, 500)
      return json({ requests: [row({ id: "r0" })] })
    })
    await renderAt("/requests")

    await userEvent.click(await screen.findByRole("button", { name: /load more/i }))
    expect(await screen.findByText(/could not load older requests/i)).toBeInTheDocument()

    fail = false
    await userEvent.click(screen.getByRole("button", { name: /try again/i }))
    await waitFor(() => expect(screen.queryByText(/could not load older requests/i)).toBeNull())
  })

  it("discards a page that was requested under filters since changed", async () => {
    // A slow second page arriving after the operator switched filters would
    // append rows from the old query under the new one.
    let release: (() => void) | undefined
    mockByPath((url) => {
      if (url.includes("cursor=")) {
        return new Promise<Response>((resolve) => {
          release = () => resolve(json({ requests: [row({ id: "stale", model: "stale-model" })] }))
        })
      }
      if (url.includes("provider=nebius")) return json({ requests: [row({ id: "n1", model: "nebius-model" })] })
      return json({ requests: [row({ id: "r1" })], next_cursor: "c1" })
    })
    const { router } = await renderAt("/requests")

    await userEvent.click(await screen.findByRole("button", { name: /load more/i }))
    await waitFor(() => expect(release).toBeDefined())
    await router.navigate({ to: "/requests", search: { provider: "nebius" } })
    await screen.findByText("nebius-model")

    release!()
    // Given a beat to arrive; nothing should happen.
    await new Promise((r) => setTimeout(r, 20))
    expect(screen.queryByText("stale-model")).toBeNull()
  })
})
