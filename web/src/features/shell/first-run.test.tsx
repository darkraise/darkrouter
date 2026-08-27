import { render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "../../app"
import { EmptyLegend } from "./empty-legend"
import { FirstRunProviders } from "./first-run-providers"

function mockStatus(configured: boolean) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify({ authenticated: false, configured }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  )
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

describe("a fresh install", () => {
  it("explains itself instead of showing a login it cannot pass", async () => {
    // §12. Every password would be refused, and the form says nothing about
    // why or what to do next.
    mockStatus(false)
    render(<App />)
    expect(await screen.findByText(/no admin password set/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/password/i)).not.toBeInTheDocument()
  })

  it("shows the login once a password exists", async () => {
    mockStatus(true)
    render(<App />)
    // The login screen is what an install that can be logged into shows.
    expect(
      await screen.findByRole("button", { name: /sign in|log in/i }),
    ).toBeInTheDocument()
  })
})

describe("the empty legend", () => {
  it("says what the well will show and what to do about it", () => {
    // A blank panel is indistinguishable from broken equipment, which is the
    // whole reason §6.11 makes empty states first-class.
    render(<EmptyLegend what="Requests appear here as clients call the gateway." hint="Point a client at Connect to see the first one." />)
    expect(screen.getByText(/requests appear here/i)).toBeInTheDocument()
    expect(screen.getByText(/point a client at connect/i)).toBeInTheDocument()
  })
})

describe("the zero-providers state", () => {
  it("teaches the three steps rather than showing an empty grid", () => {
    render(<FirstRunProviders onAdd={() => {}} />)
    expect(screen.getByText(/add a provider/i)).toBeInTheDocument()
    expect(screen.getByText(/discover/i)).toBeInTheDocument()
    expect(screen.getByText(/connect/i)).toBeInTheDocument()
  })

  it("offers the action it is teaching", () => {
    const onAdd = vi.fn()
    render(<FirstRunProviders onAdd={onAdd} />)
    screen.getByRole("button", { name: /add a provider/i }).click()
    expect(onAdd).toHaveBeenCalled()
  })
})
