import { describe, it, expect } from "vitest"
import { breakersFor, discoveryLine, providerState } from "./providers-screen"
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
        removed_upstream: 0, max_missing_streak: 0,
      }),
    ).toBe("40 of 40 live")
  })

  it("names the missing streak, which is the number that matters", () => {
    // A provider whose listing has been failing for six hours looks identical
    // to a healthy one until something counts the sweeps that omitted it.
    const line = discoveryLine({
      provider_id: "groq", total: 40, live: 30, stale: 10,
      removed_upstream: 0, max_missing_streak: 6,
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
