import { render, screen } from "@testing-library/react"
import { describe, it, expect, vi } from "vitest"
import {
  attemptSegments, ladderRows, waterfallRows, BodiesPanel, SurfaceMetaSection, metaValue,
  TraceDrawer, traceErrorMessage,
} from "./trace-drawer"
import { ApiError } from "../../lib/api"
import type { TraceAttempt } from "../../lib/api-types"

const traceMock = vi.hoisted(() => vi.fn())

vi.mock("../../lib/queries", () => ({ useTrace: traceMock }))
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, search }: { children: React.ReactNode; search: unknown }) => (
    <a href="/playground" data-search={JSON.stringify(search)}>{children}</a>
  ),
}))

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

describe("trace navigation", () => {
  it("opens the seeded request in Chat regardless of the remembered surface", () => {
    traceMock.mockReturnValue({
      data: {
        id: "01TRACE",
        ts_ms: 0,
        dialect: "openai",
        surface: "chat",
        source: "console",
        alias: "",
        model: "m",
        final_model: "m",
        provider: "groq",
        status: "success",
        cost_micros: 0,
        tokens_in: 1,
        tokens_out: 2,
        cache_read_tokens: 0,
        reasoning_tokens: 0,
        ttft_ms: 10,
        total_ms: 20,
        attempts: [],
        candidates: [],
        skips: [],
        warnings: [],
      },
      isError: false,
    })

    render(<TraceDrawer id="01TRACE" onClose={() => {}} />)

    expect(screen.getByRole("link", { name: /open in playground/i })).toHaveAttribute(
      "data-search",
      JSON.stringify({ mode: "chat", seed: "01TRACE" }),
    )
  })

  it("shows reasoning tokens as part of output tokens", () => {
    traceMock.mockReturnValue({
      data: {
        id: "01TRACE", ts_ms: 0, dialect: "openai", surface: "chat", source: "console",
        model: "m", final_model: "m", provider: "groq", status: "success",
        cost_micros: 0, tokens_in: 10, tokens_out: 30, cache_read_tokens: 0,
        reasoning_tokens: 20, ttft_ms: 10, total_ms: 20,
        attempts: [], candidates: [], skips: [], warnings: [],
      },
      isError: false,
    })

    render(<TraceDrawer id="01TRACE" onClose={() => {}} />)

    expect(screen.getByText(/of which reasoning/i)).toHaveTextContent("20")
  })
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

  it("scales latency bars against the request total", () => {
    // Against the total rather than the slowest attempt, so a bar says what
    // share of the wait this attempt was; the waterfall below uses the same
    // scale, and two scales for one trace would disagree about the same row.
    const rows = ladderRows(
      [attempt({ seq: 0, latency_ms: 50 }), attempt({ seq: 1, latency_ms: 100, outcome: "success" })],
      200,
    )
    expect(rows[1]?.latencyPx).toBe(48)
    expect(rows[0]?.latencyPx).toBe(24)
  })

  it("falls back to the slowest attempt when the request has no total", () => {
    const rows = ladderRows([attempt({ seq: 0, latency_ms: 100, outcome: "success" })], null)
    expect(rows[0]?.latencyPx).toBe(96)
  })

  it("reads provider/model, the code, the duration, the path and the cost", () => {
    const [row] = ladderRows(
      [attempt({ seq: 0, outcome: "success", status_code: 200, latency_ms: 570, path: "passthrough", cost_micros: 100 })],
      1000,
    )
    expect(row?.target).toBe("groq/m")
    expect(row?.reasonCode).toBe("200")
    expect(row?.reasonProse).toBe("570 ms · passthrough · $0.0001 · key k")
  })

  it("shows the attempt's error where there is one, and no invented cost", () => {
    const [row] = ladderRows(
      [attempt({ seq: 0, status_code: 0, outcome: "timeout", latency_ms: 30000, error: "deadline exceeded", key_label: "" })],
      30000,
    )
    expect(row?.reasonCode).toBe("timeout")
    expect(row?.reasonProse).toBe("30.0 s · deadline exceeded")
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
  it("says bodies are not stored, without editorialising about the gateway", () => {
    render(<BodiesPanel bodies={undefined} />)
    expect(screen.getByText("Bodies are not stored for this request.")).toBeInTheDocument()
  })

  it("says the same thing for an empty list as for an absent one", () => {
    render(<BodiesPanel bodies={[]} />)
    expect(screen.getByText("Bodies are not stored for this request.")).toBeInTheDocument()
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

describe("the attempt waterfall", () => {
  it("lays attempts end to end as shares of the request total", () => {
    expect(
      attemptSegments(
        [attempt({ seq: 0, latency_ms: 250 }), attempt({ seq: 1, latency_ms: 500, outcome: "success" })],
        1000,
      ),
    ).toEqual([
      { seq: 0, label: "groq/m", start: 0, fraction: 0.25, served: false },
      { seq: 1, label: "groq/m", start: 0.25, fraction: 0.5, served: true },
    ])
  })

  it("clamps attempts that overrun the total rather than drawing past the track", () => {
    const [first, second] = attemptSegments(
      [attempt({ seq: 0, latency_ms: 800 }), attempt({ seq: 1, latency_ms: 800, outcome: "success" })],
      1000,
    )
    expect(first?.fraction).toBe(0.8)
    expect(second?.start).toBe(0.8)
    expect(second?.fraction).toBeCloseTo(0.2)
  })

  it("draws nothing without a total to scale against", () => {
    expect(attemptSegments([attempt({ seq: 0 })], null)).toEqual([])
  })
})

describe("what the drawer says when the trace does not load", () => {
  it("tells a missing request apart from a failed fetch", () => {
    expect(traceErrorMessage(new ApiError(404, "not found"))).toMatch(/no request with that id/i)
    expect(traceErrorMessage(new ApiError(500, "log locked"))).toMatch(/could not load this trace/i)
    expect(traceErrorMessage(new ApiError(500, "log locked"))).toMatch(/log locked/)
    expect(traceErrorMessage(new TypeError("Failed to fetch"))).toMatch(/failed to fetch/i)
  })

  it("renders the missing state in the drawer", () => {
    traceMock.mockReturnValue({ data: undefined, isError: true, error: new ApiError(404, "not found") })
    render(<TraceDrawer id="01GONE" onClose={() => {}} />)
    expect(screen.getByText(/no request with that id/i)).toBeInTheDocument()
  })

  it("prints costs and durations the way the rest of the console does", () => {
    traceMock.mockReturnValue({
      data: {
        id: "01TRACE", ts_ms: 0, dialect: "openai", surface: "chat", source: "console",
        model: "m", final_model: "m", provider: "groq", status: "success",
        cost_micros: 3400, tokens_in: 1200, tokens_out: 30, cache_read_tokens: 0,
        ttft_ms: 250, total_ms: 8100,
        attempts: [], candidates: [], skips: [], warnings: [],
      },
      isError: false,
    })
    render(<TraceDrawer id="01TRACE" onClose={() => {}} />)
    expect(screen.getByText("$0.0034")).toBeInTheDocument()
    expect(screen.getAllByText(/8\.1 s/).length).toBeGreaterThan(0)
    expect(screen.getByText(/1,200/)).toBeInTheDocument()
    // The id names the dialog on its own; the playground link is content.
    expect(screen.getByRole("dialog", { name: "01TRACE" })).toBeInTheDocument()
  })
})
