import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "../../app"
import { FirstRun } from "./first-run"
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

/** Answers each call in turn, so a test can script setup-then-login. */
function mockSequence(...replies: Array<{ status: number; body: unknown }>) {
  let i = 0
  const calls: string[] = []
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(input))
      const r = replies[Math.min(i++, replies.length - 1)] ?? replies[0]!
      return new Response(JSON.stringify(r.body), {
        status: r.status,
        headers: { "Content-Type": "application/json" },
      })
    }),
  )
  return calls
}

async function fillSetup(token: string, password: string) {
  const user = userEvent.setup()
  await user.type(screen.getByLabelText(/setup token/i), token)
  await user.type(screen.getByLabelText(/^admin password/i), password)
  await user.click(screen.getByRole("button", { name: /set password/i }))
}

beforeEach(() => {
  vi.unstubAllGlobals()
})

describe("a fresh install", () => {
  it("offers a setup form rather than a login it cannot pass", async () => {
    // Every password would be refused, and a login form says nothing about
    // why or what to do next. The claim happens here instead.
    mockStatus(false)
    render(<App />)
    expect(await screen.findByLabelText(/setup token/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^admin password/i)).toBeInTheDocument()
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
  it("logs the operator in with the password it just set", async () => {
    // One password typed once. Setup mints no session, so the console
    // spends the new password on a real login rather than a second code
    // path that issues cookies.
    const calls = mockSequence(
      { status: 200, body: { configured: true } },
      { status: 200, body: { authenticated: true, csrf_token: "t" } },
    )
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)

    await fillSetup("the-token", "a long enough password")

    await waitFor(() => expect(onClaimed).toHaveBeenCalled())
    expect(calls[0]).toContain("/api/auth/setup")
    expect(calls[1]).toContain("/api/auth/login")
  })

  it("keeps a wrong token on the page instead of logging anyone in", async () => {
    mockSequence({ status: 401, body: { error: "invalid setup token" } })
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)

    await fillSetup("wrong", "a long enough password")

    expect(await screen.findByRole("alert")).toHaveTextContent(/setup token/i)
    expect(onClaimed).not.toHaveBeenCalled()
  })

  it("hands over to the login screen when someone else claimed it first", async () => {
    // 409. The console now has a password; this operator needs the login
    // form, not a setup form that will refuse them forever.
    mockSequence({ status: 409, body: { error: "the console has already been set up" } })
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)

    await fillSetup("the-token", "a long enough password")

    await waitFor(() => expect(onClaimed).toHaveBeenCalled())
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
