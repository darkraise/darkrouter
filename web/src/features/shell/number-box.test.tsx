import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { NumberBox } from "./number-box"

/** The box driven the way every call site drives it: from a string. */
function Harness({ seed = "", onLog }: { seed?: string; onLog?: (s: string) => void }) {
  const [value, setValue] = useState(seed)
  return (
    <>
      <NumberBox
        value={value}
        onChange={(next) => {
          setValue(next)
          onLog?.(next)
        }}
      />
      <output data-testid="stored">{JSON.stringify(value)}</output>
      <button type="button">elsewhere</button>
    </>
  )
}

const stored = () => screen.getByTestId("stored").textContent

describe("the number box over a string", () => {
  it("keeps an empty box empty rather than calling it zero", () => {
    // The whole reason these settings are strings. `Number("")` is 0, so a
    // box that coerced would turn "the operator said nothing" into "the
    // operator asked for zero" -- and nothing on screen would say so.
    render(<Harness />)
    expect(screen.getByRole("spinbutton")).toHaveValue("")
    expect(stored()).toBe('""')
  })

  it("holds a real zero apart from an empty one", async () => {
    render(<Harness />)
    await userEvent.type(screen.getByRole("spinbutton"), "0")
    expect(stored()).toBe('"0"')
  })

  it("lets a decimal be typed one character at a time", async () => {
    // "0." parses to 0, and a box that wrote back the parsed number would
    // erase the point the moment it was typed.
    render(<Harness />)
    const field = screen.getByRole("spinbutton")
    await userEvent.type(field, "0.")
    expect(stored()).toBe('"0."')
    await userEvent.type(field, "7")
    expect(stored()).toBe('"0.7"')
  })

  describe("in a browser whose locale groups with a period", () => {
    const Browser = Intl.NumberFormat
    beforeEach(() => {
      // Only the default locale changes: a caller naming one still gets it.
      Intl.NumberFormat = function (locales?: Intl.LocalesArgument, options?: Intl.NumberFormatOptions) {
        return new Browser(locales ?? "de-DE", options)
      } as unknown as typeof Intl.NumberFormat
    })
    afterEach(() => {
      Intl.NumberFormat = Browser
    })

    it("keeps a typed decimal a decimal when the field is left", async () => {
      // The stored string is a JavaScript number, so its point is a decimal
      // point whatever the operator's locale calls a period.
      render(<Harness />)
      await userEvent.type(screen.getByRole("spinbutton"), "0.7")
      await userEvent.click(screen.getByRole("button", { name: "elsewhere" }))
      expect(stored()).toBe('"0.7"')
    })
  })

  it("returns to empty when the box is cleared", async () => {
    render(<Harness seed="0.7" />)
    await userEvent.clear(screen.getByRole("spinbutton"))
    expect(stored()).toBe('""')
  })

  it("leaves a stored value alone when it is never edited", async () => {
    // A conversation or preset can hold a value this box merely displays.
    // Focusing and leaving must not rewrite it.
    const onLog = vi.fn()
    render(<Harness seed="0.50" onLog={onLog} />)
    await userEvent.click(screen.getByRole("spinbutton"))
    await userEvent.click(screen.getByRole("button", { name: "elsewhere" }))
    expect(onLog).not.toHaveBeenCalled()
    expect(stored()).toBe('"0.50"')
  })

  it("steps the value, which a plain box could not", async () => {
    render(<Harness seed="4" />)
    await userEvent.click(screen.getByRole("button", { name: "Increment" }))
    expect(stored()).toBe('"5"')
  })

  it("drops the steppers on a gated field, which has nothing to step", () => {
    render(<NumberBox value="40" disabled retainValue onChange={() => {}} />)
    expect(screen.queryByRole("button", { name: "Increment" })).toBeNull()
    expect(screen.getByRole("spinbutton")).toBeDisabled()
    // The value stays readable: a gated control still has to say what it holds.
    expect(screen.getByRole("spinbutton")).toHaveValue("40")
  })
})
