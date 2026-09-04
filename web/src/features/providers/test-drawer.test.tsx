import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { TestDrawer, logLine } from "./test-drawer"
import type { ProviderRow } from "./provider-rows"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}))

const row: ProviderRow = {
  id: "groq", name: "Groq", preset: "groq", kind: "openaicompat",
  state: "healthy", accounts: 1, priority: 10, configured: true,
  freeTier: false, connection: "key", keyless: false,
  provider: {
    id: "groq", name: "Groq", preset: "groq", kind: "openaicompat",
    base_url: "https://api.groq.com/openai/v1", priority: 10, enabled: true,
    auth_style: "bearer", free_models_only: false, allow_unsanctioned_free: false,
    credentials: [
      { id: "k1", label: "k1", masked: "sk-…", enabled: true, cooling: false, kind: "static" },
    ],
  },
}

/** A provider that asks for no credential and has never been added: the case
 *  the drawer sets up on the operator's behalf. */
const keylessRow: ProviderRow = {
  id: "ollama", name: "Ollama", preset: "ollama", kind: "openaicompat",
  state: "unconfigured", accounts: 0, priority: null, configured: false,
  freeTier: false, connection: "local", keyless: true,
}

function sse(...chunks: string[]) {
  return new ReadableStream({
    start(c) {
      for (const chunk of chunks) c.enqueue(new TextEncoder().encode(chunk))
      c.close()
    },
  })
}

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

function stubFetch(playground: () => Response) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string | URL) => {
      if (String(url).startsWith("/api/models")) {
        return new Response(
          JSON.stringify({
            models: [
              { model: "llama-3.3", providers: ["groq", "ollama"], surfaces: ["llm"] },
            ],
            aliases: [],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        )
      }
      return playground()
    }),
  )
}

beforeEach(() => vi.unstubAllGlobals())

