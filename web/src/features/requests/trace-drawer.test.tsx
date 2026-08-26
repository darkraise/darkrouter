import { describe, it, expect } from "vitest"
import { ladderRows } from "./trace-drawer"
import type { TraceAttempt } from "../../lib/api-types"

const attempt = (over: Partial<TraceAttempt> & { seq: number }): TraceAttempt => ({
  provider: "groq",
  key_label: "k",
  model: "m",
  outcome: "retryable_provider",
  status_code: 503,
  latency_ms: 100,
  error: "",
  path: "",
  tokens_in: 0,
  tokens_out: 0,
  cost_micros: null,
  ...over,
})

describe("trace ladder rows", () => {
  it("fills every mark, because these attempts happened", () => {
    const rows = ladderRows([
      attempt({ seq: 0 }),
      attempt({ seq: 1, outcome: "success", status_code: 200 }),
    ])
    expect(rows.map((r) => r.mark)).toEqual(["failed", "served"])
  })

  it("terminates rows after the one that served", () => {
    // A candidate the router never reached is dimmed rather than drawn as an
    // attempt: nothing was sent to it.
    const rows = ladderRows([
      attempt({ seq: 0 }),
      attempt({ seq: 1, outcome: "success" }),
      attempt({ seq: 2 }),
    ])
    expect(rows.map((r) => r.terminated)).toEqual([false, false, true])
  })

  it("finds the serving attempt by outcome, not by position", () => {
    // A request can fail after something served, so the last attempt is not
    // reliably the one that answered.
    const rows = ladderRows([
      attempt({ seq: 0, outcome: "success" }),
      attempt({ seq: 1 }),
    ])
    expect(rows[0]?.mark).toBe("served")
    expect(rows[1]?.terminated).toBe(true)
  })

  it("scales latency bars against the slowest attempt in the trace", () => {
    const rows = ladderRows([
      attempt({ seq: 0, latency_ms: 50 }),
      attempt({ seq: 1, latency_ms: 100, outcome: "success" }),
    ])
    expect(rows[1]?.latencyPx).toBe(96)
    expect(rows[0]?.latencyPx).toBe(48)
  })

  it("ranks from one, not from the stored sequence", () => {
    expect(ladderRows([attempt({ seq: 0 })])[0]?.rank).toBe(1)
  })
})
