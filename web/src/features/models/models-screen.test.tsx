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
import { ModelsScreen } from "./models-screen"
import type { CatalogResponse } from "../../lib/api-types"

// PageHeader calls useRouterAdapter unconditionally, and this screen never
// navigates through it, so a stub satisfying the interface is enough — the
// same shortcut settings-screen.test.tsx takes.
const stubRouterAdapter: RouterAdapter = {
  Link: ({ children }) => <>{children}</>,
  useNavigate: () => () => {},
  usePathname: () => "/models",
  useBack: () => () => {},
  useInvalidate: () => () => {},
}

/**
 * useSearchFilters reads the URL through TanStack Router's own hooks, not
 * darkraise-ui's adapter, so the screen needs a real router in context — a
 * stub cannot answer useRouterState. A standalone route tree keeps this from
 * pulling in the app's full router, which would import every screen it
 * mounts.
 */
async function renderAt(initialUrl: string) {
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
    component: ModelsScreen,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: [initialUrl] }),
  })
  await router.load()
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

const catalog = (): CatalogResponse => ({
  models: [
    {
      model: "gpt-5",
      providers: ["openai"],
      surfaces: ["chat"],
      context_window: 128000,
      max_output_tokens: 4096,
      tools: true,
      vision: false,
      reasoning: true,
      inferred: false,
      state: "live",
      pricing: null,
      merge_source: "models_dev",
    },
  ],
  aliases: [],
})

function mockCatalog() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify(catalog()), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  )
}

beforeEach(() => vi.unstubAllGlobals())

describe("the models empty state", () => {
  it("says nothing matches the filter, not that no sweep has run", async () => {
    // The catalog is populated and a real model exists — a search that
    // matches nothing is a search problem, not a discovery problem. Telling
    // the operator to go add a provider and probe it would be false.
    mockCatalog()
    await renderAt("/?model=does-not-exist")

    await waitFor(() =>
      expect(screen.getByText(/no models match these filters/i)).toBeInTheDocument(),
    )
    expect(screen.queryByText(/discovery fills this catalogue/i)).not.toBeInTheDocument()
  })

  it("still teaches discovery when the catalog itself is empty", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(JSON.stringify({ models: [], aliases: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    )
    await renderAt("/")

    await waitFor(() =>
      expect(screen.getByText(/discovery fills this catalogue/i)).toBeInTheDocument(),
    )
    expect(screen.queryByText(/no models match these filters/i)).not.toBeInTheDocument()
  })
})

describe("the models table", () => {
  it("labels its facets and filters in the reader's words", async () => {
    // A facet is named after its column header, and a header rendered as a
    // sort button has no string to take — so four of five facets were
    // labelled with their accessor keys: surface_list, merge_source.
    mockCatalog()
    await renderAt("/")
    for (const name of ["Surfaces", "State", "Band", "Source"]) {
      expect(await screen.findByRole("button", { name: new RegExp(name) })).toBeInTheDocument()
    }
    expect(screen.getByPlaceholderText("Model")).toBeInTheDocument()
    expect(screen.getByPlaceholderText("Provider")).toBeInTheDocument()
  })
})
