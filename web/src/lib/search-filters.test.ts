import { describe, it, expect } from "vitest"
import { filterQuery } from "./search-filters"

describe("filterQuery", () => {
  it("omits empty values", () => {
    // Two ways of writing "no filter" would produce two cache keys for one
    // view, and a URL carrying ?provider= reads as a filter that is set.
    expect(filterQuery({ provider: "groq", model: "" })).toBe("?provider=groq")
  })

  it("is empty when nothing is filtered", () => {
    expect(filterQuery({ provider: "", model: "" })).toBe("")
  })

  it("encodes values that need it", () => {
    expect(filterQuery({ model: "a/b c" })).toBe("?model=a%2Fb+c")
  })
})
