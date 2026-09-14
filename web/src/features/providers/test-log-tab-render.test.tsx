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
})
