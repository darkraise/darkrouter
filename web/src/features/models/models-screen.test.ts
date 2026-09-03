import { describe, it, expect } from "vitest"
import { compressedRows, matches, priceBand, priceMarker, facetRow } from "./models-screen"
import type { Model, Pricing } from "../../lib/api-types"

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
  pricing: null,
  merge_source: "models_dev",
  ...over,
})

const basePricing: Pricing = {
  input_micros: 150000,
  output_micros: 600000,
  price_source: "models_dev",
  price_grade: "indexed",
}

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

describe("the price-band facet", () => {
  it("bands by input price", () => {
    expect(priceBand({ ...basePricing, input_micros: 150000 })).toBe("under $1/MTok")
    expect(priceBand({ ...basePricing, input_micros: 3000000 })).toBe("$1–$5/MTok")
    expect(priceBand({ ...basePricing, input_micros: 9000000 })).toBe("over $5/MTok")
  })

  it("bands an unpriced model as unpriced rather than as free", () => {
    expect(priceBand(null)).toBe("unpriced")
  })
})

describe("the price marker", () => {
  it("marks a measured price and cautions a guessed one", () => {
    expect(priceMarker({ ...basePricing, price_grade: "measured" })).toBe("verified")
    expect(priceMarker({ ...basePricing, price_grade: "declared" })).toBe(null)
    expect(priceMarker({ ...basePricing, price_grade: "indexed" })).toBe(null)
    expect(priceMarker({ ...basePricing, price_grade: "guessed" })).toBe("caution")
  })

  it("marks nothing for an unpriced model", () => {
    expect(priceMarker(null)).toBe(null)
  })
})

describe("facet rows", () => {
  it("flattens list fields into scalars a facet can group by", () => {
    // DataTable facets take a scalar column. An array renders as one distinct
    // value per permutation, which is one facet entry per row.
    const row = facetRow({
      model: "m", providers: ["groq"], surfaces: ["llm", "embedding"],
      context_window: 128000, max_output_tokens: 4096,
      tools: true, vision: false, reasoning: false,
      inferred: false, state: "live", pricing: null, merge_source: "discovered",
    })
    expect(row.surface_list).toBe("llm, embedding")
    expect(row.caps).toBe("tools")
    expect(row.band).toBe("unpriced")
  })

  it("says none rather than blank when a model declares no capabilities", () => {
    const row = facetRow({
      model: "m", providers: [], surfaces: [], context_window: 0,
      max_output_tokens: 0, tools: false, vision: false, reasoning: false,
      inferred: false, state: "live", pricing: null, merge_source: "inferred",
    })
    // An empty facet value groups every capability-less model under a blank
    // label, which reads as a broken facet rather than as a real category.
    expect(row.caps).toBe("none")
  })
})
