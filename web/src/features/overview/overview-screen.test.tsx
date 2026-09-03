import { describe, it, expect } from "vitest"
import {
  errorSeries,
  flowProviders,
  requestSeries,
  spendQualifier,
  spendReading,
  spendSeries,
} from "./overview-screen"
import { failoverLabel } from "./failover-label"
import { durationParts as duration, money } from "../../lib/format"
import type { Overview, ProviderTile, UsageRow } from "../../lib/api-types"

const tile = (over: Partial<ProviderTile> & { id: string }): ProviderTile => ({
  name: over.id,
  state: "healthy",
  cooling: 0,
  credentials: 1,
  enabled: true,
  needs_reauth: false,
  ...over,
})

const overview = (providers: ProviderTile[]): Overview => ({
  providers,
  requests_per_min: 0,
  error_rate: 0,
  window_sec: 300,
  today_spend: { micros: 0, priced: false, estimated: false },
  latency: { p50_ms: 0, p95_ms: 0 },
  series: [],
  failovers: [],
  failover_edges: [],
})

const usage = (key: string, requests: number): UsageRow => ({
  day: "2026-08-26",
  key,
  requests,
  attempts: requests,
  tokens_in: 0,
  tokens_out: 0,
  cost_micros: null,
})

describe("flowProviders", () => {
  it("keeps a degraded provider in the path", () => {
    // A credential cools; a provider degrades. Degraded still routes, so
    // drawing it as idle would claim traffic is not reaching it.
    const got = flowProviders(overview([tile({ id: "a", state: "degraded" })]), [])
    expect(got[0]?.candidate).toBe(true)
  })

  it("takes a disabled provider out of the path but keeps its row", () => {
    // Switched off is a configuration an operator chose and needs to see.
    const got = flowProviders(overview([tile({ id: "off", state: "disabled" })]), [])
    expect(got.map((p) => p.id)).toEqual(["off"])
    expect(got[0]?.candidate).toBe(false)
  })

  it("drops a provider whose last account was removed", () => {
    // A provider keeps its database row after its credentials are deleted.
    // Drawing that row would put a provider nobody has set up in the routing
    // flow beside the ones actually carrying traffic.
    const got = flowProviders(
      overview([tile({ id: "a" }), tile({ id: "groq", state: "unconfigured", credentials: 0 })]),
      [usage("a", 5)],
    )
    expect(got.map((p) => p.id)).toEqual(["a"])
  })

  it("keeps an emptied provider that still has traffic in the window", () => {
    // The window is history: erasing the row would erase the requests it
    // served and any return arc drawn to or from it.
    const got = flowProviders(
      overview([tile({ id: "groq", state: "unconfigured", credentials: 0 })]),
      [usage("groq", 7)],
    )
    expect(got).toHaveLength(1)
    expect(got[0]).toMatchObject({ id: "groq", requests: 7, candidate: false })
  })

  it("numbers providers in the order the router walks them", () => {
    const got = flowProviders(
      overview([tile({ id: "first" }), tile({ id: "second" })]),
      [],
    )
    expect(got.map((p) => p.priority)).toEqual([1, 2])
  })

  it("numbers the rows it draws, without gaps for the ones it dropped", () => {
    const got = flowProviders(
      overview([
        tile({ id: "first" }),
        tile({ id: "gone", state: "unconfigured", credentials: 0 }),
        tile({ id: "second" }),
      ]),
      [],
    )
    expect(got.map((p) => p.priority)).toEqual([1, 2])
  })

  it("sums a provider's requests across days", () => {
    const got = flowProviders(overview([tile({ id: "a" })]), [
      usage("a", 4),
      usage("a", 6),
      usage("other", 99),
    ])
    expect(got[0]?.requests).toBe(10)
  })

  it("carries the credential facts a row needs to explain itself", () => {
    const got = flowProviders(
      overview([tile({ id: "a", state: "degraded", cooling: 2, credentials: 3 })]),
      [],
    )
    // The count is the denominator: 2 cooling out of 3 is a different
    // reading from 2 out of 2, and the node cannot say which without it.
    expect(got[0]).toMatchObject({ cooling: 2, credentials: 3, needsReauth: false })
  })
})

