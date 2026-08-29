import { useEffect, useRef, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import {
  CircleCheck,
  CircleSlash,
  CircleX,
  Eraser,
  MessagesSquare,
  Play,
  Square,
} from "lucide-react"
import {
  Badge,
  Button,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Textarea,
} from "darkraise-ui"
import { api, stream } from "../../lib/api"
import { keys, useModels } from "../../lib/queries"
import { drainSSE } from "../playground/chat"
import {
  MetricsStrip,
  NO_METRICS,
  metricsFromTrace,
  traceWhenWritten,
  type StreamMetrics,
} from "../playground/metrics"
import { ModelCombobox } from "../shell/model-combobox"
import { TestLogTab } from "./test-log-tab"
import { modelsFor } from "./provider-stats"
import { ProviderIcon } from "./provider-icon"
import type { ProviderRow } from "./provider-rows"

/** One line of the run log. `at` is wall-clock because the log's whole job is
 *  to say when, and a monotonic clock has no reading an operator can compare
 *  against the requests screen. */
export type LogLine = { at: number; level: "info" | "error"; text: string }

export function logLine(level: LogLine["level"], text: string): LogLine {
  return { at: Date.now(), level, text }
}

/** One turn of the conversation. `model` is carried on the assistant turn
 *  because a chat can change model between turns, and "which model said this"
 *  is the question a test drawer exists to answer. */
export type Turn = {
  role: "user" | "assistant"
  content: string
  model?: string
  failed?: boolean
}

/** The message the composer opens with. A test tool that made an operator
 *  think of a prompt before it would prove anything is one they will not
 *  reach for. */
const FIRST_PROBE = "Reply with the single word: ok"

/** What the last run proved. `idle` is not a result — it is the absence of
 *  one, and saying so beats an empty panel that reads as a failure. */
export type Verdict =
  | { kind: "idle" }
  | { kind: "running" }
  | { kind: "served"; totalMs: number }
  | { kind: "refused"; reason: string }

/**
 * The one sentence the drawer exists to produce.
 *
 * An operator opens this to answer "is this provider working", and the old
 * layout made them read a log to find out. The verdict is the headline; the
 * transcript and the timings are the evidence behind it.
 */
function VerdictLine({ verdict }: { verdict: Verdict }) {
  if (verdict.kind === "idle") {
    return (
      <p className="flex items-center gap-2 text-sm text-[hsl(var(--muted-foreground))]">
        <CircleSlash className="size-[var(--icon-size)]" aria-hidden="true" />
        Not tested yet
      </p>
    )
  }
  if (verdict.kind === "running") {
    return (
      <p className="flex items-center gap-2 text-sm text-[hsl(var(--muted-foreground))]">
        <span className="size-[var(--icon-size)] animate-pulse rounded-full bg-[hsl(var(--muted-foreground))]" />
        Waiting for the provider…
      </p>
    )
  }
  if (verdict.kind === "served") {
    return (
      <p className="flex items-center gap-2 text-sm font-medium text-[hsl(var(--success))]">
        <CircleCheck className="size-[var(--icon-size)]" aria-hidden="true" />
        Served in {verdict.totalMs >= 1000
          ? `${(verdict.totalMs / 1000).toFixed(1)} s`
          : `${Math.round(verdict.totalMs)} ms`}
      </p>
    )
  }
  return (
    // The reason, not just the fact: "refused" sends an operator to the log,
    // and the log's first useful line is the one already in hand.
    <p className="flex items-start gap-2 text-sm font-medium text-[hsl(var(--destructive))]">
      <CircleX className="mt-0.5 size-[var(--icon-size)] shrink-0" aria-hidden="true" />
      <span className="font-normal">{verdict.reason}</span>
    </p>
  )
}

/**
 * One turn, as a chat draws it.
 *
 * Sides, not rows: the reader's own words on the right in a filled bubble,
 * the provider's on the left under its own mark. Alignment is what makes a
 * transcript legible at a glance — who said what is answered by where it sits,
 * before a single word is read — and it is the thing a list of labelled
 * paragraphs cannot do however neatly it is ruled.
 */
function Bubble({
  turn,
  row,
  streaming,
}: {
  turn: Turn
  row: ProviderRow | null
  streaming: boolean
}) {
  const isUser = turn.role === "user"
  if (isUser) {
    return (
      <div className="flex justify-end">
        <div className="max-w-[80%] rounded-[var(--radius)] rounded-br-sm bg-[hsl(var(--primary))] px-3 py-2 text-sm whitespace-pre-wrap break-words text-[hsl(var(--primary-foreground))]">
          {turn.content}
        </div>
      </div>
    )
  }
  return (
    <div className="flex items-start gap-2">
      <span className="mt-1 shrink-0" aria-hidden="true">
        {turn.failed ? (
          <span className="flex size-6 items-center justify-center rounded-[6px] border text-[hsl(var(--destructive))]">
            <CircleX className="size-[var(--icon-size,1rem)]" />
          </span>
        ) : (
          // The provider's own mark. This drawer is about one provider and the
          // console already knows its face; a generic robot would say less.
          row && <ProviderIcon preset={row.preset} id={row.id} name={row.name} size={24} />
        )}
      </span>
      <div className="flex min-w-0 max-w-[80%] flex-col gap-1">
        <div
          className={
            turn.failed
              ? "rounded-[var(--radius)] rounded-bl-sm border border-[hsl(var(--destructive))] px-3 py-2 text-sm whitespace-pre-wrap break-words text-[hsl(var(--destructive))]"
              : "rounded-[var(--radius)] rounded-bl-sm border bg-[hsl(var(--muted))] px-3 py-2 text-sm whitespace-pre-wrap break-words"
          }
        >
          {turn.content}
          {streaming && (
            // The caret is the difference between "thinking" and "stopped".
            <span className="ml-0.5 inline-block h-4 w-1.5 animate-pulse bg-[hsl(var(--foreground))] align-text-bottom" />
          )}
        </div>
        {turn.model && !turn.failed && (
          // Under the bubble, not in it: which model answered is a footnote to
          // the answer, and inside the bubble it competes with the words.
          <span className="truncate px-1 font-mono text-sm text-[hsl(var(--legend))]" title={turn.model}>
            {turn.model}
          </span>
        )}
      </div>
    </div>
  )
}

/**
 * Is this provider answering, right now, with the models it claims?
 *
 * The probe button asks whether the credential is accepted. This asks the
 * question after it: does a real completion come back, from the model an
 * operator picked, through the whole executor. It goes through
 * `/api/playground` rather than a mock for the same reason the playground
 * does — a mock would pass while the thing it exists to test is broken.
 *
 * Two tabs because there are two questions and they want different shapes: the
 * reply is prose to read, and the run is a sequence to scan.
 */
export function TestDrawer({
  row,
  open,
  onOpenChange,
}: {
  row: ProviderRow | null
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const catalog = useModels()
  const queryClient = useQueryClient()
  const [model, setModel] = useState("")
  // Prefilled, and cleared on send like any composer. This is a test tool
  // before it is a chat: one click should prove the provider answers without
  // an operator having to invent a prompt first.
  const [draft, setDraft] = useState(FIRST_PROBE)
  const [messages, setMessages] = useState<Turn[]>([])
  const [log, setLog] = useState<LogLine[]>([])
  const [metrics, setMetrics] = useState<StreamMetrics>(NO_METRICS)
  const [verdict, setVerdict] = useState<Verdict>({ kind: "idle" })
  const [running, setRunning] = useState(false)
  const [tab, setTab] = useState("chat")
  const abort = useRef(false)
  const transcript = useRef<HTMLDivElement>(null)

  // A functional update: a stream appends many times inside one render, and a
  // version that read the turns this render closed over would append every
  // chunk to the same stale array.
  function appendToOpenTurn(text: string) {
    setMessages((prev) => {
      const next = prev.slice()
      const lastIndex = next.length - 1
      const last = next[lastIndex]
      if (!last) return prev
      next[lastIndex] = { ...last, content: last.content + text }
      return next
    })
  }

  // Follows the reply as it arrives, which is what makes a transcript feel
  // live rather than something to scroll after the fact.
  useEffect(() => {
    const el = transcript.current
    if (el) el.scrollTop = el.scrollHeight
  }, [messages])

  const models = row ? modelsFor(catalog.data?.models ?? [], row.id) : []
  // Provider-qualified, so the router cannot satisfy the request from some
  // other provider that happens to serve the same model name. Testing groq
  // and being answered by cerebras would be the one outcome this drawer must
  // never produce.
  const target = model ? `${row?.id}/${model}` : ""

  async function run() {
    const prompt = draft.trim()
    if (!row || !target || !prompt || running) return
    abort.current = false
    setRunning(true)
    setDraft("")
    setMetrics(NO_METRICS)
    setVerdict({ kind: "running" })
    setTab("chat")

    // The turns the provider will see, and the empty one it is about to fill.
    // History goes with the request: a second question that could not refer to
    // the first would not be a conversation.
    const history: Turn[] = [...messages, { role: "user", content: prompt }]
    setMessages([...history, { role: "assistant", content: "", model }])

    const started = performance.now()
    const at = () => `${Math.round(performance.now() - started)} ms`
    const lines: LogLine[] = []
    const say = (level: LogLine["level"], text: string) => {
      lines.push(logLine(level, text))
      setLog([...lines])
    }

    // A keyless provider can be tested with nothing set up, but the router
    // still walks a database row: it holds the priority and the enabled flag,
    // and a preset alone is not something a request can be routed to. Creating
    // it here is the setup, done in the one click that was going to happen
    // anyway rather than as a step in front of it.
    if (row.keyless && !row.provider) {
      try {
        await api.post("/api/providers", { id: row.id, preset: row.preset || row.id })
        say("info", `added ${row.name} to your providers`)
        void queryClient.invalidateQueries({ queryKey: keys.providers })
      } catch (err) {
        const reason = err instanceof Error ? err.message : "could not add the provider"
        say("error", reason)
        setVerdict({ kind: "refused", reason })
        setRunning(false)
        return
      }
    }
    say("info", `POST /api/playground → ${target}`)

    let firstToken = 0
    let liveRequestId = ""
    try {
      let buffer = ""
      for await (const chunk of stream(
        "/api/playground",
        {
          model: target,
          messages: history.map((t) => ({ role: t.role, content: t.content })),
          stream: true,
          dialect: "openai",
        },
        (s) => {
          liveRequestId = s.requestId
          say("info", `request ${s.requestId} · headers at ${at()}`)
        },
      )) {
        if (abort.current) break
        buffer += chunk
        const { text, rest } = drainSSE(buffer, "openai")
        buffer = rest
        if (text) {
          if (firstToken === 0) {
            firstToken = performance.now()
            say("info", `first token at ${at()}`)
          }
          appendToOpenTurn(text)
        }
      }
      const totalMs = performance.now() - started
      say("info", abort.current ? `stopped at ${at()}` : `complete in ${at()}`)
      const measured: StreamMetrics = {
        ...NO_METRICS,
        ttftMs: firstToken === 0 ? null : firstToken - started,
        totalMs,
      }
      setMetrics(measured)
      setVerdict({ kind: "served", totalMs })
      if (liveRequestId) {
        const trace = await traceWhenWritten(liveRequestId)
        if (trace) setMetrics(metricsFromTrace(measured, trace))
      }
    } catch (err) {
      // The failure is the answer here as often as the reply is: a refused
      // credential, a model the provider does not serve, a base URL that is a
      // web page. The verdict says which without opening the log.
      const reason = err instanceof Error ? err.message : "the request failed"
      say("error", reason)
      setVerdict({ kind: "refused", reason })
      // In the transcript too: the conversation is where an operator is
      // looking, and a turn that simply never arrives reads as a hang.
      setMessages((prev) => {
        const next = prev.slice()
        const last = next[next.length - 1]
        if (last && last.role === "assistant" && last.content === "") {
          next[next.length - 1] = { ...last, content: reason, failed: true }
        }
        return next
      })
    } finally {
      setRunning(false)
    }
  }

  const failed = log.some((l) => l.level === "error")

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex w-full flex-col gap-0 p-0 sm:max-w-xl">
        <SheetHeader className="border-b p-4">
          <SheetTitle className="flex items-center gap-2">
            {row && (
              <ProviderIcon preset={row.preset} id={row.id} name={row.name} size={24} />
            )}
            Test {row?.name}
            {row?.keyless && <Badge variant="outline">no auth</Badge>}
          </SheetTitle>
        </SheetHeader>

        {/* Ask, then answer. The controls are a block at the top rather than a
            column down the panel, so the result has the room — an operator
            reads this far more often than they retype the question. */}
        <div className="flex flex-col gap-3 border-b p-4">
          <ModelCombobox
            label="Model"
            value={model}
            onChange={setModel}
            candidates={models.map((m) => m.model)}
            placeholder={models.length ? "pick or type a model" : "type a model id"}
          />
          <div className="flex items-center gap-3">
            <VerdictLine verdict={verdict} />
            {messages.length > 0 && !running && (
              <Button
                size="sm"
                variant="ghost"
                className="ml-auto"
                onClick={() => {
                  setMessages([])
                  setVerdict({ kind: "idle" })
                  setMetrics(NO_METRICS)
                  setDraft(FIRST_PROBE)
                }}
              >
                <Eraser className="size-[var(--icon-size)]" />
                Clear
              </Button>
            )}
          </div>
          {row?.keyless && !row.provider && (
            <p className="text-sm text-[hsl(var(--muted-foreground))]">
              {row.name} asks for no credential. Sending adds it to your providers and
              routes through it — nothing else to set up.
            </p>
          )}
        </div>

        {/* The same readings the playground shows, from the same component: a
            latency that means one thing on one screen and another elsewhere is
            two vocabularies for one fact. */}
        <MetricsStrip metrics={metrics} />

        <Tabs value={tab} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col">
          <TabsList className="mx-4 mt-3 w-fit">
            <TabsTrigger value="chat">Chat</TabsTrigger>
            <TabsTrigger value="log">
              Log
              {failed && (
                <Badge variant="destructive" className="ml-1.5">
                  !
                </Badge>
              )}
            </TabsTrigger>
          </TabsList>

          <TabsContent
            value="chat"
            className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
          >
            <div ref={transcript} className="min-h-0 flex-1 overflow-y-auto">
              {messages.length === 0 ? (
                <div className="flex h-full flex-col items-center justify-center gap-2 p-8 text-center">
                  <MessagesSquare
                    className="size-8 text-[hsl(var(--legend))]"
                    aria-hidden="true"
                  />
                  <p className="text-sm font-medium">No messages yet</p>
                  <p className="max-w-prose text-sm text-[hsl(var(--muted-foreground))]">
                    Send one and it goes through the router to {row?.name}, exactly as a
                    client's would. Ask again to keep the conversation going.
                  </p>
                </div>
              ) : (
                <div className="flex flex-col gap-3 p-4">
                  {messages.map((turn, i) => (
                    <Bubble
                      key={i}
                      turn={turn}
                      row={row}
                      streaming={running && i === messages.length - 1 && turn.role === "assistant"}
                    />
                  ))}
                </div>
              )}
            </div>

            {/* The composer sits at the bottom, where a chat's does. Enter
                sends and Shift+Enter breaks the line — the convention every
                other chat surface an operator uses already follows. */}
            <div className="flex items-end gap-2 border-t p-3">
              <Textarea
                rows={2}
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key !== "Enter" || e.shiftKey) return
                  e.preventDefault()
                  void run()
                }}
                placeholder={target ? "Send a message" : "Pick a model first"}
                aria-label="Test message"
                className="max-h-32 min-h-0 flex-1 resize-none"
              />
              {running ? (
                <Button variant="secondary" onClick={() => (abort.current = true)}>
                  <Square className="size-[var(--icon-size)]" />
                  Stop
                </Button>
              ) : (
                <Button disabled={!target || draft.trim() === ""} onClick={() => void run()}>
                  <Play className="size-[var(--icon-size)]" />
                  Send
                </Button>
              )}
            </div>
          </TabsContent>

          <TabsContent
            value="log"
            className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"
          >
            {row && <TestLogTab providerId={row.id} />}
          </TabsContent>
        </Tabs>
      </SheetContent>
    </Sheet>
  )
}
