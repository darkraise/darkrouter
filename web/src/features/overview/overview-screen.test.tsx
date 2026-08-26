import { describe, it, expect } from "vitest"
import {
  droppedText,
  errorSeries,
  failoverLabel,
  flowProviders,
  spendSeries,
} from "./overview-screen"
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
  today_spend: { micros: 0, priced: false },
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

  it("takes disabled and unconfigured out of the path", () => {
    const got = flowProviders(
      overview([
        tile({ id: "off", state: "disabled" }),
        tile({ id: "blank", state: "unconfigured" }),
      ]),
      [],
    )
    expect(got.map((p) => p.candidate)).toEqual([false, false])
  })

  it("numbers providers in the order the router walks them", () => {
    const got = flowProviders(
      overview([tile({ id: "first" }), tile({ id: "second" })]),
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

  it("says why a provider is degraded when it can", () => {
    const got = flowProviders(
      overview([tile({ id: "a", state: "degraded", cooling: 2 })]),
      [],
    )
    // "1 cooling" is the reason a row is degraded, and §6.1 gives the note
    // the slack rather than a share bar that repeats the edge thickness.
    expect(got[0]?.note).toBe("2 cooling")
  })
})

describe("tile series", () => {
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

describe("the failover strip", () => {
  it("labels a row with its alias, attempt count and serving provider", () => {
    const label = failoverLabel({
      id: "x", ts: 0, alias: "fast", attempts: 3,
      final_provider_id: "nebius", final_model: "m", total_ms: 12,
    })
    expect(label).toContain("fast")
    expect(label).toContain("×3")
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

describe("the dropped-record counter", () => {
  it("reads zero as a statement rather than a number", () => {
    expect(droppedText(0, 400)).toMatch(/no records dropped/i)
  })

  it("names the shortfall when records were dropped", () => {
    // A non-zero count means usage_daily is a lower bound, which is the one
    // thing that makes every spend figure on this screen approximate. The
    // total is written + dropped: those are disjoint counters (a dropped
    // record never reaches the database), so 7 dropped on top of 400
    // written is 407 observed, not 400.
    const text = droppedText(7, 400)
    expect(text).toContain("7")
    expect(text).toContain("407")
  })
})
