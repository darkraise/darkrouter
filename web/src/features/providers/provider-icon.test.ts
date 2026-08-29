import { describe, it, expect } from "vitest"
import { BRAND_MARKS } from "./brand-marks"
import { monogramHue, monogramText } from "./provider-icon"

describe("the brand map", () => {
  it("carries the presets an operator is most likely to add", () => {
    for (const id of ["groq", "anthropic", "cerebras", "openai", "bedrock"]) {
      expect(BRAND_MARKS[id], id).toBeDefined()
    }
  })

  it("gives a gateway the mark of the vendor it fronts", () => {
    // ollama-cloud is ollama reached another way, and the mark is what tells
    // an operator which vendor's rate limit they are about to share.
    expect(BRAND_MARKS["ollama-cloud"]?.Mark).toBe(BRAND_MARKS["ollama"]?.Mark)
  })
})

describe("the monogram fallback", () => {
  it("is stable for an id, so a provider keeps its colour", () => {
    expect(monogramHue("chutes")).toBe(monogramHue("chutes"))
    expect(monogramHue("chutes")).not.toBe(monogramHue("dahl"))
  })

  it("stays inside the hue circle", () => {
    for (const id of ["a", "chutes", "very-long-provider-identifier", ""]) {
      const hue = monogramHue(id)
      expect(hue).toBeGreaterThanOrEqual(0)
      expect(hue).toBeLessThan(360)
    }
  })

  it("takes the initials of the words that carry the identity", () => {
    // "free-ai-api" is FR, not FA: ai and api are what every other gateway
    // on the list is also called.
    expect(monogramText("free-ai-api")).toBe("FR")
    expect(monogramText("electron-hub")).toBe("EH")
  })

  it("prefers the display name when there is one", () => {
    expect(monogramText("bl", "BlackBox AI")).toBe("BL")
  })

  it("falls back to the skipped words rather than to nothing", () => {
    // A provider literally called "ai" still needs a tile.
    expect(monogramText("ai")).toBe("AI")
  })
})
