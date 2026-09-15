import { describe, it, expect } from "vitest"
import {
  chartSeries,
  costTick,
  keyLabel,
  rankingHeading,
  readDimension,
  readRange,
  summarise,
  RANGES,
  topKeys,
  stackByDay,
  requestsSearch,
} from "./usage-screen"
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

describe("the cost axis", () => {
  it("shows an unpriced point as unknown and the origin as $0", () => {
    // A model with no catalog price has an unknown cost. $0.00 would claim it
    // cost nothing, which is a different and false statement; and "free" is
    // a price, which the origin of an axis is not.
    expect(costTick(null)).toBe("—")
    expect(costTick(0)).toBe("$0")
    expect(costTick(4_000)).toBe("$0.0040")
  })
})

describe("the Total view's series", () => {
  it("plots the keyless rows under one total column", () => {
    // Rows on the day view carry no key, so topKeys found nothing and every
    // chart on the Total view drew an empty frame.
    const rows = [row({ requests: 3 }), row({ requests: 4, day: "2026-08-25" })]
    const series = chartSeries(rows, "day")
    expect(series.keys).toEqual(["total"])
    expect(stackByDay(series.rows, series.keys, (r) => r.requests, ["2026-08-25", "2026-08-26"])).toEqual([
      { day: "2026-08-25", total: 4 },
      { day: "2026-08-26", total: 3 },
    ])
  })

  it("keeps a dimension's own keys", () => {
    const series = chartSeries([row({ key: "groq" }), row({ key: "nebius" })], "provider")
    expect(series.keys).toEqual(["groq", "nebius"])
  })

  it("plots requests with no alias as their own series", () => {
    const rows = [row({ key: "", requests: 5 }), row({ key: "fast", requests: 2 })]
    const series = chartSeries(rows, "alias")
    expect(series.keys).toEqual(["", "fast"])
    expect(stackByDay(series.rows, series.keys, (r) => r.requests, ["2026-08-26"])).toEqual([
      { day: "2026-08-26", "": 5, fast: 2 },
    ])
  })

  it("names the no-alias series instead of leaving it blank", () => {
    expect(keyLabel("")).toBe("(none)")
    expect(keyLabel("fast")).toBe("fast")
  })
})

describe("the URL's parameters", () => {
  it("falls back to the defaults for a value the screen has not got", () => {
    // A pasted ?dimension=anything used to reach the API as a group_by.
    expect(readDimension("provider")).toBe("provider")
    expect(readDimension("anything")).toBe("day")
    expect(readDimension("")).toBe("day")
    expect(readRange("90").days).toBe(90)
    expect(readRange("12").days).toBe(30)
  })
})

describe("the ranking heading", () => {
  it("names what is ranked", () => {
    expect(rankingHeading("day")).toBe("Busiest days")
    expect(rankingHeading("provider")).toBe("Providers ranked by requests")
    expect(rankingHeading("alias")).toBe("Aliases ranked by requests")
  })
})

