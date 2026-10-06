import { describe, expect, it } from "vitest"
import { splitTarget, targetFacts, type ChainContext } from "./chain-health"
import type { Credential, Model, Provider } from "../../lib/api-types"

const cred = (over: Partial<Credential> = {}): Credential => ({
  id: "k1", label: "k1", masked: "sk-…", enabled: true, cooling: false, kind: "static",
  ...over,
})

const provider = (id: string, over: Partial<Provider> = {}): Provider => ({
  id, name: id, preset: id, kind: "openaicompat", base_url: "https://x.example",
  priority: 10, enabled: true, auth_style: "bearer", free_models_only: false,
  allow_unsanctioned_free: false,
  credentials: [cred()],
  ...over,
})

const model = (name: string, providers: string[]): Model => ({
  model: name, providers, surfaces: ["llm"], context_window: 0, max_output_tokens: 0,
  tools: false, vision: false, reasoning: false, inferred: false, state: "live",
  pricing: null, free_tier: null, merge_source: "discovered",
})

const ctx = (over: Partial<ChainContext> = {}): ChainContext => ({
  providers: [provider("groq")],
  models: [model("llama", ["groq"])],

  ...over,
})

describe("splitTarget", () => {
  it("pins a target whose prefix names a configured provider", () => {
    expect(splitTarget("groq/llama", ["groq"])).toEqual({ providerId: "groq", model: "llama" })
  })

  it("keeps a model id that merely contains a slash whole", () => {
    // The router splits on the first slash only when the prefix is a
    // configured provider, because model identifiers legitimately contain
    // slashes. Reading `meta-llama` as a provider would misreport the target.
    expect(splitTarget("meta-llama/Llama-3.3-70B", ["groq"])).toEqual({
      providerId: null, model: "meta-llama/Llama-3.3-70B",
    })
  })

  it("splits on the first slash, not the last", () => {
    expect(splitTarget("groq/a/b", ["groq"])).toEqual({ providerId: "groq", model: "a/b" })
  })
})

describe("targetFacts on a pinned target", () => {
  it("calls a live provider offering the model routable", () => {
    expect(targetFacts("groq/llama", ctx()).state).toBe("routable")
  })

  it("names a disabled provider rather than calling the target broken", () => {
    const facts = targetFacts("groq/llama", ctx({ providers: [provider("groq", { enabled: false })] }))
    expect(facts.state).toBe("provider-disabled")
    expect(facts.problem).toBe("groq is disabled")
  })

  it("names a provider with no credentials", () => {
    const facts = targetFacts("groq/llama", ctx({ providers: [provider("groq", { credentials: [] })] }))
    expect(facts.state).toBe("provider-unconfigured")
    expect(facts.problem).toBe("groq has no credentials")
  })

  it("reports a model the provider does not offer", () => {
    expect(targetFacts("groq/nosuch", ctx()).state).toBe("model-missing")
  })

  it("says nothing about the model when the catalogue has not loaded", () => {
    // An empty catalogue means discovery has not run, not that every model in
    // every chain has gone missing.
    expect(targetFacts("groq/anything", ctx({ models: [] })).state).toBe("routable")
  })

  it("reports a provider whose every account is cooling", () => {
    const cooling = provider("groq", { credentials: [cred({ cooling: true })] })
    expect(targetFacts("groq/llama", ctx({ providers: [cooling] })).state).toBe("cooling")
  })
})

describe("targetFacts on a bare name", () => {
  it("reports which providers would serve it", () => {
    const facts = targetFacts("llama", ctx())
    expect(facts.state).toBe("any-provider")
    expect(facts.offeredBy).toEqual(["groq"])
  })

  it("reports a name nothing offers", () => {
    expect(targetFacts("nosuch", ctx()).state).toBe("unresolved")
  })

  it("treats a slashed target nothing knows as a provider that is missing", () => {
    // The operator typed a provider. Telling them no model is called
    // `ghost/m` sends them to look in the catalogue instead of at the name.
    const facts = targetFacts("ghost/m", ctx())
    expect(facts.state).toBe("provider-missing")
    expect(facts.problem).toContain("no provider named ghost")
  })

  it("still refuses a catalogued model whose id contains a slash", () => {
    // The router would resolve it, but an alias target is held to a stricter
    // rule: `aliasTargetsExist` splits every target on the first slash
    // unconditionally and 400s when the prefix is not a configured provider.
    // Passing it here would trade an inline message for a failed save.
    const facts = targetFacts("meta-llama/Llama-3.3-70B", ctx({
      models: [model("meta-llama/Llama-3.3-70B", ["groq"])],
    }))
    expect(facts.state).toBe("provider-missing")
  })

  it("distinguishes offered-but-unreachable from not offered at all", () => {
    const cooling = provider("groq", { credentials: [cred({ cooling: true })] })
    expect(targetFacts("llama", ctx({ providers: [cooling] })).state).toBe("cooling")
  })

  it("says nothing about a target not typed yet", () => {
    expect(targetFacts("  ", ctx()).state).toBe("blank")
  })
})

