import { act, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { App } from "../app"
import { router } from "./router"

// A file of its own: the router is a module singleton, and one left on a
// screen by another file's test is not one a fresh navigation can rely on.

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })

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

describe("logging out with an unsaved edit", () => {
  // The logout is a POST and then a reload, and only the reload met the
  // guard's beforeunload prompt -- after the session had already ended. "Stay"
  // kept a draft that the next Save turned into a 401 and the login screen.
  async function dirtySettings() {
    serve({
      "/api/config": () => ({
        valid: true,
        warnings: [],
        pending_restart: [],
        values: { "log.retention": "72h" },
        fields: { "log.retention": { source: "database", hot_reloadable: true, kind: "duration" } },
      }),
      "/api/sessions": () => ({ sessions: [] }),
      "/api/users": () => ({ users: [], me: "" }),
      "/api/catalog/sync": () => ({ running: false, run: 0 }),
    })
    await act(() => router.navigate({ to: "/settings" }))
    const user = userEvent.setup()
    render(<App />)
    const box = await screen.findByLabelText("Keep request records for", {}, { timeout: 8000 })
    await user.clear(box)
    await user.type(box, "96h")
    await user.click(screen.getByRole("button", { name: "Account menu" }))
    return { user, box }
  }

  const logoutPosts = () =>
    vi.mocked(fetch).mock.calls.filter(([url]) => String(url).includes("/api/auth/logout"))

  it("asks first; staying keeps the session and the draft, and leaving ends it", { timeout: 15000 }, async () => {
    const confirm = vi.fn(() => false)
    vi.stubGlobal("confirm", confirm)
    const { user, box } = await dirtySettings()
    await user.click(await screen.findByRole("menuitem", { name: "Log out" }))

    expect(confirm).toHaveBeenCalledWith("You have unsaved changes. Log out and discard them?")
    expect(logoutPosts()).toHaveLength(0)
    expect(box).toHaveValue("96h")

    // Asked again, and this time the draft goes: the session ends, and the
    // reload that follows does not put the question a second time.
    confirm.mockReturnValue(true)
    await user.click(screen.getByRole("button", { name: "Account menu" }))
    await user.click(await screen.findByRole("menuitem", { name: "Log out" }))
    await waitFor(() => expect(logoutPosts()).toHaveLength(1))
    expect(confirm).toHaveBeenCalledTimes(2)
  })
})
