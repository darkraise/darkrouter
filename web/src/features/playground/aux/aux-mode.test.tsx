import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, it, vi } from "vitest"
import { AuxMode } from "./aux-mode"

const { searchMock, traceQueryMock } = vi.hoisted(() => ({
  searchMock: vi.fn(() => ({}) as { seed?: string }),
  traceQueryMock: vi.fn(() => ({ data: undefined, isError: false }) as { data?: unknown; isError: boolean }),
}))

const { postAuxMock, signals } = vi.hoisted(() => ({
  postAuxMock: vi.fn(),
  signals: [] as AbortSignal[],
}))

vi.mock("../../shell/model-combobox", () => ({
  useModelCandidates: () => ({ candidates: [], loading: false }),
  ModelCombobox: ({ onChange, value }: { onChange: (model: string) => void; value: string }) => (
    <>
      <button onClick={() => onChange("embed-model")}>Choose model</button>
      <span data-testid="model">{value}</span>
    </>
  ),
}))
vi.mock("./tool-rail", () => ({
  ToolRail: ({ onSelect }: { onSelect: (surface: "embeddings") => void }) => (
    <button onClick={() => onSelect("embeddings")}>Pick embeddings</button>
  ),
}))
vi.mock("./tool-inputs", () => ({
  ToolInputs: ({ onField, onRun, onFile, form }: {
    onField: (key: string, value: string) => void
    onRun: () => void
    onFile: (file: File) => void
    form: Record<string, string>
  }) => (
    <>
      <button onClick={() => onField("input", "hello")}>Fill input</button>
      <button onClick={onRun}>Run auxiliary</button>
      <button onClick={() => onFile(new File(["x"], "a.wav"))}>Pick file</button>
      <span data-testid="dialect">{form.dialect ?? ""}</span>
    </>
  ),
}))
vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  useModels: () => ({ data: { models: [{ model: "embed-model", providers: ["anthropic-main"] }], aliases: [] } }),
  useProviders: () => ({ data: { providers: [{ id: "anthropic-main", kind: "anthropic" }] } }),
  useTrace: traceQueryMock,
}))
vi.mock("@tanstack/react-router", () => ({ useSearch: () => searchMock() }))
vi.mock("./results", () => ({ RunCard: () => null }))
vi.mock("./run-readings", () => ({ RunReadings: () => null }))
const readFileMock = vi.hoisted(() => vi.fn())
vi.mock("./surfaces", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./surfaces")>()),
  postAux: postAuxMock,
  readFileAsBase64: readFileMock,
}))

describe("Auxiliary visibility", () => {
  it("aborts a request when the surface becomes inactive without unmounting it", async () => {
    postAuxMock.mockImplementation((_body: unknown, signal?: AbortSignal) => {
      if (signal) signals.push(signal)
      return new Promise((_resolve, reject) => {
        signal?.addEventListener("abort", () => {
          reject(Object.assign(new Error("aborted"), { name: "AbortError" }))
        })
      })
    })
    const { rerender } = render(<AuxMode active />)
    await userEvent.click(screen.getByRole("button", { name: "Pick embeddings" }))
    await userEvent.click(screen.getByRole("button", { name: "Choose model" }))
    await userEvent.click(screen.getByRole("button", { name: "Fill input" }))
    await userEvent.click(screen.getByRole("button", { name: "Run auxiliary" }))
    await waitFor(() => expect(signals).toHaveLength(1))

    rerender(<AuxMode active={false} />)

    await waitFor(() => expect(signals[0]?.aborted).toBe(true))
  })

  it("reports a file that could not be read instead of hanging", async () => {
    // FileReader rejects on a file that vanished or cannot be opened; an
    // unhandled rejection left the form silently without its file.
    readFileMock.mockRejectedValue(new Error("file vanished"))
    render(<AuxMode active />)
    await userEvent.click(screen.getByRole("button", { name: "Pick file" }))
    expect(await screen.findByRole("alert")).toHaveTextContent(/file vanished/)
  })

  it("opens on the first tool the rail lists", () => {
    // The rail leads with Token Count; a screen that opened on the second
    // entry showed a selected row that was not the top one.
    render(<AuxMode active />)
    expect(screen.getByText("Token Count")).toBeInTheDocument()
  })

  it("offers the tools as a select where the rail is folded away", async () => {
    // Below lg the rail panel is hidden; at phone width it had squeezed the
    // names to "To…", "E…". The select carries the same seven, with their
    // blurbs.
    render(<AuxMode active />)
    await userEvent.click(screen.getByLabelText("Tool"))
    await userEvent.click(await screen.findByRole("option", { name: /^Embeddings — turn text into a vector/ }))
    expect(screen.getByText("Embeddings")).toBeInTheDocument()
  })

  it("defaults the counting dialect to the model's provider", async () => {
    render(<AuxMode active />)
    await userEvent.click(screen.getByRole("button", { name: "Choose model" }))
    expect(screen.getByTestId("dialect")).toHaveTextContent("anthropic")
  })
})

describe("Auxiliary seeded from a trace", () => {
  afterEach(() => {
    searchMock.mockReturnValue({})
    traceQueryMock.mockReturnValue({ data: undefined, isError: false })
  })

  it("opens on the trace's tool with the model it asked for", () => {
    // The trace drawer sends a non-chat request here as ?seed=; landing on
    // Token Count with no model left the operator to find both again.
    searchMock.mockReturnValue({ seed: "01EMB" })
    traceQueryMock.mockReturnValue({
      data: { id: "01EMB", surface: "embedding", model: "embed-model", alias: "" },
      isError: false,
    })
    render(<AuxMode active />)
    expect(screen.getAllByText("Embeddings").length).toBeGreaterThan(0)
    expect(screen.getByTestId("model")).toHaveTextContent("embed-model")
    expect(screen.getByText(/seeded from trace 01EMB: the tool and model carried over/i)).toBeInTheDocument()
  })

  it("leaves a chat trace to Chat, which sees the same seed", () => {
    // Every mode is mounted under one URL, so an llm trace must not also
    // switch this one to Token Count behind the chat the operator opened.
    searchMock.mockReturnValue({ seed: "01CHAT" })
    traceQueryMock.mockReturnValue({
      data: { id: "01CHAT", surface: "llm", model: "gpt", alias: "" },
      isError: false,
    })
    render(<AuxMode active />)
    expect(screen.getByTestId("model")).toHaveTextContent("")
    expect(screen.queryByText(/trace 01CHAT/)).toBeNull()
  })

  it("says so when the trace is not a request any tool sends", () => {
    searchMock.mockReturnValue({ seed: "01LLM" })
    traceQueryMock.mockReturnValue({
      data: { id: "01LLM", surface: "realtime", model: "m", alias: "" },
      isError: false,
    })
    render(<AuxMode active />)
    expect(screen.getByText(/not a request any tool here sends/i)).toBeInTheDocument()
  })
})
