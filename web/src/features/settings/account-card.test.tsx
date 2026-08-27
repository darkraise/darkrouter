import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { AccountCard } from "./account-card"
import { onUnauthorized } from "../../lib/api"

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

function stubPasswordEndpoint(status: number, error: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn<typeof fetch>(async (url, init) => {
      if (url === "/api/auth/password" && (init as RequestInit)?.method === "POST") {
        return new Response(JSON.stringify({ error }), {
          status,
          headers: { "Content-Type": "application/json" },
        })
      }
      return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
    }),
  )
}

beforeEach(() => vi.unstubAllGlobals())

describe("a wrong current password", () => {
  it("surfaces inline and leaves the session alone", async () => {
    stubPasswordEndpoint(401, "invalid password")
    const loggedOut = vi.fn()
    const unsubscribe = onUnauthorized(loggedOut)

    const user = userEvent.setup()
    mount(<AccountCard />)

    await user.type(screen.getByLabelText("Current password"), "a-typo")
    await user.type(screen.getByLabelText("New password"), "long-enough-passphrase")
    await user.type(screen.getByLabelText("Confirm new password"), "long-enough-passphrase")
    await user.click(screen.getByRole("button", { name: /change password/i }))

    // The rejection is legitimate — a typo, not a dead session — so it has to
    // read in the form rather than bounce the operator to the login screen.
    expect(await screen.findByText(/invalid password/i)).toBeInTheDocument()
    expect(loggedOut).not.toHaveBeenCalled()

    unsubscribe()
  })
})

describe("a session that actually died on the same call", () => {
  it("still logs out, because only the password rejection is exempt", async () => {
    // requireSession answers this exact status with this exact message when
    // the cookie is missing or expired, before handleChangePassword ever
    // runs. The exemption above must not swallow this one too.
    stubPasswordEndpoint(401, "not authenticated")
    const loggedOut = vi.fn()
    const unsubscribe = onUnauthorized(loggedOut)

    const user = userEvent.setup()
    mount(<AccountCard />)

    await user.type(screen.getByLabelText("Current password"), "whatever")
    await user.type(screen.getByLabelText("New password"), "long-enough-passphrase")
    await user.type(screen.getByLabelText("Confirm new password"), "long-enough-passphrase")
    await user.click(screen.getByRole("button", { name: /change password/i }))

    await waitFor(() => expect(loggedOut).toHaveBeenCalled())

    unsubscribe()
  })
})
