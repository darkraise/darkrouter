import { render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "../../app"

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
