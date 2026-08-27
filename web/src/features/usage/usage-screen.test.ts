import { describe, it, expect } from "vitest"
import { formatCost, summarise, RANGES, topKeys, stackByDay, requestsSearch } from "./usage-screen"
import type { UsageRow } from "../../lib/api-types"

const row = (over: Partial<UsageRow>): UsageRow => ({
  day: "2026-08-26",
  requests: 1,
  attempts: 1,
  tokens_in: 0,
  tokens_out: 0,
  cost_micros: null,
  ...over,
})

describe("formatCost", () => {
  it("shows an unpriced total as unknown, not as free", () => {
    // A model with no catalog price has an unknown cost. $0.00 would claim it
    // cost nothing, which is a different and false statement.
    expect(formatCost(null)).toBe("—")
    expect(formatCost(0)).toBe("$0.0000")
  })
})

describe("summarise", () => {
  it("sums a key across days", () => {
    const got = summarise([
      row({ key: "groq", requests: 3, tokens_in: 10 }),
      row({ key: "groq", requests: 4, tokens_in: 5, day: "2026-08-25" }),
    ])
    expect(got).toHaveLength(1)
    expect(got[0]?.requests).toBe(7)
    expect(got[0]?.tokensIn).toBe(15)
  })

  it("keeps a total unpriced only while every row is", () => {
    expect(summarise([row({ key: "a" }), row({ key: "a" })])[0]?.cost).toBeNull()
    // One priced row makes the total real, if partial: reporting it as unknown
    // would hide money that was actually spent.
    expect(
      summarise([row({ key: "a" }), row({ key: "a", cost_micros: 500 })])[0]?.cost,
    ).toBe(500)
  })

  it("falls back to the day when a dimension has no key", () => {
    expect(summarise([row({ requests: 2 })])[0]?.key).toBe("2026-08-26")
  })

  it("orders by volume so the busiest key leads", () => {
    const got = summarise([
      row({ key: "quiet", requests: 1 }),
      row({ key: "busy", requests: 9 }),
    ])
    expect(got.map((r) => r.key)).toEqual(["busy", "quiet"])
  })

  it("carries attempts separately from requests", () => {
    // Attempts exceed requests exactly when something failed over, which is
    // what explains a cost the request count alone does not.
    const got = summarise([row({ key: "a", requests: 1, attempts: 3 })])
    expect(got[0]?.attempts).toBe(3)
  })
})

const at = (day: string, key: string, requests: number, cost: number | null = null): UsageRow => ({
  day, key, requests, attempts: requests, tokens_in: 0, tokens_out: 0, cost_micros: cost,
})

describe("stackByDay", () => {
  it("pivots keys into one column each, in day order", () => {
    expect(
      stackByDay(
        [at("2026-08-25", "groq", 3), at("2026-08-25", "nebius", 1), at("2026-08-26", "groq", 2)],
        ["groq", "nebius"],
        (r) => r.requests,
      ),
    ).toEqual([
      { day: "2026-08-25", groq: 3, nebius: 1 },
      { day: "2026-08-26", groq: 2, nebius: 0 },
    ])
  })

  it("zero-fills a key absent on a day rather than leaving a gap", () => {
    // A stacked area with a missing key renders a hole through the stack,
    // which reads as traffic stopping everywhere rather than at one provider.
    const out = stackByDay([at("2026-08-25", "groq", 3)], ["groq", "nebius"], (r) => r.requests)
    expect(out[0]?.nebius).toBe(0)
  })

  it("ignores a key not in the series list", () => {
    const out = stackByDay(
      [at("2026-08-25", "groq", 3), at("2026-08-25", "other", 9)],
      ["groq"],
      (r) => r.requests,
    )
    expect(out[0]).toEqual({ day: "2026-08-25", groq: 3 })
  })
})

describe("topKeys", () => {
  it("ranks by total volume and caps the series count", () => {
    // Five is the ramp's width. A sixth series would reuse a fill and two
    // providers would be indistinguishable.
    const rows = ["a", "b", "c", "d", "e", "f"].map((k, i) => at("2026-08-25", k, i + 1))
    expect(topKeys(rows, 5)).toEqual(["f", "e", "d", "c", "b"])
  })
})

describe("row click-through", () => {
  it("lands in Requests filtered by the dimension that was clicked", () => {
    // requestsSearch feeds a TanStack <Link search={...}>, not a URL string:
    // `to="/requests?..."` does not typecheck against the router's registered
    // route union, and no Link in this codebase builds a query that way.
    const search = requestsSearch("provider", "groq", 7)
    expect(search.provider).toBe("groq")
    expect(search.since_ms).toBeDefined()
    expect(Number(search.since_ms)).toBeLessThan(Date.now())
  })

  it("filters by alias when the alias dimension is showing", () => {
    expect(requestsSearch("alias", "fast", 30).alias).toBe("fast")
  })

  it("carries a time window Requests' own picker can echo truthfully", () => {
    // Requests' range pills are 1h/24h/7d; a bare since_ms with no matching
    // pill renders that control as "All" while a filter is still active.
    // "7d" lines up with Requests' own "7d" pill exactly; wider spans (this
    // screen goes to 365d, Requests does not) land on no pill rather than a
    // false "All" -- still true, just less specific.
    expect(requestsSearch("provider", "groq", 7).range).toBe("7d")
    expect(requestsSearch("provider", "groq", 90).range).toBe("90d")
  })
})

describe("the range picker", () => {
  it("labels its widest option by its actual span", () => {
    // The endpoint serves 365 days. Calling that "all" claims a completeness
    // the data does not have.
    const widest = RANGES[RANGES.length - 1]
    expect(widest?.days).toBe(365)
    expect(widest?.label).not.toMatch(/all/i)
  })
})
