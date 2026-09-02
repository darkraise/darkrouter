import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { AddLocalDialog } from "./add-local-dialog"
import type { Preset, Provider } from "../../lib/api-types"

const presets: Preset[] = [
  {
    id: "groq", name: "Groq", kind: "openaicompat", base_url: "https://api.groq.com/openai/v1",
    surfaces: ["llm"], auth_kind: "bearer", website: "", free_tier: true,
  },
  {
    id: "ollama", name: "Ollama", kind: "openaicompat", base_url: "http://localhost:11434/v1",
    surfaces: ["llm"], auth_kind: "none", website: "", free_tier: false,
  },
  {
    id: "lmstudio", name: "LM Studio", kind: "openaicompat", base_url: "http://localhost:1234/v1",
    surfaces: ["llm"], auth_kind: "none", website: "", free_tier: false,
  },
]

type Script = { probe?: unknown; providers?: Provider[] }

function stub(script: Script = {}) {
  const seen: { method: string; path: string; body?: unknown }[] = []
  const json = (body: unknown, status = 200) =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    })
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    const path = String(url)
    const method = init?.method ?? "GET"
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    seen.push({ method, path, body })
    if (path === "/api/presets") return json({ presets })
    if (path === "/api/providers" && method === "GET") {
      return json({ providers: script.providers ?? [] })
    }
    if (path.endsWith("/test")) {
      return json(script.probe ?? { ok: true, probe: "listing", latency_ms: 8, model_count: 14 })
    }
    if (method === "POST") return json({ id: "ollama" }, 201)
    if (method === "DELETE") return new Response(null, { status: 204 })
    return json([])
  })
  vi.stubGlobal("fetch", fetchMock)
  return seen
}

function mount(onDone = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AddLocalDialog open onOpenChange={() => {}} onDone={onDone} />
    </QueryClientProvider>,
  )
  return onDone
}

/** The provider icon renders an <svg><title> carrying the same name, so the
 *  row is addressed by its option role rather than by its text. */
async function choose(name: string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole("option", { name: new RegExp(name) }))
  return user
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

describe("the local runtime picker", () => {
  it("offers the runtimes that live on this machine and not the hosted ones", async () => {
    stub()
    mount()
    expect(await screen.findByRole("option", { name: /Ollama/ })).toBeInTheDocument()
    expect(screen.getByRole("option", { name: /LM Studio/ })).toBeInTheDocument()
    expect(screen.queryByRole("option", { name: /Groq/ })).not.toBeInTheDocument()
  })

  it("prefills the port the chosen runtime listens on", async () => {
    stub()
    mount()
    await choose("LM Studio")
    expect(await screen.findByLabelText("Port")).toHaveValue("1234")
  })

  it("defaults the host to the address that reaches out of the container", async () => {
    // Every local preset ships a localhost URL, which from inside the
    // container is the container. Defaulting to localhost would make the
    // common case the broken one.
    stub()
    mount()
    await choose("Ollama")
    expect(await screen.findByLabelText("Host")).toHaveValue("host.docker.internal")
  })

  it("shows the endpoint the gateway will actually call", async () => {
    stub()
    mount()
    await choose("Ollama")
    expect(
      await screen.findByText("http://host.docker.internal:11434/v1"),
    ).toBeInTheDocument()
  })

  it("refuses to submit while the host is blank", async () => {
    stub()
    mount()
    const user = await choose("Ollama")
    await user.clear(await screen.findByLabelText("Host"))
    expect(screen.getByRole("button", { name: /^Add/ })).toBeDisabled()
  })
})

describe("adding a local runtime", () => {
  it("creates it enabled and hands the caller its id", async () => {
    const seen = stub()
    const onDone = mount()
    const user = await choose("Ollama")
    await user.click(screen.getByRole("button", { name: /^Add/ }))

    await waitFor(() => expect(onDone).toHaveBeenCalledWith("ollama"))
    const create = seen.find((c) => c.path === "/api/providers" && c.method === "POST")
    expect(create?.body).toEqual({
      id: "ollama",
      preset: "ollama",
      base_url: "http://host.docker.internal:11434/v1",
      enabled: true,
    })
  })

  it("reports why an unreachable endpoint was refused, and adds nothing", async () => {
    const seen = stub({
      probe: { ok: false, probe: "listing", latency_ms: 2, error: "connection refused" },
    })
    const onDone = mount()
    const user = await choose("Ollama")
    await user.click(screen.getByRole("button", { name: /^Add/ }))

    expect(await screen.findByText(/connection refused/)).toBeInTheDocument()
    expect(onDone).not.toHaveBeenCalled()
    expect(seen.some((c) => c.method === "DELETE")).toBe(true)
  })
})

describe("testing before adding", () => {
  it("reports what the probe found and leaves no provider behind", async () => {
    const seen = stub()
    mount()
    const user = await choose("Ollama")
    await user.click(screen.getByRole("button", { name: /Test connection/ }))

    expect(await screen.findByText(/14 models/)).toBeInTheDocument()
    expect(seen.some((c) => c.method === "DELETE")).toBe(true)
  })
})

describe("a runtime that is already configured", () => {
  it("says so rather than offering to add it twice", async () => {
    stub({
      providers: [{
        id: "ollama", name: "Ollama", preset: "ollama", kind: "openaicompat",
        base_url: "http://gw:11434/v1", priority: 10, enabled: true,
        auth_style: "none", free_models_only: false, credentials: [],
      }],
    })
    mount()
    await choose("Ollama")
    expect(await screen.findByText(/already added/i)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /^Add/ })).toBeDisabled()
  })
})

describe("reopening the dialog", () => {
  it("starts from scratch rather than from the last attempt", async () => {
    // The dialog outlives any one visit. A port left over from a failed
    // attempt would silently be reused by the next one, which reads as the
    // add failing for no reason.
    stub()
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const view = render(
      <QueryClientProvider client={client}>
        <AddLocalDialog open onOpenChange={() => {}} onDone={vi.fn()} />
      </QueryClientProvider>,
    )
    const user = await choose("Ollama")
    await user.clear(await screen.findByLabelText("Port"))
    await user.type(screen.getByLabelText("Port"), "11999")
    expect(screen.getByLabelText("Port")).toHaveValue("11999")

    const render_ = (open: boolean) =>
      view.rerender(
        <QueryClientProvider client={client}>
          <AddLocalDialog open={open} onOpenChange={() => {}} onDone={vi.fn()} />
        </QueryClientProvider>,
      )
    render_(false)
    render_(true)

    expect(screen.queryByLabelText("Port")).not.toBeInTheDocument()
    await choose("Ollama")
    expect(await screen.findByLabelText("Port")).toHaveValue("11434")
  })
})
