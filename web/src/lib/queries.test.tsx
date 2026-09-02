import { describe, it, expect, vi } from "vitest"
import type { ReactNode } from "react"
import { renderHook, waitFor } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import {
  keys,
  usePlaygroundConversations,
  usePlaygroundPresets,
  useProviderHealth,
  useProxyTokens,
  useSessions,
} from "./queries"
import { POLL } from "./api"

describe("query keys", () => {
  it("gives every surface a distinct key", () => {
    // A collision does not fail loudly: two screens share a cache entry and
    // one renders the other's data.
    const flat = [
      keys.authStatus,
      keys.overview,
      keys.providers,
      keys.presets,
      keys.models,
      keys.aliases,
      keys.config,
      keys.health,
      keys.proxyTokens,
      keys.sessions,
    ].map((k) => JSON.stringify(k))
    expect(new Set(flat).size).toBe(flat.length)
  })

  it("varies the requests key with its filters", () => {
    // Filters live in the URL, so two filtered views are two cache entries.
    // A key that ignored them would show the first view's rows under the
    // second view's filters.
    const a = JSON.stringify(keys.requests({ provider: "groq" }))
    const b = JSON.stringify(keys.requests({ provider: "openai" }))
    expect(a).not.toBe(b)
  })

  it("varies the usage key with its dimension", () => {
    const day = JSON.stringify(keys.usage())
    const alias = JSON.stringify(keys.usage("alias"))
    expect(day).not.toBe(alias)
  })

  it("keys a trace by request id", () => {
    expect(JSON.stringify(keys.trace("r1"))).not.toBe(
      JSON.stringify(keys.trace("r2")),
    )
  })

  it("does not invalidate an open conversation when the rail changes", () => {
    // TanStack matches a query key by prefix, so a list key that is a prefix
    // of the detail key refetches the whole open conversation on every write
    // to the rail -- two per exchange, and the body grows with the transcript.
    const client = new QueryClient()
    const detail = keys.playgroundConversation("01ABC")
    client.setQueryData(detail, { id: "01ABC" })
    client.setQueryData(keys.playgroundConversations, [])

    void client.invalidateQueries({ queryKey: keys.playgroundConversations })

    expect(client.getQueryState(detail)?.isInvalidated).toBe(false)
    expect(
      client.getQueryState(keys.playgroundConversations)?.isInvalidated,
    ).toBe(true)
  })
})

describe("poll cadence", () => {
  it("holds §5's two intervals", () => {
    // Stated once so "near real time" is one edit rather than a sweep through
    // every screen.
    expect(POLL.fast).toBe(3000)
    expect(POLL.slow).toBe(30000)
  })
})

describe("the new surfaces", () => {
  it("keys healthz, discovery and policy distinctly", () => {
    const flat = [keys.healthz, keys.discovery, keys.policy, keys.health].map((k) =>
      JSON.stringify(k),
    )
    expect(new Set(flat).size).toBe(flat.length)
  })

  it("varies the usage key with its range", () => {
    // A 7-day and a 90-day view of one dimension are two answers. One key
    // would show the first range's rows under the second range's label.
    expect(JSON.stringify(keys.usage("provider", 7))).not.toBe(
      JSON.stringify(keys.usage("provider", 90)),
    )
  })

  it("keys an override by provider and model", () => {
    expect(JSON.stringify(keys.override("groq", "m"))).not.toBe(
      JSON.stringify(keys.override("groq", "n")),
    )
  })

  it("keeps the existing string call form of the usage key", () => {
    // Overview and the command palette call useUsage("alias"). Widening the
    // signature must not move their cache entry.
    expect(JSON.stringify(keys.usage("alias"))).toBe(
      JSON.stringify(["usage", "alias", 0]),
    )
  })
})

describe("the wrapped list endpoints", () => {
  // The admin API answers `{sessions: [...]}`, `{tokens: [...]}` and so on
  // rather than a bare array. The hook is where that wrapper comes off, so
  // every screen keeps reading the list it asked for.
  async function fetchThrough<T>(
    url: string,
    body: unknown,
    hook: () => { data: T | undefined },
  ): Promise<T | undefined> {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (u: string) =>
        new Response(JSON.stringify(u === url ? body : {}), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    )
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(hook, { wrapper })
    await waitFor(() => expect(result.current.data).toBeDefined())
    vi.unstubAllGlobals()
    return result.current.data
  }

  it("unwraps sessions", async () => {
    const session = { id: "s1", prefix: "abc", created_at: "", expires_at: "", current: true }
    await expect(
      fetchThrough("/api/sessions", { sessions: [session] }, () => useSessions()),
    ).resolves.toEqual([session])
  })

  it("unwraps proxy tokens", async () => {
    const token = { id: "t1", name: "laptop", prefix: "dk_", created_at: "", last_used_at: null }
    await expect(
      fetchThrough("/api/proxy-tokens", { tokens: [token] }, () => useProxyTokens()),
    ).resolves.toEqual([token])
  })

  it("unwraps provider health", async () => {
    const entry = { provider_id: "groq", key_id: "k", model: "m", backoff_level: 0, consecutive_failures: 0 }
    await expect(
      fetchThrough("/api/health/providers", { providers: [entry] }, () => useProviderHealth()),
    ).resolves.toEqual([entry])
  })

  it("unwraps playground presets and conversations", async () => {
    await expect(
      fetchThrough("/api/playground/presets", { presets: [{ id: "p" }] }, () =>
        usePlaygroundPresets(),
      ),
    ).resolves.toEqual([{ id: "p" }])
    await expect(
      fetchThrough("/api/playground/conversations", { conversations: [{ id: "c" }] }, () =>
        usePlaygroundConversations(),
      ),
    ).resolves.toEqual([{ id: "c" }])
  })

  it("hands the query's abort signal to fetch", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      new Response(JSON.stringify({ sessions: [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    )
    vi.stubGlobal("fetch", fetchMock)
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    )
    const { result } = renderHook(() => useSessions(), { wrapper })
    await waitFor(() => expect(result.current.data).toBeDefined())
    expect(fetchMock.mock.calls[0]?.[1]?.signal).toBeInstanceOf(AbortSignal)
    vi.unstubAllGlobals()
  })
})
