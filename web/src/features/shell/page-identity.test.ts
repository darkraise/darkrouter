import { describe, expect, it } from "vitest"
import { identityFor } from "./page-identity"

describe("the page identity in the app header", () => {
  it("names every destination in the rail", () => {
    // The header is now the only place a screen's name appears, so a
    // destination missing from the table renders a bar with nothing in it.
    for (const path of [
      "/",
      "/requests",
      "/usage",
      "/providers",
      "/models",
      "/routing",
      "/playground",
      "/connect",
      "/settings",
    ]) {
      expect(identityFor(path), path).toBeDefined()
    }
  })

  it("keeps a detail page inside its section", () => {
    // /providers/groq is still Providers. The provider's own name is the
    // heading on the page; the header says which section it is in.
    expect(identityFor("/providers/groq")?.title).toBe("Providers")
    expect(identityFor("/requests/01ABC")?.title).toBe("Requests")
  })

  it("matches the root exactly, since it prefixes everything", () => {
    expect(identityFor("/")?.title).toBe("Overview")
    expect(identityFor("/usage")?.title).toBe("Usage")
  })

  it("says nothing rather than guessing for a path it does not know", () => {
    expect(identityFor("/nowhere")).toBeUndefined()
  })
})
