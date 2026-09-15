import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { TestLogTab } from "./test-log-tab"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children }: { children: React.ReactNode }) => <a>{children}</a>,
}))

beforeEach(() => vi.unstubAllGlobals())

describe("the drawer's log tab", () => {
  it("says the log did not load rather than that nothing was tested", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(JSON.stringify({ error: "database is locked" }), {
          status: 500,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    )
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <TestLogTab providerId="groq" />
      </QueryClientProvider>,
    )

    expect(await screen.findByText(/database is locked/i)).toBeInTheDocument()
    expect(screen.queryByText(/nothing tested yet/i)).not.toBeInTheDocument()
  })

  it("asks for every run that tried this provider, not only ones it served", async () => {
    // A test that failed on every attempt names no serving provider, so a
    // served-by filter dropped exactly the run an operator came to read.
    const fetch = vi.fn(
      async (_url: string) =>
        new Response(JSON.stringify({ requests: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    )
    vi.stubGlobal("fetch", fetch)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <TestLogTab providerId="groq" />
      </QueryClientProvider>,
    )

    expect(await screen.findByText(/nothing tested yet/i)).toBeInTheDocument()
    const params = new URL(String(fetch.mock.calls[0]?.[0]), "http://x").searchParams
    expect(params.get("attempted_provider")).toBe("groq")
    expect(params.has("provider")).toBe(false)
  })

  it("shows a cancelled run as cancelled rather than as a failure", async () => {
    const row = (id: string, status: string, error_code?: string) => ({
      id, ts_ms: Date.now(), dialect: "openai", surface: "chat", model: "llama", status,
      source: "console", tokens_in: 0, tokens_out: 0, cache_read_tokens: 0, cost_micros: null,
      ttft_ms: null, total_ms: 120, attempts: 1, ...(error_code ? { error_code } : {}),
    })
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(
          JSON.stringify({
            requests: [row("r1", "cancelled", "client_cancelled"), row("r2", "error", "upstream_5xx")],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    )
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={client}>
        <TestLogTab providerId="groq" />
      </QueryClientProvider>,
    )

    const cancelled = await screen.findByText("cancelled")
    expect(screen.queryByText("client_cancelled")).not.toBeInTheDocument()
    // jsdom resolves no Tailwind colour, so the tone is read from the class.
    expect(cancelled.className).toContain("--muted-foreground")
    expect(cancelled.className).not.toContain("--destructive")
    expect(screen.getByText("upstream_5xx").className).toContain("--destructive")
  })
})