describe("an empty catalogue", () => {
  it("says nothing about a bare name rather than condemning it", () => {
    // Before the first discovery sweep every chain would otherwise read as
    // broken, which is a page full of red about a gateway that is merely new.
    expect(targetFacts("llama", ctx({ models: [] })).state).toBe("unknown")
  })

  it("still names a provider prefix nothing knows, without claiming to have checked the catalogue", () => {
    const facts = targetFacts("ghost/m", ctx({ models: [] }))
    expect(facts.state).toBe("provider-missing")
    expect(facts.problem).toMatch(/no provider named ghost is configured/)
  })
})

describe("a keyless provider", () => {
  // lmstudio, vllm and every other `auth_style: none` runtime hold no
  // credential at all. sqlsource keeps them and the router walks them with
  // one attempt keyed on the empty id; a judgement that demanded a credential
  // drew every one of them amber while they served traffic.
  const lmstudio = provider("lmstudio", { auth_style: "none", credentials: [] })
  const keyless = ctx({
    providers: [lmstudio],
    models: [model("mock-fast", ["lmstudio"]), model("mock-error", ["lmstudio"])],
  })
  const breaker = (model: string, until: string | undefined, key_id = "") => ({
    provider_id: "lmstudio", key_id, model, cooling_until: until,
  })
  const NOW = Date.parse("2026-10-06T12:00:00Z")
  const LATER = "2026-10-06T12:05:00Z"
  const EARLIER = "2026-10-06T11:55:00Z"

  it("is routable with no credentials, pinned or by bare name", () => {
    expect(targetFacts("lmstudio/mock-fast", keyless).state).toBe("routable")
    expect(targetFacts("mock-fast", keyless).state).toBe("any-provider")
  })

  it("is still unconfigured when it is keyed and holds none", () => {
    const keyed = ctx({ providers: [provider("groq", { credentials: [] })] })
    expect(targetFacts("groq/llama", keyed).state).toBe("provider-unconfigured")
  })

  it("is cooling for the one model its breaker holds, and only that one", () => {
    // Breakers are per (provider, key, model). mock-error cooling says
    // nothing about mock-fast on the same runtime.
    const c = { ...keyless, now: NOW, breakers: [breaker("mock-error", LATER)] }
    const facts = targetFacts("lmstudio/mock-error", c)
    expect(facts.state).toBe("cooling")
    expect(facts.problem).toBe("lmstudio is cooling for mock-error after recent failures")
    expect(targetFacts("lmstudio/mock-fast", c).state).toBe("routable")
    expect(targetFacts("mock-error", c).state).toBe("cooling")
  })

  it("ignores a breaker whose cooldown has run out", () => {
    const c = { ...keyless, now: NOW, breakers: [breaker("mock-error", EARLIER)] }
    expect(targetFacts("lmstudio/mock-error", c).state).toBe("routable")
  })

  it("ignores a breaker entry that is tracked but not cooling", () => {
    const c = { ...keyless, now: NOW, breakers: [breaker("mock-error", undefined)] }
    expect(targetFacts("lmstudio/mock-error", c).state).toBe("routable")
  })
})

describe("per-model breakers on a keyed provider", () => {
  const NOW = Date.parse("2026-10-06T12:00:00Z")
  const LATER = "2026-10-06T12:05:00Z"

  it("cools a model only when every credential is cooling for it", () => {
    const groq = provider("groq", { credentials: [cred({ id: "k1" }), cred({ id: "k2" })] })
    const one = ctx({
      providers: [groq], now: NOW,
      breakers: [{ provider_id: "groq", key_id: "k1", model: "llama", cooling_until: LATER }],
    })
    expect(targetFacts("groq/llama", one).state).toBe("routable")
    const both = {
      ...one,
      breakers: [
        { provider_id: "groq", key_id: "k1", model: "llama", cooling_until: LATER },
        { provider_id: "groq", key_id: "k2", model: "llama", cooling_until: LATER },
      ],
    }
    expect(targetFacts("groq/llama", both).state).toBe("cooling")
  })

  it("lets a credential-wide breaker gate every model the key serves", () => {
    const c = ctx({
      now: NOW,
      breakers: [{ provider_id: "groq", key_id: "k1", model: "", cooling_until: LATER }],
    })
    expect(targetFacts("groq/llama", c).state).toBe("cooling")
  })
})
