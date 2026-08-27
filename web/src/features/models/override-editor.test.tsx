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
    mount(<OverrideEditor provider="groq" model="m" onClose={() => {}} />)
    await waitFor(() =>
      expect(screen.getByLabelText(/context window/i)).toHaveValue(64000),
    )
  })

  it("treats a 404 as no override rather than as an error", async () => {
    // A model with no override is the normal case, and an error banner over
    // the normal case teaches the operator to ignore banners.
    vi.stubGlobal("fetch", vi.fn(async () => new Response("", { status: 404 })))
    mount(<OverrideEditor provider="groq" model="m" onClose={() => {}} />)
    await waitFor(() =>
      expect(screen.getByLabelText(/context window/i)).toHaveValue(null),
    )
    expect(screen.queryByRole("alert")).not.toBeInTheDocument()
  })

  it("sends a PUT carrying only the fields that were set", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } }),
    )
    vi.stubGlobal("fetch", fetchMock)
    mount(<OverrideEditor provider="groq" model="m" onClose={() => {}} />)
    await userEvent.type(await screen.findByLabelText(/context window/i), "32000")
    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    await waitFor(() => {
      const put = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "PUT")
      expect(put).toBeDefined()
      const body = JSON.parse((put?.[1] as RequestInit).body as string)
      expect(body).toEqual({ context_window: 32000 })
    })
  })
})
