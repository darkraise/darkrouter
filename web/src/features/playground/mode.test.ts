import { beforeEach, describe, expect, it } from "vitest"
import { initialMode, isMode, rememberMode, storedMode } from "./mode"

describe("the playground mode", () => {
  beforeEach(() => localStorage.clear())

  it("recognises only the two modes", () => {
    expect(isMode("chat")).toBe(true)
    expect(isMode("lab")).toBe(true)
    expect(isMode("compare")).toBe(false)
    expect(isMode(undefined)).toBe(false)
  })

  it("survives a reload", () => {
    rememberMode("chat")
    expect(storedMode()).toBe("chat")
    expect(initialMode({})).toBe("chat")
  })

  it("lets the URL win over the stored preference", () => {
    // A link says where its sender meant. Silently redirecting it to the
    // reader's own last choice would make a shared link mean two things.
    rememberMode("chat")
    expect(initialMode({ mode: "lab" })).toBe("lab")
  })

  it("ignores a mode the URL made up", () => {
    rememberMode("chat")
    expect(initialMode({ mode: "compare" })).toBe("chat")
  })

  it("opens a seeded link in Lab", () => {
    // A seed is a routing investigation, not a conversation.
    rememberMode("chat")
    expect(initialMode({ seed: "01ABC" })).toBe("lab")
    // Unless the sender said otherwise, which they can only do on purpose.
    expect(initialMode({ seed: "01ABC", mode: "chat" })).toBe("chat")
  })

  it("opens in Lab when nothing has been chosen", () => {
    expect(initialMode({})).toBe("lab")
  })
})
