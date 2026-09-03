import { describe, it, expect } from "vitest"
import type { Credential, Preset, Provider } from "../../lib/api-types"
import {
  CONNECTION_DESCRIPTION,
  connectionCounts,
  connectionType,
  filterProviderRows,
  filterSummary,
  mergeProviderRows,
} from "./provider-rows"

const preset = (id: string, over: Partial<Preset> = {}): Preset => ({
  id,
  name: id,
  kind: "openaicompat",
  base_url: "https://x.example",
  surfaces: ["llm"],
  auth_kind: "bearer",
  website: "",
  free_tier: false,
  ...over,
})

const cred = (id: string, over: Partial<Credential> = {}): Credential => ({
  id,
  label: id,
  masked: "sk-…",
  enabled: true,
  cooling: false,
  kind: "static",
  ...over,
})

const provider = (id: string, over: Partial<Provider> = {}): Provider => ({
  id,
  name: id,
  preset: id,
  kind: "openaicompat",
  base_url: "https://x.example",
  priority: 10,
  enabled: true,
  auth_style: "bearer",
  free_models_only: false,
  allow_unsanctioned_free: false,
  credentials: [cred("k1")],
  ...over,
})

describe("mergeProviderRows", () => {
  it("lists every supported provider, configured or not", () => {
    const rows = mergeProviderRows(
      [preset("groq"), preset("cerebras"), preset("anthropic")],
      [provider("groq")],
    )
    expect(rows.map((r) => r.id)).toEqual(["groq", "anthropic", "cerebras"])
    expect(rows.map((r) => r.configured)).toEqual([true, false, false])
  })

  it("puts the configured ones first, in the order the router walks them", () => {
    // The handful that carry traffic must not be buried among two hundred
    // that do not.
    const rows = mergeProviderRows(
      [preset("a"), preset("b"), preset("zeta")],
      [provider("b", { priority: 10 }), provider("zeta", { priority: 90 })],
    )
    expect(rows.map((r) => r.id)).toEqual(["zeta", "b", "a"])
  })

  it("reads an emptied provider the same as one never configured", () => {
    // The inconsistency this merge exists to remove: both hold no account and
    // neither can be routed to, so both say unconfigured.
    const rows = mergeProviderRows(
      [preset("emptied"), preset("fresh")],
      [provider("emptied", { credentials: [] })],
    )
    expect(rows.map((r) => r.state)).toEqual(["unconfigured", "unconfigured"])
  })

  it("keeps a configured provider whose preset this build does not ship", () => {
    // It is serving requests. Dropping it because the catalogue moved would
    // hide the one row an operator most needs to find.
    const rows = mergeProviderRows([preset("groq")], [provider("legacy", { preset: "" })])
    expect(rows.map((r) => r.id)).toEqual(["legacy", "groq"])
  })

  it("carries the counts and the priority a configured row has", () => {
    const rows = mergeProviderRows(
      [preset("groq", { free_tier: true })],
      [provider("groq", { priority: 42, credentials: [cred("a"), cred("b")] })],
    )
    expect(rows[0]).toMatchObject({ accounts: 2, priority: 42, freeTier: true })
  })

  it("gives an unconfigured row no priority rather than zero", () => {
    // Zero is a real priority meaning last resort, and claiming it for a
    // provider that has never been configured would be a fact nobody set.
    const rows = mergeProviderRows([preset("groq")], [])
    expect(rows[0]?.priority).toBeNull()
    expect(rows[0]?.accounts).toBe(0)
  })
})

describe("filterProviderRows", () => {
  const rows = mergeProviderRows(
    [preset("groq", { free_tier: true }), preset("cerebras"), preset("anthropic")],
    [provider("groq"), provider("cerebras", { enabled: false })],
  )

  it("shows everything when nothing is asked", () => {
    expect(filterProviderRows(rows, {})).toHaveLength(3)
  })

  it("matches the id and the name", () => {
    expect(filterProviderRows(rows, { q: "gro" }).map((r) => r.id)).toEqual(["groq"])
  })

  it("narrows to the configured ones", () => {
    // Equal priorities tie-break by name, so cerebras leads groq.
    expect(filterProviderRows(rows, { configuredOnly: true }).map((r) => r.id)).toEqual([
      "cerebras",
      "groq",
    ])
  })

  it("drops a provider whose last account was deleted", () => {
    // The failure this guards: the row survives its keys, and keying the filter on
    // the row rather than on the accounts made "Configured only" keep a
    // provider the badge beside it called unconfigured.
    const emptied = mergeProviderRows(
      [preset("groq"), preset("cerebras")],
      [provider("groq", { credentials: [] })],
    )
    expect(filterProviderRows(emptied, { configuredOnly: true })).toEqual([])
    // And it is still in the full list, still openable, because its row --
    // and the priority and import settings on it -- are still there.
    expect(emptied.map((r) => r.id)).toEqual(["groq", "cerebras"])
    expect(emptied[0]?.provider).toBeDefined()
    expect(emptied[0]?.state).toBe("unconfigured")
  })

  it("narrows by state", () => {
    expect(filterProviderRows(rows, { state: "disabled" }).map((r) => r.id)).toEqual(["cerebras"])
  })

  it("narrows to the free tier", () => {
    expect(filterProviderRows(rows, { freeTier: true }).map((r) => r.id)).toEqual(["groq"])
  })

  it("combines filters", () => {
    expect(filterProviderRows(rows, { q: "anth", state: "unconfigured" }).map((r) => r.id)).toEqual([
      "anthropic",
    ])
  })
})

