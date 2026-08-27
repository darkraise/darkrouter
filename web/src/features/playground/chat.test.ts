import { describe, it, expect } from "vitest"
import { parseTools, chatBody, seedFromTrace } from "./chat"
import type { RequestTrace } from "../../lib/api-types"

describe("parseTools", () => {
  it("accepts a JSON array of objects", () => {
    expect(parseTools('[{"type":"function"}]')).toEqual({ tools: [{ type: "function" }] })
  })

  it("treats an empty box as no tools rather than as an error", () => {
    expect(parseTools("   ")).toEqual({})
  })

  it("names a parse failure instead of sending nothing", () => {
    // Silently dropping malformed tools would answer a different question
    // from the one the operator asked, and look like the model ignoring them.
    const out = parseTools("{not json")
    expect(out.tools).toBeUndefined()
    expect(out.error).toMatch(/json/i)
  })

  it("refuses a bare object, which is not the wire shape", () => {
    expect(parseTools('{"type":"function"}').error).toMatch(/array/i)
  })
})

describe("chatBody", () => {
  it("sends the transcript, not just the last turn", () => {
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "", maxTokens: "", toolsRaw: "",
      messages: [
        { role: "user", content: "hi" },
        { role: "assistant", content: "hello" },
        { role: "user", content: "go on" },
      ],
    })
    expect(body.messages).toHaveLength(3)
  })

  it("omits empty numeric fields rather than sending zero", () => {
    // A temperature of 0 is a real setting. An empty box is not, and sending
    // zero for it would quietly make every run deterministic.
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.temperature).toBeUndefined()
    expect(body.max_tokens).toBeUndefined()
  })

  it("sends an explicit zero temperature", () => {
    const body = chatBody({
      model: "m", dialect: "openai", system: "", stream: true,
      temperature: "0", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.temperature).toBe(0)
  })

  it("carries the dialect through", () => {
    const body = chatBody({
      model: "m", dialect: "anthropic", system: "be terse", stream: false,
      temperature: "", maxTokens: "", toolsRaw: "", messages: [],
    })
    expect(body.dialect).toBe("anthropic")
    expect(body.system).toBe("be terse")
    expect(body.stream).toBe(false)
  })
})

describe("seedFromTrace", () => {
  it("takes the model the client asked for, not the one that served", () => {
    // Replaying against the serving provider would not reproduce the routing
    // decision, which is usually the thing under investigation.
    const seeded = seedFromTrace({
      id: "r1", model: "fast", final_model: "llama-3.3", provider: "groq",
    } as RequestTrace)
    expect(seeded.model).toBe("fast")
  })

  it("carries the dialect the request arrived on", () => {
    const seeded = seedFromTrace({ id: "r1", model: "m", dialect: "anthropic" } as RequestTrace)
    expect(seeded.dialect).toBe("anthropic")
  })

  it("falls back to openai for a dialect the playground cannot send", () => {
    // The log records every inbound dialect, including the OpenAI Responses
    // wire, which this screen has no control for.
    const seeded = seedFromTrace({ id: "r1", model: "m", dialect: "responses" } as RequestTrace)
    expect(seeded.dialect).toBe("openai")
  })
})
