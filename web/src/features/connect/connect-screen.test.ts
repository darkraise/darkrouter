import { describe, expect, it, vi } from "vitest"
import { copyToClipboard, liveSurfaces, originsFor } from "./connect-screen"
import type { Model } from "../../lib/api-types"

const model = (id: string): Model => ({
  model: id,
  providers: ["groq"],
  surfaces: ["llm"],
  context_window: 0,
  max_output_tokens: 0,
  tools: false,
  vision: false,
  reasoning: false,
  inferred: false,
  state: "live",
  pricing: null,
  free_tier: null,
  merge_source: "models_dev",
})

describe("live surfaces", () => {
  it("unions the surfaces the catalog actually serves, sorted", () => {
    expect(
      liveSurfaces([
        { ...model("a"), surfaces: ["llm", "embedding"] },
        { ...model("b"), surfaces: ["llm"] },
      ]),
    ).toEqual(["embedding", "llm"])
  })

  it("is empty before anything is catalogued", () => {
    expect(liveSurfaces([])).toEqual([])
  })
})

describe("originsFor", () => {
  const lan = { origin: "http://gateway:18081", hostname: "gateway", protocol: "http:" }

  it("uses the page origin when the proxy and admin ports match", () => {
    // Two bind addresses sharing a port. The page origin already names that
    // host and port, without an explicit :80 or :443 the swap would add.
    expect(
      originsFor(
        { origin: "http://gateway:18080", hostname: "gateway", protocol: "http:" },
        ":18080",
        ":18080",
      ),
    ).toEqual({ lan: "http://gateway:18080" })
  })

  it("swaps in the proxy port when the two listeners differ", () => {
    // The console is served from the admin port, but a client needs the
    // proxy port — the whole reason this function exists rather than a
    // bare `window.location.origin` reference in the component.
    expect(originsFor(lan, ":18080", ":18081")).toEqual({
      lan: "http://gateway:18080",
    })
  })

  it("carries the page's own scheme rather than hardcoding http", () => {
    // A console served over https behind a reverse proxy with split
    // admin/proxy ports must not emit an http:// URL for a client to copy.
    expect(
      originsFor(
        { origin: "https://gateway:18081", hostname: "gateway", protocol: "https:" },
        ":18080",
        ":18081",
      ),
    ).toEqual({ lan: "https://gateway:18080" })
  })

  it("keeps the LAN address alongside a configured public one", () => {
    // Both, never one: a gateway with a domain still answers on the LAN, and
    // an operator standing on that LAN wants the address that stays local.
    expect(originsFor(lan, ":18080", ":18081", "https://llm.example.com")).toEqual({
      lan: "http://gateway:18080",
      public: "https://llm.example.com",
    })
  })

  it("keeps a path prefix, which the LAN guess cannot express at all", () => {
    expect(
      originsFor(lan, ":18080", ":18081", "https://example.com/darkrouter"),
    ).toEqual({
      lan: "http://gateway:18080",
      public: "https://example.com/darkrouter",
    })
  })

  it("strips a trailing slash so the dialect suffix does not double it", () => {
    // baseUrlFor appends "/v1"; a public URL ending in "/" would otherwise
    // produce "https://example.com//v1", which some clients normalize and
    // some send verbatim.
    expect(originsFor(lan, ":18080", ":18081", "https://example.com/").public).toBe(
      "https://example.com",
    )
  })

  it("reports no public address when the value is blank or whitespace", () => {
    // The API sends "" for an unset key rather than omitting it, and a
    // half-filled field in an editor is whitespace, not empty.
    for (const blank of [undefined, "", "   "]) {
      expect(originsFor(lan, ":18080", ":18081", blank)).toEqual({
        lan: "http://gateway:18080",
      })
    }
  })
})

describe("copyToClipboard", () => {
  it("resolves false when the Clipboard API is unavailable", async () => {
    // jsdom has no navigator.clipboard by default, and neither does a real
    // browser on an insecure origin — the plain-HTTP LAN deployment this
    // console typically runs behind. A thrown TypeError here would be a
    // console-crashing regression, not a graceful "can't copy".
    expect(navigator.clipboard).toBeUndefined()
    await expect(copyToClipboard("secret")).resolves.toBe(false)
  })

  it("resolves true and writes the text when the API is available", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.assign(navigator, { clipboard: { writeText } })
    await expect(copyToClipboard("secret")).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledWith("secret")
    Object.assign(navigator, { clipboard: undefined })
  })

  it("resolves false rather than throwing when the write itself rejects", async () => {
    Object.assign(navigator, {
      clipboard: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
    })
    await expect(copyToClipboard("secret")).resolves.toBe(false)
    Object.assign(navigator, { clipboard: undefined })
  })
})
