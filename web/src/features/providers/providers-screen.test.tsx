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
import { ProvidersScreen } from "./providers-screen"
import type { Preset, Provider } from "../../lib/api-types"

const stubRouterAdapter: RouterAdapter = {
  Link: ({ children }) => <>{children}</>,
  useNavigate: () => () => {},
  usePathname: () => "/providers",
  useBack: () => () => {},
  useInvalidate: () => () => {},
}

/** The screen keeps its filters in the URL, so it needs a real router rather
 *  than a stubbed adapter — the same harness models-screen.test.tsx uses. */
async function renderScreen(
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  const rootRoute = createRootRoute({
    component: () => (
      <RouterAdapterProvider value={stubRouterAdapter}>
        <Outlet />
      </RouterAdapterProvider>
    ),
  })
  const index = createRoute({
    getParentRoute: () => rootRoute,
    path: "/providers",
    component: ProvidersScreen,
  })
  const detail = createRoute({
    getParentRoute: () => rootRoute,
    path: "/providers/$id",
    component: () => null,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([index, detail]),
    history: createMemoryHistory({ initialEntries: ["/providers"] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

const presets: Preset[] = [
  {
    id: "groq", name: "Groq", kind: "openaicompat", base_url: "https://api.groq.com/openai/v1",
    surfaces: ["llm"], auth_kind: "bearer", website: "", free_tier: true,
  },
  {
    id: "cerebras", name: "Cerebras", kind: "openaicompat", base_url: "https://api.cerebras.ai/v1",
    surfaces: ["llm"], auth_kind: "bearer", website: "", free_tier: false,
  },
]

const groq: Provider = {
  id: "groq", name: "Groq", preset: "groq", kind: "openaicompat",
  base_url: "https://api.groq.com/openai/v1", priority: 10, enabled: true,
  auth_style: "bearer", free_models_only: false,
  credentials: [
    { id: "k1", label: "k1", masked: "sk-…", enabled: true, cooling: false, kind: "static" },
  ],
}

function stub(providers: Provider[]) {
  const json = (body: unknown) =>
    new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    const path = String(url)
    if (init?.method === "POST") return json({ ok: true, probe: "models", latency_ms: 10 })
    if (path === "/api/providers") return json({ providers })
    if (path === "/api/presets") return json({ presets })
    if (path.startsWith("/api/usage")) return json({ days: [] })
    if (path === "/api/health/discovery") {
      return json({
        providers: [{
          provider_id: "groq", total: 40, live: 30, stale: 10,
          removed_upstream: 0, max_missing_streak: 6, filtered_out: 0,
        }],
      })
    }
    return json([])
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

beforeEach(() => {
  vi.unstubAllGlobals()
  localStorage.clear()
})

describe("the providers list", () => {
  it("opens a provider through its name, and keeps the row a row", async () => {
    // A <tr role="button"> takes the row out of table semantics: a screen
    // reader loses the column headers and every cell's text becomes the
    // button's name. The name is the link; the row is a row.
    stub([groq])
    await renderScreen()

    const link = await screen.findByRole("link", { name: "Groq" })
    expect(link).toHaveAttribute("href", "/providers/groq")
    expect(document.querySelector('tr[role="button"]')).toBeNull()
    expect(link.closest("tr")).not.toHaveAttribute("tabindex")
  })

  it("keeps the actions beside the row for a configured provider", async () => {
    stub([groq])
    await renderScreen()

    const row = (await screen.findByRole("link", { name: "Groq" })).closest("tr")
    if (!row) throw new Error("expected the name inside a table row")
    expect(within(row).getByRole("button", { name: "Probe" })).toBeInTheDocument()
    expect(within(row).getByRole("button", { name: "Discover" })).toBeInTheDocument()
    // An unconfigured row offers the one thing that would make it routable.
    const other = screen.getByRole("link", { name: "Cerebras" }).closest("tr")
    if (!other) throw new Error("expected the name inside a table row")
    expect(within(other).getByRole("button", { name: /add credentials/i })).toBeInTheDocument()
  })

  it("refreshes the discovery reading after queueing a sweep", async () => {
    stub([groq])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidated = vi.spyOn(client, "invalidateQueries")
    await renderScreen(client)

    await userEvent.click(await screen.findByRole("button", { name: "Discover" }))

    await waitFor(() => {
      const keys = invalidated.mock.calls.map(([f]) => JSON.stringify(f?.queryKey))
      expect(keys).toContain(JSON.stringify(["health", "discovery"]))
    })
  })

  it("puts the whole discovery line within reach of the truncated cell", async () => {
    // The cell is eleven rems wide and the line is longer than that as soon
    // as a provider has anything to report. A title only shows on hover with
    // a mouse; the tooltip is reachable by keyboard too.
    stub([groq])
    await renderScreen()

    const trigger = await screen.findByText(/30 of 40 live/)
    await userEvent.hover(trigger)
    const tip = await screen.findByRole("tooltip")
    expect(tip).toHaveTextContent(/missing for 6 sweeps/)
  })
})

describe("adding a local runtime", () => {
  it("offers its own action, because a local runtime has no key to paste", async () => {
    stub([groq])
    await renderScreen()

    await userEvent.click(
      await screen.findByRole("button", { name: /add local runtime/i }),
    )

    expect(
      await screen.findByRole("heading", { name: /add a local runtime/i }),
    ).toBeInTheDocument()
  })
})
