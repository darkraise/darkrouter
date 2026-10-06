import { describe, expect, it } from "vitest"
import { apiFilters, newerCount, optionsFrom } from "./requests-screen"
import { attemptsTitle, buildColumns, csvRows, modelLabel } from "./requests-columns"
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
  it("sends a preset as a window the server resolves on every read", () => {
    // A since_ms frozen when the pill was pressed (or when a saved view was
    // made, or a link copied) kept "1h" growing for as long as it lived, so
    // one beside a preset is ignored. `range` itself is the toggle group's
    // bookkeeping and never reaches the API.
    expect(apiFilters({ provider: "groq", range: "1h", since_ms: "123" })).toEqual({
      provider: "groq",
      window_ms: String(60 * 60 * 1000),
    })
  })

  it("keeps an absolute since_ms that no preset produced", () => {
    // A drilldown from Usage names its own UTC-day window.
    expect(apiFilters({ model: "m", range: "30-utc-days", since_ms: "123" })).toEqual({
      model: "m",
      since_ms: "123",
    })
  })

  it("passes through a filter set with no range untouched", () => {
    expect(apiFilters({ provider: "groq" })).toEqual({ provider: "groq" })
  })
})

describe("the attempts cell", () => {
  it("says what happened on one attempt or none, not only on a failover", () => {
    expect(attemptsTitle({ attempts: 0, status: "error" })).toBe("no attempt was made")
    expect(attemptsTitle({ attempts: 1, status: "error" })).toBe("failed on its only attempt")
    expect(attemptsTitle({ attempts: 1, status: "success" })).toBe("served on the first attempt")
    expect(attemptsTitle({ attempts: 3, status: "success" })).toBe(
      "3 attempts — this request failed over",
    )
  })
})

describe("the CSV export", () => {
  it("writes the time as ISO-8601 rather than epoch milliseconds", () => {
    const [out] = csvRows([{ ...row({ id: "1", ts_ms: Date.UTC(2026, 9, 6, 4, 15) }), failover: "single" }])
    expect(out?.ts_ms).toBe("2026-10-06T04:15:00.000Z")
  })
})

describe("the column-visibility menu", () => {
  it("names the columns whose header is a component", () => {
    // The menu falls back to the column id when the header is not a string,
    // and the accessor keys read as "Ts_ms" and "Total_ms" there.
    const ids = buildColumns(() => {}).map((c) => c.id)
    expect(ids).toContain("time")
    expect(ids).toContain("latency")
  })

  it("offers no column it cannot name", () => {
    // The menu labels each entry with its header when that header is a string,
    // so a hideable column with an empty one renders as a nameless checkbox.
    const nameless = buildColumns(() => {}).filter(
      (c) => c.header === "" && c.enableHiding !== false,
    )
    expect(nameless).toEqual([])
  })
})

describe("the model column", () => {
  it("reads alias → final model when the request resolved through an alias", () => {
    // What the client asked for and what actually answered are two different
    // facts, and a row showing only one of them hides the routing decision.
    expect(modelLabel(row({ id: "1", model: "fast", alias: "fast", final_model: "llama-70b" })))
      .toBe("fast → llama-70b")
  })

  it("reads the bare model when nothing resolved through an alias", () => {
    expect(modelLabel(row({ id: "1", model: "gpt-4o" }))).toBe("gpt-4o")
  })

  it("does not draw an arrow to nothing when the final model is unknown", () => {
    // A request that failed before any provider answered has an alias and no
    // final model; "fast → " would read as a rendering fault.
    expect(modelLabel(row({ id: "1", model: "fast", alias: "fast" }))).toBe("fast")
  })
})
