import type { PlaygroundDialect } from "../../lib/api-types"

/**
 * Which sampling controls each inbound dialect can actually carry.
 *
 * The binding constraint is not what the provider's API accepts — it is what
 * darkrouter's own edge parses into ir.Request. A field the edge does not read
 * is dropped before routing begins, so a control that sends it would accept a
 * value, report nothing, and change nothing.
 *
 * Verified against internal/edge/{openai,anthropic,gemini}/parse.go rather
 * than against provider documentation, which disagrees with the edges in
 * several places.
 */
export type Control =
  | "temperature"
  | "maxTokens"
  | "topP"
  | "topK"
  | "stop"
  | "schema"
  | "reasoningEffort"
  | "reasoningBudget"

/** In the order the pane shows them. */
export const CONTROLS: Control[] = [
  "temperature",
  "maxTokens",
  "topP",
  "topK",
  "stop",
  "schema",
  "reasoningEffort",
  "reasoningBudget",
]

/** null means the dialect carries it. A string is the reason it does not. */
const UNSUPPORTED: Record<PlaygroundDialect, Partial<Record<Control, string>>> = {
  openai: {
    topK: "The OpenAI chat wire has no top_k field. Switch the dialect to anthropic or gemini to send it.",
    reasoningBudget:
      "OpenAI takes a reasoning effort tier rather than a token budget. Use Effort, or switch to anthropic or gemini to set a budget.",
  },
  anthropic: {
    schema:
      "Darkrouter's Anthropic edge does not read response_format, so a schema sent here would be dropped before routing. Switch to openai or gemini.",
    reasoningEffort:
      "Anthropic takes a thinking budget in tokens rather than an effort tier. Use Budget, or switch to openai to set an effort.",
  },
  gemini: {
    reasoningEffort:
      "Gemini takes a thinking budget in tokens rather than an effort tier. Use Budget, or switch to openai to set an effort.",
  },
}

export function reasonFor(dialect: PlaygroundDialect, control: Control): string | null {
  return UNSUPPORTED[dialect]?.[control] ?? null
}

export function supports(dialect: PlaygroundDialect, control: Control): boolean {
  return reasonFor(dialect, control) === null
}
