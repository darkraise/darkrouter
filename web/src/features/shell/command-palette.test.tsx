import { describe, expect, it } from "vitest"
import { paletteMatches } from "./command-palette"
import type { Model, Provider } from "../../lib/api-types"

const provider = (id: string, name: string): Provider => ({
  id,
  name,
  preset: "",
  kind: "openai",
  base_url: "",
  priority: 1,
  enabled: true,
  auth_style: "bearer",
  credentials: [],
  free_models_only: false,
})

const model = (id: string, providers: string[]): Model => ({
  model: id,
  providers,
  surfaces: ["llm"],
  context_window: 0,
  max_output_tokens: 0,
  tools: false,
  vision: false,
  reasoning: false,
  inferred: false,
  state: "live",
  pricing: null,
  merge_source: "models_dev",
})

describe("the palette's matching", () => {
  it("finds a provider by id and by name", () => {
    const providers = [provider("groq", "Groq Cloud"), provider("nebius", "Nebius")]
    expect(paletteMatches("groq", { providers }).providers.map((p) => p.id)).toEqual(["groq"])
    expect(paletteMatches("cloud", { providers }).providers.map((p) => p.id)).toEqual(["groq"])
    expect(paletteMatches("NEB", { providers }).providers.map((p) => p.id)).toEqual(["nebius"])
  })

  it("filters the whole catalog before capping the list", () => {
    // The old palette sliced the first fifty models and filtered inside them,
    // so a model further down never matched however exactly it was named.
    const models = [
      ...Array.from({ length: 80 }, (_, i) => model(`filler-${i}`, ["groq"])),
      model("claude-opus", ["anthropic"]),
    ]
    expect(paletteMatches("opus", { models }).models.map((m) => m.model)).toEqual(["claude-opus"])
    expect(paletteMatches("filler", { models }).models).toHaveLength(50)
  })

  it("matches a model by the provider that serves it", () => {
    const models = [model("m1", ["groq"]), model("m2", ["nebius"])]
    expect(paletteMatches("nebius", { models }).models.map((m) => m.model)).toEqual(["m2"])
  })

  it("lists nothing under any group when nothing matches", () => {
    // One "no matches" row, never a stack of empty headings.
    const got = paletteMatches("zzz", {
      providers: [provider("groq", "Groq")],
      aliases: { fast: ["groq/m"] },
      models: [model("m", ["groq"])],
    })
    expect(got.destinations).toEqual([])
    expect(got.providers).toEqual([])
    expect(got.aliases).toEqual([])
    expect(got.models).toEqual([])
    expect(got.requestId).toBeNull()
  })

  it("offers the destinations and every provider before anything is typed", () => {
    const got = paletteMatches("", { providers: [provider("groq", "Groq")], models: [model("m", ["groq"])] })
    expect(got.destinations.map((d) => d.label)).toContain("Providers")
    expect(got.providers).toHaveLength(1)
    // The catalog is hundreds of rows; it only appears once it is asked for.
    expect(got.models).toEqual([])
  })

  it("recognises a request id by shape", () => {
    expect(paletteMatches("01abc9ff", {}).requestId).toBe("01abc9ff")
    expect(paletteMatches("groq", {}).requestId).toBeNull()
  })
})
