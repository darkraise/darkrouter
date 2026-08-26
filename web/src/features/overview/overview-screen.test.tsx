import { describe, it, expect } from "vitest"
import { flowProviders } from "./overview-screen"
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
