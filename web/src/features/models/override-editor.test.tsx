import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { OverrideEditor } from "./override-editor"

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

beforeEach(() => vi.unstubAllGlobals())

describe("the override editor", () => {
  it("loads the current override and offers it for editing", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      new Response(JSON.stringify({ context_window: 64000, capabilities: { tools: true } }), {
        status: 200, headers: { "Content-Type": "application/json" },
      }),
    ))
    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    await waitFor(() =>
      // A string, not a number: NumberBox renders a text field that parses
      // rather than an <input type="number">, so the DOM value is the text.
      expect(screen.getByLabelText(/context window/i)).toHaveValue("64000"),
    )
  })

  it("treats a 404 as no override rather than as an error", async () => {
    // A model with no override is the normal case, and an error banner over
    // the normal case teaches the operator to ignore banners.
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })))
    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    await waitFor(() =>
      expect(screen.getByLabelText(/context window/i)).toHaveValue(""),
    )
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("resends the untouched loaded fields alongside the edited one", async () => {
    // PUT replaces the whole row, so editing only surfaces must not erase
    // the context window and capabilities the editor already loaded and is
    // still displaying.
    const fetchMock = vi.fn<typeof fetch>(async (_url, init) => {
      if ((init as RequestInit)?.method === "PUT") {
        return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
      }
      return new Response(
        JSON.stringify({
          context_window: 64000,
          capabilities: { tools: true },
          surfaces: ["chat"],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      )
    })
    vi.stubGlobal("fetch", fetchMock)
    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    const surfaces = await screen.findByLabelText(/surfaces/i)
    await waitFor(() => expect(surfaces).toHaveValue("chat"))
    await userEvent.clear(surfaces)
    await userEvent.type(surfaces, "chat, embedding")
    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "PUT")
      expect(put).toBeDefined()
      const body = JSON.parse((put?.[1] as RequestInit).body as string)
      expect(body).toEqual({
        context_window: 64000,
        capabilities: { tools: true, vision: false, reasoning: false },
        surfaces: ["chat", "embedding"],
      })
    })
  })

  it("sends a full patch for a genuinely new override, with nothing to merge over", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (_url, init) => {
      if ((init as RequestInit)?.method === "PUT") {
        return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
      }
      return new Response("", { status: 404 })
    })
    vi.stubGlobal("fetch", fetchMock)
    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    await userEvent.type(await screen.findByLabelText(/context window/i), "32000")
    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "PUT")
      expect(put).toBeDefined()
      const body = JSON.parse((put?.[1] as RequestInit).body as string)
      expect(body).toEqual({
        context_window: 32000,
        capabilities: { tools: false, vision: false, reasoning: false },
        surfaces: [],
      })
    })
  })
})

describe("the override editor's state", () => {
  it("offers a provider picker only when the model is served by more than one", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })))
    const { unmount } = mount(<OverrideEditor providers={["groq", "nebius"]} model="m" onClose={() => {}} />)
    const picker = await screen.findByRole("combobox", { name: /provider/i })
    expect(picker).toHaveTextContent("groq")
    unmount()

    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    await screen.findByLabelText(/context window/i)
    expect(screen.queryByRole("combobox", { name: /provider/i })).not.toBeInTheDocument()
  })

  it("says it is loading, and will not save, until the current override is known", async () => {
    // Save on an editor still loading would PUT a blank row over whatever
    // is there: the replace semantics make "not loaded yet" the same as
    // "empty" on the wire.
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => {})))
    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    expect(await screen.findByText(/loading/i)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /save/i })).toBeDisabled()
  })

  it("will not save over an override it could not read", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      new Response(JSON.stringify({ error: "store unavailable" }), {
        status: 500, headers: { "Content-Type": "application/json" },
      }),
    ))
    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    expect(await screen.findByRole("alert")).toHaveTextContent(/could not load/i)
    expect(screen.getByRole("button", { name: /save/i })).toBeDisabled()
  })

  it("holds Save down while a write is in flight", async () => {
    let settle: (r: Response) => void = () => {}
    const fetchMock = vi.fn<typeof fetch>(async (_url, init) => {
      if ((init as RequestInit)?.method === "PUT") return new Promise<Response>((r) => { settle = r })
      return new Response("", { status: 404 })
    })
    vi.stubGlobal("fetch", fetchMock)
    mount(<OverrideEditor providers={["groq"]} model="m" onClose={() => {}} />)
    await userEvent.type(await screen.findByLabelText(/context window/i), "32000")
    const save = screen.getByRole("button", { name: /save/i })
    await userEvent.click(save)
    await waitFor(() => expect(save).toBeDisabled())
    settle(new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }))
    await waitFor(() => expect(save).toBeEnabled())
  })
})
