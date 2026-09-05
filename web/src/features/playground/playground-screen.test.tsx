import { useState, useSyncExternalStore } from "react"
import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { PlaygroundScreen } from "./playground-screen"

/**
 * A search string that changes, because the screen reads the surface out of
 * the URL rather than keeping a copy in state. A constant here would have
 * every tab click land on a location that never moves, which is the one thing
 * this screen must not do.
 */
const url = vi.hoisted(() => {
  let current: { mode?: string; seed?: string } = { mode: "chat" }
  const listeners = new Set<() => void>()
  return {
    read: () => current,
    /** The browser restoring a location: Back, or a pasted link. */
    go(next: { mode?: string; seed?: string }) {
      current = next
      listeners.forEach((fn) => fn())
    },
    subscribe(fn: () => void) {
      listeners.add(fn)
      return () => listeners.delete(fn)
    },
    reset() {
      current = { mode: "chat" }
    },
  }
})

vi.mock("@tanstack/react-router", () => ({
  useSearch: () => useSyncExternalStore(url.subscribe, url.read),
  useNavigate: () => (opts: { search: (prev: Record<string, unknown>) => typeof opts.search }) =>
    url.go(opts.search(url.read()) as { mode?: string; seed?: string }),
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
    url.reset()
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

  it("shows the surface the location names when the browser goes Back", async () => {
    // The router does not remount the route when only its search changes, so
    // a screen holding its own copy of the mode moved the URL and left the
    // previous surface on screen.
    url.reset()
    render(<PlaygroundScreen />)
    await userEvent.click(screen.getByRole("tab", { name: "Auxiliary" }))
    expect(screen.getByLabelText("Auxiliary state")).toHaveAttribute("data-active", "true")

    url.go({ mode: "compare" })
    expect(await screen.findByLabelText("Compare state")).toHaveAttribute("data-active", "true")
    expect(screen.getByLabelText("Auxiliary state")).toHaveAttribute("data-active", "false")
  })
})
