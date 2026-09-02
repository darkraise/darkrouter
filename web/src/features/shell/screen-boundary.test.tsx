import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, it, expect, vi, afterEach } from "vitest"
import { ScreenBoundary } from "./screen-boundary"

function Boom(): React.ReactNode {
  throw new Error("the usage endpoint answered with an object")
}

// Keeps failing until the reset flips it: React retries a concurrent render
// synchronously before it reaches a boundary, so a component that throws
// once would recover on its own and never exercise the fallback.
let broken = true
function UntilReset(): React.ReactNode {
  if (broken) throw new Error("still broken")
  return <p>Recovered</p>
}

afterEach(() => {
  vi.restoreAllMocks()
})

describe("ScreenBoundary", () => {
  it("keeps a failing screen from unmounting everything around it", () => {
    // React logs the caught error; silencing it keeps the run readable without
    // hiding the assertion below.
    vi.spyOn(console, "error").mockImplementation(() => {})

    render(
      <div>
        <nav>Rail</nav>
        <ScreenBoundary>
          <Boom />
        </ScreenBoundary>
      </div>,
    )

    // The rail is how an operator reaches a screen that still works, so it is
    // the thing that must survive.
    expect(screen.getByText("Rail")).toBeInTheDocument()
    expect(screen.getByText(/could not render/i)).toBeInTheDocument()
  })

  it("says what went wrong rather than only that something did", () => {
    vi.spyOn(console, "error").mockImplementation(() => {})
    render(
      <ScreenBoundary>
        <Boom />
      </ScreenBoundary>,
    )
    expect(
      screen.getByText(/the usage endpoint answered with an object/),
    ).toBeInTheDocument()
  })

  it("tries again from the fallback, and clears the error when that works", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {})
    broken = true
    const onReset = vi.fn(() => {
      broken = false
    })
    render(
      <ScreenBoundary onReset={onReset}>
        <UntilReset />
      </ScreenBoundary>,
    )
    expect(screen.getByText(/could not render/i)).toBeInTheDocument()
    await userEvent.setup().click(screen.getByRole("button", { name: /try again/i }))
    expect(screen.getByText("Recovered")).toBeInTheDocument()
    expect(onReset).toHaveBeenCalled()
  })

  it("renders its children untouched when nothing throws", () => {
    render(
      <ScreenBoundary>
        <p>Overview</p>
      </ScreenBoundary>,
    )
    expect(screen.getByText("Overview")).toBeInTheDocument()
  })
})
