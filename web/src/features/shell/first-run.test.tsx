import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "../../app"
import { FirstRun, claimProblem } from "./first-run"
import { EmptyState, NoMatch } from "./empty-state"
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

/** Answers each call in turn, so a test can script setup-then-login, and
 *  records every request's path and parsed JSON body so a test can check
 *  what was actually sent, not just that something was. */
function mockSequence(...replies: Array<{ status: number; body: unknown }>) {
  let i = 0
  const calls: Array<{ path: string; body: unknown }> = []
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({
        path: String(input),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      })
      const r = replies[Math.min(i++, replies.length - 1)] ?? replies[0]!
      return new Response(JSON.stringify(r.body), {
        status: r.status,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
  return calls
}

async function fillClaim(username: string, password: string, confirm = password) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(/username/i), username)
  await user.type(screen.getByLabelText(/^password/i), password)
  await user.type(screen.getByLabelText(/confirm/i), confirm)
  await user.click(screen.getByRole("button", { name: /claim/i }))
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

describe("a fresh install", () => {
  it("offers a claim form rather than a login it cannot pass", async () => {
    // Every password would be refused, and a login form says nothing about
    // why or what to do next. The claim happens here instead.
    mockStatus(false)
    render(<App />)
    expect(await screen.findByLabelText(/username/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^password/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/confirm/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/token/i)).not.toBeInTheDocument()
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

describe("claiming the console", () => {
  it("posts the username, password and confirmation, then logs in with them", async () => {
    // One password typed once. Setup mints no session, so the console
    // spends the new password on a real login rather than a second code
    // path that issues cookies.
    const calls = mockSequence(
      { status: 200, body: { configured: true } },
      { status: 200, body: { authenticated: true, csrf_token: "t" } },
    )
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)

    await fillClaim("alice", "a long enough password")

    await waitFor(() => expect(onClaimed).toHaveBeenCalled())
    expect(calls[0]!.path).toContain("/api/auth/setup")
    expect(calls[0]!.body).toEqual({
      username: "alice",
      password: "a long enough password",
      confirm: "a long enough password",
    })
    expect(calls[1]!.path).toContain("/api/auth/login")
    expect(calls[1]!.body).toEqual({ username: "alice", password: "a long enough password" })
  })

  it("refuses to submit when the two passwords differ", async () => {
    const calls = mockSequence({ status: 200, body: { configured: true } })
    render(<FirstRun onClaimed={() => {}} />)

    await fillClaim("alice", "a long enough password", "a long enough passwordX")

    expect(calls).toHaveLength(0)
    expect(screen.getByText(/do not match/i)).toBeInTheDocument()
  })

  it("refuses a password under twelve characters before the round trip", async () => {
    const calls = mockSequence({ status: 200, body: { configured: true } })
    render(<FirstRun onClaimed={() => {}} />)

    await fillClaim("alice", "short", "short")

    expect(calls).toHaveLength(0)
    expect(screen.getByRole("alert")).toHaveTextContent(/at least 12/i)
  })

  it("hands over to the login screen when someone else claimed it first", async () => {
    // 409. The console now has a password; this operator needs the login
    // form, not a setup form that will refuse them forever.
    mockSequence({ status: 409, body: { error: "the console has already been set up" } })
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)

    await fillClaim("alice", "a long enough password")

    await waitFor(() => expect(onClaimed).toHaveBeenCalled())
  })

  it("shows the server's message when the claim is refused for a reason the screen didn't already check", async () => {
    mockSequence({ status: 400, body: { error: "a username is required" } })
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)

    await fillClaim("alice", "a long enough password")

    expect(await screen.findByRole("alert")).toHaveTextContent(/username is required/i)
    expect(onClaimed).not.toHaveBeenCalled()
  })
})

describe("claimProblem", () => {
  it("holds the server's floor so a typo costs no round trip", () => {
    expect(claimProblem("short", "short")).toMatch(/at least 12/i)
  })

  it("catches the mismatched confirmation", () => {
    expect(claimProblem("a long enough password", "a long enough passwordX")).toMatch(
      /do not match/i,
    )
  })

  it("passes a password that satisfies both", () => {
    expect(claimProblem("a long enough password", "a long enough password")).toBeNull()
  })
})

describe("the empty state", () => {
  it("says what the well will hold and what fills it", () => {
    // A blank panel is indistinguishable from broken equipment, which is the
    // whole reason §6.11 makes empty states first-class.
    render(
      <EmptyState
        title="Every request the gateway serves is logged here"
        hint="Point a client at the proxy and the first one appears within seconds."
      />,
    )
    expect(screen.getByText(/every request the gateway serves/i)).toBeInTheDocument()
    expect(screen.getByText(/point a client at the proxy/i)).toBeInTheDocument()
  })

  it("carries the action that fills it, when there is one", () => {
    render(
      <EmptyState title="Nothing yet" hint="Do the thing." action={<button>Do it</button>} />,
    )
    expect(screen.getByRole("button", { name: "Do it" })).toBeInTheDocument()
  })

  it("hides the wireframe from the accessibility tree", () => {
    // It teaches a shape to the eye. To a screen reader it is furniture, and
    // the title and hint already carry the whole message.
    const { container } = render(
      <EmptyState title="T" hint="H" preview={<div data-testid="ghost" />} />,
    )
    expect(container.querySelector('[aria-hidden="true"]')).toBeInTheDocument()
  })
})

describe("a filter that matches nothing", () => {
  it("is not the same state as a screen that never had data", () => {
    // Different fixes: one widens a filter, the other goes and makes data
    // exist. A screen that renders the same sentence for both sends an
    // operator to the wrong place.
    const onClear = vi.fn()
    render(<NoMatch what="requests" onClear={onClear} />)
    expect(screen.getByText(/no requests match these filters/i)).toBeInTheDocument()
    screen.getByRole("button", { name: /clear filters/i }).click()
    expect(onClear).toHaveBeenCalled()
  })

  it("offers no way back when the caller has none to give", () => {
    render(<NoMatch what="models" />)
    expect(screen.queryByRole("button", { name: /clear filters/i })).not.toBeInTheDocument()
  })
})

describe("the zero-providers state", () => {
  it("teaches the three steps rather than showing an empty grid", () => {
    render(<FirstRunProviders onAdd={() => {}} />)
    expect(screen.getByText(/give a provider an account/i)).toBeInTheDocument()
    expect(screen.getByText(/discover/i)).toBeInTheDocument()
    expect(screen.getByText(/connect/i)).toBeInTheDocument()
  })

  it("offers the action it is teaching", () => {
    const onAdd = vi.fn()
    render(<FirstRunProviders onAdd={onAdd} />)
    // Accounts, not providers: the provider set ships with the release.
    screen.getByRole("button", { name: /add accounts/i }).click()
    expect(onAdd).toHaveBeenCalled()
  })
})
