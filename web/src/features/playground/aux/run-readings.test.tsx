import { render } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { RunReadings } from "./run-readings"

const traceMock = vi.hoisted(() => vi.fn())

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

vi.mock("../metrics", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../metrics")>()),
  traceWhenWritten: traceMock,
}))

describe("the run readings", () => {
  it("stops waiting for the trace when unmounted", () => {
    // traceWhenWritten retries for a second and a half; a panel that left
    // the surface kept the timer alive and then wrote into nothing.
    let signal: AbortSignal | undefined
    traceMock.mockImplementation((_id: string, s?: AbortSignal) => {
      signal = s
      return new Promise(() => {})
    })
    const view = render(
      <RunReadings
        run={{ id: 1, at: Date.now(), summary: "s", requestId: "01A", outcome: { kind: "json", json: {} } }}
      />,
    )
    expect(signal).toBeDefined()
    expect(signal?.aborted).toBe(false)

    view.unmount()
    expect(signal?.aborted).toBe(true)
  })
})
