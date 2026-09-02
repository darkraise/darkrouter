import { describe, expect, it } from "vitest"
import { moveTarget, previewRows, validateChain } from "./routing-screen"
import type { RoutePreview } from "../../lib/api-types"

describe("moveTarget", () => {
  it("reorders without mutating the original chain", () => {
    const chain = ["a", "b", "c"]
    expect(moveTarget(chain, 2, 0)).toEqual(["c", "a", "b"])
    expect(chain).toEqual(["a", "b", "c"])
  })

  it("is a no-op when a target is dropped on itself", () => {
    expect(moveTarget(["a", "b"], 1, 1)).toEqual(["a", "b"])
  })

  it("ignores an index outside the chain", () => {
    // A drop outside the list is a cancelled drag, not a reorder to position
    // minus one.
    expect(moveTarget(["a", "b"], 0, 5)).toEqual(["a", "b"])
  })
})

describe("validateChain", () => {
  it("names a qualified target whose provider is not configured", () => {
    const problems = validateChain(["groq/m-a", "ghost/m-b"], ["groq"])
    expect(problems).toHaveLength(1)
    expect(problems[0]).toMatch(/^ghost\/m-b: no provider named ghost is configured/)
  })

  it("accepts a bare model target, which any provider may serve", () => {
    expect(validateChain(["m-a"], ["groq"])).toEqual([])
  })

  it("refuses an empty chain, which routes nowhere", () => {
    expect(validateChain([], ["groq"])).toEqual([
      "an alias with no targets routes nowhere",
    ])
  })

  it("reports every problem rather than stopping at the first", () => {
    // A save that fixes one typo and fails on the next reads as the editor
    // refusing arbitrarily.
    expect(validateChain(["x/m", "y/m"], ["groq"])).toHaveLength(2)
  })
})

describe("previewRows", () => {
  const candidate = (provider_id: string, key_id: string, model = "m") => ({
    provider_id, key_id, model, kind: "openaicompat", inferred: false,
  })
  const preview = (over: Partial<RoutePreview>): RoutePreview => ({
    candidates: [], skips: [], ...over,
  })

  it("collapses one provider's credentials into one rung with a count", () => {
    // The endpoint lists every (provider, credential, model) the router
    // would try. Three keys on groq are three rows saying "groq/m", which
    // reads as three providers until the operator notices the keys differ.
    const rows = previewRows(
      preview({ candidates: [candidate("groq", "k1"), candidate("groq", "k2"), candidate("nebius", "k3")] }),
    )
    expect(rows.map((r) => r.target)).toEqual(["groq/m", "nebius/m"])
    expect(rows.map((r) => r.rank)).toEqual([1, 2])
    expect(rows[0]?.reasonProse).toBe("× 2 credentials")
    expect(rows[1]?.reasonProse).toBeUndefined()
  })

  it("keeps the router's order: the first key decides where the rung sits", () => {
    const rows = previewRows(
      preview({ candidates: [candidate("nebius", "k3"), candidate("groq", "k1"), candidate("nebius", "k4")] }),
    )
    expect(rows.map((r) => r.target)).toEqual(["nebius/m", "groq/m"])
  })

  it("collapses skips the same way, but only when the reason matches", () => {
    // Two cooling keys are one fact; a cooling key and a disabled one are
    // two, and folding them would hide the reason an operator can act on.
    const rows = previewRows(
      preview({
        skips: [
          { provider_id: "groq", key_id: "k1", model: "m", reason: "cooling" },
          { provider_id: "groq", key_id: "k2", model: "m", reason: "cooling" },
          { provider_id: "groq", key_id: "k3", model: "m", reason: "disabled" },
        ],
      }),
    )
    expect(rows.map((r) => [r.target, r.reasonCode, r.reasonProse])).toEqual([
      ["groq/m", "cooling", "× 2 credentials"],
      ["groq/m", "disabled", undefined],
    ])
    expect(rows.map((r) => r.rank)).toEqual([1, 2])
  })

  it("keeps the inferred note beside the count", () => {
    const rows = previewRows(
      preview({
        candidates: [
          { ...candidate("groq", "k1"), inferred: true },
          { ...candidate("groq", "k2"), inferred: true },
        ],
      }),
    )
    expect(rows[0]?.reasonCode).toBe("inferred")
    expect(rows[0]?.reasonProse).toBe("capabilities were guessed · × 2 credentials")
  })
})
