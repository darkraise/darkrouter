import { DIALECTS, type PlaygroundConfig } from "../config"
import { supports } from "../dialect-support"
import type {
  PlaygroundChatBody,
  PlaygroundDialect,
  PlaygroundMessage,
  RequestTrace,
} from "../../../lib/api-types"

/** The shared request settings plus this surface's own conversation. The
 *  settings live beside the tabs now; the turns belong to the chat. */
export type ChatState = PlaygroundConfig & {
  messages: PlaygroundMessage[]
}

export function parseTools(raw: string): { tools?: Record<string, unknown>[]; error?: string } {
  const trimmed = raw.trim()
  if (trimmed === "") return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch (err) {
    // Named rather than dropped: sending nothing would answer a different
    // question and read as the model ignoring the tools.
    return { error: `tools must be JSON: ${(err as Error).message}` }
  }
  if (!Array.isArray(parsed)) return { error: "tools must be a JSON array" }
  return { tools: parsed as Record<string, unknown>[] }
}

/** One stop sequence per line. Blank lines are not sequences. */
export function parseStopLines(raw: string): string[] {
  return raw
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "")
}

/** The structured-output schema, or the reason it could not be read. */
export function parseSchema(raw: string): { schema?: unknown; error?: string } {
  const trimmed = raw.trim()
  if (trimmed === "") return {}
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch (err) {
    return { error: `schema must be JSON: ${(err as Error).message}` }
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    return { error: "schema must be a JSON object" }
  }
  return { schema: parsed }
}

export function chatBody(state: ChatState): PlaygroundChatBody {
  const body: PlaygroundChatBody = {
    model: state.model,
    messages: state.messages,
    stream: state.stream,
    dialect: state.dialect,
  }
  if (state.system !== "") body.system = state.system
  if (state.temperature !== "") body.temperature = Number(state.temperature)
  if (state.maxTokens !== "") body.max_tokens = Number(state.maxTokens)
  // Dropped here rather than sent and ignored: a value the dialect's edge does
  // not parse never reaches the router, so putting it on the wire would make
  // the request body disagree with what actually happened.
  const d = state.dialect
  if (state.topP !== "" && supports(d, "topP")) body.top_p = Number(state.topP)
  if (state.topK !== "" && supports(d, "topK")) body.top_k = Number(state.topK)
  const stop = parseStopLines(state.stopRaw)
  if (stop.length > 0 && supports(d, "stop")) body.stop = stop
  const { schema } = parseSchema(state.schemaRaw)
  if (schema !== undefined && supports(d, "schema")) body.response_schema = schema
  if (state.reasoningEffort !== "" && supports(d, "reasoningEffort")) {
    body.reasoning_effort = state.reasoningEffort
  }
  if (state.reasoningBudget !== "" && supports(d, "reasoningBudget")) {
    body.reasoning_budget = Number(state.reasoningBudget)
  }
  const { tools } = parseTools(state.toolsRaw)
  if (tools) body.tools = tools
  return body
}

export function seedFromTrace(trace: RequestTrace): Partial<ChatState> {
  // The model the client asked for, not the one that served: replaying
  // against the serving provider would skip the routing decision, which is
  // usually the thing under investigation.
  const dialect = trace.dialect as PlaygroundDialect
  return {
    model: trace.alias || trace.model,
    // The log records inbound dialects this screen has no control for, the
    // OpenAI Responses wire among them.
    dialect: DIALECTS.includes(dialect) ? dialect : "openai",
  }
}
