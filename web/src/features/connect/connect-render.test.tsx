import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ConnectScreen, publicOrigin } from "./connect-screen"

afterEach(() => {
  vi.unstubAllGlobals()
})

function mount(tokens: unknown[], values: Record<string, string> = {}, saveError?: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === "/api/config" && init?.method === "PUT") {
        if (saveError) return new Response(JSON.stringify({ error: saveError }), { status: 400 })
        const patch = JSON.parse(init.body as string)
        Object.assign(values, patch.set)
        return new Response(JSON.stringify({ valid: true, restart_required: [] }))
      }
      const body = url.includes("/api/proxy-tokens")
        ? { tokens }
        : url.includes("/api/models")
          ? { models: [], aliases: [] }
          : url.includes("/api/config")
            ? {
                valid: true,
                warnings: [],
                fields: {},
                pending_restart: [],
                values: {
                  "server.proxy_listen": ":18080",
                  "server.admin_listen": ":18081",
                  ...values,
                },
              }
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
  it("saves a published host port and updates URLs and snippets", async () => {
    const user = userEvent.setup()
    mount([], { "server.proxy_listen": ":8080", "server.admin_listen": ":8081" })
    expect(await screen.findByText("http://localhost:8080/v1")).toBeInTheDocument()
    await user.type(await screen.findByLabelText("Public base URL"), "http://prod-host:18080")
    await user.click(screen.getByRole("button", { name: "Save address" }))
    expect(await screen.findByText("http://prod-host:18080/v1")).toBeInTheDocument()
    expect(screen.getByText(/ANTHROPIC_BASE_URL=http:\/\/prod-host:18080/)).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledWith("/api/config", expect.objectContaining({
      method: "PUT",
      body: JSON.stringify({ set: { "server.public_url": "http://prod-host:18080" } }),
    }))
  })

  it("clears the configured address and returns to the estimate", async () => {
    const user = userEvent.setup()
    mount([], { "server.public_url": "https://llm.example.com" })
    await user.clear(await screen.findByLabelText("Public base URL"))
    await user.click(screen.getByRole("button", { name: "Save address" }))
    await waitFor(() => expect(screen.queryByText("https://llm.example.com/v1")).not.toBeInTheDocument())
    expect(screen.getByText(/ANTHROPIC_BASE_URL=http:\/\/localhost:18080/)).toBeInTheDocument()
  })

  it("keeps the draft and existing URLs when saving is rejected", async () => {
    const user = userEvent.setup()
    mount([], { "server.public_url": "https://llm.example.com" }, "Invalid URL")
    const input = await screen.findByLabelText("Public base URL")
    await user.clear(input)
    await user.type(input, "invalid?")
    await user.click(screen.getByRole("button", { name: "Save address" }))
    await waitFor(() => expect(screen.getByRole("button", { name: "Save address" })).toBeEnabled())
    expect(input).toHaveValue("invalid?")
    expect(screen.getByText("https://llm.example.com/v1")).toBeInTheDocument()
  })

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

  it("shows both addresses once a public one is configured", async () => {
    // The point of configuring a domain is not to replace the LAN address:
    // the gateway still answers on both, and the page has to say so.
    mount([], { "server.public_url": "https://llm.example.com" })
    expect(await screen.findByText("https://llm.example.com/v1")).toBeInTheDocument()
    expect(screen.getByText("http://localhost:18080/v1")).toBeInTheDocument()
    expect(screen.getByRole("heading", { name: "Public" })).toBeInTheDocument()
    expect(
      screen.getByRole("heading", { name: "Estimated address" }),
    ).toBeInTheDocument()
  })

  it("shows only the LAN address when no public one is configured", async () => {
    mount([])
    expect(await screen.findByText("http://localhost:18080/v1")).toBeInTheDocument()
    expect(screen.queryByRole("heading", { name: "Public" })).not.toBeInTheDocument()
    expect(
      screen.queryByRole("heading", { name: "Estimated address" }),
    ).not.toBeInTheDocument()
  })

  it("tells the operator the lone LAN address was worked out, not configured", async () => {
    mount([])
    expect(await screen.findByText(/worked out from this page/i)).toBeInTheDocument()
    // Naming the key is the whole point: a caveat that does not say what to
    // do about it leaves the operator debugging their client instead.
    expect(await screen.findByText("server.public_url")).toBeInTheDocument()
  })

  it("writes snippets against the public address by default", async () => {
    mount([], { "server.public_url": "https://llm.example.com" })
    expect(
      await screen.findByText(/ANTHROPIC_BASE_URL=https:\/\/llm\.example\.com/),
    ).toBeInTheDocument()
  })

  it("rewrites the snippets for the LAN when that side is chosen", async () => {
    // A snippet naming the wrong side of the router is the exact failure this
    // screen exists to prevent, so the choice has to reach the snippet text.
    const user = userEvent.setup()
    mount([], { "server.public_url": "https://llm.example.com" })
    await user.click(await screen.findByRole("button", { name: "This network" }))
    expect(
      await screen.findByText(/ANTHROPIC_BASE_URL=http:\/\/localhost:18080/),
    ).toBeInTheDocument()
  })

  it("offers no address toggle when there is only one address", async () => {
    mount([])
    expect(await screen.findByLabelText("Name")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "This network" })).not.toBeInTheDocument()
  })
})

describe("publicOrigin", () => {
  it("reads the public URL from the flat values map", () => {
    // The block tree is gone; a screen still walking it would render the LAN
    // address as though no public URL were set, which is the one state this
    // page exists to distinguish.
    const cfg = {
      valid: true,
      warnings: [],
      values: { "server.public_url": "https://llm.example.test" },
      fields: {},
      pending_restart: [],
    }
    expect(publicOrigin(cfg)).toBe("https://llm.example.test")
  })

  it("is empty when no public URL is stored", () => {
    const cfg = { valid: true, warnings: [], values: {}, fields: {}, pending_restart: [] }
    expect(publicOrigin(cfg)).toBe("")
  })
})
