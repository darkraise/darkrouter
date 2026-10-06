import { describe, it, expect } from "vitest"
import { render } from "@testing-library/react"
import { ProviderIcon } from "./provider-icon"
import { PROVIDER_ASSETS } from "./provider-assets"

describe("ProviderIcon", () => {
  it("draws a shipped logo for a preset with no brand mark", () => {
    const { container } = render(<ProviderIcon preset="chutes" id="chutes" name="Chutes" />)
    const img = container.querySelector("img")
    expect(img?.getAttribute("src")).toBe("/providers/chutes.svg")
  })

  it("inverts a black-ink logo so the dark canvas does not swallow it", () => {
    // The shipped set follows upstream and may hold no black-ink logo at all,
    // so the entry is the test's own.
    PROVIDER_ASSETS["ink-only"] = { file: "ink-only.svg", mono: true }
    try {
      const { container } = render(<ProviderIcon preset="ink-only" id="ink-only" />)
      expect(container.querySelector("img")?.className).toContain("provider-asset-mono")
    } finally {
      delete PROVIDER_ASSETS["ink-only"]
    }
  })

  it("prefers the brand mark over a file", () => {
    const { container } = render(<ProviderIcon preset="groq" id="groq" />)
    expect(container.querySelector("img")).toBeNull()
    expect(container.querySelector("svg")).not.toBeNull()
  })

  it("falls back to the monogram for a preset with neither", () => {
    const { container } = render(<ProviderIcon preset="agnes" id="agnes" name="Agnes" />)
    expect(container.querySelector("img")).toBeNull()
    expect(container.textContent).toBe("AG")
  })

  it("sizes the monogram from the type scale, never in pixels", () => {
    // CLAUDE.md: a pixel size opts out of the font-size axis, and the 28px
    // tile came out at 11px, below the 14px floor.
    for (const [size, step] of [[28, "text-sm"], [36, "text-sm"], [44, "text-lg"]] as const) {
      const { container, unmount } = render(<ProviderIcon id="my-thing" name="My Thing" size={size} />)
      const tile = container.firstElementChild as HTMLElement
      expect(tile.style.fontSize).toBe("")
      expect(tile.className).toContain(step)
      unmount()
    }
  })

  it("draws OVHcloud's glyph on its brand tile", () => {
    const { container } = render(<ProviderIcon preset="ovhcloud" id="ovhcloud" name="OVHcloud AI" />)
    expect(container.querySelector("img")).toBeNull()
    expect(container.querySelector("svg path")).not.toBeNull()
  })

  it("claims no brand for a provider that has no preset", () => {
    const { container } = render(<ProviderIcon id="my-thing" name="My Thing" />)
    expect(container.querySelector("img")).toBeNull()
    expect(container.textContent).toBe("MT")
  })
})
