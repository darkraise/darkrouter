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

function mount(onDone = vi.fn(), preset?: Preset) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <AddLocalDialog preset={preset} open onOpenChange={() => {}} onDone={onDone} />
    </QueryClientProvider>,
  )
  return onDone
}

const ollama = presets[1]

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

  it("prefills the address the chosen runtime documents", async () => {
    stub()
    mount()
    await choose("LM Studio")
    expect(await screen.findByLabelText("Base URL")).toHaveValue("http://localhost:1234/v1")
  })

  it("asks for the address and nothing that restates it", async () => {
    // Host and port were how the URL got built before the URL itself was
    // editable. Keeping both would be two ways to say one thing, and a rule
    // about which of them wins.
    stub()
    mount()
    await choose("Ollama")
    expect(await screen.findByLabelText("Base URL")).toBeInTheDocument()
    expect(screen.queryByLabelText("Host")).toBeNull()
    expect(screen.queryByLabelText("Port")).toBeNull()
  })

  it("offers the address that reaches out of the container, since that default cannot", async () => {
    // Every local preset ships a localhost URL, which from inside the
    // container is the container. The default is the documented address, so
    // the containerised case needs somewhere to be said.
    stub()
    mount()
    const user = await choose("Ollama")
    await user.click(await screen.findByRole("button", { name: /host\.docker\.internal/ }))
    expect(screen.getByLabelText("Base URL")).toHaveValue(
      "http://host.docker.internal:11434/v1",
    )
  })

  it("refuses to submit while the address is blank", async () => {
    stub()
    mount()
    const user = await choose("Ollama")
    await user.clear(await screen.findByLabelText("Base URL"))
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
      base_url: "http://localhost:11434/v1",
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
        auth_style: "none", free_models_only: false, allow_unsanctioned_free: false, credentials: [],
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
    // The dialog outlives any one visit. An address left over from a failed
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
    await user.clear(await screen.findByLabelText("Base URL"))
    await user.type(screen.getByLabelText("Base URL"), "http://gw:11999/v1")
    expect(screen.getByLabelText("Base URL")).toHaveValue("http://gw:11999/v1")

    const render_ = (open: boolean) =>
      view.rerender(
        <QueryClientProvider client={client}>
          <AddLocalDialog open={open} onOpenChange={() => {}} onDone={vi.fn()} />
        </QueryClientProvider>,
      )
    render_(false)
    render_(true)

    expect(screen.queryByLabelText("Base URL")).not.toBeInTheDocument()
    await choose("Ollama")
    expect(await screen.findByLabelText("Base URL")).toHaveValue("http://localhost:11434/v1")
  })
})

describe("the base URL box", () => {
  it("carries an address no host-and-port pair could have expressed", async () => {
    const seen = stub()
    const onDone = mount()
    const user = await choose("Ollama")
    const url = await screen.findByLabelText("Base URL")
    await user.clear(url)
    await user.type(url, "https://box.lan:8443/openai/v1")

    await user.click(screen.getByRole("button", { name: /^Add/ }))
    await waitFor(() => expect(onDone).toHaveBeenCalledWith("ollama"))
    const create = seen.find((c) => c.path === "/api/providers" && c.method === "POST")
    expect(create?.body).toMatchObject({ base_url: "https://box.lan:8443/openai/v1" })
  })

  it("refuses to submit what is not an http URL", async () => {
    stub()
    mount()
    const user = await choose("Ollama")
    const url = await screen.findByLabelText("Base URL")
    await user.clear(url)
    await user.type(url, "box.lan:8443")
    expect(screen.getByRole("button", { name: /^Add/ })).toBeDisabled()
  })
})

describe("a runtime started behind a token", () => {
  it("stores the key and replaces the preset's keyless auth style", async () => {
    const seen = stub()
    const onDone = mount()
    const user = await choose("Ollama")
    await user.type(await screen.findByLabelText("API key"), "sk-local-1")
    await user.click(screen.getByRole("button", { name: /^Add/ }))

    await waitFor(() => expect(onDone).toHaveBeenCalledWith("ollama"))
    const create = seen.find((c) => c.path === "/api/providers" && c.method === "POST")
    expect(create?.body).toMatchObject({ auth_style: "bearer" })
    const key = seen.find((c) => c.path === "/api/providers/ollama/keys")
    expect(key?.body).toEqual({ label: "default", secret: "sk-local-1" })
  })

  it("asks nothing about how to send it, because there is one answer", async () => {
    // Every local server that reads a token reads Authorization: Bearer. The
    // x-api-key and api-key styles are hosted-API conventions, so a picker
    // here would be a choice with three wrong options.
    stub()
    mount()
    const user = await choose("Ollama")
    await user.type(await screen.findByLabelText("API key"), "sk-local-1")
    expect(screen.queryByLabelText("Sent as")).toBeNull()
  })

  it("stores the key before the probe, so the probe is not refused by it", async () => {
    const seen = stub()
    mount()
    const user = await choose("Ollama")
    await user.type(await screen.findByLabelText("API key"), "sk-local-1")
    await user.click(screen.getByRole("button", { name: /^Add/ }))

    await waitFor(() => expect(seen.some((c) => c.path.endsWith("/test"))).toBe(true))
    const keyAt = seen.findIndex((c) => c.path === "/api/providers/ollama/keys")
    const probeAt = seen.findIndex((c) => c.path.endsWith("/test"))
    expect(keyAt).toBeGreaterThan(-1)
    expect(keyAt).toBeLessThan(probeAt)
  })

  it("adds nothing about auth when the box is left empty", async () => {
    const seen = stub()
    mount()
    const user = await choose("Ollama")
    await user.click(screen.getByRole("button", { name: /^Add/ }))

    await waitFor(() => expect(seen.some((c) => c.path.endsWith("/test"))).toBe(true))
    const create = seen.find((c) => c.path === "/api/providers" && c.method === "POST")
    expect(create?.body).not.toHaveProperty("auth_style")
    expect(seen.some((c) => c.path === "/api/providers/ollama/keys")).toBe(false)
  })
})

describe("opening on a runtime a row already named", () => {
  it("skips the picker and starts from that runtime's address", async () => {
    stub()
    mount(vi.fn(), ollama)
    expect(await screen.findByLabelText("Base URL")).toHaveValue("http://localhost:11434/v1")
    expect(screen.queryByRole("option", { name: /LM Studio/ })).not.toBeInTheDocument()
  })
})
