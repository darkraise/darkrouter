import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { afterEach, describe, expect, it, vi } from "vitest"
import { ChangePasswordDialog, passwordProblem, revokedText } from "./change-password-dialog"
import { onUnauthorized } from "../../lib/api"

afterEach(() => {
  vi.unstubAllGlobals()
})

function stubChange(status: number, body: unknown) {
  const fetchMock = vi.fn<typeof fetch>(async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  )
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <ChangePasswordDialog open onOpenChange={() => {}} />
    </QueryClientProvider>,
  )
}

describe("passwordProblem", () => {
  it("holds the server's floor so a typo costs no round trip", () => {
    expect(passwordProblem("short", "short")).toMatch(/12 characters/)
  })

  it("catches the mistyped confirmation", () => {
    expect(passwordProblem("correct horse battery", "correct horse bettery")).toMatch(
      /do not match/,
    )
  })

  it("passes a password that satisfies both", () => {
    expect(passwordProblem("correct horse battery", "correct horse battery")).toBeNull()
  })
})

describe("revokedText", () => {
  it("says so when there was nothing else to revoke", () => {
    // Silence here makes the next login failure elsewhere look like a fault.
    expect(revokedText(0)).toMatch(/no other sessions/i)
  })

  it("counts the sessions it signed out", () => {
    expect(revokedText(1)).toMatch(/1 other session\b/)
    expect(revokedText(3)).toMatch(/3 other sessions/)
  })
})

describe("a wrong current password", () => {
  it("shows an inline error instead of signing the operator out", async () => {
    // The server's exact wording (sessionapi.go's handleChangePassword). If
    // this ever drifts from what the dialog expects, the 401 falls through
    // to the global logout listener instead of staying on this dialog — a
    // mistyped current password would sign the operator out of the whole
    // console rather than just refusing the form.
    stubChange(401, { error: "the current password is wrong" })
    const loggedOut = vi.fn()
    const off = onUnauthorized(loggedOut)
    const user = userEvent.setup()
    mount()

    await user.type(screen.getByLabelText(/current password/i), "wrong-one")
    await user.type(screen.getByLabelText(/^new password/i), "a long enough password")
    await user.type(screen.getByLabelText(/confirm new password/i), "a long enough password")
    await user.click(screen.getByRole("button", { name: /change password/i }))

    expect(await screen.findByText(/the current password is wrong/i)).toBeInTheDocument()
    expect(loggedOut).not.toHaveBeenCalled()
    off()
  })
})
