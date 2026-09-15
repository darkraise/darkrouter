import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { TestDrawer, conversationHistory, logLine } from "./test-drawer"
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

/** A gateway that answers every route the drawer calls, with the playground
 *  reply supplied per call. No request id, so no run waits on a trace. */
function stubRoutes(playground: () => Response = () => new Response(sse(OK_FRAME))) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string | URL, init?: RequestInit) => {
      const json = (body: unknown, status = 200) =>
        new Response(JSON.stringify(body), {
          status,
          headers: { "Content-Type": "application/json" },
        })
      const u = String(url)
      if (u.startsWith("/api/models")) {
        return json({
          models: [{ model: "llama-3.3", providers: ["groq", "ollama"], surfaces: ["llm"] }],
          aliases: [],
        })
      }
      if (u === "/api/providers" && init?.method === "POST") return json({ id: "ollama" }, 201)
      if (u === "/api/playground") return playground()
      return json({ providers: [], requests: [] })
    }),
  )
}

const OK_FRAME = 'data: {"choices":[{"delta":{"content":"ok"}}]}\n\n'

function fetchCalls() {
  return (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls
}

function playgroundBodies(): { messages: { role: string; content: string }[] }[] {
  return fetchCalls()
    .filter(([u]) => String(u) === "/api/playground")
    .map(([, i]) => JSON.parse((i as RequestInit).body as string))
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

  it("is added once, not again on the next message", async () => {
    // The drawer's row is a snapshot taken when it opened, so it goes on
    // saying "no provider" after the first send has made one.
    stubRoutes()
    mount(<TestDrawer row={keylessRow} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findByText("ok")
    await screen.findByRole("button", { name: /send/i })

    await userEvent.type(screen.getByLabelText("Test message"), "and again")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => expect(playgroundBodies()).toHaveLength(2))
    expect(
      fetchCalls().filter(
        ([u, i]) => String(u) === "/api/providers" && (i as RequestInit)?.method === "POST",
      ),
    ).toHaveLength(1)
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

  it("fails a run whose stream carries an error after it started", async () => {
    // The edge reports a provider failing mid-stream as an error frame on a
    // 200, because the status line has already been sent.
    stubRoutes(
      () =>
        new Response(
          sse(
            OK_FRAME.replace('"ok"', '"o"'),
            'data: {"error":{"message":"upstream exploded","type":"api_error"}}\n\n',
            "data: [DONE]\n\n",
          ),
        ),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    expect((await screen.findAllByText(/upstream exploded/i)).length).toBeGreaterThan(0)
    await screen.findByRole("button", { name: /send/i })
    expect(screen.queryByText(/served in/i)).not.toBeInTheDocument()
  })

  it("does not call a reply with no text served", async () => {
    stubRoutes(() => new Response(sse("data: [DONE]\n\n")))
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    expect((await screen.findAllByText(/without a reply/i)).length).toBeGreaterThan(0)
    expect(screen.queryByText(/served in/i)).not.toBeInTheDocument()
  })

  it("counts a stream that carried only reasoning as served", async () => {
    stubRoutes(
      () =>
        new Response(
          sse(
            'data: {"choices":[{"delta":{"reasoning_content":"thinking it over"}}]}\n\n',
            "data: [DONE]\n\n",
          ),
        ),
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    expect(await screen.findByText(/served in/i)).toBeInTheDocument()
    expect(screen.getByText(/thinking it over/)).toBeInTheDocument()
    expect(screen.getByText(/only reasoning arrived/i)).toBeInTheDocument()
    expect(screen.queryByText(/without a reply/i)).not.toBeInTheDocument()
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

  it("does not send a failed answer back as the model's own words", async () => {
    let first = true
    stubRoutes(() => {
      if (!first) return new Response(sse(OK_FRAME))
      first = false
      return new Response(
        JSON.stringify({ error: { message: "provider refused the credential" } }),
        { status: 401, headers: { "Content-Type": "application/json" } },
      )
    })
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findAllByText(/refused the credential/i)

    await userEvent.type(screen.getByLabelText("Test message"), "and again")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => expect(playgroundBodies()).toHaveLength(2))
    expect(playgroundBodies()[1]?.messages).toEqual([{ role: "user", content: "and again" }])
  })

  it("does not send the partial reply of a stream that failed", async () => {
    let first = true
    stubRoutes(() => {
      if (!first) return new Response(sse(OK_FRAME))
      first = false
      return new Response(
        sse(
          'data: {"choices":[{"delta":{"content":"half a reply"}}]}\n\n',
          'data: {"error":{"message":"upstream exploded","type":"api_error"}}\n\n',
        ),
      )
    })
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findAllByText(/upstream exploded/i)
    expect(screen.getByText(/half a reply/)).toBeInTheDocument()

    await userEvent.type(screen.getByLabelText("Test message"), "and again")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => expect(playgroundBodies()).toHaveLength(2))
    expect(playgroundBodies()[1]?.messages).toEqual([{ role: "user", content: "and again" }])
  })

  it("does not send the partial reply of a run the operator stopped", async () => {
    // Truncated where the operator cut it, so replaying it would put words in
    // the model's mouth it never finished saying.
    let first = true
    stubRoutes()
    const routes = vi.mocked(globalThis.fetch).getMockImplementation()!
    ;vi.mocked(globalThis.fetch).mockImplementation(
      async (url: string | URL | Request, init?: RequestInit) => {
        if (String(url) !== "/api/playground" || !first) return routes(url, init)
        first = false
        const signal = init?.signal ?? undefined
        return new Response(
          new ReadableStream({
            start(c) {
              c.enqueue(new TextEncoder().encode('data: {"choices":[{"delta":{"content":"half a reply"}}]}\n\n'))
              signal?.addEventListener("abort", () =>
                c.error(new DOMException("The operation was aborted.", "AbortError")),
              )
            },
          }),
        )
      },
    )
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findByText(/half a reply/)
    await userEvent.click(screen.getByRole("button", { name: /stop/i }))
    await screen.findByText(/stopped before the reply finished/i)
    expect(screen.getByText(/half a reply/)).toBeInTheDocument()

    await userEvent.type(screen.getByLabelText("Test message"), "and again")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => expect(playgroundBodies()).toHaveLength(2))
    expect(playgroundBodies()[1]?.messages).toEqual([{ role: "user", content: "and again" }])
  })

  it("still sends the prompt a reasoning-only reply answered", async () => {
    // The reply had no text to send back, but the prompt was answered. It is
    // folded into the next one rather than sent as a second user turn in a
    // row, which a provider holding to strict alternation refuses.
    let first = true
    stubRoutes(() => {
      if (!first) return new Response(sse(OK_FRAME))
      first = false
      return new Response(
        sse('data: {"choices":[{"delta":{"reasoning_content":"thinking it over"}}]}\n\n', "data: [DONE]\n\n"),
      )
    })
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.clear(screen.getByLabelText("Test message"))
    await userEvent.type(screen.getByLabelText("Test message"), "remember the number 7")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findByText(/only reasoning arrived/i)

    await userEvent.type(
      await screen.findByLabelText("Test message", {}, { timeout: 3000 }),
      "which number?",
    )
    await userEvent.click(await screen.findByRole("button", { name: /send/i }, { timeout: 3000 }))

    await waitFor(() => expect(playgroundBodies()).toHaveLength(2))
    expect(playgroundBodies()[1]?.messages).toEqual([
      { role: "user", content: "remember the number 7\n\nwhich number?" },
    ])
  })

  it("does not send the empty turn a failed setup left behind", async () => {
    stubRoutes()
    const routes = vi.mocked(globalThis.fetch).getMockImplementation()!
    let failCreate = true
    ;vi.mocked(globalThis.fetch).mockImplementation(
      async (url: string | URL | Request, init?: RequestInit) => {
        if (String(url) === "/api/providers" && init?.method === "POST" && failCreate) {
          failCreate = false
          return new Response(JSON.stringify({ error: "database is locked" }), {
            status: 500,
            headers: { "Content-Type": "application/json" },
          })
        }
        return routes(url, init)
      },
    )
    mount(<TestDrawer row={keylessRow} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findAllByText(/database is locked/i)

    await userEvent.type(screen.getByLabelText("Test message"), "and again")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))

    await waitFor(() => expect(playgroundBodies()).toHaveLength(1))
    expect(playgroundBodies()[0]?.messages).toEqual([{ role: "user", content: "and again" }])
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

describe("stopping a run", () => {
  /** A provider that never sends headers, until the request is aborted. */
  function stallPlayground() {
    const signals: AbortSignal[] = []
    stubRoutes()
    const routes = vi.mocked(globalThis.fetch).getMockImplementation()!
    ;vi.mocked(globalThis.fetch).mockImplementation(
      (url: string | URL | Request, init?: RequestInit) => {
        if (String(url) !== "/api/playground") return routes(url, init)
        const signal = init?.signal ?? undefined
        signals.push(signal as AbortSignal)
        return new Promise<Response>((_, reject) => {
          signal?.addEventListener("abort", () =>
            reject(new DOMException("The operation was aborted.", "AbortError")),
          )
        })
      },
    )
    return signals
  }

  it("cancels a request that is still waiting on the provider", async () => {
    const signals = stallPlayground()
    mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await userEvent.click(await screen.findByRole("button", { name: /stop/i }))

    await waitFor(() => expect(signals[0]?.aborted).toBe(true))
    expect(await screen.findByRole("button", { name: /send/i })).toBeInTheDocument()
    expect(screen.queryByText(/served in/i)).not.toBeInTheDocument()
  })

  it("cancels the request when the drawer goes away", async () => {
    const signals = stallPlayground()
    const { unmount } = mount(<TestDrawer row={row} open onOpenChange={() => {}} />)
    await userEvent.type(await screen.findByLabelText("Model"), "llama-3.3")
    await userEvent.click(screen.getByRole("button", { name: /send/i }))
    await screen.findByRole("button", { name: /stop/i })

    unmount()
    expect(signals[0]?.aborted).toBe(true)
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

describe("conversationHistory", () => {
  const thinking = { role: "assistant" as const, content: "", reasoning: "thinking it over" }

  it("keeps an answered prompt when the prompt folded into it fails", () => {
    const history = conversationHistory([
      { role: "user", content: "remember the number 7" },
      thinking,
      { role: "user", content: "which number?" },
      { role: "assistant", content: "upstream exploded", failed: true },
    ])
    expect(history).toEqual([{ role: "user", content: "remember the number 7" }])
  })

  it("drops only the last folded prompt when several were folded", () => {
    const history = conversationHistory([
      { role: "user", content: "one" },
      thinking,
      { role: "user", content: "two" },
      thinking,
      { role: "user", content: "three" },
      { role: "assistant", content: "", stopped: true },
      { role: "user", content: "four" },
    ])
    expect(history).toEqual([{ role: "user", content: "one\n\ntwo\n\nfour" }])
  })
})
