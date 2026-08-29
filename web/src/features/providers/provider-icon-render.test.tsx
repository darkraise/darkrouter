import { describe, it, expect } from "vitest"
import { render } from "@testing-library/react"
import { ProviderIcon } from "./provider-icon"

describe("ProviderIcon", () => {
  it("draws a shipped logo for a preset with no brand mark", () => {
    const { container } = render(<ProviderIcon preset="chutes" id="chutes" name="Chutes" />)
    const img = container.querySelector("img")
    expect(img?.getAttribute("src")).toBe("/providers/chutes.svg")
  })

  it("inverts a black-ink logo so the dark canvas does not swallow it", () => {
    const { container } = render(<ProviderIcon preset="bazaarlink" id="bazaarlink" />)
    expect(container.querySelector("img")?.className).toContain("provider-asset-mono")
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

  it("claims no brand for a provider that has no preset", () => {
    const { container } = render(<ProviderIcon id="my-thing" name="My Thing" />)
    expect(container.querySelector("img")).toBeNull()
    expect(container.textContent).toBe("MT")
  })
})
