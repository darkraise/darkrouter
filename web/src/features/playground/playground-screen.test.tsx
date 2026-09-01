import { useState } from "react"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { PlaygroundScreen } from "./playground-screen"

vi.mock("@tanstack/react-router", () => ({
  useSearch: () => ({ mode: "chat" }),
  useNavigate: () => vi.fn(),
}))

function Surface({ name, active }: { name: string; active?: boolean }) {
  const [value, setValue] = useState("")
  return (
    <label>
      {name}
      <input
        aria-label={`${name} state`}
        data-active={String(active)}
        value={value}
        onChange={(event) => setValue(event.target.value)}
      />
    </label>
  )
}

vi.mock("./chat/chat-mode", () => ({
  ChatMode: ({ active }: { active?: boolean }) => <Surface name="Chat" active={active} />,
}))
vi.mock("./compare", () => ({
  Compare: ({ active }: { active?: boolean }) => <Surface name="Compare" active={active} />,
}))
vi.mock("./aux/aux-mode", () => ({
  AuxMode: ({ active }: { active?: boolean }) => <Surface name="Auxiliary" active={active} />,
}))

describe("Playground surfaces", () => {
  it("preserves each surface's session state and marks only the visible one active", async () => {
    render(<PlaygroundScreen />)
    await userEvent.type(screen.getByLabelText("Chat state"), "draft")
    expect(screen.getByLabelText("Chat state")).toHaveAttribute("data-active", "true")

    await userEvent.click(screen.getByRole("tab", { name: "Compare" }))
    await userEvent.type(screen.getByLabelText("Compare state"), "result")
    expect(screen.getByLabelText("Chat state")).toHaveAttribute("data-active", "false")
    expect(screen.getByLabelText("Compare state")).toHaveAttribute("data-active", "true")

    await userEvent.click(screen.getByRole("tab", { name: "Auxiliary" }))
    await userEvent.type(screen.getByLabelText("Auxiliary state"), "reading")
    expect(screen.getByLabelText("Auxiliary state")).toHaveAttribute("data-active", "true")

    await userEvent.click(screen.getByRole("tab", { name: "Chat" }))
    expect(screen.getByLabelText("Chat state")).toHaveValue("draft")
    expect(screen.getByLabelText("Compare state")).toHaveValue("result")
    expect(screen.getByLabelText("Auxiliary state")).toHaveValue("reading")
  })
})
