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
import { ModelsScreen } from "./models-screen"
import type { CatalogResponse, Model, Pricing } from "../../lib/api-types"

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
      providers: ["openai", "groq"],
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

const baseModel: Omit<Model, "model" | "pricing"> = {
  providers: ["openai"],
  surfaces: ["chat"],
  context_window: 128000,
  max_output_tokens: 4096,
  tools: false,
  vision: false,
  reasoning: false,
  inferred: false,
  state: "live",
  merge_source: "models_dev",
}

const pricing = (over: Partial<Pricing>): Pricing => ({
  input_micros: 150000,
  output_micros: 600000,
  price_source: "models_dev",
  price_grade: "indexed",
  ...over,
})

/** One row per grade the Band cell treats differently, so a render test can
 *  tell the verified mark, the caution mark and no mark apart. */
function pricedCatalog(): CatalogResponse {
  return {
    models: [
      {
        ...baseModel,
        model: "measured-model",
        pricing: pricing({ price_source: "discovered", price_grade: "measured" }),
      },
      {
        ...baseModel,
        model: "indexed-model",
        pricing: pricing({ price_source: "models_dev", price_grade: "indexed" }),
      },
      {
        ...baseModel,
        model: "guessed-model",
        pricing: pricing({ price_source: "inferred", price_grade: "guessed" }),
      },
    ],
    aliases: [],
  }
}

function mockCatalog(data: CatalogResponse = catalog()) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify(data), {
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

  it("shows the first serving provider in full, and the ladder on request", async () => {
    // The per-row ladder is what pushed Publisher, Surfaces, State and
    // Source off the right edge at 1440. The row now carries one chip and
    // opens the ladder only when asked.
    mockCatalog()
    await renderAt("/")
    const chip = await screen.findByText("openai/gpt-5")
    const row = chip.closest("tr")
    if (!row) throw new Error("expected the chip inside a table row")
    expect(within(row).queryByText("groq/gpt-5")).not.toBeInTheDocument()

    await userEvent.click(within(row).getByRole("button", { name: /1 more/ }))
    expect(within(row).getByText("groq/gpt-5")).toBeInTheDocument()
  })

  it("opens the override editor with every provider the row serves through", async () => {
    mockCatalog()
    await renderAt("/")
    await userEvent.click(await screen.findByRole("button", { name: "Override" }))
    // Two providers, so the editor has to ask which one.
    const sheet = await screen.findByRole("dialog")
    expect(await within(sheet).findByRole("combobox", { name: /provider/i })).toHaveTextContent("openai")
  })
})

describe("the price marker in the Band cell", () => {
  it("verifies a measured price, cautions a guessed one, and marks an indexed one plainly", async () => {
    mockCatalog(pricedCatalog())
    await renderAt("/")

    const verified = await screen.findByTitle("Price quoted by the provider")
    const caution = await screen.findByTitle("No published price; this is an estimate")
    // Only two of the three rows should carry a mark at all — the indexed
    // row is the majority case the spec says to leave unmarked.
    expect(screen.getAllByTitle("Price quoted by the provider")).toHaveLength(1)
    expect(screen.getAllByTitle("No published price; this is an estimate")).toHaveLength(1)

    // The mark rides in the same cell as the price it explains, on one line.
    const verifiedCell = verified.closest("td")
    const cautionCell = caution.closest("td")
    if (!verifiedCell || !cautionCell) throw new Error("expected the marker inside a table cell")
    expect(verifiedCell).toHaveTextContent("$0.1500 / $0.6000")
    expect(cautionCell).toHaveTextContent("$0.1500 / $0.6000")

    // The indexed row's own cell has neither mark.
    const indexedRow = (await screen.findByText("indexed-model")).closest("tr")
    if (!indexedRow) throw new Error("expected the model name inside a table row")
    expect(within(indexedRow).queryByTitle("Price quoted by the provider")).not.toBeInTheDocument()
    expect(within(indexedRow).queryByTitle("No published price; this is an estimate")).not.toBeInTheDocument()
  })
})
