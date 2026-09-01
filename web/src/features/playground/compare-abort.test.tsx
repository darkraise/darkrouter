import { render, screen, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { Compare } from "./compare"
import { emptyConfig } from "./config"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

vi.mock("../shell/model-combobox", () => ({
  ModelCombobox: ({ label, value, onChange, disabled }: {
    label: string
    value: string
    onChange: (next: string) => void
    disabled?: boolean
  }) => <input aria-label={label} value={value} disabled={disabled} onChange={(e) => onChange(e.target.value)} />,
  useModelCandidates: () => ({ candidates: [], loading: false }),
}))

const { streamMock, signals } = vi.hoisted(() => ({
  streamMock: vi.fn(),
  signals: [] as AbortSignal[],
}))

vi.mock("../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/api")>()),
  stream: streamMock,
}))

/** A stream that starts and then never finishes until its signal aborts, which
 *  is the state a removed or rerun column is actually in. */
function hangs() {
  streamMock.mockImplementation(async function* (
    _path: string,
    _body: unknown,
    onStart?: (s: { requestId: string }) => void,
    signal?: AbortSignal,
  ) {
    if (signal) signals.push(signal)
    onStart?.({ requestId: "01A" })
    yield `data: ${JSON.stringify({ choices: [{ delta: { content: "…" } }] })}\n\n`
    await new Promise<void>((_resolve, reject) => {
      signal?.addEventListener("abort", () => reject(Object.assign(new Error("aborted"), { name: "AbortError" })))
    })
  })
}

/** Names every column, then runs. Clears first and checks the result, so the
 *  helper still names each column "gpt" if a column ever starts out carrying
 *  one -- typing into a filled box would otherwise quietly produce "gptgpt". */
async function nameEveryColumn() {
  for (const box of screen.getAllByRole("textbox", { name: /model/i })) {
    await userEvent.clear(box)
    await userEvent.type(box, "gpt")
    expect(box).toHaveValue("gpt")
  }
}

async function startARun() {
  renderCompare({ ...emptyConfig(), model: "gpt" })
  await nameEveryColumn()
  await userEvent.type(screen.getByPlaceholderText("Prompt"), "compare these")
  await userEvent.click(screen.getByRole("button", { name: "Run" }))
  await waitFor(() => expect(signals).toHaveLength(2))
}

// Compare carries the request pane that every column is sent under, and the
// pane's preset picker reads a list. Stubbed rather than served, so these
// tests stay about the columns.
vi.mock("../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [] }),
}))

/** Compare carries the request pane now, whose preset picker both reads a
 *  query and owns a mutation. Both need a client above them. */
function renderCompare(config: Parameters<typeof Compare>[0]["config"]) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <Compare config={config} onConfigChange={() => {}} />
    </QueryClientProvider>,
  )
}

describe("a Compare column that stops being watched", () => {
  beforeEach(() => {
    streamMock.mockReset()
    signals.length = 0
    hangs()
  })

  it("aborts every request when Compare unmounts", async () => {
    const mounted = renderCompare({ ...emptyConfig(), model: "gpt" })
    await userEvent.click(screen.getByRole("button", { name: "Add model" }))
    await nameEveryColumn()
    await userEvent.type(screen.getByPlaceholderText("Prompt"), "compare these")
    await userEvent.click(screen.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(signals).toHaveLength(3))

    mounted.unmount()
    await waitFor(() => expect(signals.every((signal) => signal.aborted)).toBe(true))
  })

  it("will not start a second run while columns are still streaming", async () => {
    // The guard that stops two runs appending into the same columns. It is
    // also the one a removed streaming column used to slip past: dropping out
    // of the busy count re-enabled Run while the orphan was still arriving.
    await startARun()
    expect(screen.getByRole("button", { name: /running/i })).toBeDisabled()

    expect(screen.getByRole("button", { name: "Add model" })).toBeDisabled()
    expect(screen.getAllByRole("button", { name: /remove/i })
      .every((button) => button.hasAttribute("disabled"))).toBe(true)
    expect(screen.getByRole("button", { name: /running/i })).toBeDisabled()
    expect(signals[0]!.aborted).toBe(false)
    expect(signals[1]!.aborted).toBe(false)
  })

  it("keeps the model labels fixed while their outputs are streaming", async () => {
    await startARun()
    const first = screen.getAllByRole("textbox", { name: /model/i })[0]!

    expect(first).toBeDisabled()
    await userEvent.type(first, "-changed")
    expect(first).toHaveValue("gpt")
  })

  it("aborts every live column when Compare becomes inactive", async () => {
    const { rerender } = renderCompare({ ...emptyConfig(), model: "gpt" })
    await nameEveryColumn()
    await userEvent.type(screen.getByPlaceholderText("Prompt"), "compare these")
    await userEvent.click(screen.getByRole("button", { name: "Run" }))
    await waitFor(() => expect(signals).toHaveLength(2))

    rerender(
      <QueryClientProvider client={new QueryClient()}>
        <Compare config={{ ...emptyConfig(), model: "gpt" }} onConfigChange={() => {}} active={false} />
      </QueryClientProvider>,
    )

    await waitFor(() => expect(signals.every((signal) => signal.aborted)).toBe(true))
    await waitFor(() => expect(screen.getAllByRole("img", { name: "stopped" })).toHaveLength(2))
    expect(screen.getByRole("button", { name: "Add model" })).toBeEnabled()
  })
})
