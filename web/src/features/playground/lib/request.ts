import { DIALECTS, type PlaygroundConfig } from "../config"
import { reasonFor, supports } from "../dialect-support"
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

/**
 * Why this request must not be sent, if anything.
 *
 * chatBody drops a field it cannot parse rather than send it broken, so a
 * request sent anyway asks a different question from the one the pane shows.
 * One check for every surface that sends, so Chat and Compare cannot disagree
 * about what is sendable. A schema the dialect never carries is not checked:
 * the pane has already said it will not be sent.
 */
export function requestProblem(config: PlaygroundConfig): string | undefined {
  const tools = toolsProblem(config)
  if (tools !== undefined) return tools
  if (supports(config.dialect, "schema")) return parseSchema(config.schemaRaw).error
  return undefined
}

/** Why the Tools field cannot be sent as it stands, if anything. Its own
 *  export so the pane can say it under the field as well as beside Send. */
export function toolsProblem(config: Pick<PlaygroundConfig, "toolsRaw" | "dialect">): string | undefined {
  const { tools, error } = parseTools(config.toolsRaw)
  if (error !== undefined) return error
  if (tools === undefined || tools.length === 0) return undefined
  // Unlike the sampling controls, tools are not dropped quietly: the gemini
  // path refuses the whole request, so this says so before Send rather than
  // after a 400.
  const refused = reasonFor(config.dialect, "tools")
  if (refused !== null) return refused
  return toolShapeProblem(config.dialect, tools)
}

/**
 * Why a tool written for one dialect would not survive another's edge.
 *
 * Each edge reads only its own shape. The OpenAI edge takes a tool's name and
 * schema from `function`, so an Anthropic-shaped entry arrives nameless; the
 * Anthropic edge reads a `type` it does not know as a provider-run tool and
 * carries it whole, and an OpenAI target then drops it with a warning under
 * the answer. Either way the request succeeds without the tool, which reads
 * as the model ignoring it.
 */
function toolShapeProblem(
  dialect: PlaygroundDialect,
  tools: Record<string, unknown>[],
): string | undefined {
  const at = tools.findIndex((t) =>
    dialect === "anthropic"
      ? t.type === "function" || typeof t.function === "object"
      : dialect === "openai"
        ? typeof t.function !== "object" || t.function === null
        : false,
  )
  if (at < 0) return undefined
  return dialect === "anthropic"
    ? `tool ${at + 1} is in the OpenAI shape; the anthropic dialect takes {"name": …, "input_schema": {…}} and would drop it`
    : `tool ${at + 1} is not in the OpenAI shape; the openai dialect takes {"type": "function", "function": {"name": …, "parameters": {…}}}`
}

/** What the Tools field shows as an example, in the shape the dialect reads. */
export const TOOLS_PLACEHOLDER: Record<PlaygroundDialect, string> = {
  openai: 'JSON array, e.g. [{"type":"function","function":{"name":"lookup","parameters":{…}}}]',
  anthropic: 'JSON array, e.g. [{"name":"lookup","input_schema":{"type":"object",…}}]',
  gemini: "Not sent on the gemini dialect",
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
