import { describe, it, expect } from "vitest"
import type { BreakerEntry, Credential, Model, Provider, UsageRow } from "../../lib/api-types"
import {
  accountSummary,
  capabilityCount,
  discoveryFraction,
  discoveryNote,
  modelsFor,
  requestsByDay,
  totalRequests,
} from "./provider-stats"

const usage = (key: string, day: string, requests: number): UsageRow => ({
  day,
  key,
  requests,
  attempts: requests,
  tokens_in: 0,
  tokens_out: 0,
  cost_micros: null,
})

const model = (over: Partial<Model> & { model: string }): Model => ({
  providers: ["groq"],
  surfaces: ["llm"],
  context_window: 8192,
  max_output_tokens: 4096,
  tools: false,
  vision: false,
  reasoning: false,
  inferred: false,
  state: "live",
  pricing: null,
  free_tier: null,
  merge_source: "models_dev",
  ...over,
})

const cred = (over: Partial<Credential> & { id: string }): Credential => ({
  label: over.id,
  masked: "sk-…",
  enabled: true,
  cooling: false,
  kind: "static",
  ...over,
})

const provider = (credentials: Credential[]): Provider => ({
  id: "groq",
  name: "Groq",
  preset: "groq",
  kind: "openaicompat",
  base_url: "https://api.groq.com/v1",
  priority: 10,
  enabled: true,
  auth_style: "bearer",
  free_models_only: false,
  allow_unsanctioned_free: false,
  credentials,
})

describe("the traffic series", () => {
  it("keeps only this provider's rows, in day order", () => {
    const rows = [
      usage("groq", "2026-08-26", 5),
      usage("cerebras", "2026-08-26", 99),
      usage("groq", "2026-08-24", 3),
    ]
    expect(requestsByDay(rows, "groq")).toEqual([3, 5])
    expect(totalRequests(rows, "groq")).toBe(8)
  })

  it("sums a day split across rows", () => {
    const rows = [usage("groq", "2026-08-26", 5), usage("groq", "2026-08-26", 2)]
    expect(requestsByDay(rows, "groq")).toEqual([7])
  })

  it("reads a provider with no traffic as zero rather than as an error", () => {
    expect(requestsByDay([], "groq")).toEqual([])
    expect(totalRequests([], "groq")).toBe(0)
  })
})

describe("the model list", () => {
  it("takes every catalogue row that names this provider", () => {
    // The catalogue is one row per model listing every provider that offers
    // it, so a model shared by two providers belongs to both lists.
    const models = [
      model({ model: "b-model", providers: ["groq", "cerebras"] }),
      model({ model: "a-model", providers: ["groq"] }),
      model({ model: "other", providers: ["cerebras"] }),
    ]
    expect(modelsFor(models, "groq").map((m) => m.model)).toEqual(["a-model", "b-model"])
  })
})

describe("capability counts", () => {
  it("counts each capability against the whole catalogue", () => {
    const caps = capabilityCount([
      model({ model: "a", tools: true, vision: true }),
      model({ model: "b", tools: true }),
      model({ model: "c" }),
    ])
    expect(caps).toEqual({ tools: 2, vision: 1, reasoning: 0, total: 3 })
  })

  it("survives a provider with no models", () => {
    // The bar divides by total, and a provider whose catalogue has not been
    // fetched has none.
    expect(capabilityCount([])).toEqual({ tools: 0, vision: 0, reasoning: 0, total: 0 })
  })
})

describe("the account summary", () => {
  it("counts a cooling account as neither usable nor gone", () => {
    // An enabled credential that is cooling is not one the router can send to
    // right now, which is the whole reading.
    const got = accountSummary(
      provider([cred({ id: "a" }), cred({ id: "b", cooling: true }), cred({ id: "c", enabled: false })]),
      [],
    )
    expect(got).toEqual({ total: 3, usable: 1, cooling: 1, disabled: 1 })
  })

  it("believes the breaker as well as the credential's own flag", () => {
    // The breaker table and the credential row are two sources for the same
    // fact, and the credential's flag can lag a trip.
    const entry: BreakerEntry = {
      provider_id: "groq",
      key_id: "a",
      model: "",
      cooling_until: new Date().toISOString(),
      backoff_level: 1,
      consecutive_failures: 2,
    }
    expect(accountSummary(provider([cred({ id: "a" })]), [entry])).toMatchObject({
      usable: 0,
      cooling: 1,
    })
  })
})

describe("the discovery note", () => {
  const row = (over: Partial<import("../../lib/api-types").DiscoveryHealthRow> = {}) => ({
    provider_id: "groq", total: 9, live: 8, stale: 0, removed_upstream: 0,
    max_missing_streak: 0, filtered_out: 0, ...over,
  })

  it("tells an all-paid provider apart from an empty one", () => {
    // The whole point of the count. Both hold zero models; only one has a
    // problem, and it is not the one the filter emptied.
    expect(discoveryNote(row({ total: 0, live: 0, filtered_out: 40 }))).toBe(
      "none free of 40 listed",
    )
    expect(discoveryNote(undefined)).toBe("never discovered")
  })

  it("reports a partial filter without alarm", () => {
    expect(discoveryNote(row({ total: 3, live: 3, filtered_out: 37 }))).toBe(
      "37 paid, not imported",
    )
  })

  it("still reports a provider going missing", () => {
    expect(discoveryNote(row({ max_missing_streak: 3 }))).toBe("missing for 3 sweeps")
  })
})

describe("the discovery reading", () => {
  it("says nothing at all when no sweep has run", () => {
    // "0/0" would read as a sweep that ran and found nothing.
    expect(discoveryFraction(undefined)).toBeNull()
  })

  it("reads live against known", () => {
    expect(
      discoveryFraction({
        provider_id: "groq",
        total: 9,
        live: 8,
        stale: 0,
        removed_upstream: 1,
        max_missing_streak: 0, filtered_out: 0,
      }),
    ).toBe("8/9")
  })
})
