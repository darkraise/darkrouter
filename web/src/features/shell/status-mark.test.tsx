import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { RequestStatus } from "./status-mark"

describe("RequestStatus", () => {
  it("marks a served request as served", () => {
    render(<RequestStatus status="success" />)
    expect(screen.getByTitle("served")).toBeInTheDocument()
  })

  it("marks an abandoned request apart from a failed one", () => {
    // A client that hung up is not a failure, so it must not wear the
    // failure's shape or colour.
    const { container } = render(
      <>
        <RequestStatus status="cancelled" />
        <RequestStatus status="error" />
      </>,
    )
    const [cancelled, failed] = Array.from(container.querySelectorAll("span[title]"))
    expect(cancelled).toHaveAttribute("title", "cancelled by the client")
    expect(cancelled?.className).not.toContain("destructive")
    expect(failed).toHaveAttribute("title", "error")
    expect(failed?.className).toContain("destructive")
    expect(cancelled?.innerHTML).not.toBe(failed?.innerHTML)
  })
})
