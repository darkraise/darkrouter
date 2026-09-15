import { render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ChatMode } from "./chat-mode"
import { ApiError } from "../../../lib/api"
import type { PlaygroundConversation, PlaygroundConversationDetail } from "../../../lib/api-types"

// A reopened assistant turn carries a request_id, so the transcript renders a
// route line, whose trace link is a router Link.
// jsdom implements no layout, so it has no scrollIntoView; the transcript
// calls one every time it grows while a run is in flight.
Element.prototype.scrollIntoView = vi.fn()

// The screen reads ?seed= to carry a trace's model and dialect into the
// request pane -- the job Lab's Single tab used to do.
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
  useSearch: () => ({}),
}))

const stored: PlaygroundConversation = {
  id: "c1",
  title: "speculative decoding",
  dialect: "anthropic",
  model: "claude",
  config: { system: "answer in one line" },
  preview: "explain it to me",
  created_at: "2026-08-30T10:00:00Z",
  updated_at: "2026-08-30T10:00:00Z",
}

const detail: PlaygroundConversationDetail = {
  ...stored,
  messages: [
    { seq: 0, role: "user", content: "explain it", request_id: "", created_at: "2026-08-30T10:00:00Z" },
    { seq: 1, role: "assistant", content: "in one line", request_id: "01OLD", created_at: "2026-08-30T10:00:01Z" },
  ],
}

const { getMock, postMock, patchMock, delMock, streamMock, traceMock, conversationQueryMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
  patchMock: vi.fn(),
  delMock: vi.fn(),
  streamMock: vi.fn(),
  traceMock: vi.fn(),
  conversationQueryMock: vi.fn(),
}))

vi.mock("../../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/api")>()),
  api: { get: getMock, post: postMock, patch: patchMock, del: delMock },
  stream: streamMock,
}))

vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  // The request pane's preset picker is on this screen now, and it reads a
  // list. Left real it would take whatever the shared api.get mock is
  // returning for the trace under test.
  usePlaygroundPresets: () => ({ data: [] }),
  usePlaygroundConversations: () => ({ data: [stored], isLoading: false }),
  usePlaygroundConversation: (id: string) => conversationQueryMock(id),
}))

// traceWhenWritten waits 300ms and retries six times by design; left real,
// every test here would spend 1.8s inside it.
vi.mock("../metrics", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../metrics")>()),
  traceWhenWritten: traceMock,
}))

vi.mock("../../shell/model-combobox", () => ({
  useModelCandidates: () => ({ candidates: ["gpt"], loading: false }),
  ModelCombobox: ({ value, onChange, label }: {
    value: string
    onChange: (next: string) => void
    label: string
  }) => <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />,
}))

function mounted() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ChatMode />
    </QueryClientProvider>,
  )
}

function mountedWithActive(active: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const view = render(
    <QueryClientProvider client={client}>
      <ChatMode active={active} />
    </QueryClientProvider>,
  )
  return {
    ...view,
    setActive(next: boolean) {
      view.rerender(
        <QueryClientProvider client={client}>
          <ChatMode active={next} />
        </QueryClientProvider>,
      )
    },
  }
}

async function send(text: string) {
  await userEvent.type(screen.getByLabelText("Message"), text)
  await userEvent.click(screen.getByRole("button", { name: "Send" }))
}


/** Name the model the way an operator does: through the dialog that opens with
 *  a new conversation. It used to be a popover on the header, which is why so
 *  many tests reached for one. */
