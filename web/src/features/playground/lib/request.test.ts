import { describe, expect, it } from "vitest"
import { chatBody, parseSchema, parseStopLines } from "./request"
import { emptyConfig } from "../config"

const base = { ...emptyConfig(), model: "m", messages: [{ role: "user", content: "hi" }] }

describe("stop sequences", () => {
  it("takes one per line, ignoring blank lines and surrounding space", () => {
    expect(parseStopLines("END\n  STOP  \n\nDONE\n")).toEqual(["END", "STOP", "DONE"])
  })

  it("treats an empty box as no stop sequences", () => {
    expect(parseStopLines("   \n\n")).toEqual([])
  })
})

describe("the structured output schema", () => {
  it("accepts a JSON object", () => {
    expect(parseSchema('{"type":"object"}')).toEqual({ schema: { type: "object" } })
  })

  it("treats an empty box as no schema rather than as an error", () => {
    expect(parseSchema("  ")).toEqual({})
  })

  it("names a parse failure instead of sending nothing", () => {
    const out = parseSchema("{not json")
    expect(out.schema).toBeUndefined()
    expect(out.error).toMatch(/json/i)
  })

  it("refuses a bare array, which is not a schema", () => {
    expect(parseSchema("[1,2]").error).toMatch(/object/i)
  })
})

describe("building the request body", () => {
  it("sends top_p on every dialect", () => {
    for (const dialect of ["openai", "anthropic", "gemini"] as const) {
      expect(chatBody({ ...base, dialect, topP: "0.9" }).top_p).toBe(0.9)
    }
  })

  it("drops top_k on openai, whose edge never reads it", () => {
    // Sending it would look like a setting that works. It is dropped here, at
    // the boundary, so the request carries only what the wire can express.
    expect(chatBody({ ...base, dialect: "openai", topK: "40" }).top_k).toBeUndefined()
    expect(chatBody({ ...base, dialect: "anthropic", topK: "40" }).top_k).toBe(40)
  })

  it("drops the schema on anthropic, whose edge never reads it", () => {
    const raw = '{"type":"object"}'
    expect(chatBody({ ...base, dialect: "anthropic", schemaRaw: raw }).response_schema).toBeUndefined()
    expect(chatBody({ ...base, dialect: "gemini", schemaRaw: raw }).response_schema).toEqual({
      type: "object",
    })
  })

  it("sends effort only on openai and budget only on the other two", () => {
    const openai = chatBody({ ...base, dialect: "openai", reasoningEffort: "high", reasoningBudget: "2048" })
    expect(openai.reasoning_effort).toBe("high")
    expect(openai.reasoning_budget).toBeUndefined()

    const anthropic = chatBody({ ...base, dialect: "anthropic", reasoningEffort: "high", reasoningBudget: "2048" })
    expect(anthropic.reasoning_effort).toBeUndefined()
    expect(anthropic.reasoning_budget).toBe(2048)
  })

  it("omits every new field when its box is empty", () => {
    const body = chatBody(base)
    for (const k of ["top_p", "top_k", "stop", "response_schema", "reasoning_effort", "reasoning_budget"]) {
      expect(body).not.toHaveProperty(k)
    }
  })

  it("still sends the transcript and the settings that predate this", () => {
    const body = chatBody({ ...base, temperature: "0.2", maxTokens: "100", system: "be brief" })
    expect(body.messages).toHaveLength(1)
    expect(body.temperature).toBe(0.2)
    expect(body.max_tokens).toBe(100)
    expect(body.system).toBe("be brief")
  })
})