describe("the provider test drawer", () => {
  it("sends the model provider-qualified", async () => {
    // Testing groq and being answered by cerebras is the one outcome this
    // drawer must never produce, so the target names the provider.
    const fetchMock = vi.fn()
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    fetchMock.mockImplementation(globalThis.fetch as never)

    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => {
      const call = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find(
        ([u]) => String(u) === "/api/playground",
      )
      expect(JSON.parse((call?.[1] as RequestInit).body as string).model).toBe("groq/llama-3.3")
    })
  })

  it("streams the reply into the first tab", async () => {
    stubFetch(
      () =>
        new Response(
          sse(
            'data: {"choices":[{"delta":{"content":"o"}}]}\n\n',
            'data: {"choices":[{"delta":{"content":"k"}}]}\n\n',
          ),
          { status: 200, headers: { "X-Darkrouter-Request": "req-1" } },
        ),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    expect(await screen.findByText("ok")).toBeInTheDocument()
  })

  it("shows the failure in the log rather than swallowing it", async () => {
    // The failure is the answer as often as the reply is: a refused
    // credential, a base URL that is a web page.
    //
    // The nested error shape is the executor's, and it is load-bearing: a 401
    // carrying a bare string means the session died, and the client logs out
    // on it rather than showing it here.
    stubFetch(
      () =>
        new Response(
          JSON.stringify({ error: { message: "provider refused the credential" } }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        ),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    // Three places, deliberately: the verdict, the failed turn in the
    // transcript, and the log. A turn that simply never arrives reads as a
    // hang, so the refusal has to land where the operator is looking.
    await waitFor(() =>
      expect(screen.getAllByText(/refused the credential/i).length).toBeGreaterThanOrEqual(2),
    )
  })

  it("will not send without a model", async () => {
    stubFetch(() => new Response("", { status: 200 }))
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    expect(await screen.findByRole("button", { name: /send/i })).toBeDisabled()
  })
})

describe("a keyless provider with no row yet", () => {
  it("is added on the way to the first test", async () => {
    // The whole offer: nothing to set up. The router still walks a database
    // row, so the drawer makes one in the click that was going to happen
    // anyway rather than as a step in front of it.
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    mount(<TestDrawer row={keylessRow} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => {
      const create = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.find(
        ([u, i]) => String(u) === "/api/providers" && (i as RequestInit)?.method === "POST",
      )
      expect(JSON.parse((create?.[1] as RequestInit).body as string)).toEqual({
        id: "ollama", preset: "ollama",
      })
    })
  })

  it("says so before it does it", async () => {
    stubFetch(() => new Response("", { status: 200 }))
    mount(<TestDrawer row={keylessRow} open onOpenChange={() => {}} />)
    expect(await screen.findByText(/asks for no credential/i)).toBeInTheDocument()
  })

  it("leaves a provider that already exists alone", async () => {
    // Only the keyless-and-absent case is created. A second POST against a
    // provider that is already there would 409.
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() =>
      expect(
        (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.some(
          ([u]) => String(u) === "/api/providers",
        ),
      ).toBe(false),
    )
  })
})

describe("the verdict", () => {
  it("says nothing has been tried before anything is", async () => {
    // Not a failure, and it must not look like one: an empty panel on a
    // screen whose job is a pass/fail reads as a fail.
    stubFetch(() => new Response("", { status: 200 }))
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    expect(await screen.findByText(/not tested yet/i)).toBeInTheDocument()
  })

  it("reports a served request without opening the log", async () => {
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    expect(await screen.findByText(/served in/i)).toBeInTheDocument()
  })

  it("carries the reason a refusal happened, not just that it did", async () => {
    // "Refused" alone sends an operator to the log, whose first useful line
    // is the one already in hand.
    stubFetch(
      () =>
        new Response(
          JSON.stringify({ error: { message: "upstream rejected the credential" } }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        ),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    // Twice over: once as the verdict, once in the log.
    await waitFor(() =>
      expect(screen.getAllByText(/rejected the credential/i).length).toBeGreaterThan(0),
    )
  })
})

describe("the exchange", () => {
  it("shows what was sent beside what came back", async () => {
    // Two turns read as a conversation. One blob of reply text does not say
    // what it was answering.
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    expect(await screen.findByText("ok")).toBeInTheDocument()
    // The composer clears on send, like any chat: the prompt now lives in the
    // transcript alone.
    const asked = screen.getByText("Reply with the single word: ok")
    expect(asked).toBeInTheDocument()

    // Identity by side, not by label: the reader's own words sit right, the
    // provider's left. That is what a transcript is read by, and it is the
    // reason these are bubbles rather than a ruled list.
    expect(asked.closest("div")?.parentElement?.className).toContain("justify-end")
    expect(screen.getByText("ok").closest("div")?.className).not.toContain("justify-end")
  })
})

describe("a log line", () => {
  it("is stamped when it happened", () => {
    const before = Date.now()
    const line = logLine("error", "boom")
    expect(line.at).toBeGreaterThanOrEqual(before)
    expect(line).toMatchObject({ level: "error", text: "boom" })
  })
})

describe("the conversation", () => {
  it("sends the history so a second question can refer to the first", async () => {
    // The difference between a chat and a form that clears itself: without
    // the earlier turns the provider is answering each message cold.
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findByText("ok")
    // The run holds Stop until the trace lookup finishes, so wait for the
    // composer to come back rather than racing it.
    await screen.findByRole("button", { name: /send/i }, { timeout: 3000 })

    await userEvent.type(screen.getByLabelText("Test message"), "and again")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => {
      const calls = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.filter(
        ([u]) => String(u) === "/api/playground",
      )
      expect(calls).toHaveLength(2)
      const second = JSON.parse((calls[1]?.[1] as RequestInit).body as string)
      expect(second.messages.map((m: { role: string }) => m.role)).toEqual([
        "user",
        "assistant",
        "user",
      ])
    })
  })

  it("clears back to a fresh probe", async () => {
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findByText("ok")
    await userEvent.click(
      await screen.findByRole("button", { name: /clear/i }, { timeout: 3000 }),
    )
    expect(screen.getByText(/no messages yet/i)).toBeInTheDocument()
    expect(screen.getByText(/not tested yet/i)).toBeInTheDocument()
  })

  it("will not send an empty message", async () => {
    stubFetch(() => new Response("", { status: 200 }))
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.clear(screen.getByLabelText("Test message"))
    expect(screen.getByRole("button", { name: /send/i })).toBeDisabled()
  })
})

describe("aiming the drawer at another provider", () => {
  it("starts that provider from nothing rather than carrying the last one's run over", async () => {
    // The drawer is mounted once for the whole list. Without a reset the
    // model picked for groq, and groq's transcript, sat under ollama's name.
    stubFetch(
      () =>
        new Response(sse('data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'), {
          status: 200,
          headers: { "X-Darkrouter-Request": "req-1" },
        }),
    )
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const { rerender } = render(
      <QueryClientProvider client={client}>
        <TestDrawer row={row} open onOpenChange={() => {}} />
      </QueryClientProvider>,
    )
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findByText("ok")

    rerender(
      <QueryClientProvider client={client}>
        <TestDrawer row={keylessRow} open onOpenChange={() => {}} />
      </QueryClientProvider>,
    )
    expect(screen.getByLabelText("Model")).toHaveValue("")
    expect(screen.queryByText("ok")).not.toBeInTheDocument()
    expect(screen.getByText(/not tested yet/i)).toBeInTheDocument()
  })
})
