import { render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { App } from "../../app"
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
