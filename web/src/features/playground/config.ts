import type { PlaygroundDialect } from "../../lib/api-types"

/**
 * The settings every playground surface sends, held once for all of them.
 *
 * They used to live inside the chat tab, which meant a system prompt typed
 * there was invisible to Compare and lost on a tab switch. They describe the
 * request rather than the surface — the same model, the same dialect, the same
 * ceiling — so they belong beside the tabs rather than inside one.
 */
export type PlaygroundConfig = {
  model: string
  dialect: PlaygroundDialect
  system: string
  stream: boolean
  /** Held as strings: an empty box and a zero are different settings, and a
   *  number state cannot hold both. */
  temperature: string
  maxTokens: string
  toolsRaw: string
}

export function emptyConfig(): PlaygroundConfig {
  return {
    model: "",
    dialect: "openai",
    system: "",
    stream: true,
    temperature: "",
    maxTokens: "",
    toolsRaw: "",
  }
}

export const DIALECTS: PlaygroundDialect[] = ["openai", "anthropic", "gemini"]
