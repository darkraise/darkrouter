import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { AddKeylessDialog } from "./add-keyless-dialog"
import type { Preset } from "../../lib/api-types"

const aihorde: Preset = {
  id: "aihorde", name: "AI Horde", kind: "openaicompat",
  base_url: "https://stablehorde.net/api/v2", surfaces: ["llm"],
  auth_kind: "anonymous", website: "", free_tier: true,
}

function stub() {
  const seen: { method: string; path: string; body?: unknown }[] = []
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    const method = init?.method ?? "GET"
    seen.push({
      method,
      path: String(url),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
    })
    return new Response(JSON.stringify({ id: "aihorde" }), {
      status: method === "POST" ? 201 : 200,
      headers: { "Content-Type": "application/json" },
    })
  })
  vi.stubGlobal("fetch", fetchMock)
  return seen
}

function mount(onDone = vi.fn(), preset: Preset | null = aihorde) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const ui = (open: boolean) => (
    <QueryClientProvider client={client}>
      <AddKeylessDialog preset={preset} open={open} onOpenChange={() => {}} onDone={onDone} />
    </QueryClientProvider>
  )
  const view = render(ui(true))
  return { onDone, rerender: (open: boolean) => view.rerender(ui(open)) }
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

describe("adding a keyless provider", () => {
  it("names the provider it is about to add", async () => {
    stub()
    mount()
    expect(await screen.findByRole("button", { name: /Add AI Horde/ })).toBeInTheDocument()
  })

  it("creates the provider from the preset alone", async () => {
    const seen = stub()
    const { onDone } = mount()
    await userEvent.click(screen.getByRole("button", { name: /Add AI Horde/ }))

    await waitFor(() => expect(onDone).toHaveBeenCalledWith("aihorde"))
    expect(seen.find((c) => c.method === "POST")?.body).toEqual({
      id: "aihorde",
      preset: "aihorde",
      free_models_only: false,
    })
  })

  it("carries the import filter, which cannot be chosen afterwards", async () => {
    // The first discovery sweep starts within seconds of this POST, so a
    // choice made later is made too late. That is the whole reason this
    // dialog exists rather than a one-click button on the row.
    const seen = stub()
    mount()
    await userEvent.click(screen.getByLabelText(/import free models only/i))
    await userEvent.click(screen.getByRole("button", { name: /Add AI Horde/ }))

    await waitFor(() =>
      expect(seen.find((c) => c.method === "POST")?.body).toEqual({
        id: "aihorde",
        preset: "aihorde",
        free_models_only: true,
      }),
    )
  })

  it("forgets a choice made on a visit that was abandoned", async () => {
    stub()
    const { rerender } = mount()
    await userEvent.click(screen.getByLabelText(/import free models only/i))
    expect(screen.getByLabelText(/import free models only/i)).toBeChecked()

    rerender(false)
    rerender(true)

    expect(screen.getByLabelText(/import free models only/i)).not.toBeChecked()
  })

  it("renders nothing without a preset to add", () => {
    stub()
    mount(vi.fn(), null)
    expect(screen.queryByRole("button", { name: /^Add / })).not.toBeInTheDocument()
  })
})
