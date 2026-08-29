import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { Sampling } from "./sampling"
import { emptyConfig } from "../config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

const noop = () => {}

describe("the sampling controls", () => {
  it("enables top K on anthropic", () => {
    render(<Sampling config={{ ...emptyConfig(), dialect: "anthropic" }} onChange={noop} />)
    expect(screen.getByLabelText(/top k/i)).toBeEnabled()
  })

  it("disables top K on openai and says why", () => {
    // Disabled rather than hidden: a control that appears and disappears as
    // the dialect changes reads as a bug, while a disabled one with a reason
    // teaches something true about the three wires.
    render(<Sampling config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    const topK = screen.getByLabelText(/top k/i)
    expect(topK).toBeDisabled()
    expect(screen.getByText(/no top_k field/i)).toBeInTheDocument()
  })

  it("keeps a disabled control's stored value visible", () => {
    // A preset written under another dialect keeps what it stored. Blanking
    // the box would make the setting quietly lossy on every round trip.
    render(
      <Sampling config={{ ...emptyConfig(), dialect: "openai", topK: "40" }} onChange={noop} />,
    )
    expect(screen.getByLabelText(/top k/i)).toHaveValue(40)
  })

  it("enables top P on every dialect", () => {
    for (const dialect of ["openai", "anthropic", "gemini"] as const) {
      const { unmount } = render(<Sampling config={{ ...emptyConfig(), dialect }} onChange={noop} />)
      expect(screen.getByLabelText(/top p/i)).toBeEnabled()
      unmount()
    }
  })
})
