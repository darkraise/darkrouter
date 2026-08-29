import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { Transcript } from "./transcript"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

describe("the transcript before anything is said", () => {
  it("says a model is what is missing", () => {
    // An empty panel cannot be told apart from one that failed to load.
    render(<Transcript messages={[]} routes={{}} busy={false} model="" />)
    expect(screen.getByText(/name a model to send to/i)).toBeInTheDocument()
  })

  it("says it is ready once one is named", () => {
    render(<Transcript messages={[]} routes={{}} busy={false} model="fast" />)
    expect(screen.getByText(/ready to send to fast/i)).toBeInTheDocument()
  })
})

describe("the transcript with turns", () => {
  it("renders each turn in order", () => {
    render(
      <Transcript
        messages={[
          { role: "user", content: "why did that fail over" },
          { role: "assistant", content: "the alias resolved" },
        ]}
        routes={{}}
        busy={false}
        model="fast"
      />,
    )
    expect(screen.getByText(/why did that fail over/)).toBeInTheDocument()
    expect(screen.getByText(/the alias resolved/)).toBeInTheDocument()
  })

  it("shows the seed note when a run was seeded from a trace", () => {
    // capture.bodies has a retention sweep and no writer, so a seeded run
    // restores model and dialect and nothing else. Left unsaid, the operator
    // discovers it from a transcript that is silently empty.
    render(
      <Transcript
        messages={[]} routes={{}} busy={false} model="fast"
        seedNote="Seeded from trace 01ABC: model and dialect carried over."
      />,
    )
    expect(screen.getByText(/model and dialect carried over/i)).toBeInTheDocument()
  })
})
