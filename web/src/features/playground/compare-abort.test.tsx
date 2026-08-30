import { render, screen, waitFor } from "@testing-library/react"
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

async function startARun() {
  render(<Compare config={{ ...emptyConfig(), model: "gpt" }} />)
  for (const box of screen.getAllByRole("textbox", { name: /model/i })) {
    await userEvent.type(box, "gpt")
  }
  await userEvent.type(screen.getByPlaceholderText("Prompt"), "compare these")
  await userEvent.click(screen.getByRole("button", { name: "Run" }))
  await waitFor(() => expect(signals).toHaveLength(2))
}

describe("a Compare column that stops being watched", () => {
  beforeEach(() => {
    streamMock.mockReset()
    signals.length = 0
    hangs()
  })

  it("aborts its request when a third column is removed", async () => {
    // Left running, the orphan keeps costing tokens for output nothing renders,
    // and busy — which counts streaming columns — drops while it arrives, so
    // Run re-enables mid-stream.
    await startARun()
    await userEvent.click(screen.getByRole("button", { name: "Add a column" }))
    const third = screen.getAllByRole("textbox", { name: /model/i })[2]!
    await userEvent.type(third, "gpt")
    // Still labelled "Running…" at this point — the first run is still
    // streaming — so an exact "Run" match would miss the very button
    // under test.
    await userEvent.click(screen.getByRole("button", { name: /run/i }))
    await waitFor(() => expect(signals).toHaveLength(5))

    const removes = screen.getAllByRole("button", { name: /remove/i })
    await userEvent.click(removes[removes.length - 1]!)
    await waitFor(() => expect(signals[4]!.aborted).toBe(true))
    expect(signals[3]!.aborted).toBe(false)
  })

  it("abandons the previous run when Run is pressed again", async () => {
    await startARun()
    // Still labelled "Running…" at this point — the first run is still
    // streaming — so an exact "Run" match would miss the very button
    // under test.
    await userEvent.click(screen.getByRole("button", { name: /run/i }))
    await waitFor(() => expect(signals).toHaveLength(4))
    expect(signals[0]!.aborted).toBe(true)
    expect(signals[1]!.aborted).toBe(true)
    expect(signals[2]!.aborted).toBe(false)
  })
})
