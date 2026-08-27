import { describe, expect, it, vi } from "vitest"
import { copyToClipboard, liveSurfaces, originFor } from "./connect-screen"
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

describe("originFor", () => {
  it("uses the page origin when the proxy and admin ports match", () => {
    // A single combined listener behind a reverse proxy should not be
    // second-guessed by rewriting a port the operator already fronted.
    expect(
      originFor({ origin: "http://gateway:8080", hostname: "gateway" }, ":8080", ":8080"),
    ).toBe("http://gateway:8080")
  })

  it("swaps in the proxy port when the two listeners differ", () => {
    // The console is served from the admin port, but a client needs the
    // proxy port — the whole reason this function exists rather than a
    // bare `window.location.origin` reference in the component.
    expect(
      originFor({ origin: "http://gateway:8081", hostname: "gateway" }, ":8080", ":8081"),
    ).toBe("http://gateway:8080")
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
