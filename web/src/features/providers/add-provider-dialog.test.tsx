import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { AddProviderDialog, createBodyFromPreset, filterPresets } from "./add-provider-dialog"
import type { Preset } from "../../lib/api-types"

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

function stubPresets(presets: Preset[]) {
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    if (url === "/api/presets") {
      return new Response(JSON.stringify({ presets }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    if (url === "/api/providers" && (init as RequestInit)?.method === "POST") {
      return new Response(JSON.stringify({ id: "my-groq" }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      })
    }
    return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

beforeEach(() => vi.unstubAllGlobals())

const preset = (over: Partial<Preset> & { id: string }): Preset => ({
  name: over.id, kind: "openaicompat", base_url: "https://x.example",
  surfaces: ["llm"], auth_kind: "bearer", website: "", free_tier: false,
  ...over,
})

describe("filterPresets", () => {
  it("matches the search against name and id, case-insensitively", () => {
    const out = filterPresets([preset({ id: "groq", name: "Groq" }), preset({ id: "nebius" })], {
      q: "GRO",
    })
    expect(out.map((p) => p.id)).toEqual(["groq"])
  })

  it("filters by declared surface", () => {
    const out = filterPresets(
      [preset({ id: "a", surfaces: ["llm"] }), preset({ id: "b", surfaces: ["embedding"] })],
      { surface: "embedding" },
    )
    expect(out.map((p) => p.id)).toEqual(["b"])
  })

  it("treats free tier as a narrowing switch, not a toggle between two sets", () => {
    // Unset shows everything. A false that hid free-tier providers would make
    // the filter impossible to clear.
    const all = [preset({ id: "a", free_tier: true }), preset({ id: "b" })]
    expect(filterPresets(all, {})).toHaveLength(2)
    expect(filterPresets(all, { freeTier: true }).map((p) => p.id)).toEqual(["a"])
  })

  it("combines every filter", () => {
    const out = filterPresets(
      [
        preset({ id: "groq", surfaces: ["llm"], auth_kind: "bearer", free_tier: true }),
        preset({ id: "grok", surfaces: ["llm"], auth_kind: "bearer" }),
      ],
      { q: "gro", surface: "llm", authKind: "bearer", freeTier: true },
    )
    expect(out.map((p) => p.id)).toEqual(["groq"])
  })
})

describe("createBodyFromPreset", () => {
  it("sends the preset name and lets the server supply the rest", () => {
    // The preset already carries kind, base_url and auth_style. Echoing them
    // back would freeze this provider against a later preset correction.
    expect(createBodyFromPreset(preset({ id: "groq" }), { id: "my-groq" })).toEqual({
      id: "my-groq",
      preset: "groq",
    })
  })
})

describe("the dialog", () => {
  // `mount` wraps in a QueryClientProvider and `stubPresets` stubs fetch to
  // return a presets envelope — both local to this file, the same two helpers
  // Task 15's override-editor test defines for itself. Two test files sharing
  // a mount helper through a third module is a dependency neither needs.
  it("shows a preset card per matching provider and creates from it", async () => {
    const fetchMock = stubPresets([preset({ id: "groq", name: "Groq" })])
    mount(<AddProviderDialog open onOpenChange={() => {}} />)
    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/provider id/i), "my-groq")
    await userEvent.click(screen.getByRole("button", { name: /create/i }))
    await waitFor(() => {
      const post = fetchMock.mock.calls.find(
        ([url, init]) => url === "/api/providers" && (init as RequestInit)?.method === "POST",
      )
      expect(JSON.parse((post?.[1] as RequestInit).body as string)).toEqual({
        id: "my-groq", preset: "groq",
      })
    })
  })
})
