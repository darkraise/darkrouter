import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { Composer } from "./composer"

describe("the composer", () => {
  it("ignores Enter while an input method is composing", () => {
    // Confirming a candidate in a Japanese or Chinese input method is an
    // Enter keystroke, and a composer that sent on it shipped half a word.
    const onSend = vi.fn()
    render(<Composer model="gpt" busy={false} error="" onSend={onSend} onStop={() => {}} />)
    const field = screen.getByLabelText("Message")
    fireEvent.change(field, { target: { value: "こんにちは" } })

    fireEvent.keyDown(field, { key: "Enter", isComposing: true })
    expect(onSend).not.toHaveBeenCalled()

    fireEvent.keyDown(field, { key: "Enter" })
    expect(onSend).toHaveBeenCalledWith("こんにちは")
  })

  it("announces a failed send and ties it to the field", () => {
    // Focus stays in the field after Enter, so an error that is only drawn
    // is one a screen-reader user never hears about.
    render(
      <Composer model="gpt" busy={false} error="no configured provider offers this model" onSend={() => {}} onStop={() => {}} />,
    )
    const alert = screen.getByRole("alert")
    expect(alert).toHaveTextContent("no configured provider offers this model")
    expect(screen.getByLabelText("Message")).toHaveAttribute("aria-describedby", alert.id)
  })
})
