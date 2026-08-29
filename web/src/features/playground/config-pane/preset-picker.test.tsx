import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import { PresetPicker } from "./preset-picker"
import { emptyConfig } from "../config"
import type { PlaygroundDialect, PlaygroundPreset } from "../../../lib/api-types"

const saved: PlaygroundPreset = {
  id: "p1",
  name: "terse",
  dialect: "anthropic",
  model: "claude",
  config: { system: "be brief", topK: "40" },
  created_at: "2026-08-30T00:00:00Z",
  updated_at: "2026-08-30T00:00:00Z",
}

// The admin API is operator-facing, so this row is what a hand-written curl
// against an unpatched server can produce: a dialect column outside the
// three the console understands.
const wildDialect: PlaygroundPreset = {
  ...saved,
  id: "p2",
  name: "wild",
  dialect: "responses" as PlaygroundDialect,
}

vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [saved, wildDialect], isLoading: false }),
}))

const { postMock, patchMock, delMock } = vi.hoisted(() => ({
  postMock: vi.fn(),
  patchMock: vi.fn(),
  delMock: vi.fn(),
}))

vi.mock("../../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/api")>()),
  api: { get: vi.fn(), post: postMock, patch: patchMock, del: delMock },
}))

function mounted(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

describe("loading a preset", () => {
  it("replaces the pane wholesale, model and dialect included", async () => {
    // Wholesale is the point: a preset that merged into whatever was already
    // typed would produce a request neither the operator nor the preset asked
    // for.
    const onChange = vi.fn()
    mounted(
      <PresetPicker config={{ ...emptyConfig(), model: "gpt", dialect: "openai" }} onChange={onChange} />,
    )
    await userEvent.click(screen.getByRole("combobox", { name: /load a preset|preset/i }))
    await userEvent.click(await screen.findByRole("option", { name: "terse" }))

    expect(onChange).toHaveBeenCalledTimes(1)
    const next = onChange.mock.calls[0]![0]
    expect(next.model).toBe("claude")
    expect(next.dialect).toBe("anthropic")
    expect(next.system).toBe("be brief")
    expect(next.topK).toBe("40")
  })

  it("falls back to openai for a preset carrying an unknown dialect", async () => {
    // A row can be written by hand with curl, not only by the console, so a
    // stored dialect the config pane cannot render must not crash it.
    const onChange = vi.fn()
    mounted(
      <PresetPicker config={{ ...emptyConfig(), model: "gpt", dialect: "openai" }} onChange={onChange} />,
    )
    await userEvent.click(screen.getByRole("combobox", { name: /load a preset|preset/i }))
    await userEvent.click(await screen.findByRole("option", { name: "wild" }))

    expect(onChange).toHaveBeenCalledTimes(1)
    const next = onChange.mock.calls[0]![0]
    expect(next.dialect).toBe("openai")
  })
})

describe("saving a preset", () => {
  it("asks for a name before writing anything", async () => {
    mounted(<PresetPicker config={{ ...emptyConfig(), model: "gpt" }} onChange={() => {}} />)
    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    expect(await screen.findByLabelText(/name/i)).toBeInTheDocument()
  })

  it("offers to overwrite a name that already exists, and sends the clash's id", async () => {
    patchMock.mockResolvedValue({ id: "p1" })
    mounted(<PresetPicker config={{ ...emptyConfig(), model: "gpt" }} onChange={() => {}} />)

    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    await userEvent.type(await screen.findByLabelText(/name/i), "terse")
    await userEvent.click(await screen.findByRole("button", { name: "Overwrite" }))

    expect(patchMock).toHaveBeenCalledTimes(1)
    expect(patchMock.mock.calls[0]![0]).toBe("/api/playground/presets/p1")
  })
})

describe("deleting a preset", () => {
  it("removes the saved row from the Manage dialog", async () => {
    delMock.mockResolvedValue(undefined)
    mounted(<PresetPicker config={{ ...emptyConfig(), model: "gpt" }} onChange={() => {}} />)

    await userEvent.click(screen.getByRole("button", { name: /manage presets/i }))
    await userEvent.click(await screen.findByRole("button", { name: "Delete terse" }))

    expect(delMock).toHaveBeenCalledTimes(1)
    expect(delMock.mock.calls[0]![0]).toBe("/api/playground/presets/p1")
  })
})
