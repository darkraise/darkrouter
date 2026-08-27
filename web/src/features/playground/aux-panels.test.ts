import { describe, it, expect } from "vitest"
import { auxBodyFor, vectorPreview, readCount } from "./aux-panels"

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

describe("readCount", () => {
  it("marks a locally estimated count from the response header", () => {
    // The body cannot carry a marker — clients parse these responses
    // strictly — so the header is the only signal there is.
    const res = new Response("{}", { headers: { "X-Darkrouter-Estimated": "true" } })
    expect(readCount(res, { input_tokens: 12 })).toEqual({ tokens: 12, estimated: true })
  })

  it("reads a native count as native", () => {
    expect(readCount(new Response("{}"), { input_tokens: 12 })).toEqual({
      tokens: 12, estimated: false,
    })
  })

  it("reads Gemini's field name too", () => {
    expect(readCount(new Response("{}"), { totalTokens: 40 }).tokens).toBe(40)
  })

  it("reports zero rather than NaN for a shape it does not recognise", () => {
    expect(readCount(new Response("{}"), { surprise: 1 }).tokens).toBe(0)
  })
})
