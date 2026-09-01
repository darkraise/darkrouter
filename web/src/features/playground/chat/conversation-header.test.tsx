import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { ConversationHeader } from "./conversation-header"
import { emptyConfig } from "../config"

const noop = () => {}

function header(over: Record<string, unknown> = {}) {
  const props = {
    config: { ...emptyConfig(), model: "gpt" },
    title: "speculative decoding",
    onTitleChange: noop,
    onDelete: noop,
    onOpenSettings: noop,
    canDelete: true,
    ...over,
  }
  return render(<ConversationHeader {...(props as Parameters<typeof ConversationHeader>[0])} />)
}

describe("the conversation header", () => {
  it("shows the model on the pill rather than hiding it in a pane", () => {
    header()
    expect(screen.getByText("gpt")).toBeInTheDocument()
  })

  it("names no model as an absence rather than as an empty pill", () => {
    // A blank strip beside the title reads as a header that failed to load.
    header({ config: emptyConfig() })
    expect(screen.getByText("No model")).toBeInTheDocument()
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

  it("offers deleting the conversation from its actions menu", async () => {
    // The menu used to hold "Edit system prompt" and "Open in Lab" too. The
    // system prompt is in the request pane now, under the same lock as the
    // rest of what a request carries, and there is no Lab to open.
    const onDelete = vi.fn()
    header({ onDelete })
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /delete conversation/i }))
    expect(onDelete).toHaveBeenCalled()
  })

  it("opens the request settings from the actions menu while nothing has been sent", async () => {
    // The pane beside the transcript does not edit, so this is the only way
    // back to a mistyped temperature before the first message shuts it.
    const onOpenSettings = vi.fn()
    header({ onOpenSettings })
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /request settings/i }))
    expect(onOpenSettings).toHaveBeenCalled()
  })

  it("refuses the request settings once a turn has been sent", async () => {
    // Disabled rather than absent: an item that vanishes reads as a menu
    // that lost something, while one that stays and refuses says the
    // settings are shut -- which is the fact being looked for.
    const onOpenSettings = vi.fn()
    header({ locked: true, onOpenSettings })
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    const item = await screen.findByRole("menuitem", { name: /request settings/i })
    await userEvent.click(item)
    expect(onOpenSettings).not.toHaveBeenCalled()
  })

  it("offers no way to change the model from the header itself", async () => {
    // It was a popover that edited the model and the dialect. With the
    // dialog owning setup that made three surfaces for one value, each with
    // its own idea of when the settings close.
    header()
    expect(screen.queryByRole("button", { name: /gpt/ })).toBeNull()
    expect(screen.queryByLabelText("Model or alias")).toBeNull()
  })

  it("shows the model as a fixed reading once a turn has been sent", async () => {
    // Section 4: every answer in the transcript was produced by this model,
    // so a picker that still opened would offer a change the conversation
    // cannot honestly record. A plain reading rather than a disabled button,
    // because an operator clicks a control before reading why it did nothing.
    header({ locked: true })
    expect(screen.getByText("gpt")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /gpt/ })).toBeNull()

    // And the name is still the operator's to change: what a thread is
    // called was never part of what was sent.
    expect(screen.getByLabelText("Conversation title")).toBeEnabled()
  })
})
