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

/**
 * Every cell, stated. null means the dialect carries it; a string is the
 * reason it does not.
 *
 * Total rather than partial on both axes, so the compiler refuses a new
 * control until someone has decided it for all three dialects. The omitted
 * cell would otherwise default to supported, which is the one direction that
 * fails silently: an unsupported value the pane enables is dropped by the edge
 * with no error, and nothing surfaces until a real provider call.
 */
const REASONS: Record<PlaygroundDialect, Record<Control, string | null>> = {
  openai: {
    temperature: null,
    maxTokens: null,
    topP: null,
    topK: "The OpenAI chat wire has no top_k field. Switch the dialect to anthropic or gemini to send it.",
    stop: null,
    schema: null,
    reasoningEffort: null,
    reasoningBudget:
      "OpenAI takes a reasoning effort tier rather than a token budget. Use Effort, or switch to anthropic or gemini to set a budget.",
  },
  anthropic: {
    temperature: null,
    maxTokens: null,
    topP: null,
    topK: null,
    stop: null,
    schema:
      "Darkrouter's Anthropic edge does not read response_format, so a schema sent here would be dropped before routing. Switch to openai or gemini.",
    reasoningEffort:
      "Anthropic takes a thinking budget in tokens rather than an effort tier. Use Budget, or switch to openai to set an effort.",
    reasoningBudget: null,
  },
  gemini: {
    temperature: null,
    maxTokens: null,
    topP: null,
    topK: null,
    stop: null,
    schema: null,
    reasoningEffort:
      "Gemini takes a thinking budget in tokens rather than an effort tier. Use Budget, or switch to openai to set an effort.",
    reasoningBudget: null,
  },
}

/** No fallback for an unknown dialect: the types make one unreachable, and a
 *  lookup that quietly answered "supported" would reinstate exactly the silent
 *  default this table exists to remove. */
export function reasonFor(dialect: PlaygroundDialect, control: Control): string | null {
  return REASONS[dialect][control]
}

export function supports(dialect: PlaygroundDialect, control: Control): boolean {
  return reasonFor(dialect, control) === null
}
