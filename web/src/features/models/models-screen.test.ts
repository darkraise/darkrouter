import { describe, it, expect } from "vitest"
import { compressedRows, matches } from "./models-screen"
import type { Model } from "../../lib/api-types"

const model = (over: Partial<Model> & { model: string }): Model => ({
  providers: ["groq"],
  surfaces: ["llm"],
  context_window: 0,
  max_output_tokens: 0,
  tools: false,
  vision: false,
  reasoning: false,
  inferred: false,
  state: "live",
  ...over,
})

describe("the compressed ladder", () => {
  it("never fills a mark", () => {
    // Nothing has been sent. A filled mark would claim an attempt happened.
    const rows = compressedRows(model({ model: "m", providers: ["a", "b"] }))
    expect(rows.map((r) => r.mark)).toEqual(["skipped", "skipped"])
  })

  it("keeps catalog order, which is the order the router would walk", () => {
    const rows = compressedRows(model({ model: "m", providers: ["first", "second"] }))
    expect(rows.map((r) => r.target)).toEqual(["first/m", "second/m"])
  })
})

describe("model filtering", () => {
  it("matches a model by substring, case-insensitively", () => {
    expect(matches(model({ model: "GPT-OSS-120b" }), { model: "oss" })).toBe(true)
    expect(matches(model({ model: "llama" }), { model: "oss" })).toBe(false)
  })

  it("matches any provider that serves the model", () => {
    const m = model({ model: "m", providers: ["groq", "together"] })
    expect(matches(m, { provider: "together" })).toBe(true)
    expect(matches(m, { provider: "openai" })).toBe(false)
  })

  it("treats an absent filter as no filter", () => {
    expect(matches(model({ model: "m" }), {})).toBe(true)
  })
})
