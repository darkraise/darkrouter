import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { AuxMode } from "./aux-mode"

const { postAuxMock, signals } = vi.hoisted(() => ({
  postAuxMock: vi.fn(),
  signals: [] as AbortSignal[],
}))

vi.mock("../../shell/model-combobox", () => ({
  useModelCandidates: () => ({ candidates: [], loading: false }),
  ModelCombobox: ({ onChange }: { onChange: (model: string) => void }) => (
    <button onClick={() => onChange("embed-model")}>Choose model</button>
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
}))
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

  it("defaults the counting dialect to the model's provider", async () => {
    render(<AuxMode active />)
    await userEvent.click(screen.getByRole("button", { name: "Choose model" }))
    expect(screen.getByTestId("dialect")).toHaveTextContent("anthropic")
  })
})
