import { describe, it, expect } from "vitest"
import { filterQuery, parseSearch, stringifySearch } from "./search-filters"

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

  it("keeps the query string free of empty filters", () => {
    expect(filterQuery({ provider: "groq", model: "", status: "error" })).toBe(
      "?provider=groq&status=error",
    )
  })
})

describe("the router's query-string codec", () => {
  it("writes a string that looks like a number without quotes", () => {
    // TanStack's default JSON-encodes each value, and quotes a numeric-looking
    // string so it parses back as a string: since_ms="1788739200000", which
    // the API refused with a 400.
    expect(stringifySearch({ since_ms: "1788739200000" })).toBe("?since_ms=1788739200000")
  })

  it("reads every value back as the same string", () => {
    const search = { since_ms: "1788739200000", model: "a/b c", flag: "true", n: "007" }
    expect(parseSearch(stringifySearch(search))).toEqual(search)
  })

  it("reads a plain query string the way URLSearchParams does", () => {
    expect(parseSearch("?provider=groq&since_ms=1788739200000")).toEqual({
      provider: "groq",
      since_ms: "1788739200000",
    })
    expect(parseSearch("")).toEqual({})
  })

  it("leaves out a key whose value is undefined, rather than writing the word", () => {
    expect(stringifySearch({ provider: undefined, model: "m" })).toBe("?model=m")
    expect(stringifySearch({})).toBe("")
  })
})
