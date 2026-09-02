import { describe, it, expect } from "vitest"
import {
  isLocalPreset,
  isLoopbackUrl,
  localRuntimes,
  validBaseUrl,
  withHost,
} from "./local-runtimes"
import type { Preset } from "../../lib/api-types"

function preset(id: string, base_url: string, extra: Partial<Preset> = {}): Preset {
  return {
    id,
    name: id,
    kind: "openaicompat",
    base_url,
    surfaces: ["llm"],
    auth_kind: "none",
    website: "",
    free_tier: false,
    ...extra,
  }
}

describe("isLocalPreset", () => {
  it("accepts every loopback spelling the backend accepts", () => {
    // Mirrors the regex at internal/catalog/preset.go:160. A spelling this
    // misses is a runtime the operator cannot add.
    for (const host of ["localhost", "127.0.0.1", "[::1]", "0.0.0.0"]) {
      expect(isLocalPreset(preset("x", `http://${host}:11434/v1`))).toBe(true)
    }
  })

  it("rejects a hosted provider", () => {
    expect(isLocalPreset(preset("groq", "https://api.groq.com/openai/v1"))).toBe(false)
  })

  it("rejects a host that merely starts with a loopback name", () => {
    expect(isLocalPreset(preset("x", "https://localhost.example.com/v1"))).toBe(false)
  })

  it("rejects a scheme of its own, which is a local program rather than a server", () => {
    // The auggie:// kind is reached by running a binary, not by HTTP.
    expect(isLocalPreset(preset("auggie", "auggie://local"))).toBe(false)
  })
})

describe("localRuntimes", () => {
  it("keeps only the local presets, ordered by name", () => {
    const all = [
      preset("groq", "https://api.groq.com/openai/v1"),
      preset("ollama", "http://localhost:11434/v1", { name: "Ollama" }),
      preset("lmstudio", "http://localhost:1234/v1", { name: "LM Studio" }),
    ]
    expect(localRuntimes(all).map((p) => p.id)).toEqual(["lmstudio", "ollama"])
  })
})

describe("validBaseUrl", () => {
  it("accepts what the backend accepts", () => {
    // Mirrors internal/admin/providers.go:33. Anything this lets through and
    // the server rejects is a create that fails after the form said it would
    // not.
    expect(validBaseUrl("http://localhost:11434/v1")).toBe(true)
    expect(validBaseUrl("https://box.lan:8443/openai/v1")).toBe(true)
    expect(validBaseUrl("  http://localhost:11434/v1  ")).toBe(true)
  })

  it("rejects what cannot be reached over HTTP", () => {
    expect(validBaseUrl("")).toBe(false)
    expect(validBaseUrl("localhost:11434")).toBe(false)
    expect(validBaseUrl("ftp://localhost/v1")).toBe(false)
    expect(validBaseUrl("auggie://local")).toBe(false)
  })
})

describe("isLoopbackUrl", () => {
  it("catches every loopback spelling, since each one is the container", () => {
    for (const host of ["localhost", "127.0.0.1", "[::1]", "0.0.0.0"]) {
      expect(isLoopbackUrl(`http://${host}:11434/v1`)).toBe(true)
    }
  })

  it("leaves an address that already names a reachable host alone", () => {
    expect(isLoopbackUrl("http://host.docker.internal:11434/v1")).toBe(false)
    expect(isLoopbackUrl("http://192.168.1.10:11434/v1")).toBe(false)
    expect(isLoopbackUrl("http://localhost.example.com/v1")).toBe(false)
  })

  it("says no rather than throwing on something half-typed", () => {
    expect(isLoopbackUrl("http://")).toBe(false)
  })
})

describe("withHost", () => {
  it("replaces the host and nothing else", () => {
    expect(withHost("http://localhost:11434/v1", "host.docker.internal")).toBe(
      "http://host.docker.internal:11434/v1",
    )
  })

  it("keeps a path the runtime needs", () => {
    // lemonade serves /api/v1 and docker-model-runner /engines/v1, so a swap
    // that dropped the path would point at a route neither answers.
    expect(withHost("http://localhost:12434/engines/v1", "gw")).toBe(
      "http://gw:12434/engines/v1",
    )
  })

  it("keeps a scheme and an absent port as they were", () => {
    expect(withHost("https://localhost/v1", "gw")).toBe("https://gw/v1")
  })

  it("brackets an IPv6 host, which is otherwise unparseable back", () => {
    expect(withHost("http://localhost:11434/v1", "::1")).toBe("http://[::1]:11434/v1")
  })
})