describe("summarise", () => {
  it("sums a key across days", () => {
    const got = summarise(
      [
        row({ key: "groq", requests: 3, tokens_in: 10 }),
        row({ key: "groq", requests: 4, tokens_in: 5, day: "2026-08-25" }),
      ],
      "provider",
    )
    expect(got).toHaveLength(1)
    expect(got[0]?.requests).toBe(7)
    expect(got[0]?.tokensIn).toBe(15)
  })

  it("keeps a total unpriced only while every row is", () => {
    expect(summarise([row({ key: "a" }), row({ key: "a" })], "provider")[0]?.cost).toBeNull()
    // One priced row makes the total real, if partial: reporting it as unknown
    // would hide money that was actually spent.
    expect(
      summarise([row({ key: "a" }), row({ key: "a", cost_micros: 500 })], "provider")[0]?.cost,
    ).toBe(500)
  })

  it("keys the Total view by day", () => {
    expect(summarise([row({ requests: 2 })], "day")[0]?.key).toBe("2026-08-26")
  })

  it("keeps requests with no alias under an empty key rather than a date", () => {
    // A request for a model by name resolves no alias. Keying it by its day
    // listed dates as alias names and linked them to Requests as filters.
    const got = summarise(
      [row({ key: "", requests: 2 }), row({ key: "", requests: 1, day: "2026-08-25" })],
      "alias",
    )
    expect(got).toHaveLength(1)
    expect(got[0]?.key).toBe("")
    expect(got[0]?.requests).toBe(3)
  })

  it("orders by volume so the busiest key leads", () => {
    const got = summarise(
      [row({ key: "quiet", requests: 1 }), row({ key: "busy", requests: 9 })],
      "provider",
    )
    expect(got.map((r) => r.key)).toEqual(["busy", "quiet"])
  })

  it("carries attempts separately from requests", () => {
    // Attempts exceed requests exactly when something failed over, which is
    // what explains a cost the request count alone does not.
    const got = summarise([row({ key: "a", requests: 1, attempts: 3 })], "provider")
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
        ["2026-08-25", "2026-08-26"],
      ),
    ).toEqual([
      { day: "2026-08-25", groq: 3, nebius: 1 },
      { day: "2026-08-26", groq: 2, nebius: 0 },
    ])
  })

  it("zero-fills a key absent on a day rather than leaving a gap", () => {
    // A stacked area with a missing key renders a hole through the stack,
    // which reads as traffic stopping everywhere rather than at one provider.
    const out = stackByDay([at("2026-08-25", "groq", 3)], ["groq", "nebius"], (r) => r.requests, [
      "2026-08-25",
    ])
    expect(out[0]?.nebius).toBe(0)
  })

  it("zero-fills a day with no traffic at all rather than dropping it", () => {
    // The x-axis is categorical, so a missing day is not a gap: its
    // neighbours close up and idle days vanish from the timeline.
    const out = stackByDay(
      [at("2026-08-24", "groq", 3), at("2026-08-27", "groq", 5)],
      ["groq"],
      (r) => r.cost_micros,
      ["2026-08-24", "2026-08-25", "2026-08-26", "2026-08-27"],
    )
    expect(out).toEqual([
      { day: "2026-08-24", groq: null },
      { day: "2026-08-25", groq: 0 },
      { day: "2026-08-26", groq: 0 },
      { day: "2026-08-27", groq: null },
    ])
  })

  it("ignores a key not in the series list", () => {
    const out = stackByDay(
      [at("2026-08-25", "groq", 3), at("2026-08-25", "other", 9)],
      ["groq"],
      (r) => r.requests,
      ["2026-08-25"],
    )
    expect(out[0]).toEqual({ day: "2026-08-25", groq: 3 })
  })
})

describe("stackByDay with a nullable value", () => {
  it("keeps a cell unknown when every contributing row is unpriced", () => {
    // Unpriced is not free: a cost cell must not collapse to the same zero a
    // key with no rows at all gets.
    const out = stackByDay(
      [at("2026-08-25", "groq", 1, null), at("2026-08-25", "groq", 1, null)],
      ["groq"],
      (r) => r.cost_micros,
      ["2026-08-25"],
    )
    expect(out[0]?.groq).toBeNull()
  })

  it("turns a cell real once any contributing row is priced", () => {
    // Mirrors summarise: one priced row among unpriced ones makes the total
    // real, if partial, rather than hiding money that was actually spent.
    const out = stackByDay(
      [at("2026-08-25", "groq", 1, null), at("2026-08-25", "groq", 1, 500)],
      ["groq"],
      (r) => r.cost_micros,
      ["2026-08-25"],
    )
    expect(out[0]?.groq).toBe(500)
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
    const search = requestsSearch("provider", "groq", "2026-08-20", 7)
    expect(search.provider).toBe("groq")
  })

  it("starts where the chart's window starts, not a rolling span back from now", () => {
    // The charts cover whole UTC days from the served first_day. A rolling
    // now-minus-7-days boundary cut part of that first day out of the
    // drilldown, or let in part of the day before it.
    expect(requestsSearch("provider", "groq", "2026-08-20", 7).since_ms).toBe(
      String(Date.UTC(2026, 7, 20)),
    )
  })

  it("filters by alias when the alias dimension is showing", () => {
    expect(requestsSearch("alias", "fast", "2026-08-20", 30).alias).toBe("fast")
  })

  it("carries a time window Requests' own picker can echo truthfully", () => {
    // Requests' pills are rolling 1h/24h/7d. Calendar days match none of them,
    // so the range names no pill -- lighting "7d" would claim a rolling window
    // the filter is not, and an empty range would show a false "All".
    const range = requestsSearch("provider", "groq", "2026-08-20", 7).range
    expect(range).toBeTruthy()
    expect(["1h", "24h", "7d", "all"]).not.toContain(range)
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
