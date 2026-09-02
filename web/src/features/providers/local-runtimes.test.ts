import { describe, it, expect } from "vitest"
import { composeBaseUrl, isLocalPreset, localRuntimes, normalizeHost, portOf } from "./local-runtimes"
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

describe("portOf", () => {
  it("reads the port the preset ships", () => {
    expect(portOf(preset("ollama", "http://localhost:11434/v1"))).toBe("11434")
  })

  it("falls back to the scheme's own port when the preset names none", () => {
    expect(portOf(preset("x", "http://localhost/v1"))).toBe("80")
    expect(portOf(preset("x", "https://localhost/v1"))).toBe("443")
  })
})

describe("normalizeHost", () => {
  it("keeps a bare hostname", () => {
    expect(normalizeHost("host.docker.internal")).toBe("host.docker.internal")
  })

  it("trims surrounding whitespace", () => {
    expect(normalizeHost("  192.168.1.10 ")).toBe("192.168.1.10")
  })

  it("strips a scheme and path from a pasted URL", () => {
    // Pasting the whole endpoint into a box labelled Host is the likeliest
    // wrong input there is, and it is unambiguous what was meant.
    expect(normalizeHost("http://192.168.1.10:11434/v1")).toBe("192.168.1.10")
  })

  it("keeps an IPv6 literal's brackets and its colons", () => {
    expect(normalizeHost("[::1]")).toBe("[::1]")
  })
})

describe("composeBaseUrl", () => {
  it("replaces the authority and keeps the preset's own path", () => {
    expect(composeBaseUrl(preset("ollama", "http://localhost:11434/v1"), "host.docker.internal", "11434")).toBe(
      "http://host.docker.internal:11434/v1",
    )
  })

  it("keeps a path that is not /v1", () => {
    // lemonade serves /api/v1 and docker-model-runner /engines/v1. Assuming
    // /v1 would point both at nothing.
    expect(composeBaseUrl(preset("lemonade", "http://localhost:8000/api/v1"), "gw", "8000")).toBe(
      "http://gw:8000/api/v1",
    )
    expect(
      composeBaseUrl(preset("dmr", "http://localhost:12434/engines/v1"), "gw", "12434"),
    ).toBe("http://gw:12434/engines/v1")
  })

  it("brackets an IPv6 host that arrives without brackets", () => {
    expect(composeBaseUrl(preset("ollama", "http://localhost:11434/v1"), "::1", "11434")).toBe(
      "http://[::1]:11434/v1",
    )
  })

  it("normalizes the host it is given", () => {
    expect(composeBaseUrl(preset("ollama", "http://localhost:11434/v1"), " gw ", "11434")).toBe(
      "http://gw:11434/v1",
    )
  })

  it("returns null for a blank host, so the form can refuse to submit", () => {
    expect(composeBaseUrl(preset("ollama", "http://localhost:11434/v1"), "   ", "11434")).toBeNull()
  })

  it("returns null for a port outside the range a TCP port can hold", () => {
    const p = preset("ollama", "http://localhost:11434/v1")
    expect(composeBaseUrl(p, "gw", "0")).toBeNull()
    expect(composeBaseUrl(p, "gw", "65536")).toBeNull()
    expect(composeBaseUrl(p, "gw", "")).toBeNull()
    expect(composeBaseUrl(p, "gw", "11434x")).toBeNull()
  })
})
