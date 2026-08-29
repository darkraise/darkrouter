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
  credentials: [cred()],
  ...over,
})

const model = (name: string, providers: string[]): Model => ({
  model: name, providers, surfaces: ["llm"], context_window: 0, max_output_tokens: 0,
  tools: false, vision: false, reasoning: false, inferred: false, state: "live",
  pricing: null, merge_source: "discovered",
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

  it("names a provider with no accounts", () => {
    const facts = targetFacts("groq/llama", ctx({ providers: [provider("groq", { credentials: [] })] }))
    expect(facts.state).toBe("provider-unconfigured")
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
