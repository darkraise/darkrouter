import { describe, expect, it } from "vitest"
import { baseUrlFor, snippetFor, TOOLS } from "./snippets"

describe("baseUrlFor", () => {
  it("suffixes each dialect the way its routes are served", () => {
    // These come from the proxy mux, not from prose: a snippet that disagrees
    // with the served routes fails at the client, far from this screen.
    expect(baseUrlFor("http://127.0.0.1:8080", "openai")).toBe("http://127.0.0.1:8080/v1")
    expect(baseUrlFor("http://127.0.0.1:8080", "anthropic")).toBe("http://127.0.0.1:8080")
    expect(baseUrlFor("http://127.0.0.1:8080", "gemini")).toBe("http://127.0.0.1:8080/v1beta")
  })

  it("does not double a trailing slash", () => {
    expect(baseUrlFor("http://x/", "openai")).toBe("http://x/v1")
  })
})

describe("snippetFor", () => {
  it("points Claude Code at the anthropic base url", () => {
    const s = snippetFor("claude-code", "http://127.0.0.1:8080", "dr_tok")
    expect(s).toContain("ANTHROPIC_BASE_URL=http://127.0.0.1:8080")
    expect(s).toContain("dr_tok")
  })

  it("points the OpenAI SDK at the /v1 base url", () => {
    expect(snippetFor("openai-sdk", "http://127.0.0.1:8080/v1", "dr_tok")).toContain(
      "http://127.0.0.1:8080/v1",
    )
  })

  it("gives every tool a snippet", () => {
    // A tab that renders an empty block reads as a broken screen rather than
    // as a tool nobody wrote a snippet for.
    for (const tool of TOOLS) {
      expect(snippetFor(tool, "http://x", "t").trim()).not.toBe("")
    }
  })

  it("shows a placeholder rather than an empty value when no token exists", () => {
    // A fresh install has no proxy token, and a snippet ending in "=" is one
    // an operator will paste and then debug.
    expect(snippetFor("claude-code", "http://x", "")).toContain("<your-token>")
  })

  describe("with a value carrying shell or Python syntax", () => {
    // server.public_url accepts any absolute URL path and every signed-in
    // account can set it, so the snippet an operator pastes must carry the
    // value as data rather than as code.
    const hostile = "https://x.test/$(id)`id`'\"\\ a;b"

    it("single-quotes the shell value so nothing in it expands", () => {
      expect(snippetFor("claude-code", hostile, "dr_tok").split("\n")[0]).toBe(
        "export ANTHROPIC_BASE_URL='https://x.test/$(id)`id`'\\''\"\\ a;b'",
      )
      expect(snippetFor("codex", hostile, "dr_tok").split("\n")[0]).toBe(
        "export OPENAI_BASE_URL='https://x.test/$(id)`id`'\\''\"\\ a;b'",
      )
    })

    it("quotes a tilde, which an export expands after : or =", () => {
      expect(snippetFor("claude-code", "https://h/a:~/b", "dr_tok").split("\n")[0]).toBe(
        "export ANTHROPIC_BASE_URL='https://h/a:~/b'",
      )
      expect(snippetFor("codex", "http://x", "~tok").split("\n")[1]).toBe(
        "export OPENAI_API_KEY='~tok'",
      )
    })

    it("quotes the placeholder token, which a shell reads as a redirect", () => {
      expect(snippetFor("claude-code", "http://x", "")).toContain(
        "export ANTHROPIC_AUTH_TOKEN='<your-token>'",
      )
    })

    it("writes a Python string literal that decodes back to the value", () => {
      for (const tool of ["openai-sdk", "anthropic-sdk"] as const) {
        const m = /base_url=("(?:[^"\\]|\\.)*"),/.exec(snippetFor(tool, hostile, "dr_tok"))
        expect(JSON.parse(m?.[1] ?? "null")).toBe(hostile)
      }
    })
  })
})
