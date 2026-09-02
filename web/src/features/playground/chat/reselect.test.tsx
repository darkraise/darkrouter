import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { ChatMode } from "./chat-mode"
import type {
  PlaygroundConversation,
  PlaygroundConversationDetail,
  PlaygroundStoredTurn,
} from "../../../lib/api-types"

Element.prototype.scrollIntoView = vi.fn()

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
  useSearch: () => ({}),
}))

const first: PlaygroundConversation = {
  id: "c1",
  title: "speculative decoding",
  dialect: "openai",
  model: "gpt",
  config: {},
  preview: "explain it",
  created_at: "2026-08-30T10:00:00Z",
  updated_at: "2026-08-30T10:00:00Z",
}

const second: PlaygroundConversation = { ...first, id: "c2", title: "another thread", preview: "" }

/** The server, as far as this screen can tell: appends land in the row they
 *  were sent to, so a later read returns them. */
const store = vi.hoisted(() => ({ turns: {} as Record<string, PlaygroundStoredTurn[]> }))

const { getMock, postMock, streamMock } = vi.hoisted(() => ({
  getMock: vi.fn(),
  postMock: vi.fn(),
  streamMock: vi.fn(),
}))

vi.mock("../../../lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/api")>()),
  api: { get: getMock, post: postMock, patch: vi.fn(), del: vi.fn() },
  stream: streamMock,
}))

// The conversation detail hook is deliberately left real: this test is about
// what the query cache holds after an append, which a stub could not show.
vi.mock("../../../lib/queries", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../../lib/queries")>()),
  usePlaygroundPresets: () => ({ data: [] }),
  usePlaygroundConversations: () => ({ data: [first, second], isLoading: false }),
}))

vi.mock("../metrics", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../metrics")>()),
  traceWhenWritten: vi.fn().mockResolvedValue(null),
}))

vi.mock("../../shell/model-combobox", () => ({
  useModelCandidates: () => ({ candidates: ["gpt"], loading: false }),
  ModelCombobox: ({ value, onChange, label }: {
    value: string
    onChange: (next: string) => void
    label: string
  }) => <input aria-label={label} value={value} onChange={(e) => onChange(e.target.value)} />,
}))

function detailOf(c: PlaygroundConversation): PlaygroundConversationDetail {
  // Copied: a real response is a fresh document, and handing the cache the
  // store's own array would let a later append edit it in place.
  return { ...c, messages: [...(store.turns[c.id] ?? [])] }
}

describe("reselecting a conversation after sending to it", () => {
  beforeEach(() => {
    store.turns = {
      c1: [
        { seq: 0, role: "user", content: "explain it", request_id: "", created_at: "2026-08-30T10:00:00Z" },
        { seq: 1, role: "assistant", content: "in one line", request_id: "", created_at: "2026-08-30T10:00:01Z" },
      ],
      c2: [],
    }
    getMock.mockReset()
    getMock.mockImplementation((path: string) => {
      if (path === "/api/playground/conversations/c1") return Promise.resolve(detailOf(first))
      if (path === "/api/playground/conversations/c2") return Promise.resolve(detailOf(second))
      return Promise.reject(new Error("no trace"))
    })
    postMock.mockReset()
    postMock.mockImplementation((path: string, body: { role: string; content: string; request_id: string }) => {
      const id = path.split("/")[4]!
      const turns = store.turns[id]!
      const seq = turns.length
      turns.push({ seq, ...body, created_at: "2026-08-30T10:01:00Z" })
      return Promise.resolve({ seq })
    })
    streamMock.mockReset()
    streamMock.mockImplementation(async function* (
      _path: string,
      _body: unknown,
      onStart?: (s: { requestId: string }) => void,
    ) {
      onStart?.({ requestId: "01NEW" })
      yield `data: ${JSON.stringify({ choices: [{ delta: { content: "a fresh answer" } }] })}\n\n`
    })
  })

  it("shows the turns just sent, not the transcript as it was first opened", async () => {
    // The detail query was read once when the conversation was opened and
    // never told about the appends, so coming back to it loaded the cached
    // two turns and the refetch that followed, carrying four, was ignored
    // because the conversation id had not changed.
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <ChatMode />
      </QueryClientProvider>,
    )

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    await screen.findByText("in one line")
    await userEvent.type(screen.getByLabelText("Message"), "follow up")
    await userEvent.click(screen.getByRole("button", { name: "Send" }))
    await screen.findByText("a fresh answer")
    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2))

    await userEvent.click(screen.getByRole("button", { name: /another thread/ }))
    await waitFor(() => expect(screen.queryByText("a fresh answer")).toBeNull())

    await userEvent.click(screen.getByRole("button", { name: /speculative decoding/ }))
    expect(await screen.findByText("a fresh answer")).toBeInTheDocument()
    expect(screen.getByText("follow up")).toBeInTheDocument()
  })
})
