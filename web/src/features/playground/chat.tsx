import { useEffect, useRef, useState } from "react"
import { Link, useSearch } from "@tanstack/react-router"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Textarea } from "darkraise-ui/components/textarea"
import { Switch } from "darkraise-ui/components/switch"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "darkraise-ui/components/select"
import { stream, type StreamStart } from "../../lib/api"
import { useTrace } from "../../lib/queries"
import type {
  PlaygroundChatBody,
  PlaygroundDialect,
  PlaygroundMessage,
  RequestTrace,
} from "../../lib/api-types"

/** Reads the assistant text out of whatever complete SSE frames have arrived. */
export function drainSSE(buffer: string): { text: string; rest: string } {
  let text = ""
  let rest = buffer
  for (;;) {
    const i = rest.indexOf("\n\n")
    if (i < 0) break
    const frame = rest.slice(0, i)
    rest = rest.slice(i + 2)
    for (const line of frame.split("\n")) {
      if (!line.startsWith("data: ")) continue
      const payload = line.slice(6)
      if (payload === "[DONE]") continue
      try {
        const obj = JSON.parse(payload) as {
          choices?: { delta?: { content?: string } }[]
        }
        const delta = obj?.choices?.[0]?.delta?.content
        if (typeof delta === "string") text += delta
      } catch {
        // A frame that is not JSON is a provider quirk, not a client error.
        // Skipping it beats aborting a stream that is otherwise fine.
      }
    }
  }
  return { text, rest }
}

export type ChatState = {
  model: string
  dialect: PlaygroundDialect
  system: string
  stream: boolean
  /** Held as strings: an empty box and a zero are different settings, and a
   *  number state cannot hold both. */
  temperature: string
  maxTokens: string
  toolsRaw: string
  messages: PlaygroundMessage[]
}

function emptyChatState(): ChatState {
  return {
    model: "",
    dialect: "openai",
    system: "",
    stream: true,
    temperature: "",
    maxTokens: "",
    toolsRaw: "",
    messages: [],
  }
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
  const { tools } = parseTools(state.toolsRaw)
  if (tools) body.tools = tools
  return body
}

const DIALECTS: PlaygroundDialect[] = ["openai", "anthropic", "gemini"]

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

export function Chat() {
  const [state, setState] = useState<ChatState>(emptyChatState)
  const [draft, setDraft] = useState("")
  const [requestId, setRequestId] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [seededFrom, setSeededFrom] = useState<string | undefined>(undefined)
  const buffer = useRef("")

  const search = useSearch({ strict: false })
  const seed = search.seed
  const trace = useTrace(seed ?? "", { enabled: seed !== undefined })

  // Applied once per seed: a seed that already produced this run's state
  // must not re-fire and stomp on turns the operator has since typed.
  useEffect(() => {
    if (!trace.data || seed === undefined || seededFrom === seed) return
    setState((s) => ({ ...s, ...seedFromTrace(trace.data as RequestTrace) }))
    setSeededFrom(seed)
  }, [trace.data, seed, seededFrom])

  const toolsError = parseTools(state.toolsRaw).error

  async function send() {
    if (busy || state.model === "" || draft === "" || toolsError !== undefined) return
    const turns = [...state.messages, { role: "user", content: draft } satisfies PlaygroundMessage]
    setState((s) => ({
      ...s,
      messages: [...turns, { role: "assistant", content: "" }],
    }))
    setDraft("")
    setError("")
    setRequestId("")
    setBusy(true)
    buffer.current = ""
    try {
      for await (const chunk of stream(
        "/api/playground",
        chatBody({ ...state, messages: turns }),
        // The id arrives with the headers, before the body this is rendering.
        (s: StreamStart) => setRequestId(s.requestId),
      )) {
        buffer.current += chunk
        const { text, rest } = drainSSE(buffer.current)
        buffer.current = rest
        if (text) {
          setState((s) => {
            const messages = s.messages.slice()
            const lastIndex = messages.length - 1
            const last = messages[lastIndex]
            if (!last) return s
            messages[lastIndex] = { ...last, content: last.content + text }
            return { ...s, messages }
          })
        }
      }
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="grid gap-4 sm:grid-cols-2">
        <Input
          placeholder="alias or provider/model"
          value={state.model}
          onChange={(e) => setState((s) => ({ ...s, model: e.target.value }))}
        />
        <Select
          value={state.dialect}
          onValueChange={(v) => setState((s) => ({ ...s, dialect: v as PlaygroundDialect }))}
        >
          <SelectTrigger>
            <SelectValue placeholder="dialect" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="openai">openai</SelectItem>
            <SelectItem value="anthropic">anthropic</SelectItem>
            <SelectItem value="gemini">gemini</SelectItem>
          </SelectContent>
        </Select>
        <Input
          type="number"
          placeholder="temperature"
          value={state.temperature}
          onChange={(e) => setState((s) => ({ ...s, temperature: e.target.value }))}
        />
        <Input
          type="number"
          placeholder="max tokens"
          value={state.maxTokens}
          onChange={(e) => setState((s) => ({ ...s, maxTokens: e.target.value }))}
        />
      </div>
      <Textarea
        placeholder="System prompt"
        value={state.system}
        onChange={(e) => setState((s) => ({ ...s, system: e.target.value }))}
      />
      <div>
        <Textarea
          placeholder='Tools, as a JSON array (e.g. [{"type":"function",...}])'
          value={state.toolsRaw}
          onChange={(e) => setState((s) => ({ ...s, toolsRaw: e.target.value }))}
        />
        {toolsError ? <p className="text-destructive mt-1 text-sm">{toolsError}</p> : null}
      </div>
      <label className="flex items-center gap-2 text-sm">
        <Switch
          checked={state.stream}
          onCheckedChange={(checked) => setState((s) => ({ ...s, stream: checked }))}
        />
        Stream
      </label>

      {seed !== undefined ? (
        // capture.bodies has a retention sweep and no writer, so a trace
        // carries no prompt text — the model and dialect are all a seeded
        // run can restore. Stated here rather than left for the operator to
        // discover from a transcript that is silently empty. The three
        // states are worded separately so the note stays true while the
        // trace is still loading or failed to load.
        <p className="text-sm text-[hsl(var(--muted-foreground))]">
          {seededFrom === seed
            ? `Seeded from trace ${seed}: model and dialect carried over. The original prompt was not retained and is not recoverable.`
            : trace.isError
              ? `Trace ${seed} could not be loaded, so nothing was seeded.`
              : `Loading trace ${seed}…`}
        </p>
      ) : null}

      <div className="flex flex-col gap-3">
        {state.messages.map((m, i) => (
          <Card key={i} className="p-3 text-sm">
            <p className="mb-1 font-medium">{m.role}</p>
            <p className="font-mono whitespace-pre-wrap">{m.content}</p>
          </Card>
        ))}
      </div>

      <div className="flex gap-2">
        <Input
          placeholder="Message"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
        />
        <Button
          onClick={() => void send()}
          disabled={busy || state.model === "" || draft === "" || toolsError !== undefined}
        >
          {busy ? "Streaming…" : "Send"}
        </Button>
      </div>
      {error ? <p className="text-destructive text-sm">{error}</p> : null}
      {requestId ? (
        // Spec §6: "follow a link to the trace it produced". Verifying a
        // credential means seeing which provider actually served.
        <Link to="/requests/$id" params={{ id: requestId }} className="text-sm underline">
          View the trace for this request
        </Link>
      ) : null}
    </div>
  )
}
