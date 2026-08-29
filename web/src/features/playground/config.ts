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
  topP: string
  topK: string
  /** One sequence per line. A comma would be a character a sequence may contain. */
  stopRaw: string
  /** A JSON Schema object. Structured output is a schema, never a boolean:
   *  the OpenAI edge honours response_format only when a schema is present,
   *  so a bare "JSON mode" switch would be a control that does nothing. */
  schemaRaw: string
  /** "" | "low" | "medium" | "high" — OpenAI's spelling of reasoning. */
  reasoningEffort: string
  /** A token budget — Anthropic's and Gemini's spelling of the same idea. */
  reasoningBudget: string
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
    topP: "",
    topK: "",
    stopRaw: "",
    schemaRaw: "",
    reasoningEffort: "",
    reasoningBudget: "",
    toolsRaw: "",
  }
}

export const DIALECTS: PlaygroundDialect[] = ["openai", "anthropic", "gemini"]
