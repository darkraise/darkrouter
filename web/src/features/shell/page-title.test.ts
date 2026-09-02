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

  it("falls back to the app alone for a path it does not know", () => {
    expect(pageTitle("/nowhere")).toBe("Darkrouter")
  })
})
