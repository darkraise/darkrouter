import { describe, expect, it } from "vitest"
import { detailRows, relativeTime } from "./test-log-tab"
import type { RequestRow } from "../../lib/api-types"

const row = (over: Partial<RequestRow> = {}): RequestRow =>
  ({
    id: "01ABC", ts_ms: Date.now(), dialect: "openai", surface: "llm",
    model: "openai/gpt-oss-20b", provider: "groq", status: "success", source: "console",
    tokens_in: 78, tokens_out: 34, cache_read_tokens: 0, cost_micros: null,
    ttft_ms: 240, total_ms: 633, attempts: 1, path: "passthrough",
    ...over,
  }) as RequestRow

describe("how long ago a request was", () => {
  const now = new Date("2026-08-28T12:00:00Z").getTime()

  it("reads as relative while that is the useful reading", () => {
    expect(relativeTime(now - 2_000, now)).toBe("just now")
    expect(relativeTime(now - 30_000, now)).toBe("30s ago")
    expect(relativeTime(now - 5 * 60_000, now)).toBe("5m ago")
    expect(relativeTime(now - 3 * 3_600_000, now)).toBe("3h ago")
  })

  it("falls back to a date once relative stops carrying", () => {
    // "48h ago" is a number nobody converts; the date is what they wanted.
    expect(relativeTime(now - 2 * 86_400_000, now)).toMatch(/\d/)
    expect(relativeTime(now - 2 * 86_400_000, now)).not.toMatch(/ago/)
  })
})

describe("an expanded log row", () => {
  it("names the alias and the model it resolved to", () => {
    // Two different facts: what the client asked for, and what served. A row
    // showing only one cannot explain a routing decision.
    const detail = detailRows(row({ alias: "fast", final_model: "llama-3.3" }))
    const asked = detail.find((d) => d.label === "Asked for")?.value
    expect(asked).toBe("fast → openai/gpt-oss-20b")
    expect(detail.find((d) => d.label === "Served")?.value).toBe("llama-3.3")
  })

  it("carries the error code beside a failed status", () => {
    const detail = detailRows(row({ status: "error", error_code: "upstream_401" }))
    expect(detail.find((d) => d.label === "Status")?.value).toBe("error · upstream_401")
  })

  it("says nothing rather than zero for a timing that was never taken", () => {
    const detail = detailRows(row({ ttft_ms: null, total_ms: null }))
    expect(detail.find((d) => d.label === "First token")?.value).toBe("—")
    expect(detail.find((d) => d.label === "Latency")?.value).toBe("—")
  })
})
