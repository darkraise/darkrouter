import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { HistoryRail } from "./history-rail"
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
        collapsed={false}
        onToggleCollapsed={noop}
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
        collapsed={false}
        onToggleCollapsed={noop}
      />,
    )
    expect(screen.getByText(/no saved conversations/i)).toBeInTheDocument()
  })

  it("selects a conversation and starts a new one", async () => {
    const onSelect = vi.fn()
    const onNew = vi.fn()
    render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={onSelect}
        onNew={onNew}
        onDelete={noop}
        collapsed={false}
        onToggleCollapsed={noop}
      />,
    )
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    expect(onSelect).toHaveBeenCalledWith("c1")
    await userEvent.click(screen.getByRole("button", { name: "New" }))
    expect(onNew).toHaveBeenCalled()
  })

  it("collapses to nothing", () => {
    // 260px of rail beside a 320px pane is three columns on a laptop. The
    // operator has to be able to give the transcript the width back.
    const { container } = render(
      <HistoryRail
        conversations={[conversation()]}
        activeId=""
        onSelect={noop}
        onNew={noop}
        onDelete={noop}
        collapsed
        onToggleCollapsed={noop}
      />,
    )
    expect(screen.queryByText("speculative decoding")).not.toBeInTheDocument()
    expect(container.querySelector("aside")).toBeNull()
  })
})
