import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { AccountStrip, CapabilityTriad, Pips, ScaleBar, logFraction } from "./measures"

describe("the log scale", () => {
  it("puts the geometric middle of the domain at the middle of the bar", () => {
    // 1000 is the midpoint of 100..10000 on a log scale, which is the whole
    // reason these bars are drawn on one: on a linear scale it would sit at
    // one tenth and every fast request would share an empty bar.
    expect(logFraction(1000, 100, 10_000)).toBeCloseTo(0.5, 5)
  })

  it("clamps rather than overflowing at either end", () => {
    expect(logFraction(5, 100, 10_000)).toBe(0)
    expect(logFraction(999_999, 100, 10_000)).toBe(1)
  })

  it("refuses a domain it cannot take a logarithm of", () => {
    // Zero and negatives have no log. Returning nothing beats returning NaN,
    // which renders as a bar of width "NaN%" — an invisible mark that reads
    // as a measurement of zero.
    expect(logFraction(0, 100, 10_000)).toBe(0)
    expect(logFraction(500, 0, 10_000)).toBe(0)
    expect(logFraction(500, 10_000, 100)).toBe(0)
  })
})

describe("a measured value", () => {
  it("keeps its number beside the bar", () => {
    // The bar answers "which of these is the slow one" and the digits answer
    // "how slow". Dropping the number trades a precise reading for a rough
    // one, and a bar is not a value a screen reader can announce.
    render(<ScaleBar value={1400} min={100} max={100_000} label="1.4 s" />)
    expect(screen.getByText("1.4 s")).toBeInTheDocument()
  })

  it("says so when there is nothing to measure", () => {
    render(<ScaleBar value={null} min={100} max={100_000} label="—" />)
    expect(screen.getByText("—")).toBeInTheDocument()
  })
})

describe("attempt pips", () => {
  it("announces the count it draws", () => {
    render(<Pips count={3} />)
    expect(screen.getByText("3")).toBeInTheDocument()
  })

  it("becomes a number past the point anyone counts marks", () => {
    const { container } = render(<Pips count={9} />)
    expect(screen.getByText("9")).toBeInTheDocument()
    expect(container.querySelectorAll("span.rounded-full")).toHaveLength(0)
  })
})

describe("the account strip", () => {
  it("keeps the written count beside the reserved colours", () => {
    // Status colour is never the only thing saying a provider is in trouble.
    render(<AccountStrip mix={{ usable: 1, cooling: 2, disabled: 0 }} label="1/3" />)
    expect(screen.getByText("1/3")).toBeInTheDocument()
    expect(screen.getByTitle(/1 usable · 2 cooling/)).toBeInTheDocument()
  })

  it("draws an empty track rather than nothing for a provider with no keys", () => {
    const { container } = render(
      <AccountStrip mix={{ usable: 0, cooling: 0, disabled: 0 }} label="0/0" />,
    )
    expect(container.querySelector('[title="no credentials"]')).toBeInTheDocument()
  })
})

describe("the capability triad", () => {
  it("holds every capability in a fixed slot, present or not", () => {
    // A column of loose badges cannot be scanned down: "tools" sits somewhere
    // different on every line.
    render(<CapabilityTriad tools vision={false} reasoning />)
    expect(screen.getByTitle("tools: yes")).toBeInTheDocument()
    expect(screen.getByTitle("vision: no")).toBeInTheDocument()
    expect(screen.getByTitle("reasoning: yes")).toBeInTheDocument()
  })

  it("never carries presence by colour alone", () => {
    render(<CapabilityTriad tools={false} vision={false} reasoning={false} />)
    for (const letter of ["T", "V", "R"]) {
      expect(screen.getByText(letter)).toBeInTheDocument()
    }
  })
})
