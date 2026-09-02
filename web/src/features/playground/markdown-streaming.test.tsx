import { act, render } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { Markdown } from "./markdown"

describe("markdown while an answer is still arriving", () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it("re-parses the tail at most every 50 ms", () => {
    // A stream delivers many chunks a second, and parsing the whole answer
    // on every one made a long reply stutter as it grew.
    const view = render(<Markdown text="one" streaming />)
    expect(view.container.textContent).toBe("one")

    view.rerender(<Markdown text="one two" streaming />)
    expect(view.container.textContent).toBe("one")

    act(() => vi.advanceTimersByTime(50))
    expect(view.container.textContent).toBe("one two")
  })

  it("shows the whole answer the moment the stream ends", () => {
    const view = render(<Markdown text="one" streaming />)
    view.rerender(<Markdown text="one two three" streaming />)
    view.rerender(<Markdown text="one two three" />)
    expect(view.container.textContent).toBe("one two three")
  })
})
