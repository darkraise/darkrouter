import { beforeEach, describe, expect, it } from "vitest"
import { initialMode, isMode, readMode, rememberMode, storedMode } from "./mode"

describe("the playground mode", () => {
  beforeEach(() => localStorage.clear())

  it("recognises the three surfaces", () => {
    expect(isMode("chat")).toBe(true)
    expect(isMode("compare")).toBe(true)
    expect(isMode("auxiliary")).toBe(true)
    expect(isMode("lab")).toBe(false)
    expect(isMode(undefined)).toBe(false)
  })

  it("lands a retired name where it used to go", () => {
    // Lab's Single surface and its request pane are Chat now. A bookmark or a
    // stored preference still saying "lab" is a reader who meant that screen,
    // not a value to throw away.
    expect(readMode("lab")).toBe("chat")
    expect(readMode("count")).toBe("auxiliary")
    expect(readMode("nonsense")).toBeUndefined()
    rememberMode("compare")
    expect(initialMode({ mode: "lab" })).toBe("chat")
  })

  it("survives a reload", () => {
    rememberMode("compare")
    expect(storedMode()).toBe("compare")
    expect(initialMode({})).toBe("compare")
  })

  it("lets the URL win over the stored preference", () => {
    // A link says where its sender meant. Silently redirecting it to the
    // reader's own last choice would make a shared link mean two things.
    rememberMode("chat")
    expect(initialMode({ mode: "auxiliary" })).toBe("auxiliary")
  })

  it("ignores a mode the URL made up", () => {
    rememberMode("compare")
    expect(initialMode({ mode: "nonsense" })).toBe("compare")
  })

  it("opens a seeded link in Chat, which is where the request pane is now", () => {
    // A seed carries a model and a dialect into the settings that send them,
    // and those live on Chat since Lab was folded into it.
    expect(initialMode({ seed: "01ABC" })).toBe("chat")
    // A stored preference still wins: the seed no longer picks a surface.
    rememberMode("compare")
    expect(initialMode({ seed: "01ABC" })).toBe("compare")
  })

  it("opens in Chat when nothing has been chosen", () => {
    expect(initialMode({})).toBe("chat")
  })

  it("treats blocked storage as no preference rather than as an error", () => {
    // A private window and a browser set to block site data both throw on
    // read and on write. The mode is a convenience; losing it must not take
    // the screen down with it.
    const store = globalThis.localStorage
    const throws = {
      getItem: () => { throw new Error("blocked") },
      setItem: () => { throw new Error("blocked") },
    }
    Object.defineProperty(globalThis, "localStorage", {
      value: throws, configurable: true, writable: true,
    })
    try {
      expect(storedMode()).toBeUndefined()
      expect(initialMode({})).toBe("chat")
      expect(() => rememberMode("chat")).not.toThrow()
    } finally {
      Object.defineProperty(globalThis, "localStorage", {
        value: store, configurable: true, writable: true,
      })
    }
  })
})
