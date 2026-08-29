import { describe, expect, it } from "vitest"
import { apiFilters, newerCount, optionsFrom } from "./requests-screen"
import { buildColumns } from "./requests-columns"
import type { RequestRow } from "../../lib/api-types"

const row = (over: Partial<RequestRow> & { id: string }): RequestRow => ({
  ts_ms: 0, dialect: "openai", surface: "llm", model: "m", status: "success",
  source: "proxy",
  tokens_in: 0, tokens_out: 0, cache_read_tokens: 0,
  cost_micros: null, ttft_ms: null, total_ms: null, attempts: 1,
  ...over,
})

describe("the newer pill", () => {
  it("counts rows ahead of the one the reader is anchored to", () => {
    // The poll must not shift the scroll position out from under a reader,
    // so new rows are counted and held rather than inserted.
    const page = [row({ id: "c" }), row({ id: "b" }), row({ id: "a" })]
    expect(newerCount(page, "a")).toBe(2)
  })

  it("counts nothing when the anchor is still the newest", () => {
    expect(newerCount([row({ id: "a" })], "a")).toBe(0)
  })

  it("counts nothing before the first page has an anchor", () => {
    expect(newerCount([row({ id: "a" })], "")).toBe(0)
  })

  it("counts the whole page when the anchor has aged out of it", () => {
    // Retention or a long absence: the anchor is gone, and claiming zero new
    // rows would be the one answer that is certainly wrong.
    expect(newerCount([row({ id: "c" }), row({ id: "b" })], "gone")).toBe(2)
  })
})

describe("filter options", () => {
  it("offers each distinct value once, sorted", () => {
    const rows = [
      row({ id: "1", provider: "nebius" }),
      row({ id: "2", provider: "groq" }),
      row({ id: "3", provider: "groq" }),
    ]
    expect(optionsFrom(rows, "provider")).toEqual(["groq", "nebius"])
  })

  it("omits rows where the field is absent", () => {
    // A request nothing served has no provider, and an empty option would
    // filter on the empty string, which matches nothing.
    expect(optionsFrom([row({ id: "1" })], "provider")).toEqual([])
  })
})

describe("api filters", () => {
  it("excludes the UI-only time-range bookkeeping key from the request", () => {
    // `range` records which preset produced `since_ms` for the toggle group
    // to redisplay; the API has no such parameter, and sending it anyway
    // would vary the query cache key for no reason.
    expect(apiFilters({ provider: "groq", range: "1h", since_ms: "123" })).toEqual({
      provider: "groq",
      since_ms: "123",
    })
  })

  it("passes through a filter set with no range untouched", () => {
    expect(apiFilters({ provider: "groq" })).toEqual({ provider: "groq" })
  })
})

describe("the column-visibility menu", () => {
  it("offers no column it cannot name", () => {
    // The menu labels each entry with its header when that header is a string,
    // so a hideable column with an empty one renders as a nameless checkbox.
    const nameless = buildColumns(() => {}).filter(
      (c) => c.header === "" && c.enableHiding !== false,
    )
    expect(nameless).toEqual([])
  })
})
