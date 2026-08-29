import { fireEvent, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it } from "vitest"
import { AliasEditor } from "./routing-screen"

const cred = () => ({
  id: "k1", label: "k1", masked: "sk-…", enabled: true, cooling: false, kind: "static",
})

/** A context the editor would really be given. Passing none used to leave
 *  every test running against an empty provider set, where each qualified
 *  target read as fatal and Save was disabled — so no test could have caught a
 *  regression in the very validation they were exercising. */
const context = {
  providers: [{
    id: "groq", name: "groq", preset: "groq", kind: "openaicompat",
    base_url: "https://x.example", priority: 10, enabled: true, auth_style: "bearer",
    free_models_only: false, credentials: [cred()],
  }],
  models: [],
} as never

function mount(aliases: Record<string, string[]>) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <AliasEditor aliases={aliases} knownProviders={["groq"]} context={context} />
    </QueryClientProvider>,
  )
}

describe("AliasEditor drag reorder", () => {
  it("keeps a focused row's value pinned to that row, not to its old screen position", () => {
    mount({ chain: ["groq/a", "groq/b", "groq/c"] })

    // The chain reads as pills until it is opened for editing: the ordered
    // list of fields is the exception, not the resting state.
    fireEvent.click(screen.getByRole("button", { name: "Edit" }))

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

describe("a chain at rest", () => {
  it("shows its targets in order without opening an editor", () => {
    mount({ chain: ["groq/a", "groq/b"] })

    // Pills, not fields: the fallback order is what an operator reads, and
    // reading it should not require entering an edit mode.
    expect(screen.queryByLabelText("chain target 1")).not.toBeInTheDocument()
    expect(screen.getByText("groq/a")).toBeInTheDocument()
    expect(screen.getByText("groq/b")).toBeInTheDocument()
  })

  it("says so once when a chain would route nowhere", () => {
    // The empty row and the problem beneath it are one fact. Saying it twice
    // reads as two problems.
    mount({ chain: [] })
    expect(screen.getByText("no targets yet")).toBeInTheDocument()
    expect(screen.getByText(/routes nowhere/i)).toBeInTheDocument()
  })

  it("asks before dropping a whole chain", () => {
    mount({ chain: ["groq/a"] })
    fireEvent.click(screen.getByRole("button", { name: "Remove" }))
    expect(screen.getByText(/remove the chain chain\?/i)).toBeInTheDocument()
    // Nothing has gone yet: the prompt is the decision point.
    expect(screen.getByText("groq/a")).toBeInTheDocument()
  })

  it("offers a preview of the chain from the chain itself", () => {
    const seen: string[] = []
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <AliasEditor
          aliases={{ chain: ["groq/a"] }}
          knownProviders={["groq"]}
          onPreview={(name) => seen.push(name)}
        />
      </QueryClientProvider>,
    )
    fireEvent.click(screen.getByRole("button", { name: "Preview" }))
    expect(seen).toEqual(["chain"])
  })
})

describe("adding an alias", () => {
  it("creates it with the targets typed in the dialog, in order", async () => {
    // The name alone never created anything useful: an alias with no targets
    // routes nowhere. Both are given at once.
    mount({})
    const user = userEvent.setup()

    await user.click(screen.getByRole("button", { name: "Add alias" }))
    await user.type(screen.getByLabelText("Alias name"), "sonnet")
    await user.type(screen.getByLabelText("New alias target 1"), "groq/a")
    await user.click(screen.getByRole("button", { name: "Add target" }))
    await user.type(screen.getByLabelText("New alias target 2"), "groq/b")
    await user.click(screen.getByRole("button", { name: /create with 2 targets/i }))

    // Opened rather than left collapsed: the next thing an operator does is
    // usually to the chain they just made.
    expect(screen.getByLabelText("sonnet target 1")).toHaveValue("groq/a")
    expect(screen.getByLabelText("sonnet target 2")).toHaveValue("groq/b")
  })

  it("will not create an alias that has no targets", async () => {
    mount({})
    const user = userEvent.setup()

    await user.click(screen.getByRole("button", { name: "Add alias" }))
    await user.type(screen.getByLabelText("Alias name"), "sonnet")

    expect(screen.getByRole("button", { name: /create alias/i })).toBeDisabled()
  })

  it("refuses a name that is already taken", async () => {
    // Creating it would silently replace the chain of that name, and the
    // editor holds one entry per name.
    mount({ chain: ["groq/a"] })
    const user = userEvent.setup()

    await user.click(screen.getByRole("button", { name: "Add alias" }))
    await user.type(screen.getByLabelText("Alias name"), "chain")
    await user.type(screen.getByLabelText("New alias target 1"), "groq/b")

    expect(screen.getByText(/already an alias called chain/i)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /create alias/i })).toBeDisabled()
  })
})

describe("an alias added somewhere else", () => {
  it("is adopted rather than deleted by the next Save", () => {
    // PUT /api/aliases replaces the whole map. A draft seeded once would send
    // a map without the new alias and the server would drop it, under a toast
    // reading "Aliases saved".
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <AliasEditor aliases={{ chain: ["groq/a"] }} knownProviders={["groq"]} context={context} />
      </QueryClientProvider>,
    )
    expect(screen.queryByText("haiku")).not.toBeInTheDocument()

    rerender(
      <QueryClientProvider client={client}>
        <AliasEditor
          aliases={{ chain: ["groq/a"], haiku: ["groq/b"] }}
          knownProviders={["groq"]}
          context={context}
        />
      </QueryClientProvider>,
    )

    expect(screen.getByText("haiku")).toBeInTheDocument()
  })

  it("does not overwrite a chain being edited", () => {
    // Adoption is for names the draft has never seen. A refetch must not
    // reach in and undo what is being typed.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <AliasEditor aliases={{ chain: ["groq/a"] }} knownProviders={["groq"]} context={context} />
      </QueryClientProvider>,
    )
    fireEvent.click(screen.getByRole("button", { name: "Edit" }))
    fireEvent.change(screen.getByLabelText("chain target 1"), {
      target: { value: "groq/edited" },
    })

    rerender(
      <QueryClientProvider client={client}>
        <AliasEditor aliases={{ chain: ["groq/a"], haiku: ["groq/b"] }} knownProviders={["groq"]} context={context} />
      </QueryClientProvider>,
    )

    expect(screen.getByLabelText("chain target 1")).toHaveValue("groq/edited")
  })
})
