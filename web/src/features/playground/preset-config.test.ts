import { describe, expect, it } from "vitest"
import { mergeStoredConfig, toStoredConfig } from "./preset-config"
import { emptyConfig } from "./config"

describe("reading a stored preset", () => {
  it("takes the model and dialect from the columns, not the blob", () => {
    const out = mergeStoredConfig({ model: "ignored", dialect: "ignored" }, "claude", "anthropic")
    expect(out.model).toBe("claude")
    expect(out.dialect).toBe("anthropic")
  })

  it("keeps every stored value whose type matches", () => {
    const out = mergeStoredConfig(
      { system: "be brief", topK: "40", stream: false },
      "m",
      "anthropic",
    )
    expect(out.system).toBe("be brief")
    expect(out.topK).toBe("40")
    expect(out.stream).toBe(false)
  })

  it("defaults a field the preset was saved before", () => {
    // A blob written by an older console has no key for a control added since.
    // It must arrive at its default rather than as undefined: chatBody calls
    // .split and .trim on these without checking.
    const out = mergeStoredConfig({ system: "hi" }, "m", "openai")
    expect(out.stopRaw).toBe("")
    expect(out.schemaRaw).toBe("")
    expect(out.reasoningBudget).toBe("")
  })

  it("drops a stored value of the wrong type rather than passing it on", () => {
    // Reachable through the operator-facing API. Passing 42 through would be a
    // TypeError at parseStopLines, not a degraded setting.
    const out = mergeStoredConfig({ stopRaw: 42, schemaRaw: null, stream: "yes" }, "m", "openai")
    expect(out.stopRaw).toBe("")
    expect(out.schemaRaw).toBe("")
    expect(out.stream).toBe(true)
  })

  it("ignores a key the config does not have", () => {
    const out = mergeStoredConfig({ fieldFromTheFuture: 7 }, "m", "openai")
    expect(out).not.toHaveProperty("fieldFromTheFuture")
  })

  it("survives a blob that is not an object at all", () => {
    for (const junk of [null, "text", 7, [1, 2]]) {
      const out = mergeStoredConfig(junk, "m", "openai")
      expect(out).toEqual({ ...emptyConfig(), model: "m", dialect: "openai" })
    }
  })
})

describe("writing a preset", () => {
  it("stores everything except model and dialect, which are columns", () => {
    const stored = toStoredConfig({ ...emptyConfig(), model: "m", dialect: "gemini", topP: "0.9" })
    expect(stored).not.toHaveProperty("model")
    expect(stored).not.toHaveProperty("dialect")
    expect(stored.topP).toBe("0.9")
    expect(stored.stream).toBe(true)
  })

  it("round-trips a config unchanged", () => {
    const original = { ...emptyConfig(), model: "m", dialect: "anthropic" as const, topK: "40" }
    expect(mergeStoredConfig(toStoredConfig(original), "m", "anthropic")).toEqual(original)
  })
})
