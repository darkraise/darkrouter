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

export function snippetFor(tool: Tool, baseUrl: string, token: string): string {
  // A snippet ending in "=" is one an operator pastes and then debugs.
  const key = token || "<your-token>"
  switch (tool) {
    case "claude-code":
      return `export ANTHROPIC_BASE_URL=${baseUrl}
export ANTHROPIC_AUTH_TOKEN=${key}
claude`
    case "codex":
      return `export OPENAI_BASE_URL=${baseUrl}
export OPENAI_API_KEY=${key}
codex`
    case "cursor":
      return `Settings → Models → Override OpenAI Base URL
  ${baseUrl}
API key
  ${key}`
    case "openai-sdk":
      return `from openai import OpenAI

client = OpenAI(base_url="${baseUrl}", api_key="${key}")`
    case "anthropic-sdk":
      return `from anthropic import Anthropic

client = Anthropic(base_url="${baseUrl}", auth_token="${key}")`
  }
}
