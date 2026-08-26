import { describe, it, expect } from "vitest"
import { keys } from "./queries"
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
