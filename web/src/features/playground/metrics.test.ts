import { describe, expect, it } from "vitest"
import { NO_METRICS, metricsFromTrace, tokensPerSecond } from "./metrics"
import type { RequestTrace } from "../../lib/api-types"

describe("the run readings", () => {
  it("computes a rate only when both halves are known", () => {
    // A rate derived from a missing count is a fabricated number, and it
    // would sit in the strip looking exactly as authoritative as a real one.
    expect(tokensPerSecond({ ...NO_METRICS, totalMs: 1000 })).toBeNull()
    expect(tokensPerSecond({ ...NO_METRICS, tokensOut: 100 })).toBeNull()
    expect(tokensPerSecond({ ...NO_METRICS, tokensOut: 100, totalMs: 2000 })).toBe(50)
  })

  it("refuses to divide by a zero duration", () => {
    expect(tokensPerSecond({ ...NO_METRICS, tokensOut: 10, totalMs: 0 })).toBeNull()
  })

  it("takes token counts from the gateway rather than guessing", () => {
    // The gateway counted them. A client-side tokenisation would disagree
    // with every other screen that reports the same request.
    const measured = { ...NO_METRICS, ttftMs: 120, totalMs: 900 }
    const merged = metricsFromTrace(measured, {
      tokens_in: 12,
      tokens_out: 34,
    } as RequestTrace)
    expect(merged).toMatchObject({ ttftMs: 120, totalMs: 900, tokensIn: 12, tokensOut: 34 })
  })
})
