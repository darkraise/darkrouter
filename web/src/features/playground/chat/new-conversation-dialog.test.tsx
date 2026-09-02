import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi } from "vitest"
import { NewConversationDialog } from "./new-conversation-dialog"
import { emptyConfig, type PlaygroundConfig } from "../config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

// The pane inside the dialog carries the preset picker, which reads a list.
vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [] }),
}))

vi.mock("../../shell/model-combobox", () => ({
  useModelCandidates: () => ({ candidates: ["gpt", "claude"], loading: false }),
  ModelCombobox: ({ value, onChange, label }: {
    value: string
    onChange: (next: string) => void
    label: string
  }) => (
    <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />
  ),
}))

function dialog(over: Record<string, unknown> = {}) {
  const props = {
    open: true,
    onOpenChange: () => {},
    seed: emptyConfig(),
    onStart: () => {},
    ...over,
  }
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <NewConversationDialog
        {...(props as Parameters<typeof NewConversationDialog>[0])}
      />
    </QueryClientProvider>,
  )
}

describe("the new-conversation dialog", () => {
  it("offers the request settings rather than a second copy of a few of them", async () => {
    // The whole pane, not a hand-picked subset: a dialog that carried only
    // the model would send an operator looking for a temperature to a panel
    // that no longer edits one.
    dialog()
    expect(screen.getByLabelText("Model or alias")).toBeInTheDocument()
    expect(screen.getByLabelText(/dialect/i)).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: /sampling/i }))
    expect(screen.getByLabelText(/temperature/i)).toBeInTheDocument()
  })

  it("hands back what was set when the conversation is started", async () => {
    const onStart = vi.fn()
    const onOpenChange = vi.fn()
    dialog({ onStart, onOpenChange })

    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")
    await userEvent.click(screen.getByRole("button", { name: /start conversation/i }))

    expect(onStart).toHaveBeenCalledWith(expect.objectContaining({ model: "gpt" }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("hands back nothing when it is cancelled", async () => {
    // Cancel is not a way to refuse a new conversation -- the rail's own
    // button already started one. It refuses only these settings, leaving
    // the blank chat on the defaults it was given.
    const onStart = vi.fn()
    const onOpenChange = vi.fn()
    dialog({ onStart, onOpenChange })

    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")
    await userEvent.click(screen.getByRole("button", { name: /cancel/i }))

    expect(onStart).not.toHaveBeenCalled()
    expect(onOpenChange).toHaveBeenCalledWith(false)
  })

  it("starts from the settings it was seeded with", () => {
    // The conversation being left behind is the seed, so the model an
    // operator has been working with carries into the next thread rather
    // than being retyped every time.
    const seed: PlaygroundConfig = { ...emptyConfig(), model: "claude", temperature: "0.2" }
    dialog({ seed })
    expect(screen.getByLabelText("Model or alias")).toHaveValue("claude")
  })

  it("forgets an abandoned draft when it is opened again", async () => {
    // Reopened after a Cancel, it must show the seed rather than the edits
    // that were just refused -- a dialog that remembers what you discarded
    // hands them back the moment you press Start.
    const seed = { ...emptyConfig(), model: "claude" }
    const view = dialog({ seed, open: false })

    view.rerender(
      <QueryClientProvider client={new QueryClient()}>
        <NewConversationDialog
          open
          onOpenChange={() => {}}
          seed={seed}
          onStart={() => {}}
        />
      </QueryClientProvider>,
    )
    await userEvent.clear(screen.getByLabelText("Model or alias"))
    await userEvent.type(screen.getByLabelText("Model or alias"), "abandoned")

    view.rerender(
      <QueryClientProvider client={new QueryClient()}>
        <NewConversationDialog
          open={false}
          onOpenChange={() => {}}
          seed={seed}
          onStart={() => {}}
        />
      </QueryClientProvider>,
    )
    view.rerender(
      <QueryClientProvider client={new QueryClient()}>
        <NewConversationDialog
          open
          onOpenChange={() => {}}
          seed={seed}
          onStart={() => {}}
        />
      </QueryClientProvider>,
    )
    expect(screen.getByLabelText("Model or alias")).toHaveValue("claude")
  })

  it("says it is amending rather than starting when a conversation is already open", () => {
    // Reached from the header's actions menu on a conversation that has no
    // turns yet. Nothing is being started, so a button that said so would
    // be describing the wrong act.
    dialog({ amending: true })
    expect(screen.queryByRole("button", { name: /start conversation/i })).toBeNull()
    expect(screen.getByRole("button", { name: /apply/i })).toBeInTheDocument()
  })

  it("closes only the Manage presets dialog on Escape, not itself", async () => {
    // Both dialogs portal to the body as siblings, so neither is nested in
    // the other's DOM and both took Escape as their own: one keystroke
    // closed the preset list and threw away the settings behind it.
    const onOpenChange = vi.fn()
    dialog({ onOpenChange })

    await userEvent.click(screen.getByRole("button", { name: /manage presets/i }))
    expect(await screen.findByRole("dialog", { name: /manage presets/i })).toBeInTheDocument()

    await userEvent.keyboard("{Escape}")

    // Whether the closed dialog is already out of the tree depends on the
    // exit transition, which jsdom does not play; either way it is closed.
    const manage = screen.queryByRole("dialog", { name: /manage presets/i })
    expect(manage === null || manage.getAttribute("data-state") === "closed").toBe(true)
    expect(screen.getByRole("dialog", { name: /new conversation/i })).toBeInTheDocument()
    expect(onOpenChange).not.toHaveBeenCalledWith(false)
  })
})
