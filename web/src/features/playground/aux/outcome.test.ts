import { describe, it, expect } from "vitest"
import { documentLines, outcomeOfResponse, runSummary } from "./surfaces"

/** A JSON response the way the executor writes one. */
function jsonRes(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  })
}

const noRevoke = () => {}

describe("reading a rerank response", () => {
  it("puts the documents in rank order with their scores", async () => {
    // The wire returns indices into what was sent, in relevance order --
    // not the documents themselves unless they were asked for.
    const out = await outcomeOfResponse(
      "rerank",
      jsonRes({
        results: [
          { index: 2, relevance_score: 0.91 },
          { index: 0, relevance_score: 0.12 },
        ],
      }),
      { documents: "first\nsecond\nthird" },
      noRevoke,
    )
    expect(out).toEqual({
      kind: "rerank",
      ranked: [
        { index: 2, score: 0.91, text: "third" },
        { index: 0, score: 0.12, text: "first" },
      ],
    })
  })

  it("prefers the text the provider sent over the line that was submitted", async () => {
    // A provider asked for documents echoes them back, and its own copy is
    // what it actually ranked.
    const out = await outcomeOfResponse(
      "rerank",
      jsonRes({ results: [{ index: 0, relevance_score: 0.5, document: { text: "echoed" } }] }),
      { documents: "submitted" },
      noRevoke,
    )
    expect(out).toMatchObject({ ranked: [{ text: "echoed" }] })
  })

  it("names a document it cannot resolve rather than drawing a blank row", async () => {
    const out = await outcomeOfResponse(
      "rerank",
      jsonRes({ results: [{ index: 7, relevance_score: 0.5 }] }),
      { documents: "only one" },
      noRevoke,
    )
    expect(out).toMatchObject({ ranked: [{ text: "document 7" }] })
  })
})

describe("reading a moderation response", () => {
  it("orders the categories by score, so the reason is at the top", async () => {
    // A moderation response carries every category the model knows, mostly
    // at zero. Alphabetical order buries the one row that says something.
    const out = await outcomeOfResponse(
      "moderations",
      jsonRes({
        results: [
          {
            flagged: true,
            categories: { violence: true, hate: false },
            category_scores: { violence: 0.98, hate: 0.01 },
          },
        ],
      }),
      {},
      noRevoke,
    )
    expect(out).toEqual({
      kind: "moderation",
      flagged: true,
      scores: [
        { name: "violence", score: 0.98, flagged: true },
        { name: "hate", score: 0.01, flagged: false },
      ],
    })
  })

  it("reports a clean verdict as clean rather than as an absence", async () => {
    const out = await outcomeOfResponse(
      "moderations",
      jsonRes({ results: [{ flagged: false, categories: {}, category_scores: {} }] }),
      {},
      noRevoke,
    )
    expect(out).toMatchObject({ kind: "moderation", flagged: false, scores: [] })
  })
})

describe("reading the other surfaces", () => {
  it("takes the transcript as prose rather than as a tree", async () => {
    const out = await outcomeOfResponse(
      "transcriptions",
      jsonRes({ text: "what was said" }),
      {},
      noRevoke,
    )
    expect(out).toEqual({ kind: "transcript", text: "what was said" })
  })

  it("takes the first embedding vector", async () => {
    const out = await outcomeOfResponse(
      "embeddings",
      jsonRes({ data: [{ embedding: [0.1, -0.2] }] }),
      {},
      noRevoke,
    )
    expect(out).toEqual({ kind: "embedding", vector: [0.1, -0.2] })
  })

  it("falls back to a tree for a shape it does not know", async () => {
    // An unfamiliar response is still a result. Throwing here would report a
    // failed run for one that succeeded.
    const out = await outcomeOfResponse("embeddings", jsonRes({ surprise: true }), {}, noRevoke)
    expect(out).toEqual({ kind: "json", json: { surprise: true } })
  })
})

describe("the line a run is headed by", () => {
  it("uses the query for a rerank, which is what was asked", () => {
    expect(runSummary("rerank", { query: "which is faster", documents: "a\nb" })).toBe(
      "which is faster",
    )
  })

  it("uses the file name for a transcription", () => {
    expect(runSummary("transcriptions", { filename: "call.wav" })).toBe("call.wav")
  })

  it("collapses a long input rather than letting it run off the heading", () => {
    const long = "word ".repeat(40)
    const summary = runSummary("embeddings", { input: long })
    expect(summary.length).toBeLessThanOrEqual(80)
    expect(summary.endsWith("…")).toBe(true)
  })

  it("says so when there is nothing to summarise", () => {
    expect(runSummary("embeddings", {})).toBe("empty input")
  })
})

describe("splitting documents", () => {
  it("drops blank lines and surrounding space", () => {
    expect(documentLines(" a \n\n  b\n")).toEqual(["a", "b"])
  })
})
