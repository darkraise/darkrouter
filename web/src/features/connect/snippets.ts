export const TOOLS = [
  "claude-code",
  "codex",
  "cursor",
  "openai-sdk",
  "anthropic-sdk",
] as const

export type Tool = (typeof TOOLS)[number]

/**
 * The base URL each dialect is served at.
 *
 * Derived from the routes the proxy mux registers (internal/server/server.go)
 * rather than written as prose: Anthropic is served at the root because its
 * path is /v1/messages, while the OpenAI SDK appends its own
 * /chat/completions to a /v1 base, and Gemini's routes sit under /v1beta.
 */
export function baseUrlFor(
  origin: string,
  dialect: "openai" | "anthropic" | "gemini",
): string {
  const root = origin.replace(/\/+$/, "")
  switch (dialect) {
    case "openai":
      return `${root}/v1`
    case "gemini":
      return `${root}/v1beta`
    case "anthropic":
      return root
  }
}

// A plain URL or token stays bare so the usual snippet reads as typed; any
// other value is single-quoted, the one shell form in which no character but
// the quote itself is special.
function shellWord(value: string): string {
  if (/^[A-Za-z0-9_.:/@%+=,~-]+$/.test(value)) return value
  return `'${value.replace(/'/g, "'\\''")}'`
}

// A JSON string is also a valid Python string literal: both escape quotes,
// backslashes and control characters the same way.
function pythonString(value: string): string {
  return JSON.stringify(value)
}

export function snippetFor(tool: Tool, baseUrl: string, token: string): string {
  // A snippet ending in "=" is one an operator pastes and then debugs.
  const key = token || "<your-token>"
  switch (tool) {
    case "claude-code":
      return `export ANTHROPIC_BASE_URL=${shellWord(baseUrl)}
export ANTHROPIC_AUTH_TOKEN=${shellWord(key)}
claude`
    case "codex":
      return `export OPENAI_BASE_URL=${shellWord(baseUrl)}
export OPENAI_API_KEY=${shellWord(key)}
codex`
    case "cursor":
      return `Settings → Models → Override OpenAI Base URL
  ${baseUrl}
API key
  ${key}`
    case "openai-sdk":
      return `from openai import OpenAI

client = OpenAI(base_url=${pythonString(baseUrl)}, api_key=${pythonString(key)})`
    case "anthropic-sdk":
      return `from anthropic import Anthropic

client = Anthropic(base_url=${pythonString(baseUrl)}, auth_token=${pythonString(key)})`
  }
}