describe("tile series", () => {
  it("sums requests per day", () => {
    expect(
      requestSeries([
        { ...usage("groq", 3), day: "2026-08-25" },
        { ...usage("nebius", 4), day: "2026-08-25" },
        { ...usage("groq", 5), day: "2026-08-26" },
      ]),
    ).toEqual([7, 5])
  })

  it("sums spend per day across a dimension's rows", () => {
    expect(
      spendSeries([
        { ...usage("groq", 1), day: "2026-08-25", cost_micros: 100 },
        { ...usage("nebius", 1), day: "2026-08-25", cost_micros: 50 },
        { ...usage("groq", 1), day: "2026-08-26", cost_micros: 70 },
      ]),
    ).toEqual([150, 70])
  })

  it("treats an unpriced day as no spend rather than dropping the day", () => {
    // The shape has to keep its x-axis: a missing day would compress the
    // sparkline and misreport when spending happened.
    expect(
      spendSeries([
        { ...usage("groq", 1), day: "2026-08-25", cost_micros: null },
        { ...usage("groq", 1), day: "2026-08-26", cost_micros: 70 },
      ]),
    ).toEqual([0, 70])
  })

  it("derives errors from attempts beyond requests, floored at zero", () => {
    expect(
      errorSeries([
        { ...usage("groq", 3), day: "2026-08-25", attempts: 5 },
        { ...usage("groq", 4), day: "2026-08-26", attempts: 4 },
      ]),
    ).toEqual([2, 0])
  })
})

describe("the spend readout", () => {
  it("prints a sub-cent day rather than rounding it to nothing", () => {
    // $0.00 is the exact string that would claim a gateway which has spent
    // something has spent nothing.
    expect(money(4_000)).toBe("$0.0040")
    // Past a cent it is a headline figure again, not a scientific one.
    expect(money(3_470_000)).toBe("$3.47")
  })

  it("says unknown rather than free when nothing in scope had a price", () => {
    expect(money(null)).toBe("—")
  })
})

describe("the spend tile", () => {
  it("says no spend yet for a priced day at zero, and unknown for unpriced", () => {
    // "free" is a price; a day nothing has been charged to yet is not.
    expect(spendReading({ micros: 0, priced: true, estimated: false })).toBe("no spend yet")
    expect(spendReading({ micros: null, priced: false, estimated: false })).toBe("—")
    expect(spendReading({ micros: 4_000, priced: true, estimated: false })).toBe("$0.0040")
  })

  it("qualifies a total that counted a price nobody quoted", () => {
    expect(spendQualifier({ micros: 4_000, priced: true, estimated: true }))
      .toBe("includes estimated prices")
  })

  it("says nothing when every contributing price was firsthand", () => {
    expect(spendQualifier({ micros: 4_000, priced: true, estimated: false })).toBeNull()
    // Nothing priced means nothing to qualify: the tile already reads unknown.
    expect(spendQualifier({ micros: null, priced: false, estimated: true })).toBeNull()
  })
})

describe("the latency readout", () => {
  it("reads past the thousand in seconds", () => {
    // 4100ms makes the reader do the division.
    expect(duration(4100)).toEqual({ value: "4.1", unit: "s" })
  })

  it("keeps sub-second readings in milliseconds", () => {
    expect(duration(890)).toEqual({ value: "890", unit: "ms" })
  })
})

describe("the failover strip", () => {
  it("labels a row with its alias and the provider that served it", () => {
    const label = failoverLabel({
      id: "x", ts: 0, alias: "fast", attempts: 3,
      final_provider_id: "nebius", final_model: "m", total_ms: 12,
    })
    expect(label).toContain("fast")
    expect(label).toContain("nebius")
  })

  it("says so when a request had no alias", () => {
    // A bare model name is not an alias, and printing an empty arrow would
    // read as a rendering fault.
    const label = failoverLabel({
      id: "x", ts: 0, alias: "", attempts: 2,
      final_provider_id: "groq", final_model: "m-4", total_ms: 9,
    })
    expect(label).toContain("m-4")
    expect(label).not.toContain("→ →")
  })
})
