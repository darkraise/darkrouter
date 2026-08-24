import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { TraceDrawer } from "./trace-drawer"

// seq is 0-indexed at the source, exactly as exec.recordAttempt writes it.
const failover = {
  id: "01FAIL",
  ts_ms: 1700000000000,
  dialect: "openai",
  surface: "llm",
  model: "fast",
  alias: "fast",
  provider: "b",
  final_model: "m2",
  status: "success",
  tokens_in: 10,
  tokens_out: 20,
  cost_micros: null,
  ttft_ms: 56,
  total_ms: 460,
  candidates: ["a/m1", "b/m2", "c/m3"],
  skips: ["c/m3:cooling", "d/m4:no_credential"],
  attempts: [
    {
      seq: 0,
      provider: "a",
      key_label: "k1",
      model: "m1",
      outcome: "retryable_provider",
      status_code: 500,
      latency_ms: 120,
      error: "upstream 500",
      path: "passthrough",
    },
    {
      seq: 1,
      provider: "b",
      key_label: "k2",
      model: "m2",
      outcome: "success",
      status_code: 200,
      latency_ms: 340,
      path: "ir",
    },
  ],
  warnings: ["top_k -> openai: not expressible"],
  surface_meta: {},
  response_bytes: 0,
  response_content_type: "",
  bodies: [],
}

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify(failover), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    ),
  )
})

function renderDrawer() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <TraceDrawer id="01FAIL" onClose={() => {}} />
    </QueryClientProvider>,
  )
}

describe("the trace drawer", () => {
  it("renders every attempt of a failover in order", async () => {
    renderDrawer()
    // Spec §6: three tries must read as three labelled rows.
    expect(await screen.findByText("retryable_provider")).toBeInTheDocument()
    expect(await screen.findByText("upstream 500")).toBeInTheDocument()
    expect(await screen.findByText("120 ms")).toBeInTheDocument()
    expect(await screen.findByText("340 ms")).toBeInTheDocument()
  })

  it("numbers the attempts from one", async () => {
    renderDrawer()
    // seq 0 displayed as "Attempt 0" reads as a bug to an operator.
    const rows = await screen.findAllByRole("row")
    const cells = rows.map((r) => r.firstElementChild?.textContent)
    expect(cells).toContain("1")
    expect(cells).toContain("2")
    expect(cells).not.toContain("0")
  })

  it("says why a candidate was never tried", async () => {
    renderDrawer()
    // The half of the screen that explains the routing decision rather than
    // the failover.
    expect(await screen.findByText("cooling")).toBeInTheDocument()
    expect(await screen.findByText("no_credential")).toBeInTheDocument()
    expect(await screen.findByText("d/m4")).toBeInTheDocument()
  })

  it("says bodies were not captured rather than showing an empty panel", async () => {
    renderDrawer()
    expect(await screen.findByText(/not captured/i)).toBeInTheDocument()
  })

  it("shows which path each attempt took", async () => {
    renderDrawer()
    // Spec §11's first done criterion is only checkable from the drawer if a
    // retried attempt and its passthrough predecessor read as different rows.
    expect(await screen.findByText("passthrough")).toBeInTheDocument()
    expect(await screen.findByText("ir")).toBeInTheDocument()
  })
})
