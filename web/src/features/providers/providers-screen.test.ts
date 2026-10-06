import { describe, it, expect } from "vitest"
import { breakersFor, discoveryLine, probeOutcome, providerState } from "./providers-screen"
import { filterProviderRows, mergeProviderRows } from "./provider-rows"
import { coolingSubject, coolingTitle, discoveryFailing, takesCredential } from "./provider-state"
import type { BreakerEntry, Credential, DiscoveryHealthRow, Provider } from "../../lib/api-types"

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

  it("does not call a provider healthy when every credential is switched off", () => {
    // The router drops disabled credentials, so none of these can be sent to.
    expect(providerState(provider({ credentials: [cred({ enabled: false })] }))).toBe(
      "degraded",
    )
    expect(
      providerState(provider({ credentials: [cred(), cred({ id: "k2", enabled: false })] })),
    ).toBe("healthy")
  })

  it("ignores a disabled credential's cooldown", () => {
    expect(
      providerState(
        provider({ credentials: [cred(), cred({ id: "k2", enabled: false, cooling: true })] }),
      ),
    ).toBe("healthy")
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

  it("does not claim a credential was accepted on a provider that has none", () => {
    // LM Studio holds no key; "Credential accepted" claimed a check that
    // never happened.
    const lmstudio = provider({ id: "lmstudio", auth_style: "none", credentials: [] })
    expect(probeOutcome({ ok: true, probe: "models", latency_ms: 1, model_count: 5 }, lmstudio))
      .toEqual({ kind: "success", message: "Endpoint answered · 5 models · 1 ms" })
  })

  it("still names the credential when the provider has one", () => {
    expect(probeOutcome({ ok: true, probe: "models", latency_ms: 1 }, provider()).message)
      .toMatch(/^Credential accepted/)
  })
})

describe("a failing discovery sweep", () => {
  const failing: DiscoveryHealthRow = {
    provider_id: "aihorde", total: 0, live: 0, stale: 0, removed_upstream: 0,
    max_missing_streak: 0, filtered_out: 0,
    consecutive_failures: 1, last_error: "Forbidden",
  }

  it("is named as failing rather than read as an empty catalogue", () => {
    expect(discoveryLine(failing)).toBe("discovery failing · Forbidden")
  })

  it("degrades a keyless provider, whose sweep is the only evidence it works", () => {
    const aihorde = provider({ id: "aihorde", auth_style: "anonymous", credentials: [] })
    expect(providerState(aihorde, failing)).toBe("degraded")
    expect(providerState(aihorde)).toBe("healthy")
    expect(mergeProviderRows([], [aihorde], [failing])[0]?.state).toBe("degraded")
  })

  it("leaves a provider still serving what it last listed alone", () => {
    // A sweep that times out while the models it found an hour ago are still
    // live is a blip, not a provider that cannot serve.
    const blip = { ...failing, total: 4, live: 4 }
    expect(discoveryFailing(blip)).toBe(false)
    expect(discoveryLine(blip)).toBe("4 of 4 live")
  })

  it("is not read off a server that predates the failure fields", () => {
    const { consecutive_failures: _c, last_error: _e, ...old } = failing
    expect(discoveryFailing(old)).toBe(false)
  })
})

describe("whether a credential would ever be sent", () => {
  it("is no for a style that writes no key", () => {
    expect(takesCredential({ auth_style: "none" })).toBe(false)
  })

  it("is yes for the keyless styles that still read one", () => {
    expect(takesCredential({ auth_style: "optional" })).toBe(true)
    expect(takesCredential({ auth_style: "anonymous" })).toBe(true)
    expect(takesCredential({ auth_style: "bearer" })).toBe(true)
  })
})

describe("a breaker entry", () => {
  const entry = (over: Partial<BreakerEntry> = {}): BreakerEntry => ({
    provider_id: "lmstudio", key_id: "", model: "mock-error",
    cooling_until: "2026-10-06T05:00:00Z", backoff_level: 3, consecutive_failures: 5,
    ...over,
  })

  it("names the model when no credential is involved", () => {
    expect(coolingSubject(entry())).toBe("mock-error")
  })

  it("names the credential by its label when it has one", () => {
    expect(coolingSubject(entry({ key_id: "01K" }), "primary")).toBe("primary/mock-error")
    expect(coolingSubject(entry({ key_id: "01K", model: "" }))).toBe("01K/all models")
  })

  it("is counted as what is cooling", () => {
    expect(coolingTitle([entry(), entry({ model: "mock-ratelimit" })])).toBe("2 models cooling")
    expect(coolingTitle([entry({ key_id: "a" })])).toBe("1 credential cooling")
    expect(coolingTitle([entry({ key_id: "a" }), entry()])).toBe("2 breakers cooling")
  })
})
