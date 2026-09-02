import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ConnectScreen } from "./connect-screen"

afterEach(() => {
  vi.unstubAllGlobals()
})

function mount(tokens: unknown[]) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => {
      const body = url.includes("/api/proxy-tokens")
        ? { tokens }
        : url.includes("/api/models")
          ? { models: [], aliases: [] }
          : url.includes("/api/config")
            ? { valid: true, warnings: [], fields: {}, blocks: { server: { proxy_listen: ":8080", admin_listen: ":8081" } } }
            : {}
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  // The screen links to /providers, and a Link needs a router above it.
  const router = createRouter({
    routeTree: createRootRoute({ component: ConnectScreen }),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

describe("the connect screen", () => {
  it("labels the token name and points the empty copy at the form", async () => {
    mount([])
    expect(await screen.findByLabelText("Name")).toBeInTheDocument()
    // The form is above the table; the well below it must say so, and the
    // snippets card above the form must not say "above".
    expect(await screen.findByText(/create one in the form above/i)).toBeInTheDocument()
    expect(screen.getByText(/create one under new client token, below/i)).toBeInTheDocument()
  })

  it("lists a token with its dates in the console's one format", async () => {
    mount([
      {
        id: "t1",
        name: "laptop",
        prefix: "dk_abc",
        created_at: "2026-08-01T10:00:00Z",
        last_used_at: null,
      },
    ])
    expect(await screen.findByText("laptop")).toBeInTheDocument()
    expect(screen.getByText("never")).toBeInTheDocument()
    expect(screen.getByText(/last used \(UTC/i)).toBeInTheDocument()
  })
})
