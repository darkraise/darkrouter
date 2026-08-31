import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { renderHook, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { useAppendTurn } from "./conversations"

const { postMock } = vi.hoisted(() => ({ postMock: vi.fn() }))

vi.mock("../../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/api")>()),
  api: { get: vi.fn(), post: postMock, patch: vi.fn(), del: vi.fn() },
}))

describe("appending a turn", () => {
  it("runs one at a time, so seq follows the order they were made", () => {
    // A transcript is only its seq order, and the server assigns seq as the
    // appends arrive. Two exchanges whose saves overlap -- the second model
    // round trip outlasting the first pair of local writes -- would otherwise
    // interleave and scramble it.
    const started: string[] = []
    const release: Array<() => void> = []
    postMock.mockImplementation((_path: string, body: { content: string }) => {
      started.push(body.content)
      return new Promise((resolve) => release.push(() => resolve({ seq: 0 })))
    })

    const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } })
    const { result } = renderHook(() => useAppendTurn(), {
      wrapper: ({ children }) => (
        <QueryClientProvider client={client}>{children}</QueryClientProvider>
      ),
    })

    const turn = (content: string) => ({ id: "c1", role: "user" as const, content, requestId: "" })
    void result.current.mutateAsync(turn("first"))
    void result.current.mutateAsync(turn("second"))

    return waitFor(() => expect(started).toEqual(["first"]))
      .then(() => {
        // The second has not been sent while the first is unresolved.
        expect(started).toEqual(["first"])
        release[0]!()
        return waitFor(() => expect(started).toEqual(["first", "second"]))
      })
      .then(() => {
        release[1]!()
      })
  })
})
