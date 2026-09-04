import { beforeEach, describe, expect, it } from "vitest"
import { clearHiddenAxisStorage } from "./theme-storage"
import { themeConfig } from "./theme.config"

describe("clearing storage for axes the switcher no longer shows", () => {
  beforeEach(() => localStorage.clear())

  it("keeps the axes that are still offered", () => {
    localStorage.setItem("mode", "dark")
    localStorage.setItem("theme-accent", "violet")

    clearHiddenAxisStorage({ mode: true, accentColor: true })

    expect(localStorage.getItem("mode")).toBe("dark")
    expect(localStorage.getItem("theme-accent")).toBe("violet")
  })

  it("drops a stored value an operator can no longer reach", () => {
    localStorage.setItem("theme-radius", "sharp")
    localStorage.setItem("theme-font-size", "large")
    localStorage.setItem("theme-accent-intensity", "vivid")

    clearHiddenAxisStorage({ mode: true, accentColor: true })

    expect(localStorage.getItem("theme-radius")).toBeNull()
    expect(localStorage.getItem("theme-font-size")).toBeNull()
    // theme-accent-intensity shares a prefix with theme-accent and is a
    // different, hidden axis; prefix matching would have kept it.
    expect(localStorage.getItem("theme-accent-intensity")).toBeNull()
  })

  it("drops per-preset axis values when preset axes are not offered", () => {
    localStorage.setItem("theme-glass-blur", "heavy")
    clearHiddenAxisStorage({ mode: true, accentColor: true })
    expect(localStorage.getItem("theme-glass-blur")).toBeNull()
  })

  it("leaves storage that is not the theme's alone", () => {
    localStorage.setItem("darkrouter_saved_views", "[]")
    localStorage.setItem("layout-variant", "sidebar")

    clearHiddenAxisStorage({ mode: true, accentColor: true })

    expect(localStorage.getItem("darkrouter_saved_views")).toBe("[]")
    expect(localStorage.getItem("layout-variant")).toBe("sidebar")
  })

  it("is driven by the shipped config, so the two cannot drift", () => {
    localStorage.setItem("theme-radius", "sharp")
    localStorage.setItem("theme-accent", "coral")

    clearHiddenAxisStorage(themeConfig.switcher!.axes!)

    expect(localStorage.getItem("theme-radius")).toBeNull()
    expect(localStorage.getItem("theme-accent")).toBe("coral")
  })
})
