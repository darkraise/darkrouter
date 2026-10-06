import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { App } from "../app"
import { router } from "./router"
import { requestsSearch } from "../features/usage/usage-screen"

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })

const row = (id: string, model: string) => ({
  id,
  source: "proxy",
  ts_ms: Date.now(),
  dialect: "openai",
  surface: "llm",
  model,
  status: "success",
  tokens_in: 0,
  tokens_out: 0,
  cache_read_tokens: 0,
  cost_micros: null,
  ttft_ms: null,
  total_ms: null,
  attempts: 1,
})

const trace = (id: string) => ({
  id,
  ts_ms: 0,
  dialect: "openai",
  surface: "llm",
  model: "m",
  provider: "groq",
  status: "success",
  tokens_in: 1,
  tokens_out: 2,
  cache_read_tokens: 0,
  cost_micros: 0,
  ttft_ms: 10,
  total_ms: 20,
  attempts: [],
  candidates: [],
  skips: [],
})

/** The console behind a signed-in session, with each endpoint answered by
 *  path; anything a test does not name answers with an empty object. */
function serve(routes: Record<string, (url: string) => unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input)
      if (url.includes("/api/auth/status")) {
        return json({ authenticated: true, configured: true, csrf_token: "t" })
      }
      for (const [prefix, answer] of Object.entries(routes)) {
        if (url.includes(prefix)) return json(answer(url))
      }
      return json({})
    }),
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("the URL the router writes", () => {
  it("carries a numeric-looking string unquoted", () => {
    // The Usage table's click-through into Requests. TanStack's default
    // encoder wrote since_ms="1788739200000", the quotes reached the API, and
    // every one of those links opened on a 400.
    const search = requestsSearch("model", "mock-error", "2026-09-06", 30)
    const location = router.buildLocation({ to: "/requests", search })
    expect(location.searchStr).toMatch(/[?&]since_ms=\d+(&|$)/)
    expect(location.href).not.toContain("%22")
    expect(location.searchStr).toContain("model=mock-error")
  })

  it("reads the same string back, rather than a number the schema drops", () => {
    // A value parsed as a number was dropped by the root route's
    // string-only validateSearch, so a trace opened under a since_ms filter
    // came back without it.
    const location = router.buildLocation({
      to: "/requests",
      search: { since_ms: "1788739200000", provider: "groq" },
    })
    expect(router.options.parseSearch(location.searchStr)).toEqual({
      since_ms: "1788739200000",
      provider: "groq",
    })
  })

  it("keeps the trace drawer's playground link plain too", () => {
    const location = router.buildLocation({
      to: "/playground",
      search: { mode: "chat", seed: "0123" },
    })
    expect(location.searchStr).toBe("?mode=chat&seed=0123")
  })
})

describe("opening a trace over the Requests screen", () => {
  it("keeps the log the operator had loaded when the trace closes", async () => {
    serve({
      "/api/requests/r2": () => trace("r2"),
      "/api/requests": (url) =>
        url.includes("cursor=c1")
          ? { requests: [row("r2", "older-model")] }
          : { requests: [row("r1", "newer-model")], next_cursor: "c1" },
    })
    await act(() => router.navigate({ to: "/requests" }))
    const user = userEvent.setup()
    render(<App />)

    await user.click(await screen.findByRole("button", { name: "Load more" }, { timeout: 5000 }))
    const older = await screen.findByText("older-model")
    const open = within(older.closest("tr")!).getByRole("button", { name: "Open" })

    await user.click(open)
    await screen.findByRole("dialog", { name: "r2" })
    await user.keyboard("{Escape}")
    await waitFor(() => expect(router.state.location.pathname).toBe("/requests"))
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument())

    // The second page is still there -- a remounted screen starts again from
    // the first -- and focus is back on the button that opened the trace.
    expect(screen.getByText("older-model")).toBeInTheDocument()
    expect(open).toBeInTheDocument()
    expect(open).toHaveFocus()
  })
})
