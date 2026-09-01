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
vi.mock("./tool-rail", () => ({ ToolRail: () => null }))
vi.mock("./tool-inputs", () => ({
  ToolInputs: ({ onField, onRun }: {
    onField: (key: string, value: string) => void
    onRun: () => void
  }) => (
    <>
      <button onClick={() => onField("input", "hello")}>Fill input</button>
      <button onClick={onRun}>Run auxiliary</button>
    </>
  ),
}))
vi.mock("./results", () => ({ RunCard: () => null }))
vi.mock("./run-readings", () => ({ RunReadings: () => null }))
vi.mock("./surfaces", async (importOriginal) => ({
  ...(await importOriginal<typeof import("./surfaces")>()),
  postAux: postAuxMock,
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
    await userEvent.click(screen.getByRole("button", { name: "Choose model" }))
    await userEvent.click(screen.getByRole("button", { name: "Fill input" }))
    await userEvent.click(screen.getByRole("button", { name: "Run auxiliary" }))
    await waitFor(() => expect(signals).toHaveLength(1))

    rerender(<AuxMode active={false} />)

    await waitFor(() => expect(signals[0]?.aborted).toBe(true))
  })
})
