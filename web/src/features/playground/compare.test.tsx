import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { Compare, MAX_COLUMNS } from "./compare"
import { emptyConfig } from "./config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

vi.mock("../shell/model-combobox", () => ({
  ModelCombobox: ({ label, value }: { label: string; value: string }) => (
    <input aria-label={label} value={value} readOnly />
  ),
  useModelCandidates: () => ({ candidates: [], loading: false }),
}))

describe("the compare columns", () => {
  it("starts with two, which is the comparison the screen is named for", () => {
    render(<Compare config={emptyConfig()} />)
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(2)
  })

  it("adds a column on request", async () => {
    render(<Compare config={emptyConfig()} />)
    await userEvent.click(screen.getByRole("button", { name: /add/i }))
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(3)
  })

  it("stops at four, past which no column is wide enough to read", async () => {
    render(<Compare config={emptyConfig()} />)
    const add = screen.getByRole("button", { name: /add/i })
    await userEvent.click(add)
    await userEvent.click(add)
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(MAX_COLUMNS)
    expect(add).toBeDisabled()
  })

  it("removes a column but never the last two", async () => {
    render(<Compare config={emptyConfig()} />)
    await userEvent.click(screen.getByRole("button", { name: /add/i }))
    const removes = screen.getAllByRole("button", { name: /remove/i })
    await userEvent.click(removes[0]!)
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(2)
    for (const button of screen.queryAllByRole("button", { name: /remove/i })) {
      expect(button).toBeDisabled()
    }
  })
})
