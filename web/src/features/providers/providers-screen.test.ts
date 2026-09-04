import { describe, it, expect } from "vitest"
import { breakersFor, discoveryLine, probeOutcome, providerState } from "./providers-screen"
import { filterProviderRows, mergeProviderRows } from "./provider-rows"
import type { BreakerEntry, Credential, Provider } from "../../lib/api-types"

const cred = (over: Partial<Credential> = {}): Credential => ({
  id: "k1",
  label: "key",
  masked: "sk-…",
  enabled: true,
  cooling: false,
  kind: "static",
  ...over,
})

const provider = (over: Partial<Provider> = {}): Provider => ({
  id: "groq",
  name: "Groq",
  preset: "groq",
  kind: "openaicompat",
  base_url: "https://x",
  free_models_only: false,
  allow_unsanctioned_free: false,
  priority: 1,
  enabled: true,
  auth_style: "bearer",
  credentials: [cred()],
  ...over,
})

describe("providerState", () => {
  it("separates a degraded provider from a cooling credential", () => {
    // A credential cools; a provider degrades. They are not synonyms, and the
    // pip vocabulary depends on the distinction.
    expect(providerState(provider({ credentials: [cred({ cooling: true })] }))).toBe(
      "degraded",
    )
  })

  it("calls a provider with no credential unconfigured, not disabled", () => {
    expect(providerState(provider({ credentials: [] }))).toBe("unconfigured")
  })

  it("puts disabled ahead of every other state", () => {
    // A disabled provider with no credentials is disabled: the operator turned
    // it off, which is a decision rather than a gap.
    expect(providerState(provider({ enabled: false, credentials: [] }))).toBe(
      "disabled",
    )
  })

  it("is healthy only when enabled, credentialled and cool", () => {
    expect(providerState(provider())).toBe("healthy")
  })
})

describe("breakersFor", () => {
  const entry = (over: Partial<BreakerEntry> & { provider_id: string }): BreakerEntry => ({
    key_id: "k",
    model: "",
    backoff_level: 1,
    consecutive_failures: 3,
    ...over,
  })

  it("keeps only the entries that are actually cooling", () => {
    // The breaker remembers a credential that has recovered; showing it as
    // cooling would send an operator to reset something already fine.
    const got = breakersFor(
      [
        entry({ provider_id: "groq", cooling_until: "2026-08-26T10:00:00Z" }),
        entry({ provider_id: "groq" }),
        entry({ provider_id: "other", cooling_until: "2026-08-26T10:00:00Z" }),
      ],
      "groq",
    )
    expect(got).toHaveLength(1)
  })
})

describe("the discovery line", () => {
  it("reports a healthy catalogue as live out of total", () => {
    expect(
      discoveryLine({
        provider_id: "groq", total: 40, live: 40, stale: 0,
        removed_upstream: 0, max_missing_streak: 0, filtered_out: 0,
      }),
    ).toBe("40 of 40 live")
  })

  it("names the missing streak, which is the number that matters", () => {
    // A provider whose listing has been failing for six hours looks identical
    // to a healthy one until something counts the sweeps that omitted it.
    const line = discoveryLine({
      provider_id: "groq", total: 40, live: 30, stale: 10,
      removed_upstream: 0, max_missing_streak: 6, filtered_out: 0,
    })
    expect(line).toContain("10 stale")
    expect(line).toContain("6")
  })

  it("says never discovered when the provider has no rows at all", () => {
    // Absence is the signal. "0 of 0 live" reads as a sweep that ran and
    // found nothing, which is a different fact.
    expect(discoveryLine(undefined)).toBe("never discovered")
  })
})

describe("a keyless provider", () => {
  const keyless = provider({ id: "ollama", auth_style: "none", credentials: [] })

  it("is configured the moment it exists", () => {
    // There is nothing an operator could add that would change how it is
    // reached, so "unconfigured" would send them looking for a key that does
    // nothing.
    expect(providerState(keyless)).toBe("healthy")
  })

  it("is still unconfigured when it does need a key", () => {
    expect(providerState(provider({ credentials: [] }))).toBe("unconfigured")
  })

  it("counts an optional-auth gateway as keyless too", () => {
    // It answers without a key and answers better with one. An operator
    // scanning for "what can I just add" gets the same answer either way.
    const optional = provider({ id: "hackclub", auth_style: "optional", credentials: [] })
    expect(providerState(optional)).toBe("healthy")
    expect(mergeProviderRows([], [optional])[0]?.connection).toBe("none")
  })

  it("survives the configured-only filter with no accounts", () => {
    // The filter means "ones the router can choose", and it can choose this.
    const rows = mergeProviderRows([], [keyless])
    expect(filterProviderRows(rows, { configuredOnly: true }).map((r) => r.id)).toEqual([
      "ollama",
    ])
  })
})

describe("a row probe's verdict", () => {
  it("reads the rejection out of a 200 rather than calling it sent", () => {
    // A refused credential is a 200 with ok:false. Toasting "Probe sent" on
    // it told the operator the opposite of what the provider just said.
    expect(probeOutcome({ ok: false, probe: "models", latency_ms: 120, error: "401 from upstream" }))
      .toEqual({ kind: "error", message: "401 from upstream" })
  })

  it("names the model count and latency when the credential is accepted", () => {
    expect(probeOutcome({ ok: true, probe: "models", latency_ms: 120, model_count: 40 }))
      .toEqual({ kind: "success", message: "Credential accepted · 40 models · 120 ms" })
  })

  it("has a reason even when the provider gave none", () => {
    expect(probeOutcome({ ok: false, probe: "models", latency_ms: 0 }).message).toMatch(/refused/i)
  })
})
