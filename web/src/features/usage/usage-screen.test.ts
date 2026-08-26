import { describe, it, expect } from "vitest"
import { formatCost, summarise } from "./usage-screen"
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
