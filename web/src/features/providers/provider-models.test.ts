import { describe, it, expect } from "vitest"
import type { Model } from "../../lib/api-types"
import { contextLabel, filterModels } from "./provider-models"

const model = (over: Partial<Model> & { model: string }): Model => ({
  providers: ["groq"],
  surfaces: ["llm"],
  context_window: 8192,
  max_output_tokens: 4096,
  tools: false,
  vision: false,
  reasoning: false,
  inferred: false,
  state: "live",
  pricing: null,
  free_tier: null,
  merge_source: "models_dev",
  ...over,
})

describe("contextLabel", () => {
  it("reads tokens in the units the vendor's own page uses", () => {
    expect(contextLabel(131072)).toBe("131k")
    expect(contextLabel(1_000_000)).toBe("1M")
    expect(contextLabel(1_500_000)).toBe("1.5M")
    expect(contextLabel(512)).toBe("512")
  })

  it("says nothing rather than zero when the catalogue has no window", () => {
    // 0 would claim a model that accepts no input at all.
    expect(contextLabel(0)).toBe("—")
  })
})

describe("filterModels", () => {
  const models = [
    model({ model: "llama-3.3-70b" }),
    model({ model: "whisper-large", surfaces: ["transcription"] }),
  ]

  it("matches on the model id", () => {
    expect(filterModels(models, "llama").map((m) => m.model)).toEqual(["llama-3.3-70b"])
  })

  it("matches on the surface, which is how an operator asks what can embed", () => {
    expect(filterModels(models, "transcription").map((m) => m.model)).toEqual(["whisper-large"])
  })

  it("treats an empty box as no filter rather than as no match", () => {
    expect(filterModels(models, "   ")).toHaveLength(2)
  })
})
