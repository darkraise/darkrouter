import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { ConversationHeader } from "./conversation-header"
import { emptyConfig } from "../config"

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

const noop = () => {}

function header(over: Record<string, unknown> = {}) {
  const props = {
    config: { ...emptyConfig(), model: "gpt" },
    onConfigChange: noop,
    onConfigCommit: noop,
    title: "speculative decoding",
    onTitleChange: noop,
    onOpenInLab: noop,
    onDelete: noop,
    canDelete: true,
    ...over,
  }
  return render(<ConversationHeader {...(props as Parameters<typeof ConversationHeader>[0])} />)
}

describe("the conversation header", () => {
  it("shows the model on the pill rather than hiding it in a pane", () => {
    header()
    expect(screen.getByRole("button", { name: /gpt/ })).toBeInTheDocument()
  })

  it("commits a retitle on Enter and abandons it on Escape", async () => {
    const onTitleChange = vi.fn()
    header({ onTitleChange })
    const field = screen.getByLabelText("Conversation title")
    await userEvent.clear(field)
    await userEvent.type(field, "renamed{Enter}")
    expect(onTitleChange).toHaveBeenCalledWith("renamed")
    // Enter blurs, and blur commits. Calling both used to fire the change
    // twice, which the parent turns into two PATCH requests for one rename.
    expect(onTitleChange).toHaveBeenCalledTimes(1)

    onTitleChange.mockClear()
    await userEvent.clear(field)
    await userEvent.type(field, "discarded{Escape}")
    expect(onTitleChange).not.toHaveBeenCalled()
    expect(screen.getByLabelText("Conversation title")).toHaveValue("speculative decoding")
  })

  it("never commits an empty title, because the rail would draw a blank row", async () => {
    const onTitleChange = vi.fn()
    header({ onTitleChange })
    const field = screen.getByLabelText("Conversation title")
    await userEvent.clear(field)
    await userEvent.type(field, "{Enter}")
    expect(onTitleChange).not.toHaveBeenCalled()
  })

  it("carries the configuration into Lab", async () => {
    const onOpenInLab = vi.fn()
    header({ onOpenInLab })
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /open in lab/i }))
    expect(onOpenInLab).toHaveBeenCalled()
  })

  it("flushes a pending model commit on unmount rather than dropping it", async () => {
    // The model field commits after a quiet period. Switching to Lab inside
    // that window unmounts the header, and a cancelled timer loses a change
    // the operator made -- silently, which is the part that matters.
    const onConfigCommit = vi.fn()
    const view = header({ onConfigCommit })
    await userEvent.click(screen.getByRole("button", { name: /gpt/ }))
    await userEvent.type(screen.getByLabelText("Model or alias"), "x")
    expect(onConfigCommit).not.toHaveBeenCalled()

    view.unmount()
    expect(onConfigCommit).toHaveBeenCalledWith(
      expect.objectContaining({ model: "gptx" }),
    )
  })

  it("returns focus to the menu trigger when the dialog closes", async () => {
    // The menu item preventDefaults its own focus return so the dialog can
    // take focus, which leaves the dialog nothing to hand it back to: focus
    // lands on document.body and the next Tab starts from the top of the
    // page, a long way from where the operator was working.
    header()
    const trigger = screen.getByRole("button", { name: "Conversation actions" })
    await userEvent.click(trigger)
    await userEvent.click(screen.getByRole("menuitem", { name: /system prompt/i }))
    expect(screen.getByLabelText("System prompt")).toBeInTheDocument()

    await userEvent.keyboard("{Escape}")
    await waitFor(() => expect(document.activeElement).toBe(trigger))
  })

  it("edits the system prompt, which is the one Lab setting a conversation needs", async () => {
    const onConfigChange = vi.fn()
    header({ onConfigChange })
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /system prompt/i }))
    await userEvent.type(screen.getByLabelText("System prompt"), "be brief")
    await userEvent.click(screen.getByRole("button", { name: "Save" }))
    expect(onConfigChange).toHaveBeenCalledWith(
      expect.objectContaining({ system: "be brief" }),
    )
  })
})
