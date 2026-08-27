import { fireEvent, render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it } from "vitest"
import { AliasEditor } from "./routing-screen"

function mount(aliases: Record<string, string[]>) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <AliasEditor aliases={aliases} knownProviders={["groq"]} />
    </QueryClientProvider>,
  )
}

describe("AliasEditor drag reorder", () => {
  it("keeps a focused row's value pinned to that row, not to its old screen position", () => {
    mount({ chain: ["groq/a", "groq/b", "groq/c"] })

    // Focus the middle target and leave the cursor there, as an operator
    // mid-edit would.
    const focused = screen.getByLabelText("chain target 2")
    focused.focus()
    expect(focused).toHaveValue("groq/b")

    // Drag the first row past the third: moveTarget(_, 0, 2) reorders
    // [a, b, c] to [b, c, a], so the row the operator is looking at moves
    // from screen position 2 to position 1.
    const firstRow = screen.getByLabelText("chain target 1").closest("li")
    const thirdRow = screen.getByLabelText("chain target 3").closest("li")
    if (!firstRow || !thirdRow) throw new Error("expected both rows to render")
    fireEvent.dragStart(firstRow)
    fireEvent.dragOver(thirdRow)
    fireEvent.drop(thirdRow)

    // The DOM node that had focus is still focused and still shows the
    // target it held before the drag — not whatever text a plain
    // index-keyed list would have swapped into its old screen slot.
    expect(document.activeElement).toBe(focused)
    expect(focused).toHaveValue("groq/b")
  })
})
