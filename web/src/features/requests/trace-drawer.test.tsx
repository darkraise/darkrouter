import { render, screen } from "@testing-library/react"
import { describe, it, expect } from "vitest"
import {
  ladderRows, waterfallRows, BodiesPanel, SurfaceMetaSection, metaValue,
} from "./trace-drawer"
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

describe("the waterfall", () => {
  it("scales time-to-first-token against the total", () => {
    expect(waterfallRows({ ttft_ms: 250, total_ms: 1000 })).toEqual([
      { label: "time to first token", ms: 250, fraction: 0.25 },
      { label: "total", ms: 1000, fraction: 1 },
    ])
  })

  it("renders no ttft row for a unary response", () => {
    // A non-streamed request has no first token, and a zero-width bar would
    // read as an instant one.
    expect(waterfallRows({ ttft_ms: null, total_ms: 900 })).toEqual([
      { label: "total", ms: 900, fraction: 1 },
    ])
  })

  it("renders nothing at all when the request never completed", () => {
    expect(waterfallRows({ ttft_ms: null, total_ms: null })).toEqual([])
  })

  it("clamps a ttft that exceeds the total rather than overflowing the bar", () => {
    // Clock skew across the two measurements is possible, and a fraction
    // above one draws a bar outside its own track.
    const rows = waterfallRows({ ttft_ms: 1200, total_ms: 1000 })
    expect(rows[0]?.fraction).toBe(1)
  })

  it("renders nothing for a zero total rather than dividing by it", () => {
    expect(waterfallRows({ ttft_ms: null, total_ms: 0 })).toEqual([])
  })
})

describe("the bodies panel", () => {
  it("explains an empty panel instead of drawing an empty box", () => {
    // capture.bodies has a retention sweep and no writer, so this is the
    // permanent state today. §2 makes saying so the requirement.
    render(<BodiesPanel bodies={undefined} />)
    expect(screen.getByText(/capture\.bodies/i)).toBeInTheDocument()
    expect(screen.getByText(/not captured/i)).toBeInTheDocument()
  })

  it("says the same thing for an empty list as for an absent one", () => {
    render(<BodiesPanel bodies={[]} />)
    expect(screen.getByText(/not captured/i)).toBeInTheDocument()
  })

  it("renders each captured body under its kind", () => {
    render(<BodiesPanel bodies={[{ kind: "request", content: "{\"a\":1}" }]} />)
    expect(screen.getByText("request")).toBeInTheDocument()
    expect(screen.getByText(/"a":1/)).toBeInTheDocument()
  })
})

describe("the surface metadata section", () => {
  it("renders nothing for the {} the backend sends on every ordinary request", () => {
    // capture writes {} rather than null for a NOT NULL column, so a truthy
    // check on the object passes and draws a heading over an empty list.
    const { container } = render(<SurfaceMetaSection meta={{}} />)
    expect(container).toBeEmptyDOMElement()
  })

  it("renders nothing when meta is absent entirely", () => {
    const { container } = render(<SurfaceMetaSection meta={undefined} />)
    expect(container).toBeEmptyDOMElement()
  })

  it("renders each entry when the object actually has one", () => {
    render(<SurfaceMetaSection meta={{ region: "us-east" }} />)
    expect(screen.getByText("region")).toBeInTheDocument()
    expect(screen.getByText("us-east")).toBeInTheDocument()
  })
})

describe("a surface metadata value", () => {
  it("prints a scalar as itself", () => {
    expect(metaValue(3)).toBe("3")
    expect(metaValue("wav")).toBe("wav")
    expect(metaValue(false)).toBe("false")
  })

  it("prints a nested value as JSON rather than as [object Object]", () => {
    // surface_meta is Record<string, unknown> and is written from arbitrary
    // JSON on the Go side. Every writer emits scalars today; nothing in the
    // type says the next one must.
    expect(metaValue({ w: 1024, h: 768 })).toBe('{"w":1024,"h":768}')
    expect(metaValue(["a", "b"])).toBe('["a","b"]')
  })

  it("does not print null as the word object", () => {
    expect(metaValue(null)).toBe("null")
  })
})