async function chooseModel(model: string) {
  await userEvent.click(screen.getByRole("button", { name: "New conversation" }))
  const dialog = await screen.findByRole("dialog")
  await userEvent.type(within(dialog).getByLabelText("Model or alias"), model)
  await userEvent.click(within(dialog).getByRole("button", { name: /start conversation/i }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
}

describe("Chat mode", () => {
  beforeEach(() => {
    getMock.mockReset()
    getMock.mockRejectedValue(new Error("no trace"))
    postMock.mockReset()
    patchMock.mockReset()
    delMock.mockReset()
    streamMock.mockReset()
    traceMock.mockReset()
    conversationQueryMock.mockReset()
    conversationQueryMock.mockImplementation((id: string) => ({
      data: id === "c1" ? detail : undefined,
      isLoading: false,
    }))
    traceMock.mockResolvedValue(null)
    streamMock.mockImplementation(async function* (
      _path: string,
      _body: unknown,
      onStart?: (s: { requestId: string }) => void,
    ) {
      onStart?.({ requestId: "01NEW" })
      yield `data: ${JSON.stringify({ choices: [{ delta: { content: "an answer" } }] })}\n\n`
    })
  })

  it("keeps the transcript when saving is off and the create is refused", async () => {
    // playground.save_conversations off makes every write a 403. The answer
    // on screen was paid for either way, so a refused save must not take it
    // down -- verified live at the stage 4 gate, pinned by nothing until now.
    postMock.mockRejectedValue(
      new ApiError(403, "playground.save_conversations is off, so conversations are not saved"),
    )
    mounted()
    await chooseModel("gpt")
    await send("hello there")

    await waitFor(() => expect(postMock).toHaveBeenCalled())
    // The turn stays on screen, and the header still says this is a new chat
    // because no conversation was created to name.
    expect(await screen.findByText("an answer")).toBeInTheDocument()
    expect(screen.getByText("hello there")).toBeInTheDocument()
    expect(screen.getByLabelText("Conversation title")).toHaveValue("New chat")
  })

  it("keeps a title typed before the first send", async () => {
    // retitle cannot persist before a conversation exists, so the typed name
    // lived only in local state and titleFromPrompt then overwrote it.
    postMock.mockImplementation((path: string, body: unknown) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: (body as { title: string }).title })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await chooseModel("gpt")

    const field = screen.getByLabelText("Conversation title")
    await userEvent.clear(field)
    await userEvent.type(field, "speculative decoding{Enter}")
    await send("hello there")

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))
    const [, createBody] = postMock.mock.calls.find(
      (c) => c[0] === "/api/playground/conversations",
    ) as [string, { title: string }]
    expect(createBody.title).toBe("speculative decoding")
  })

  it("saves exactly one user turn and one assistant turn per exchange", async () => {
    // The count is the assertion. A second create, or a duplicated message,
    // is the failure this feature makes easy and expensive.
    postMock.mockImplementation((path: string) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: "hello there" })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await chooseModel("gpt")
    await send("hello there")

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))
    const paths = postMock.mock.calls.map((c) => c[0] as string)
    expect(paths.filter((p) => p === "/api/playground/conversations")).toHaveLength(1)
    expect(paths.filter((p) => p === "/api/playground/conversations/new1/messages")).toHaveLength(2)

    const [, createBody] = postMock.mock.calls.find(
      (c) => c[0] === "/api/playground/conversations",
    ) as [string, { title: string }]
    // Titled from the first turn rather than left as "New chat": a rail of
    // identical placeholders retrieves nothing.
    expect(createBody.title).toBe("hello there")

    const turns = postMock.mock.calls.filter((c) =>
      (c[0] as string).endsWith("/messages"),
    ) as [string, { role: string; content: string; request_id: string }][]
    expect(turns[0]![1]).toEqual({ role: "user", content: "hello there", request_id: "" })
    expect(turns[1]![1]).toEqual({ role: "assistant", content: "an answer", request_id: "01NEW" })
  })

  it("creates one conversation across two exchanges, not two", async () => {
    postMock.mockImplementation((path: string) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: "first" })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await chooseModel("gpt")
    await send("first")
    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))
    await send("second")
    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(5))

    const creates = postMock.mock.calls.filter((c) => c[0] === "/api/playground/conversations")
    expect(creates).toHaveLength(1)
  })

  it("saves a second exchange only after the whole first one", async () => {
    // seq is assigned in arrival order, and one exchange is two writes. A
    // second exchange finishing while the first user turn is still saving
    // used to queue its user turn between the first exchange's two.
    let releaseFirst = () => {}
    const held = new Promise<void>((resolve) => {
      releaseFirst = resolve
    })
    const order: string[] = []
    postMock.mockImplementation(async (path: string, body: { content?: string }) => {
      if (path === "/api/playground/conversations") return { ...stored, id: "new1", title: "first" }
      order.push(body.content ?? "")
      if (body.content === "first") await held
      return { seq: order.length - 1 }
    })
    mounted()
    await chooseModel("gpt")
    await send("first")
    await waitFor(() => expect(order).toEqual(["first"]))
    await send("second")
    await waitFor(() => expect(streamMock).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.getByRole("button", { name: "Send" })).toBeInTheDocument())

    releaseFirst()
    await waitFor(() => expect(order).toHaveLength(4))
    expect(order).toEqual(["first", "an answer", "second", "an answer"])
  })

  it("resumes an exchange whose save failed part-way before saving the next", async () => {
    // The user turn was stored and the answer was not. Abandoned, reopening
    // the conversation shows a question with no reply; retried whole, it
    // stores the question twice.
    let answers = 0
    const posts: string[] = []
    postMock.mockImplementation(async (path: string, body: { role?: string; content?: string }) => {
      if (path === "/api/playground/conversations") return { ...stored, id: "new1", title: "first" }
      posts.push(`${body.role}:${body.content}`)
      if (body.role === "assistant" && answers++ === 0) throw new ApiError(503, "store is busy")
      return { seq: posts.length - 1 }
    })
    mounted()
    await chooseModel("gpt")
    await send("first")
    expect(await screen.findByText(/1 exchange was not saved/i)).toBeInTheDocument()

    await send("second")
    await waitFor(() => expect(posts).toHaveLength(5))
    expect(posts).toEqual([
      "user:first",
      "assistant:an answer",
      "assistant:an answer",
      "user:second",
      "assistant:an answer",
    ])
    await waitFor(() => expect(screen.queryByText(/was not saved/i)).toBeNull())
  })

  it("retries an unsaved exchange on request", async () => {
    let failing = true
    const posts: string[] = []
    postMock.mockImplementation(async (path: string, body: { role?: string; content?: string }) => {
      if (path === "/api/playground/conversations") return { ...stored, id: "new1", title: "first" }
      posts.push(`${body.role}:${body.content}`)
      if (body.role === "assistant" && failing) throw new TypeError("Failed to fetch")
      return { seq: posts.length - 1 }
    })
    mounted()
    await chooseModel("gpt")
    await send("first")
    await screen.findByText(/1 exchange was not saved/i)

    failing = false
    await userEvent.click(screen.getByRole("button", { name: "Retry saving" }))
    await waitFor(() => expect(screen.queryByText(/was not saved/i)).toBeNull())
    expect(posts).toEqual(["user:first", "assistant:an answer", "assistant:an answer"])
  })

  it("saves another conversation's exchanges while one conversation's save keeps failing", async () => {
    const posts: string[] = []
    postMock.mockImplementation(async (path: string, body: { title?: string; role?: string; content?: string }) => {
      if (path === "/api/playground/conversations") {
        return { ...stored, id: body.title === "stuck" ? "stuck1" : "next1", title: body.title }
      }
      posts.push(`${path}:${body.role}:${body.content}`)
      if (path.includes("stuck1")) throw new ApiError(503, "store is busy")
      return { seq: posts.length - 1 }
    })
    mounted()
    await chooseModel("gpt")
    await send("stuck")
    await screen.findByText(/1 exchange was not saved/i)

    await chooseModel("gpt")
    await send("next")
    await waitFor(() =>
      expect(posts).toContain("/api/playground/conversations/next1/messages:assistant:an answer"),
    )
    expect(posts.filter((p) => p.includes("next1"))).toEqual([
      "/api/playground/conversations/next1/messages:user:next",
      "/api/playground/conversations/next1/messages:assistant:an answer",
    ])
    expect(await screen.findByText(/1 exchange was not saved/i)).toBeInTheDocument()
  })

  it("discards an exchange whose save keeps failing so the next one can be saved", async () => {
    const posts: string[] = []
    postMock.mockImplementation(async (path: string, body: { role?: string; content?: string }) => {
      if (path === "/api/playground/conversations") return { ...stored, id: "new1", title: "first" }
      posts.push(`${body.role}:${body.content}`)
      if (body.content === "first") throw new ApiError(503, "store is busy")
      return { seq: posts.length - 1 }
    })
    mounted()
    await chooseModel("gpt")
    await send("first")
    await screen.findByText(/1 exchange was not saved/i)

    await userEvent.click(screen.getByRole("button", { name: "Discard" }))
    await waitFor(() => expect(screen.queryByText(/was not saved/i)).toBeNull())

    await send("second")
    await waitFor(() => expect(posts).toHaveLength(3))
    expect(posts).toEqual(["user:first", "user:second", "assistant:an answer"])
    expect(screen.queryByText(/was not saved/i)).toBeNull()
  })

  it.each([408, 429])("holds an exchange refused with %i for another try", async (status) => {
    let failing = true
    const posts: string[] = []
    postMock.mockImplementation(async (path: string, body: { role?: string; content?: string }) => {
      if (path === "/api/playground/conversations") return { ...stored, id: "new1", title: "first" }
      posts.push(`${body.role}:${body.content}`)
      if (body.role === "assistant" && failing) throw new ApiError(status, "try again later")
      return { seq: posts.length - 1 }
    })
    mounted()
    await chooseModel("gpt")
    await send("first")
    await screen.findByText(/1 exchange was not saved/i)

    failing = false
    await userEvent.click(screen.getByRole("button", { name: "Retry saving" }))
    await waitFor(() => expect(screen.queryByText(/was not saved/i)).toBeNull())
    expect(posts).toEqual(["user:first", "assistant:an answer", "assistant:an answer"])
  })

  it("reopens a conversation with its system prompt intact", async () => {
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))

    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())
    expect(screen.getByText("explain it")).toBeInTheDocument()
    expect(screen.getByLabelText("Conversation title")).toHaveValue("speculative decoding")

    // The setting that shaped every answer above, restored rather than lost.
    // It reads from the request pane now rather than from a dialog behind the
    // actions menu -- and it reads there under the lock, which is the point:
    // a system prompt that could still be edited would change what the turns
    // above were supposedly answered under.
    await userEvent.click(screen.getByRole("button", { name: /system & tools/i }))
    expect(screen.getByLabelText("System prompt")).toHaveValue("answer in one line")
    expect(screen.getByLabelText("System prompt")).toBeDisabled()
  })

  it("does not let the previous transcript send while a selected conversation loads", async () => {
    conversationQueryMock.mockImplementation((id: string) => ({
      data: undefined,
      isLoading: id === "c1",
    }))
    mounted()
    await chooseModel("gpt")
    await userEvent.type(screen.getByLabelText("Message"), "belongs to the old draft")

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))

    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
    expect(screen.getByLabelText("Conversation title")).toBeDisabled()
    expect(screen.getByRole("button", { name: "Conversation actions" })).toBeDisabled()
  })

  it("explains when a selected conversation cannot be loaded", async () => {
    conversationQueryMock.mockImplementation((id: string) => ({
      data: undefined,
      isLoading: false,
      isError: id === "c1",
    }))
    mounted()

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))

    expect(await screen.findByRole("alert")).toHaveTextContent(/could not load/i)
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled()
  })

  it("does not let a delayed create response steal focus from a selected conversation", async () => {
    let finishCreate: (conversation: PlaygroundConversation) => void = () => {}
    postMock.mockImplementation((path: string) => {
      if (path === "/api/playground/conversations") {
        return new Promise<PlaygroundConversation>((resolve) => { finishCreate = resolve })
      }
      return Promise.resolve({ seq: 0 })
    })
    mounted()
    await chooseModel("gpt")
    await send("starts a new thread")
    await waitFor(() => expect(postMock).toHaveBeenCalledWith(
      "/api/playground/conversations",
      expect.anything(),
    ))

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByLabelText("Conversation title")).toHaveValue(stored.title))

    finishCreate({ ...stored, id: "new1", title: "starts a new thread" })

    await waitFor(() => expect(postMock).toHaveBeenCalledWith(
      "/api/playground/conversations/new1/messages",
      expect.anything(),
    ))
    expect(screen.getByLabelText("Conversation title")).toHaveValue(stored.title)
  })

  it("persists a live turn to the conversation that sent it after selection changes", async () => {
    let finishStream: (() => void) | undefined
    streamMock.mockImplementation(async function* (
      _path: string,
      _body: unknown,
      onStart?: (s: { requestId: string }) => void,
    ) {
      onStart?.({ requestId: "01NEW" })
      await new Promise<void>((resolve) => { finishStream = resolve })
      yield `data: ${JSON.stringify({ choices: [{ delta: { content: "old answer" } }] })}\n\n`
    })
    conversationQueryMock.mockImplementation((id: string) => ({
      data: undefined,
      isLoading: id === "c1",
    }))
    postMock.mockImplementation((path: string) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: "old prompt" })
        : Promise.resolve({ seq: 0 }),
    )

    mounted()
    await chooseModel("gpt")
    await send("old prompt")
    await waitFor(() => expect(finishStream).toBeDefined())

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    finishStream!()

    await waitFor(() => expect(postMock).toHaveBeenCalledWith(
      "/api/playground/conversations/new1/messages",
      expect.objectContaining({ content: "old prompt" }),
    ))
    expect(postMock).not.toHaveBeenCalledWith(
      "/api/playground/conversations/c1/messages",
      expect.anything(),
    )
  })

  it("saves a half answer to its own conversation when another one is opened mid-stream", async () => {
    // Opening a conversation whose detail is at hand replaces the transcript
    // and ends the request in flight, the way leaving the screen does. The
    // exchange already paid for still belongs to the thread that sent it.
    postMock.mockImplementation((path: string) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: "first" })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await chooseModel("gpt")
    await send("first")
    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))

    let signal: AbortSignal | undefined
    streamMock.mockImplementation(async function* (
      _path: string,
      _body: unknown,
      onStart?: (s: { requestId: string }) => void,
      requestSignal?: AbortSignal,
    ) {
      signal = requestSignal
      onStart?.({ requestId: "01HALF" })
      yield `data: ${JSON.stringify({ choices: [{ delta: { content: "half an" } }] })}\n\n`
      await new Promise<void>((_resolve, reject) => {
        requestSignal?.addEventListener("abort", () => {
          reject(Object.assign(new Error("aborted"), { name: "AbortError" }))
        })
      })
    })
    await send("second")
    expect(await screen.findByText("half an")).toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())
    expect(signal?.aborted).toBe(true)

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(5))
    const turns = postMock.mock.calls
      .slice(3)
      .map((c) => [c[0], c[1]] as [string, unknown])
    expect(turns).toEqual([
      ["/api/playground/conversations/new1/messages", { role: "user", content: "second", request_id: "" }],
      ["/api/playground/conversations/new1/messages", { role: "assistant", content: "half an", request_id: "01HALF" }],
    ])
    expect(screen.queryByText("half an")).toBeNull()
  })

  it("creates a new thread under its own title and settings when another is opened before its trace", async () => {
    // The answer is whole and the run is waiting on its trace. Nothing has
    // been stored yet, so the conversation is created now -- after the
    // screen has already taken the opened conversation's title and model.
    traceMock.mockImplementation(
      (_id: string, signal?: AbortSignal) =>
        new Promise((resolve) => signal?.addEventListener("abort", () => resolve(null))),
    )
    postMock.mockImplementation((path: string) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: "left before its trace" })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await chooseModel("gpt")
    await send("left before its trace")
    expect(await screen.findByText("an answer")).toBeInTheDocument()
    await waitFor(() => expect(traceMock).toHaveBeenCalled())
    expect(postMock).not.toHaveBeenCalled()

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))
    expect(postMock.mock.calls[0]![0]).toBe("/api/playground/conversations")
    expect(postMock.mock.calls[0]![1]).toMatchObject({ title: "left before its trace", model: "gpt" })
    expect(postMock.mock.calls.slice(1).map((c) => [c[0], c[1]])).toEqual([
      ["/api/playground/conversations/new1/messages", { role: "user", content: "left before its trace", request_id: "" }],
      ["/api/playground/conversations/new1/messages", { role: "assistant", content: "an answer", request_id: "01NEW" }],
    ])
    expect(screen.getByLabelText("Conversation title")).toHaveValue(stored.title)
  })

  it("keeps a new thread's first turn out of the unsaved thread it replaced", async () => {
    // The thread left mid-answer creates its conversation after New
    // conversation has already started the next one; the next one's first
    // turn must make its own rather than share the one being created.
    postMock.mockImplementation((path: string, body: unknown) => {
      if (path !== "/api/playground/conversations") return Promise.resolve({ seq: 0 })
      const title = (body as { title: string }).title
      return Promise.resolve({ ...stored, id: title === "left thread" ? "left1" : "next1", title })
    })
    streamMock.mockImplementationOnce(async function* (
      _path: string,
      _body: unknown,
      onStart?: (s: { requestId: string }) => void,
      requestSignal?: AbortSignal,
    ) {
      onStart?.({ requestId: "01LEFT" })
      yield `data: ${JSON.stringify({ choices: [{ delta: { content: "half" } }] })}\n\n`
      await new Promise<void>((_resolve, reject) => {
        requestSignal?.addEventListener("abort", () => {
          reject(Object.assign(new Error("aborted"), { name: "AbortError" }))
        })
      })
    })
    mounted()
    await chooseModel("gpt")
    await send("left thread")
    expect(await screen.findByText("half")).toBeInTheDocument()

    await chooseModel("gpt")
    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(3))
    await send("next thread")

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(6))
    const byPath = (p: string) =>
      postMock.mock.calls.filter((c) => c[0] === p).map((c) => (c[1] as { content: string }).content)
    expect(byPath("/api/playground/conversations/left1/messages")).toEqual(["left thread", "half"])
    expect(byPath("/api/playground/conversations/next1/messages")).toEqual(["next thread", "an answer"])
    expect(screen.getByLabelText("Conversation title")).toHaveValue("next thread")
  })

  it("aborts the live request when Chat becomes inactive", async () => {
    let signal: AbortSignal | undefined
    streamMock.mockImplementation(async function* (
      _path: string,
      _body: unknown,
      _onStart?: (s: { requestId: string }) => void,
      requestSignal?: AbortSignal,
    ) {
      signal = requestSignal
      await new Promise<void>((_resolve, reject) => {
        requestSignal?.addEventListener("abort", () => {
          reject(Object.assign(new Error("aborted"), { name: "AbortError" }))
        })
      })
      yield ""
    })
    const view = mountedWithActive(true)
    await chooseModel("gpt")
    await send("keep streaming")
    await waitFor(() => expect(signal).toBeDefined())

    view.setActive(false)

    await waitFor(() => expect(signal?.aborted).toBe(true))
  })

  it("opens conversation history in a sheet on narrow screens", async () => {
    mounted()

    await userEvent.click(screen.getByRole("button", { name: /show conversations/i }))

    const dialog = await screen.findByRole("dialog", { name: /conversations/i })
    expect(within(dialog).getByRole("button", { name: /speculative decoding/i })).toBeInTheDocument()
    expect(within(dialog).getByRole("button", { name: /new conversation/i })).toBeInTheDocument()
  })

  it("recovers a reopened turn's route from its stored request id", async () => {
    // The store keeps only the request id, so before this a restored answer
    // said "routed" and nothing else -- no provider, no duration, no cost --
    // and drew a gutter that read as still loading, forever.
    getMock.mockResolvedValue({
      id: "01OLD",
      provider: "groq",
      model: "claude",
      final_model: "claude",
      total_ms: 2400,
      tokens_in: 88,
      tokens_out: 921,
      cost_micros: 566,
      attempts: [{ provider: "groq", model: "claude", cost_micros: 3100 }],
      warnings: [],
    })
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    expect(getMock).toHaveBeenCalledWith("/api/requests/01OLD")
    // The duration replaces the bare "routed" the stored row could offer.
    // The duration replaces the bare "routed" that the stored row alone
    // could offer. (The button's accessible name is its aria-label, so the
    // reading is the text.)
    expect(await screen.findByText("2.4 s")).toBeInTheDocument()
  })

  it("fixes the model once a turn has been sent", async () => {
    // Section 4. Every answer above was produced by this model, so a way to
    // change it now would offer the conversation a record it cannot honestly
    // keep. Before the first turn the same settings are still reachable --
    // that is the whole seam, and it is time rather than place.
    mounted()
    await chooseModel("gpt")
    expect(screen.getByText("gpt")).toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    // The stored model reads on the header, and no control offers to move it.
    expect(screen.queryByRole("button", { name: /^claude$/ })).toBeNull()
    expect(screen.getByText("claude")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    expect(await screen.findByRole("menuitem", { name: /request settings/i }))
      .toHaveAttribute("aria-disabled", "true")
  })

  it("keeps the request settings open until the first message", async () => {
    // The other half of the same seam: Lab's request pane is on this screen
    // now, and it is only useful if it can be set before the request it
    // describes goes out.
    mounted()
    expect(screen.getByRole("heading", { name: "Request" })).toBeInTheDocument()
    expect(screen.queryByText(/set by the first message/i)).toBeNull()
    await userEvent.click(screen.getByRole("button", { name: /system & tools/i }))
    expect(screen.getByLabelText("System prompt")).toBeEnabled()

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())
    expect(screen.getByText(/set by the first message/i)).toBeInTheDocument()
    expect(screen.getByLabelText("System prompt")).toBeDisabled()
  })

  it("leaves the settings open after a first message that failed", async () => {
    // Nothing was answered and nothing stored, so no exchange was produced
    // under these settings; fixing them would leave the operator to start
    // over to correct the setting that most likely caused the failure.
    streamMock.mockImplementation(async function* () {
      throw new Error("upstream refused")
      // Unreachable, and there to make this a generator.
      yield ""
    })
    mounted()
    await chooseModel("gpt")
    await send("hello")
    expect(await screen.findByText("upstream refused")).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: /system & tools/i }))
    expect(screen.getByLabelText("System prompt")).toBeEnabled()
  })

  it("sends what was typed into the pane beside the transcript", async () => {
    // The pane edits until the first message, and what it holds is what the
    // next send carries — the same value the dialog would have set.
    mounted()
    await chooseModel("gpt")
    await userEvent.click(screen.getByRole("button", { name: /system & tools/i }))
    await userEvent.type(screen.getByLabelText("System prompt"), "answer tersely")
    await send("hello")

    await waitFor(() => expect(streamMock).toHaveBeenCalled())
    const [, body] = streamMock.mock.calls[0] as [string, { system?: string }]
    expect(body.system).toBe("answer tersely")
  })

  it("opens the settings from the empty transcript's own button", async () => {
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /choose a model/i }))
    expect(await screen.findByRole("dialog")).toBeInTheDocument()
  })

  it("totals what the conversation has spent, not just the last turn", async () => {
    // A thread that has quietly grown to a large context is billing for all
    // of it on every turn, and nothing else on the screen says so.
    getMock.mockResolvedValue({
      id: "01OLD",
      provider: "groq",
      model: "claude",
      final_model: "claude",
      total_ms: 2400,
      tokens_in: 1204,
      tokens_out: 887,
      cost_micros: 3100,
      attempts: [{ provider: "groq", model: "claude", cost_micros: 3100 }],
      warnings: [],
    })
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    expect(await screen.findByText("1,204")).toBeInTheDocument()
    expect(screen.getByText("887")).toBeInTheDocument()
    expect(screen.getByText("$0.0031")).toBeInTheDocument()
  })

  it("asks what a new conversation will be sent under before anything is typed", async () => {
    // The one moment every setting is still open, and nothing used to mark
    // it: an operator who did not know to visit a side panel first simply
    // sent at the provider's defaults.
    mounted()
    await userEvent.click(screen.getByRole("button", { name: "New conversation" }))
    expect(await screen.findByRole("dialog")).toBeInTheDocument()
    expect(screen.getByText(/what every message in this thread/i)).toBeInTheDocument()

    const dialog = screen.getByRole("dialog")
    await userEvent.type(within(dialog).getByLabelText("Model or alias"), "gpt-4")
    await userEvent.click(
      within(dialog).getByRole("button", { name: /start conversation/i }),
    )

    // The chosen model reaches the screen behind it, which is what the next
    // send will actually carry.
    await waitFor(() => expect(screen.getByText("gpt-4")).toBeInTheDocument())
  })

  it("leaves the blank conversation on defaults when the dialog is cancelled", async () => {
    // Cancel refuses these settings, not the conversation -- the rail's own
    // button already started one, and closing must not strand the operator.
    mounted()
    await userEvent.click(screen.getByRole("button", { name: "New conversation" }))
    const dialog = await screen.findByRole("dialog")
    await userEvent.type(within(dialog).getByLabelText("Model or alias"), "gpt-4")
    await userEvent.click(within(dialog).getByRole("button", { name: /cancel/i }))

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(screen.queryByText("gpt-4")).toBeNull()
    expect(screen.getByText("No model")).toBeInTheDocument()
  })

  it("reopens the settings as an amendment, not as another new conversation", async () => {
    // A conversation is not stored until its first turn is, so "has an id"
    // is false for exactly the case this menu item exists to serve: a thread
    // set up and not yet sent. Keying the wording off the row made the
    // dialog offer to start something that had already been started.
    mounted()
    await chooseModel("gpt")
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(await screen.findByRole("menuitem", { name: /request settings/i }))

    const dialog = await screen.findByRole("dialog")
    expect(within(dialog).getByRole("button", { name: /apply/i })).toBeInTheDocument()
    expect(within(dialog).queryByRole("button", { name: /start conversation/i })).toBeNull()
  })

})
