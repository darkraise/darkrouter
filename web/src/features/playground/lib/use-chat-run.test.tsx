import { act, renderHook, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { useChatRun } from "./use-chat-run"
import { emptyConfig } from "../config"

const frame = (text: string) =>
  `data: ${JSON.stringify({ choices: [{ delta: { content: text } }] })}\n\n`

const streamMock = vi.hoisted(() => vi.fn())
const traceMock = vi.hoisted(() => vi.fn())

vi.mock("../../../lib/api", () => ({
  stream: streamMock,
  api: { get: vi.fn() },
}))

// traceWhenWritten waits 300ms before its first attempt and retries six
// times, by design -- the log writer batches and the row is reliably absent
// when the stream ends. Left real, every test here would spend 1.8s inside it
// and outrun waitFor's default timeout, so the retry policy is stubbed and
// the rest of the module kept.
vi.mock("../metrics", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../metrics")>()),
  traceWhenWritten: traceMock,
}))

function yields(...chunks: string[]) {
  streamMock.mockImplementation(async function* (
    _path: string,
    _body: unknown,
    onStart?: (s: { requestId: string }) => void,
  ) {
    onStart?.({ requestId: "01TRACE" })
    for (const c of chunks) yield c
  })
}

describe("running one chat turn", () => {
  // Each test sets its own stream/trace behaviour; without this, a call
  // recorded by one test's mock is still there when the next test inspects
  // it, and an assertion like "not called" fails on a call that isn't its.
  beforeEach(() => {
    streamMock.mockReset()
    traceMock.mockReset()
  })

  it("appends every streamed delta to the one assistant turn", async () => {
    // A version that read the turns its render closed over would append each
    // chunk to the same stale array, which renders as an empty transcript
    // above a request that plainly succeeded.
    yields(frame("Hel"), frame("lo "), frame("there"))
    traceMock.mockResolvedValue(null)

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))

    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(result.current.messages).toHaveLength(2)
    expect(result.current.messages[0]).toEqual({ role: "user", content: "hi" })
    expect(result.current.messages[1]!.content).toBe("Hello there")
  })

  it("refuses to send without a model", async () => {
    yields(frame("x"))
    const { result } = renderHook(() => useChatRun(emptyConfig(), () => {}))
    await act(() => result.current.send("hi"))
    expect(streamMock).not.toHaveBeenCalled()
    expect(result.current.messages).toHaveLength(0)
  })

  it("keeps the half answer when the operator stops", async () => {
    // Stopping is a decision, not a failure: the tokens were spent and the
    // partial answer is what they bought.
    streamMock.mockImplementation(async function* () {
      yield frame("par")
      throw Object.assign(new Error("aborted"), { name: "AbortError" })
    })
    traceMock.mockResolvedValue(null)

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))

    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(result.current.messages[1]!.content).toBe("par")
    expect(result.current.error).toBe("")
  })

  it("files the route under the turn it served", async () => {
    // A transcript of six answers has to say which provider produced each,
    // not only the last.
    yields(frame("hi"))
    traceMock.mockResolvedValue({
      id: "01TRACE", tokens_in: 3, tokens_out: 5, total_ms: 120,
      cost_micros: null, model: "m", final_model: "m", provider: "groq",
      attempts: [{ provider: "groq", model: "m" }],
    })

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))

    await waitFor(() => expect(result.current.routes[1]).toBeDefined())
    expect(result.current.routes[1]!.provider).toBe("groq")
  })

  it("clears the transcript and its routes together", async () => {
    yields(frame("hi"))
    traceMock.mockResolvedValue(null)
    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    act(() => result.current.clear())
    expect(result.current.messages).toHaveLength(0)
    expect(result.current.routes).toEqual({})
  })
  it("reports one completed turn, with the id its trace will carry", async () => {
    // Chat mode persists from here. Lab's Single tab passes no callback and
    // persists nothing, which is what keeps the two surfaces sharing one loop
    // without sharing a storage decision.
    yields(frame("Hel"), frame("lo"))
    traceMock.mockResolvedValue(null)
    const turns: unknown[] = []

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}, (t) => turns.push(t)),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    expect(turns).toEqual([{ prompt: "hi", answer: "Hello", requestId: "01TRACE" }])
  })

  it("reports nothing for a turn that failed", async () => {
    // A failed run leaves an empty assistant bubble on screen and nothing
    // worth keeping. Persisting it would put a blank turn in the transcript
    // the operator reopens tomorrow.
    streamMock.mockImplementation(async function* () {
      throw new Error("upstream refused")
      // eslint-disable-next-line no-unreachable
      yield ""
    })
    const turns: unknown[] = []

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}, (t) => turns.push(t)),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    expect(result.current.error).toBe("upstream refused")
    expect(turns).toEqual([])
  })

  it("loads a stored transcript over whatever is on screen", async () => {
    yields(frame("x"))
    traceMock.mockResolvedValue(null)
    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    act(() =>
      result.current.load(
        [
          { role: "user", content: "from last week" },
          { role: "assistant", content: "an answer" },
        ],
        {
          1: {
            requestId: "01OLD", provider: "", model: "",
            totalMs: null, tokensIn: null, tokensOut: null, costMicros: null,
            failedOver: [], warnings: [],
          },
        },
      ),
    )

    expect(result.current.messages).toEqual([
      { role: "user", content: "from last week" },
      { role: "assistant", content: "an answer" },
    ])
    expect(result.current.routes[1]?.requestId).toBe("01OLD")
    expect(result.current.error).toBe("")
  })
})
