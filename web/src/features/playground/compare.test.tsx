import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
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

// Compare carries the request pane that every column is sent under, and the
// pane's preset picker reads a list. Stubbed rather than served, so these
// tests stay about the columns.
vi.mock("../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [] }),
}))

/** Compare carries the request pane now, whose preset picker both reads a
 *  query and owns a mutation. Both need a client above them. */
function renderCompare(config: Parameters<typeof Compare>[0]["config"]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Compare config={config} onConfigChange={() => {}} />
    </QueryClientProvider>,
  )
}

describe("the compare columns", () => {
  it("starts with two, which is the comparison the screen is named for", () => {
    renderCompare(emptyConfig())
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(2)
  })

  it("adds a column on request", async () => {
    renderCompare(emptyConfig())
    await userEvent.click(screen.getByRole("button", { name: /add/i }))
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(3)
  })

  it("stops at four, past which no column is wide enough to read", async () => {
    renderCompare(emptyConfig())
    const add = screen.getByRole("button", { name: /add/i })
    await userEvent.click(add)
    await userEvent.click(add)
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(MAX_COLUMNS)
    expect(add).toBeDisabled()
  })

  it("removes a column but never the last two", async () => {
    renderCompare(emptyConfig())
    await userEvent.click(screen.getByRole("button", { name: /add/i }))
    const removes = screen.getAllByRole("button", { name: /remove/i })
    await userEvent.click(removes[0]!)
    expect(screen.getAllByRole("textbox", { name: /model/i })).toHaveLength(2)
    for (const button of screen.queryAllByRole("button", { name: /remove/i })) {
      expect(button).toBeDisabled()
    }
  })
})

describe("the column status dot", () => {
  it("is not a live region, so four streaming columns do not talk over each other", () => {
    // role="status" made every dot announce on each idle -> streaming -> done
    // transition. The label still reaches a reader who navigates to the dot.
    render(
      <QueryClientProvider client={new QueryClient()}>
        <Compare config={emptyConfig()} onConfigChange={() => {}} />
      </QueryClientProvider>,
    )
    expect(screen.queryAllByRole("status")).toHaveLength(0)
    expect(screen.getAllByRole("img", { name: "idle" }).length).toBeGreaterThan(0)
  })
})
