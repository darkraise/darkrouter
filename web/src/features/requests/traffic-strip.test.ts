import { describe, expect, it } from "vitest"
import { bucketRequests, edgeLabels } from "./traffic-strip"
import type { TableRow } from "./requests-columns"

const row = (ts_ms: number, status = "success"): TableRow =>
  ({
    id: String(ts_ms), ts_ms, status, dialect: "openai", surface: "llm",
    model: "m", provider: "p", alias: "", attempts: 1, tokens_in: 0,
    tokens_out: 0, total_ms: 10, path: "passthrough",
    failover: "single",
  }) as unknown as TableRow

describe("the traffic strip", () => {
  it("spreads the loaded page across its buckets", () => {
    const rows = [row(0), row(500), row(1000)]
    const buckets = bucketRequests(rows, 4)
    expect(buckets).toHaveLength(4)
    expect(buckets.reduce((n, b) => n + b.total, 0)).toBe(3)
    // The newest row lands in the last bucket rather than off the end.
    expect(buckets[3]?.total).toBe(1)
  })

  it("counts failures inside their bucket, not beside it", () => {
    // A burst of errors is a shape, and it has to be the shape of the bucket
    // the errors actually happened in.
    const buckets = bucketRequests([row(0), row(10, "error")], 2)
    expect(buckets.reduce((n, b) => n + b.failed, 0)).toBe(1)
  })

  it("survives a page that spans no time at all", () => {
    // Every row in the same millisecond would divide by zero on the span.
    const buckets = bucketRequests([row(5), row(5), row(5)], 3)
    expect(buckets.reduce((n, b) => n + b.total, 0)).toBe(3)
  })

  it("draws nothing for an empty page", () => {
    expect(bucketRequests([], 10)).toEqual([])
  })
})

describe("the strip's end labels", () => {
  const t = (iso: string) => new Date(iso).getTime()

  it("shows the clock alone while the page is one day", () => {
    const [from, to] = edgeLabels(t("2026-08-28T19:10:00"), t("2026-08-28T19:14:00"))
    expect(from).not.toMatch(/\//)
    expect(to).not.toMatch(/\//)
  })

  it("shows the date once the page crosses one", () => {
    // The failure this exists for: four days of requests labelled
    // "7:10 PM → 7:14 PM" read as four minutes, which is the exact
    // misreading the strip is meant to prevent.
    const [from, to] = edgeLabels(t("2026-08-24T19:10:00"), t("2026-08-28T21:42:00"))
    expect(from).toMatch(/\//)
    expect(to).toMatch(/\//)
  })
})
