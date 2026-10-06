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
  allow_unsanctioned_free: false,
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
  free_tier: null,
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
    // A real id, as the gateway mints them: a ULID, which almost always
    // carries letters past F. The hex-only pattern this replaces said
    // "Nothing matches." to exactly this.
    expect(paletteMatches("01M47PJFXTQNG6Z3WZ39X4QZM2", {}).requestId).toBe(
      "01M47PJFXTQNG6Z3WZ39X4QZM2",
    )
    expect(paletteMatches("  01M47PJFXTQNG6Z3WZ39X4QZM2 ", {}).requestId).toBe(
      "01M47PJFXTQNG6Z3WZ39X4QZM2",
    )
    expect(paletteMatches("groq", {}).requestId).toBeNull()
  })

  it("opens a lower-cased id as the upper-case one the gateway stores", () => {
    expect(paletteMatches("01m47pjfxtqng6z3wz39x4qzm2", {}).requestId).toBe(
      "01M47PJFXTQNG6Z3WZ39X4QZM2",
    )
  })

  it("does not take a word, a hex fragment or a near-ULID for an id", () => {
    expect(paletteMatches("01abc9ff", {}).requestId).toBeNull()
    // 25 and 27 characters, and one with a U, which Crockford base-32 omits.
    expect(paletteMatches("01M47PJFXTQNG6Z3WZ39X4QZM", {}).requestId).toBeNull()
    expect(paletteMatches("01M47PJFXTQNG6Z3WZ39X4QZM22", {}).requestId).toBeNull()
    expect(paletteMatches("01M47PJFXTQNG6Z3WZ39X4QZMU", {}).requestId).toBeNull()
  })

  it("lists nothing from a response of the wrong shape, rather than throwing", () => {
    // The palette is shell: a throw here took the rail and header down with
    // it. These are what an older or broken gateway could put in the cache.
    for (const data of [
      { providers: 5, aliases: "x", models: { m: 1 } },
      { providers: [null, 3, "groq"], aliases: [], models: [null, { model: "m", providers: 7 }] },
    ]) {
      const got = paletteMatches("m", data)
      expect(got.providers).toEqual([])
      expect(got.aliases).toEqual([])
    }
    const got = paletteMatches("m", { models: [{ model: "m", providers: 7 }] })
    expect(got.models.map((m) => m.model)).toEqual(["m"])
  })
})
