import { render, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "darkraise-ui"
import { afterEach, describe, expect, it, vi } from "vitest"
import { AccountsCard, addAccountPasswordProblem } from "./accounts-card"

const ADMIN = { id: "u1", username: "alice", role: "admin", created_at: "2026-09-10T00:00:00Z" }
const MEMBER = { id: "u2", username: "bob", role: "member", created_at: "2026-09-10T01:00:00Z" }

afterEach(() => {
  vi.unstubAllGlobals()
})

// AccountsCard owns its own create/remove mutations rather than taking them as
// callbacks, matching every other mutation-bearing component in this console
// (change-password-dialog.tsx, the sessions section of settings-screen.tsx):
// a QueryClientProvider is therefore part of mounting it, same as those.
function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      {ui}
      <Toaster />
    </QueryClientProvider>,
  )
}

function stubFetch(status: number, body: unknown) {
  const fetchMock = vi.fn<typeof fetch>(async () =>
    new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  )
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

describe("AccountsCard", () => {
  it("lists every account with its role", () => {
    mount(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)
    expect(screen.getByText("alice")).toBeInTheDocument()
    expect(screen.getByText("bob")).toBeInTheDocument()
    expect(screen.getByText(/administrator/i)).toBeInTheDocument()
  })

  it("marks which account is mine and offers no remove for it", () => {
    mount(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)
    expect(screen.getByText(/this is you/i)).toBeInTheDocument()
    const removes = screen.getAllByRole("button", { name: /remove/i })
    expect(removes).toHaveLength(1)
  })

  it("says the console is not shared when there is one account", () => {
    mount(<AccountsCard users={[ADMIN]} me="u1" />)
    expect(screen.getByText(/only account/i)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /remove/i })).not.toBeInTheDocument()
  })

  it("warns that a removed account loses its sessions immediately", () => {
    mount(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)
    expect(screen.getByText(/signed out/i)).toBeInTheDocument()
  })
})

describe("addAccountPasswordProblem", () => {
  it("holds the server's floor", () => {
    expect(addAccountPasswordProblem("short", "short")).toMatch(/12 characters/)
  })

  it("catches the mistyped confirmation", () => {
    expect(addAccountPasswordProblem("correct horse battery", "correct horse bettery")).toMatch(
      /do not match/,
    )
  })

  it("passes a password that satisfies both", () => {
    expect(addAccountPasswordProblem("correct horse battery", "correct horse battery")).toBeNull()
  })
})

describe("adding an account", () => {
  it("posts the typed username, password and role", async () => {
    const fetchMock = stubFetch(201, {
      id: "u3",
      username: "carol",
      role: "admin",
      created_at: "2026-09-11T00:00:00Z",
    })
    const user = userEvent.setup()
    mount(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)

    await user.click(screen.getByRole("button", { name: /add an account/i }))
    await user.type(screen.getByLabelText(/username/i), "carol")
    await user.type(screen.getByLabelText(/^password$/i), "a long enough password")
    await user.type(screen.getByLabelText(/^confirm password$/i), "a long enough password")
    await user.click(screen.getByRole("combobox", { name: /role/i }))
    await user.click(screen.getByRole("option", { name: /administrator/i }))
    await user.click(screen.getByRole("button", { name: /add account/i }))

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/users",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          username: "carol",
          password: "a long enough password",
          role: "admin",
        }),
      }),
    )
  })

  it("refuses a short password without calling the server", async () => {
    // Proves the dialog still routes through the shared guard: a previous
    // task's create path stopped calling its validator with every existing
    // test still green, so the assertion here is on the network call, not
    // just on the rejection text.
    const fetchMock = stubFetch(201, {})
    const user = userEvent.setup()
    mount(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)

    await user.click(screen.getByRole("button", { name: /add an account/i }))
    await user.type(screen.getByLabelText(/username/i), "carol")
    await user.type(screen.getByLabelText(/^password$/i), "too short")
    await user.type(screen.getByLabelText(/^confirm password$/i), "too short")

    expect(screen.getByText(/12 characters/i)).toBeInTheDocument()
    expect(screen.getByRole("button", { name: /add account/i })).toBeDisabled()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})

describe("removing an account", () => {
  it("deletes by id after the confirmation is accepted", async () => {
    const fetchMock = stubFetch(204, null)
    const user = userEvent.setup()
    mount(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)

    await user.click(screen.getByRole("button", { name: /remove/i }))
    const dialog = screen.getByRole("alertdialog")
    await user.click(within(dialog).getByRole("button", { name: /remove/i }))

    expect(fetchMock).toHaveBeenCalledWith("/api/users/u2", expect.objectContaining({ method: "DELETE" }))
  })
})
