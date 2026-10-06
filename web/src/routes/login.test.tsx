import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { LoginScreen } from "./login"
import { onUnauthorized } from "../lib/api"

afterEach(() => {
  vi.unstubAllGlobals()
})

function stubLogin(status: number, body: unknown) {
  const fetchMock = vi.fn<typeof fetch>(async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  )
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

describe("the login screen", () => {
  it("sends the username with the password", async () => {
    const fetchMock = stubLogin(200, { authenticated: true, csrf_token: "t" })
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={() => {}} />)

    await user.type(screen.getByLabelText(/username/i), "alice")
    await user.type(screen.getByLabelText(/^password/i), "correct-horse-battery")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    await waitFor(() => expect(fetchMock).toHaveBeenCalled())
    const [path, init] = fetchMock.mock.calls[0]!
    expect(String(path)).toContain("/api/auth/login")
    expect(JSON.parse(String(init?.body))).toEqual({
      username: "alice",
      password: "correct-horse-battery",
    })
  })

  it("keeps a wrong credential on the page, inline, with the field focused", async () => {
    // The server answers 401 to a wrong username or password. That is a
    // rejection of this attempt, not a dead session: the global logout
    // listener must not fire, and the operator's next keystroke should land
    // in the field.
    stubLogin(401, { error: "invalid username or password" })
    const seen = vi.fn()
    const off = onUnauthorized(seen)
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={() => {}} />)

    await user.type(screen.getByLabelText(/username/i), "nobody")
    await user.type(screen.getByLabelText(/^password/i), "nope")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    expect(await screen.findByRole("alert")).toHaveTextContent("invalid username or password")
    expect(seen).not.toHaveBeenCalled()
    await waitFor(() => expect(screen.getByLabelText(/^password/i)).toHaveFocus())
    off()
  })

  it("keeps one message for every refusal", async () => {
    // The screen must not add its own wording that distinguishes a wrong
    // username, a wrong password, or an unclaimed console.
    stubLogin(401, { error: "invalid username or password" })
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={() => {}} />)

    await user.type(screen.getByLabelText(/username/i), "alice")
    await user.type(screen.getByLabelText(/^password/i), "wrong-but-well-formed")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    expect(await screen.findByText(/invalid username or password/i)).toBeInTheDocument()
  })

  it("reports the session on success", async () => {
    stubLogin(200, { authenticated: true, csrf_token: "t" })
    const onAuthenticated = vi.fn()
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={onAuthenticated} />)

    await user.type(screen.getByLabelText(/username/i), "alice")
    await user.type(screen.getByLabelText(/^password/i), "correct horse")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    await waitFor(() => expect(onAuthenticated).toHaveBeenCalled())
  })

  it("shows the server's own message for any other failure", async () => {
    stubLogin(503, { error: "database unavailable" })
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={() => {}} />)

    await user.type(screen.getByLabelText(/username/i), "alice")
    await user.type(screen.getByLabelText(/^password/i), "x")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    expect(await screen.findByRole("alert")).toHaveTextContent("database unavailable")
  })

  it("keeps sign in disabled until both fields are filled", async () => {
    render(<LoginScreen onAuthenticated={() => {}} />)
    const user = userEvent.setup()

    expect(screen.getByRole("button", { name: /sign in/i })).toBeDisabled()
    await user.type(screen.getByLabelText(/username/i), "alice")
    expect(screen.getByRole("button", { name: /sign in/i })).toBeDisabled()
    await user.type(screen.getByLabelText(/^password/i), "x")
    expect(screen.getByRole("button", { name: /sign in/i })).toBeEnabled()
  })

  it("names the tab for the sign-in form", () => {
    document.title = "Requests · Darkrouter"
    render(<LoginScreen onAuthenticated={() => {}} />)
    expect(document.title).toBe("Sign in · Darkrouter")
  })

  it("explains an ended session, and only then", () => {
    const { unmount } = render(<LoginScreen onAuthenticated={() => {}} reason="expired" />)
    expect(screen.getByText(/session has ended/i)).toHaveAttribute("role", "status")
    unmount()
    render(<LoginScreen onAuthenticated={() => {}} />)
    expect(screen.queryByText(/session has ended/i)).not.toBeInTheDocument()
  })

  it("says how long a rate-limited sign-in has to wait, without calling the password wrong", async () => {
    // The limiter refuses the attempt, not the credential: the password may
    // well be right. The wait is in Retry-After, and the server sends it.
    const fetchMock = vi.fn<typeof fetch>(
      async () =>
        new Response(JSON.stringify({ error: "too many login attempts; try again later" }), {
          status: 429,
          headers: { "Content-Type": "application/json", "Retry-After": "42" },
        }),
    )
    vi.stubGlobal("fetch", fetchMock)
    const user = userEvent.setup()
    render(<LoginScreen onAuthenticated={() => {}} />)

    await user.type(screen.getByLabelText(/username/i), "admin")
    await user.type(screen.getByLabelText(/^password/i), "right-all-along")
    await user.click(screen.getByRole("button", { name: /sign in/i }))

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Too many sign-in attempts. Try again in 42 seconds.",
    )
    expect(screen.getByLabelText(/username/i)).not.toHaveAttribute("aria-invalid")
    expect(screen.getByLabelText(/^password/i)).not.toHaveAttribute("aria-invalid")
    // Retrying early only earns another 429, so the button waits too.
    expect(screen.getByRole("button", { name: /try again in 42 s/i })).toBeDisabled()
  })

  it("opens sign-in again once the wait has passed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      vi.stubGlobal(
        "fetch",
        vi.fn<typeof fetch>(
          async () =>
            new Response(JSON.stringify({ error: "too many login attempts; try again later" }), {
              status: 429,
              headers: { "Content-Type": "application/json", "Retry-After": "2" },
            }),
        ),
      )
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
      render(<LoginScreen onAuthenticated={() => {}} />)
      await user.type(screen.getByLabelText(/username/i), "admin")
      await user.type(screen.getByLabelText(/^password/i), "pw")
      await user.click(screen.getByRole("button", { name: /sign in/i }))
      expect(await screen.findByRole("button", { name: /try again in 2 s/i })).toBeDisabled()

      await act(() => vi.advanceTimersByTimeAsync(2100))
      expect(screen.getByRole("button", { name: /sign in/i })).toBeEnabled()
    } finally {
      vi.useRealTimers()
    }
  })
})
