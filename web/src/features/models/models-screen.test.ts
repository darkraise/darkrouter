import { describe, it, expect } from "vitest"
import {
  compressedRows,
  matches,
  priceBand,
  priceMarker,
  freeLabel,
  tierWarning,
  facetRow,
} from "./models-screen"
import type { FreeTier, Model, Pricing } from "../../lib/api-types"

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
  free_tier: null,
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
      inferred: false, state: "live", pricing: null, free_tier: null, merge_source: "discovered",
    })
    expect(row.surface_list).toBe("llm, embedding")
    expect(row.caps).toBe("tools")
    expect(row.band).toBe("unpriced")
  })

  it("says none rather than blank when a model declares no capabilities", () => {
    const row = facetRow({
      model: "m", providers: [], surfaces: [], context_window: 0,
      max_output_tokens: 0, tools: false, vision: false, reasoning: false,
      inferred: false, state: "live", pricing: null, free_tier: null,
      merge_source: "inferred",
    })
    // An empty facet value groups every capability-less model under a blank
    // label, which reads as a broken facet rather than as a real category.
    expect(row.caps).toBe("none")
  })
})

describe("freeLabel", () => {
  it("names the allowance and its period", () => {
    expect(freeLabel({ free_type: "recurring-daily", monthly_tokens: 24_000_000,
      credit_tokens: 0, pool_key: "groq", tos: "ok", opt_in_required: false }))
      .toBe("free · ~24M tokens/day")
    expect(freeLabel({ free_type: "recurring-monthly", monthly_tokens: 1_000_000,
      credit_tokens: 0, pool_key: "", tos: "ok", opt_in_required: false }))
      .toBe("free · ~1M tokens/month")
    expect(freeLabel({ free_type: "one-time-initial", monthly_tokens: 0,
      credit_tokens: 200_000_000, pool_key: "", tos: "caution", opt_in_required: false }))
      .toBe("free · ~200M tokens once")
  })
  it("carries a one-off credit grant the same way as an initial one", () => {
    expect(freeLabel({ free_type: "recurring-credit", monthly_tokens: 0,
      credit_tokens: 5_000_000, pool_key: "", tos: "ok", opt_in_required: false }))
      .toBe("free · ~5M tokens once")
  })
  it("says free without a figure when the allowance is uncapped", () => {
    expect(freeLabel({ free_type: "recurring-uncapped", monthly_tokens: 0,
      credit_tokens: 0, pool_key: "", tos: "ok", opt_in_required: false })).toBe("free")
  })
  it("says free without a figure when a live tier leaves the figure out", () => {
    // free_models.json carries recurring-daily rows with no monthly_tokens and
    // recurring-credit rows with no credit_tokens. A zero there is unquantified,
    // not an allowance of nothing, so "~0 tokens/day" would be a lie.
    expect(freeLabel({ free_type: "recurring-daily", monthly_tokens: 0,
      credit_tokens: 0, pool_key: "groq", tos: "ok", opt_in_required: false })).toBe("free")
    expect(freeLabel({ free_type: "recurring-credit", monthly_tokens: 0,
      credit_tokens: 0, pool_key: "", tos: "ok", opt_in_required: false })).toBe("free")
  })
  it("is absent for a withdrawn tier and for no tier at all", () => {
    expect(freeLabel({ free_type: "discontinued", monthly_tokens: 0,
      credit_tokens: 0, pool_key: "", tos: "unknown", opt_in_required: false })).toBeNull()
    expect(freeLabel(null)).toBeNull()
  })
})

describe("tierWarning", () => {
  const tier = (over: Partial<FreeTier> = {}): FreeTier => ({
    free_type: "recurring-daily",
    monthly_tokens: 24_000_000,
    credit_tokens: 0,
    pool_key: "groq",
    tos: "avoid",
    opt_in_required: true,
    ...over,
  })
  it("warns while the router is still refusing a provider over the tier", () => {
    expect(tierWarning(tier())).toBe(
      "free tier not sanctioned by the vendor — the router skips any provider " +
        "serving it that you have not allowed",
    )
  })
  it("goes quiet once every provider serving it has been allowed", () => {
    // The verdict stays avoid after an opt-in — it is the vendor's, not the
    // operator's — and the router uses the model regardless. A row that read
    // the verdict alone went on telling an operator to throw a switch they
    // had already thrown, which no reload could clear.
    expect(tierWarning(tier({ opt_in_required: false }))).toBeNull()
  })
  it("stays quiet for every other verdict and for no tier at all", () => {
    // The vocabulary is closed: upstream grades a tier ok, caution, ambiguous,
    // avoid or unknown. Only the last one the vendor refuses is ever gated.
    for (const tos of ["ok", "caution", "ambiguous", "unknown"]) {
      expect(tierWarning(tier({ tos, opt_in_required: false }))).toBeNull()
    }
    expect(tierWarning(null)).toBeNull()
  })
})