describe("filterSummary", () => {
  it("names the total when nothing is filtered out", () => {
    expect(filterSummary(197, 197)).toBe("197 providers")
  })

  it("says how many of how many when a filter is on", () => {
    // Without the total, a short list reads as a small catalogue rather than
    // as a filter that is still applied.
    expect(filterSummary(4, 197)).toBe("4 of 197")
  })
})

describe("how a provider is reached", () => {
  it("reads a local runtime off its address, not off a flag", () => {
    // A loopback URL cannot be anybody else's machine, which is the thing
    // that actually makes a provider local.
    for (const base_url of [
      "http://localhost:11434/v1",
      "http://127.0.0.1:8000/v1",
      "https://localhost:1234/v1",
      "http://[::1]:8080/v1",
    ]) {
      expect(connectionType({ base_url, auth_kind: "none" }), base_url).toBe("local")
    }
  })

  it("does not call a hosted provider local for containing the word", () => {
    // "localhost.example.com" and "https://api.local-ai.dev" are somebody
    // else's machines.
    expect(connectionType({ base_url: "https://localhost.example.com/v1" })).not.toBe("local")
    expect(connectionType({ base_url: "https://api.local-ai.dev/v1" })).not.toBe("local")
  })

  it("counts a program on this box as local, not as a remote no-auth gateway", () => {
    // auggie://cli/v1 names a CLI the operator installed and logged into, so
    // "Local" is the honest group even though the base URL has no loopback
    // address to read it off.
    expect(connectionType({ base_url: "auggie://cli/v1", auth_kind: "none" })).toBe("local")
  })

  it("separates the credentials that are not a pasted secret", () => {
    expect(connectionType({ base_url: "https://x", auth_kind: "oauth" })).toBe("oauth")
    expect(connectionType({ base_url: "https://x", auth_kind: "sigv4" })).toBe("signed")
    expect(connectionType({ base_url: "https://x", auth_kind: "gcp-sa" })).toBe("signed")
    expect(connectionType({ base_url: "https://x", auth_kind: "none" })).toBe("none")
  })

  it("files a published credential under no auth", () => {
    // anonymous asks for a key the vendor publishes and the release ships, so
    // the operator pastes nothing — which is the question this filter answers.
    expect(connectionType({ base_url: "https://x", auth_kind: "anonymous" })).toBe("none")
  })

  it("treats every header style as one kind of key", () => {
    // bearer, x-api-key and api-key differ on the wire and not to an operator
    // holding the same string.
    for (const auth_kind of ["bearer", "x-api-key", "api-key", "query-param", undefined]) {
      expect(connectionType({ base_url: "https://x", auth_kind })).toBe("key")
    }
  })
})

describe("the quick filters", () => {
  it("narrows to one way of connecting", () => {
    const rows = mergeProviderRows(
      [
        preset("groq"),
        preset("ollama", { base_url: "http://localhost:11434/v1", auth_kind: "none" }),
      ],
      [],
    )
    expect(filterProviderRows(rows, { connection: "local" }).map((r) => r.id)).toEqual(["ollama"])
    expect(filterProviderRows(rows, { connection: "key" }).map((r) => r.id)).toEqual(["groq"])
  })

  it("counts what the chip would leave", () => {
    const rows = mergeProviderRows(
      [
        preset("groq"),
        preset("ollama", { base_url: "http://localhost:11434/v1", auth_kind: "none" }),
        preset("lmstudio", { base_url: "http://localhost:1234/v1", auth_kind: "none" }),
      ],
      [],
    )
    const counts = connectionCounts(rows)
    expect(counts.local).toBe(2)
    expect(counts.key).toBe(1)
    expect(counts.oauth).toBe(0)
  })
})

describe("the connection chips", () => {
  it("say what each way of connecting means, since the label alone cannot", () => {
    // "Signed" is the one an operator cannot expand from the word: it covers
    // the two schemes that sign every request rather than sending a key.
    expect(CONNECTION_DESCRIPTION.signed).toBe("SigV4 and service-account credentials")
    for (const type of ["key", "oauth", "signed", "none", "local"] as const) {
      expect(CONNECTION_DESCRIPTION[type]).not.toBe("")
    }
  })
})
