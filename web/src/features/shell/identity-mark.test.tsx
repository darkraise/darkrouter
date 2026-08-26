import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { IdentityMark } from "./identity-mark"
import { LoginScreen } from "../../routes/login"

describe("the identity mark", () => {
  it("draws the spine in three segments", () => {
    // One rule top to bottom would fill the 1px cores of both hollow squares,
    // and two skip marks would render solid — the ladder saying "served"
    // where it means "considered".
    const { container } = render(<IdentityMark />)
    expect(container.querySelectorAll(".spine-seg")).toHaveLength(3)
  })

  it("carries exactly one accent pip", () => {
    // Two colours only, per §3.5: one neutral hairline and one accent.
    const { container } = render(<IdentityMark />)
    expect(container.querySelectorAll(".pip")).toHaveLength(1)
  })

  it("scales through the viewBox rather than by redrawing", () => {
    const { container } = render(<IdentityMark size={36} />)
    const svg = container.querySelector("svg")
    expect(svg?.getAttribute("viewBox")).toBe("0 0 24 24")
    expect(svg?.getAttribute("width")).toBe("36")
  })

  it("names itself for a reader who cannot see it", () => {
    render(<IdentityMark />)
    expect(screen.getByRole("img", { name: /darkrouter/i })).toBeInTheDocument()
  })
})

describe("login", () => {
  it("shows the mark", () => {
    const { container } = render(<LoginScreen onAuthenticated={() => {}} />)
    expect(container.querySelector("svg .pip")).not.toBeNull()
  })
})
