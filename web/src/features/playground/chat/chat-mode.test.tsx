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

const { getMock, postMock, patchMock, delMock, streamMock, traceMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
  patchMock: vi.fn(),
  delMock: vi.fn(),
  streamMock: vi.fn(),
  traceMock: vi.fn(),
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
  usePlaygroundConversation: (id: string) => ({
    data: id === "c1" ? detail : undefined,
    isLoading: false,
  }),
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
      attempts: [{ provider: "groq", model: "claude" }],
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
    expect(await screen.findByText("2.4s")).toBeInTheDocument()
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
    // The pane states them either way. What changes is why it will not edit
    // them: a conversation not yet started can still reopen its settings.
    expect(screen.getByText(/chosen when this conversation started/i)).toBeInTheDocument()
    expect(screen.queryByText(/set by the first message/i)).toBeNull()

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())
    expect(screen.getByText(/set by the first message/i)).toBeInTheDocument()
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
      attempts: [{ provider: "groq", model: "claude" }],
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

  it("reads the settings beside the transcript rather than editing them there", async () => {
    // Two live surfaces for one value disagree the moment one of them is a
    // keystroke behind, so the pane states what was chosen and says where.
    mounted()
    expect(screen.getByText(/chosen when this conversation started/i)).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: /system & tools/i }))
    expect(screen.getByLabelText(/system prompt/i)).toBeDisabled()
  })
})
