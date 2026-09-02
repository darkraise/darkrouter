import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { Compare } from "./compare"
import { emptyConfig } from "./config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

vi.mock("../shell/model-combobox", () => ({
  ModelCombobox: ({ label, value, onChange }: {
    label: string
    value: string
    onChange: (next: string) => void
  }) => <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />,
  useModelCandidates: () => ({ candidates: [], loading: false }),
}))

const { streamMock, traceMock } = vi.hoisted(() => ({ streamMock: vi.fn(), traceMock: vi.fn() }))

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  stream: streamMock,
}))

vi.mock("./metrics", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./metrics")>()),
  traceWhenWritten: traceMock,
}))

vi.mock("../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [] }),
}))

describe("what a compare column reports", () => {
  beforeEach(() => {
    streamMock.mockReset()
    traceMock.mockReset()
    streamMock.mockImplementation(async function* (
      _path: string,
      _body: unknown,
      onStart?: (s: { requestId: string }) => void,
    ) {
      onStart?.({ requestId: "01A" })
      yield `data: ${JSON.stringify({ choices: [{ delta: { content: "an answer" } }] })}\n\n`
    })
    traceMock.mockResolvedValue({
      id: "01A", tokens_in: 1200, tokens_out: 40, cost_micros: 3400, total_ms: 1200, attempts: [],
    })
  })

  it("shows tokens and cost beside the latency once the trace lands", async () => {
    // A comparison is of what each model spent as much as of what it said;
    // a column with only a duration answered half the question.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <Compare config={{ ...emptyConfig(), model: "gpt" }} onConfigChange={() => {}} />
      </QueryClientProvider>,
    )
    for (const box of screen.getAllByRole("textbox", { name: /model/i })) {
      await userEvent.type(box, "gpt")
    }
    await userEvent.type(screen.getByPlaceholderText("Prompt"), "compare these")
    await userEvent.click(screen.getByRole("button", { name: "Run" }))

    expect((await screen.findAllByText(/1,200 in · 40 out/)).length).toBe(2)
    expect(screen.getAllByText("$0.0034")).toHaveLength(2)
  })
})
