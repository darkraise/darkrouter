import { describe, it, expect } from "vitest"
import { compressedRows, priceLabel, priceBand, facetRow } from "./models-screen"
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
  pricing: null,
  merge_source: "models_dev",
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

describe("price rendering", () => {
  it("prints dollars per million tokens", () => {
    expect(priceLabel({ input_micros: 150000, output_micros: 600000 })).toBe(
      "$0.15 / $0.60",
    )
  })

  it("prints an em-dash for an unpriced model", () => {
    // Not $0.00: a model with no catalog price cost an unknown amount, and
    // zero would claim it was free.
    expect(priceLabel(null)).toBe("—")
  })
})

describe("the price-band facet", () => {
  it("bands by input price", () => {
    expect(priceBand({ input_micros: 150000, output_micros: 0 })).toBe("under $1/MTok")
    expect(priceBand({ input_micros: 3000000, output_micros: 0 })).toBe("$1–$5/MTok")
    expect(priceBand({ input_micros: 9000000, output_micros: 0 })).toBe("over $5/MTok")
  })

  it("bands an unpriced model as unpriced rather than as free", () => {
    expect(priceBand(null)).toBe("unpriced")
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
