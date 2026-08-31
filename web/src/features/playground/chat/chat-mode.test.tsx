import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ChatMode } from "./chat-mode"
import type { PlaygroundConversation, PlaygroundConversationDetail } from "../../../lib/api-types"

// A reopened assistant turn carries a request_id, so the transcript renders a
// route line, whose trace link is a router Link.
// jsdom implements no layout, so it has no scrollIntoView; the transcript
// calls one every time it grows while a run is in flight.
Element.prototype.scrollIntoView = vi.fn()

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
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

function mounted(onOpenInLab = () => {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <ChatMode onOpenInLab={onOpenInLab} />
    </QueryClientProvider>,
  )
}

async function send(text: string) {
  await userEvent.type(screen.getByLabelText("Message"), text)
  await userEvent.click(screen.getByRole("button", { name: "Send" }))
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

  it("keeps a title typed before the first send", async () => {
    // retitle cannot persist before a conversation exists, so the typed name
    // lived only in local state and titleFromPrompt then overwrote it.
    postMock.mockImplementation((path: string, body: unknown) =>
      path === "/api/playground/conversations"
        ? Promise.resolve({ ...stored, id: "new1", title: (body as { title: string }).title })
        : Promise.resolve({ seq: 0 }),
    )
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /choose a model|gpt/i }))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")
    await userEvent.keyboard("{Escape}")

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
    await userEvent.click(screen.getByRole("button", { name: /choose a model|gpt/i }))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")
    await userEvent.keyboard("{Escape}")
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
    await userEvent.click(screen.getByRole("button", { name: /choose a model|gpt/i }))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")
    await userEvent.keyboard("{Escape}")
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
    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /system prompt/i }))
    expect(screen.getByLabelText("System prompt")).toHaveValue("answer in one line")
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

  it("hands the whole configuration to Lab", async () => {
    const onOpenInLab = vi.fn()
    mounted(onOpenInLab)
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    await userEvent.click(screen.getByRole("button", { name: "Conversation actions" }))
    await userEvent.click(screen.getByRole("menuitem", { name: /open in lab/i }))
    expect(onOpenInLab).toHaveBeenCalledWith(
      expect.objectContaining({ system: "answer in one line", model: "claude", dialect: "anthropic" }),
    )
  })

  it("saves the model once the typing settles, not once per keystroke", async () => {
    // The combobox reports every character, so a PATCH per report leaves the
    // stored model decided by whichever of eleven concurrent writes lands
    // last -- invisible until the conversation is reopened tomorrow.
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    await userEvent.click(screen.getByRole("button", { name: /claude/ }))
    await userEvent.clear(screen.getByLabelText("Model or alias"))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt-4o-mini")
    // The field still keeps up with the typing; only the write waits.
    expect(screen.getByLabelText("Model or alias")).toHaveValue("gpt-4o-mini")

    await waitFor(() => expect(patchMock).toHaveBeenCalled(), { timeout: 2000 })
    expect(patchMock).toHaveBeenCalledTimes(1)
    const [, body] = patchMock.mock.calls[0] as [string, { model: string }]
    expect(body.model).toBe("gpt-4o-mini")
  })

  it("patches the row when the model changes part-way through", async () => {
    // Section 8.5: the transcript keeps the turns that came before, and each
    // answer's route line already records what actually served it.
    mounted()
    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await waitFor(() => expect(screen.getByText("in one line")).toBeInTheDocument())

    await userEvent.click(screen.getByRole("button", { name: /claude/ }))
    await userEvent.clear(screen.getByLabelText("Model or alias"))
    await userEvent.type(screen.getByLabelText("Model or alias"), "gpt")

    await waitFor(() => expect(patchMock).toHaveBeenCalled())
    const [path, body] = patchMock.mock.calls.at(-1) as [string, { model: string }]
    expect(path).toBe("/api/playground/conversations/c1")
    expect(body.model).toBe("gpt")
  })
})
