import { render, screen, waitFor } from "@testing-library/react"
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
import { ProviderDetail } from "./provider-detail"
import type { Preset, Provider } from "../../lib/api-types"

const stubRouterAdapter: RouterAdapter = {
  Link: ({ children }) => <>{children}</>,
  useNavigate: () => () => {},
  usePathname: () => "/providers",
  useBack: () => () => {},
  useInvalidate: () => () => {},
}

/** The detail page reads its id from the route, so it needs a real router
 *  rather than a stubbed adapter — the same shape models-screen.test.tsx uses. */
async function renderProvider(
  id: string,
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } }),
) {
  const rootRoute = createRootRoute({
    component: () => (
      <RouterAdapterProvider value={stubRouterAdapter}>
        <Outlet />
      </RouterAdapterProvider>
    ),
  })
  const route = createRoute({
    getParentRoute: () => rootRoute,
    path: "/providers/$id",
    component: ProviderDetail,
  })
  const router = createRouter({
    routeTree: rootRoute.addChildren([route]),
    history: createMemoryHistory({ initialEntries: [`/providers/${id}`] }),
  })
  await router.load()
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

const preset: Preset = {
  id: "groq",
  name: "Groq",
  kind: "openaicompat",
  base_url: "https://api.groq.com/openai/v1",
  surfaces: ["llm"],
  auth_kind: "bearer",
  website: "https://groq.com",
  free_tier: true,
}

const cred = {
  id: "k1", label: "k1", masked: "sk-…", enabled: true, cooling: false, kind: "static" as const,
}

const configured: Provider = {
  id: "groq",
  name: "Groq",
  preset: "groq",
  kind: "openaicompat",
  base_url: "https://api.groq.com/openai/v1",
  priority: 10,
  enabled: true,
  auth_style: "bearer",
  free_models_only: false,
  credentials: [],
}

function stub(providers: Provider[], presets: Preset[]) {
  const json = (body: unknown) =>
    new Response(JSON.stringify(body), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    })
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>(async (url) => {
      const path = String(url)
      if (path === "/api/providers") return json({ providers })
      if (path === "/api/presets") return json({ presets })
      if (path.startsWith("/api/models")) return json({ models: [], aliases: [] })
      if (path.startsWith("/api/usage")) return json({ days: [] })
      if (path === "/api/health/discovery") return json({ providers: [] })
      return json([])
    }),
  )
}

beforeEach(() => vi.unstubAllGlobals())

describe("a provider nobody has configured", () => {
  it("renders from the preset rather than reporting a deletion", async () => {
    // The list holds every provider the release supports, so clicking one that
    // has no database row has to land somewhere that explains it.
    stub([], [preset])
    await renderProvider("groq")

    expect(await screen.findByRole("heading", { name: "Groq" })).toBeInTheDocument()
    expect(screen.getByText("unconfigured")).toBeInTheDocument()
    expect(screen.queryByText(/may have been deleted/i)).not.toBeInTheDocument()
  })

  it("shows what the release knows about reaching it", async () => {
    // The facts an operator weighs before spending a key on it.
    stub([], [preset])
    await renderProvider("groq")

    expect(await screen.findByText("https://api.groq.com/openai/v1")).toBeInTheDocument()
    expect(screen.getByText("bearer")).toBeInTheDocument()
    expect(screen.getByText("Free tier")).toBeInTheDocument()
  })

  it("offers the accounts dialog on the provider already chosen", async () => {
    // No picker: the operator named the provider by navigating to it.
    stub([], [preset])
    await renderProvider("groq")

    await userEvent.click(await screen.findByRole("button", { name: /add the first account/i }))
    expect(await screen.findByLabelText(/api key/i)).toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/search providers/i)).not.toBeInTheDocument()
  })

  it("still reports an id that is neither a provider nor a preset", async () => {
    stub([], [preset])
    await renderProvider("no-such-thing")
    expect(await screen.findByText(/may have been deleted/i)).toBeInTheDocument()
  })
})

describe("a configured provider", () => {
  it("renders the full page", async () => {
    stub([configured], [preset])
    await renderProvider("groq")

    expect(await screen.findByRole("heading", { name: "Groq" })).toBeInTheDocument()
    // The parts that only exist once there is a database row.
    expect(screen.getByRole("button", { name: /settings/i })).toBeInTheDocument()
    expect(screen.getByText(/priority 10/)).toBeInTheDocument()
  })
})

