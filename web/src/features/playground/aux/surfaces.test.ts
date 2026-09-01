import { describe, it, expect } from "vitest"
import {
  auxBodyFor,
  catalogSurfaceFor,
  countBodyFor,
  isAuxReady,
  readCount,
  vectorPreview,
} from "./surfaces"

describe("auxBodyFor", () => {
  it("puts surface-specific fields under body and the model beside it", () => {
    // The runner merges the model over the body, so a model typed in the
    // panel wins over one left in a raw body from a previous run.
    expect(auxBodyFor("embeddings", { model: "e5", input: "hello", dimensions: "256" })).toEqual({
      surface: "embeddings",
      model: "e5",
      body: { input: "hello", dimensions: 256 },
    })
  })

  it("omits a blank optional field rather than sending an empty string", () => {
    expect(auxBodyFor("embeddings", { model: "e5", input: "hi", dimensions: "" })).toEqual({
      surface: "embeddings", model: "e5", body: { input: "hi" },
    })
  })

  it("carries a file and its name for transcription", () => {
    expect(
      auxBodyFor("transcriptions", { model: "whisper-1", file_b64: "AAA=", filename: "a.wav" }),
    ).toEqual({
      surface: "transcriptions", model: "whisper-1",
      file_b64: "AAA=", filename: "a.wav", body: {},
    })
  })
})

describe("vectorPreview", () => {
  it("shows the leading components and the full length", () => {
    // A 1536-component vector printed whole is unreadable; the length is the
    // fact an operator is checking.
    const preview = vectorPreview([0.11111, 0.22222, 0.33333, 0.44444], 2)
    expect(preview).toContain("0.111")
    expect(preview).toContain("0.222")
    expect(preview).not.toContain("0.333")
    expect(preview).toContain("4")
  })

  it("does not claim a truncation that did not happen", () => {
    expect(vectorPreview([0.5], 4)).not.toContain("…")
  })
})

describe("auxiliary readiness", () => {
  it.each([
    ["embeddings", { input: "hello" }],
    ["embeddings", { model: "m" }],
    ["rerank", { model: "m", documents: "one" }],
    ["rerank", { model: "m", query: "best" }],
    ["transcriptions", { model: "m" }],
    ["count", { model: "m", dialect: "anthropic" }],
  ] as const)("does not run %s with a required value missing", (surface, form) => {
    expect(isAuxReady(surface, form)).toBe(false)
  })

  it.each([
    ["embeddings", { model: "m", input: "hello" }],
    ["rerank", { model: "m", query: "best", documents: "one\ntwo" }],
    ["transcriptions", { model: "m", file_b64: "AAA=" }],
    ["count", { model: "m", dialect: "gemini", prompt: "hello" }],
  ] as const)("runs %s when its required values are present", (surface, form) => {
    expect(isAuxReady(surface, form)).toBe(true)
  })
})

describe("token count", () => {
  it("offers models with the gateway's LLM capability", () => {
    expect(catalogSurfaceFor("count")).toBe("llm")
  })

  it("maps the count form to the existing endpoint body", () => {
    expect(countBodyFor({ model: "claude", dialect: "anthropic", prompt: "hello" })).toEqual({
      model: "claude", dialect: "anthropic", prompt: "hello",
    })
  })

  it("normalises native and estimated response metadata", async () => {
    const native = await readCount(new Response('{"input_tokens":12}'))
    const estimated = await readCount(new Response('{"totalTokens":9}', {
      headers: { "X-Darkrouter-Estimated": "true" },
    }))
    expect(native).toEqual({ kind: "count", tokens: 12, estimated: false })
    expect(estimated).toEqual({ kind: "count", tokens: 9, estimated: true })
  })
})
