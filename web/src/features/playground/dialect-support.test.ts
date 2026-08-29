import { describe, expect, it } from "vitest"
import { CONTROLS, reasonFor, supports } from "./dialect-support"
import { DIALECTS } from "./config"

describe("what each dialect can carry", () => {
  it("lets every dialect send the three every wire shares", () => {
    for (const d of DIALECTS) {
      expect(supports(d, "temperature")).toBe(true)
      expect(supports(d, "maxTokens")).toBe(true)
      expect(supports(d, "topP")).toBe(true)
    }
  })

  it("refuses top K on the OpenAI chat wire, which has no such field", () => {
    expect(supports("openai", "topK")).toBe(false)
    expect(supports("anthropic", "topK")).toBe(true)
    expect(supports("gemini", "topK")).toBe(true)
  })

  it("refuses structured output on Anthropic, whose edge never reads it", () => {
    expect(supports("anthropic", "schema")).toBe(false)
    expect(supports("openai", "schema")).toBe(true)
    expect(supports("gemini", "schema")).toBe(true)
  })

  it("splits reasoning: an effort tier on OpenAI, a token budget elsewhere", () => {
    // Same idea, two spellings. ir.Reasoning holds both, and no dialect
    // carries both, so exactly one of the pair is live per dialect.
    expect(supports("openai", "reasoningEffort")).toBe(true)
    expect(supports("openai", "reasoningBudget")).toBe(false)
    for (const d of ["anthropic", "gemini"] as const) {
      expect(supports(d, "reasoningEffort")).toBe(false)
      expect(supports(d, "reasoningBudget")).toBe(true)
    }
  })

  it("gives a reason for every control it refuses, and none for one it allows", () => {
    // A disabled control with no reason is worse than a hidden one: it looks
    // broken rather than inapplicable.
    for (const d of DIALECTS) {
      for (const c of CONTROLS) {
        const reason = reasonFor(d, c)
        if (supports(d, c)) expect(reason).toBeNull()
        else expect(reason).toMatch(/\S/)
      }
    }
  })

  it("names the dialect that can send it, so the reason is actionable", () => {
    expect(reasonFor("openai", "topK")).toMatch(/anthropic|gemini/i)
    expect(reasonFor("anthropic", "schema")).toMatch(/openai|gemini/i)
  })

  it("covers every control for every dialect, with no gaps", () => {
    expect(CONTROLS).toHaveLength(8)
    for (const d of DIALECTS) {
      for (const c of CONTROLS) {
        expect(() => reasonFor(d, c)).not.toThrow()
      }
    }
  })

  it("answers every cell with a decision, never with undefined", () => {
    // The table is total, so the compiler catches an omitted cell first. This
    // is the runtime net for a caller that reached it past the types: an
    // undefined answer would read as unsupported-with-no-reason, which draws a
    // disabled control that cannot say why.
    for (const d of DIALECTS) {
      for (const c of CONTROLS) {
        const reason = reasonFor(d, c)
        expect(reason === null || typeof reason === "string").toBe(true)
        if (typeof reason === "string") expect(reason.length).toBeGreaterThan(0)
      }
    }
  })
})
