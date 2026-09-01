import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { HistoryRail, previewLine } from "./history-rail"
import type { PlaygroundConversation } from "../../../lib/api-types"

function conversation(over: Partial<PlaygroundConversation> = {}): PlaygroundConversation {
  return {
    id: "c1",
    title: "speculative decoding",
    dialect: "openai",
    model: "gpt",
    config: {},
    preview: "explain the difference",
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...over,
  }
}

const noop = () => {}

describe("the history rail", () => {
  it("lists a conversation with what it was about", () => {
    render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={noop}
        onNew={noop}
        onDelete={noop}
      />,
    )
    expect(screen.getByText("speculative decoding")).toBeInTheDocument()
    expect(screen.getByText("explain the difference")).toBeInTheDocument()
    expect(screen.getByText("just now")).toBeInTheDocument()
  })

  it("says so when there is nothing to retrieve yet", () => {
    render(
      <HistoryRail
        conversations={[]}
        activeId=""
        onSelect={noop}
        onNew={noop}
        onDelete={noop}
      />,
    )
    expect(screen.getByText(/no saved conversations/i)).toBeInTheDocument()
  })

  it("selects a conversation, and starts one from the panel's own header", async () => {
    // The new-conversation control is an icon on the header row rather than a
    // labelled button in a footer, so its accessible name is the thing being
    // asserted -- an icon that lost its aria-label is a control a screen
    // reader cannot name.
    const onSelect = vi.fn()
    const onNew = vi.fn()
    render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={onSelect}
        onNew={onNew}
        onDelete={noop}
      />,
    )
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    expect(onSelect).toHaveBeenCalledWith("c1")
    await userEvent.click(screen.getByRole("button", { name: "New conversation" }))
    expect(onNew).toHaveBeenCalled()
  })

  it("stays on screen, with no control that can hide it", () => {
    // Retrieval is the whole point of the panel, and a rail behind a toggle
    // is one an operator stops reaching for.
    render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={noop}
        onNew={noop}
        onDelete={noop}
      />,
    )
    expect(screen.getByText("speculative decoding")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /hide conversations/i })).toBeNull()
    expect(screen.queryByRole("button", { name: /show conversations/i })).toBeNull()
  })
})

describe("the second line of a row", () => {
  it("is dropped when it only restates the title", () => {
    // One turn puts the same sentence in both, cut at two different lengths.
    expect(previewLine("A bat and a ball cost…", "A bat and a ball cost $1.10")).toBeNull()
    expect(previewLine("hi", "hi")).toBeNull()
  })

  it("is kept once the thread has moved on", () => {
    expect(previewLine("A bat and a ball cost…", "so the ball is 5 cents?")).toBe(
      "so the ball is 5 cents?",
    )
  })

  it("says a conversation has nothing in it rather than drawing a blank line", () => {
    expect(previewLine("New chat", "")).toBe("No messages yet")
  })
})
