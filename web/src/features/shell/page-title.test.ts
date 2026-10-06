import { describe, expect, it } from "vitest"
import { pageTitle } from "./page-title"

describe("the document title", () => {
  it("names the section before the app, for every destination", () => {
    // A row of browser tabs is read by its leading word, and every tab of
    // this app ends the same way.
    expect(pageTitle("/providers")).toBe("Providers · Darkrouter")
    expect(pageTitle("/")).toBe("Overview · Darkrouter")
    expect(pageTitle("/requests/01ABC")).toBe("Requests · Darkrouter")
    expect(pageTitle("/settings")).toBe("Settings · Darkrouter")
  })

  it("names the not-found state for a path it does not know", () => {
    // Not the bare app name: a background tab on a dead link should say so,
    // rather than read like the console's front page.
    expect(pageTitle("/nowhere")).toBe("Not found · Darkrouter")
  })
})
