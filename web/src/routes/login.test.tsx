import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { LoginScreen } from "./login"
import { onUnauthorized } from "../lib/api"

afterEach(() => {
  vi.unstubAllGlobals()
})

function stubLogin(status: number, body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>(async () =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  )
}

describe("the login screen", () => {
  it("keeps a wrong password on the page, inline, with the field focused", async () => {
    // The server answers 401 to a wrong password. That is a rejection of
    // this attempt, not a dead session: the global logout listener must not
    // fire, and the operator's next keystroke should land in the field.
    stubLogin(401, { error: "invalid password" })
    const seen = vi.fn()
    const off = onUnauthorized(seen)
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={() => {}} />)

    await user.type(screen.getByPlaceholderText(/admin password/i), "nope")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong password. Try again.")
    expect(seen).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByPlaceholderText(/admin password/i)).toHaveFocus())
    off()
  })

  it("reports the session on success", async () => {
    stubLogin(200, { authenticated: true, csrf_token: "t" })
    const onAuthenticated = vi.fn()
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={onAuthenticated} />)

    await user.type(screen.getByPlaceholderText(/admin password/i), "correct horse")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    await waitFor(() => expect(onAuthenticated).toHaveBeenCalled())
  })

  it("shows the server's own message for any other failure", async () => {
    stubLogin(503, { error: "database unavailable" })
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={() => {}} />)

    await user.type(screen.getByPlaceholderText(/admin password/i), "x")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    expect(await screen.findByRole("alert")).toHaveTextContent("database unavailable")
  })
})
