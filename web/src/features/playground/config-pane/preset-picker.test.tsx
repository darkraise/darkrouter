import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import { PresetPicker } from "./preset-picker"
import { emptyConfig } from "../config"
import type { PlaygroundPreset } from "../../../lib/api-types"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

const saved: PlaygroundPreset = {
  id: "p1",
  name: "terse",
  dialect: "anthropic",
  model: "claude",
  config: { system: "be brief", topK: "40" },
  created_at: "2026-08-30T00:00:00Z",
  updated_at: "2026-08-30T00:00:00Z",
}

vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [saved], isLoading: false }),
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
})

describe("saving a preset", () => {
  it("asks for a name before writing anything", async () => {
    mounted(<PresetPicker config={{ ...emptyConfig(), model: "gpt" }} onChange={() => {}} />)
    await userEvent.click(screen.getByRole("button", { name: /save/i }))
    expect(await screen.findByLabelText(/name/i)).toBeInTheDocument()
  })
})