describe("waiting for a sweep", () => {
  it("is what a provider with a key and no models is doing", async () => {
    // Discovery needs one of the provider's own keys to ask what it serves,
    // so the key is what makes the wait meaningful.
    const { awaitingModels } = await import("./provider-detail")
    expect(awaitingModels([{ ...configured, credentials: [cred] }], "groq")).toBe(true)
  })

  it("is not something a provider with no key can be doing", async () => {
    const { awaitingModels } = await import("./provider-detail")
    expect(awaitingModels([configured], "groq")).toBe(false)
  })

  it("is not something a disabled provider is doing", async () => {
    // Discovery skips a provider the router will not choose, so polling for
    // its models would be a poll that can never come back with anything.
    const { awaitingModels } = await import("./provider-detail")
    expect(
      awaitingModels([{ ...configured, enabled: false, credentials: [cred] }], "groq"),
    ).toBe(false)
  })

  it("says nothing about a provider that is not there", async () => {
    const { awaitingModels } = await import("./provider-detail")
    expect(awaitingModels([], "groq")).toBe(false)
  })
})

const keylessPreset: Preset = { ...preset, id: "opencode", name: "OpenCode Free", auth_kind: "optional" }

describe("adding a keyless provider", () => {
  it("still asks the one question that applies to it", async () => {
    // No secret to collect does not mean nothing to decide: the import filter
    // is a property of the provider, not of a key, and skipping the accounts
    // dialog was skipping the question along with the form.
    stub([], [keylessPreset])
    await renderProvider("opencode")
    expect(await screen.findByLabelText(/import free models only/i)).toBeInTheDocument()
  })

  it("carries the answer into the provider it creates", async () => {
    // The first sweep starts within seconds, so a filter set afterwards is set
    // too late for it.
    stub([], [keylessPreset])
    await renderProvider("opencode")
    await userEvent.click(await screen.findByLabelText(/import free models only/i))
    await userEvent.click(screen.getByRole("button", { name: /add opencode free/i }))

    await waitFor(() => {
      const create = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find(
        ([u, i]) => String(u) === "/api/providers" && (i as RequestInit)?.method === "POST",
      )
      expect(JSON.parse((create?.[1] as RequestInit).body as string)).toEqual({
        id: "opencode",
        preset: "opencode",
        free_models_only: true,
      })
    })
  })

  it("defaults to importing everything", async () => {
    stub([], [keylessPreset])
    await renderProvider("opencode")
    await userEvent.click(await screen.findByRole("button", { name: /add opencode free/i }))

    await waitFor(() => {
      const create = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find(
        ([u, i]) => String(u) === "/api/providers" && (i as RequestInit)?.method === "POST",
      )
      expect(JSON.parse((create?.[1] as RequestInit).body as string).free_models_only).toBe(false)
    })
  })
})

describe("waiting for a keyless provider's first sweep", () => {
  it("is a wait, even with no credentials at all", async () => {
    // It is swept with no key, so requiring one here left every local runtime
    // and every free gateway on the slow poll while its models landed.
    const { awaitingModels } = await import("./provider-detail")
    const keyless = { ...configured, id: "opencode", auth_style: "optional", credentials: [] }
    expect(awaitingModels([keyless], "opencode")).toBe(true)
  })

  it("is not a wait once it is switched off", async () => {
    const { awaitingModels } = await import("./provider-detail")
    const off = { ...configured, id: "opencode", auth_style: "optional", enabled: false, credentials: [] }
    expect(awaitingModels([off], "opencode")).toBe(false)
  })
})

describe("switching a provider back on", () => {
  it("refreshes the breaker and discovery readings, not only the provider row", async () => {
    // Enabling changes what the router may dispatch to, and the health and
    // discovery panels read from their own endpoints. Left stale, the page
    // said "enabled" beside readings taken while it was off.
    stub([{ ...configured, enabled: false }], [preset])
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const invalidated = vi.spyOn(client, "invalidateQueries")
    await renderProvider("groq", client)

    await userEvent.click(await screen.findByRole("button", { name: "Enable" }))

    await waitFor(() => {
      const keys = invalidated.mock.calls.map(([f]) => JSON.stringify(f?.queryKey))
      expect(keys).toContain(JSON.stringify(["health", "providers"]))
      expect(keys).toContain(JSON.stringify(["health", "discovery"]))
    })
  })
})
