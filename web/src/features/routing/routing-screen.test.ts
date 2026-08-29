import { describe, expect, it } from "vitest"
import { moveTarget, validateChain } from "./routing-screen"

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
