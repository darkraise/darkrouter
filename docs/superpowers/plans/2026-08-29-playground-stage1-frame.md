# Playground Stage 1 — The Screen Fills Its Frame

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the playground occupy the viewport it is given, fix the scroll-follow guard that has never fired, and break `chat.tsx` into the pieces every later stage builds on — adding no new capability.

**Architecture:** `PlaygroundScreen` stops being a document that flows and becomes a full-height flex column, with scrolling moved from `main` into the transcript. `chat.tsx` (451 lines doing four jobs) is dissolved into three pure modules under `playground/lib/` and two components beside it, with the streaming send loop becoming a hook that every sending surface shares. No component gains a feature; every extraction moves code verbatim except where the scroll fix requires otherwise.

**Tech Stack:** React 19, TanStack Router + Query, darkraise-ui 6.5.0, Tailwind 4, vitest 4 + @testing-library/react, jsdom.

**Spec:** `docs/superpowers/specs/2026-08-29-playground-overhaul-design.md` — §3 (stage 1), §5 (layout mechanics), §6 (what survives), §10 (files), §12 (testing), §13 (the adjacent bug).

## Global Constraints

These apply to every task without restating.

- **TDD.** A failing test precedes the implementation it tests. Run it and see it fail before writing the code.
- **Gates before any commit.** `cd web && npm test && npm run typecheck` clean. Task 7 additionally runs `npm run build`.
- **Extractions move code verbatim.** Tasks 2–4 are cut-and-paste plus import rewriting. If a moved function needs an edit to compile, that edit is a path or a type import — never a change in behaviour. The one deliberate behavioural change in this plan is Task 5.
- **Existing test assertions do not change.** `chat.test.ts`, `metrics.test.ts`, `message.test.tsx`, `markdown.test.tsx` and `aux-panels.test.ts` keep every assertion they have. Import lines may change; nothing else may. An assertion that needs an edit means behaviour was lost — stop and report rather than editing it.
- **Never `text-xs`, never a custom size.** 14px (`text-sm`) is the floor; only `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. In a stylesheet use `var(--text-sm)`, never a pixel value. Hierarchy below body text comes from colour (`--legend`, `--muted-foreground`) and weight.
- **Comments explain WHY, never WHAT.** No comment may reference this plan, a task number, or the fact that something was recently added or moved. Existing comments move with their code unchanged — several encode past bugs.
- **Commit subjects** are `<type>(<scope>): <subject>`, imperative, 50 characters or fewer, no trailing period. Stage explicit paths — never `git add -A`. English only.
- **Branch.** All work lands on `playground-overhaul`, which exists and currently holds the three spec commits. Check it out before Task 1.
- **No new dependencies.**

## Definition of Done

| # | Criterion | Verification |
|---|---|---|
| D2 | `chat.tsx` no longer exists and nothing imports it | `rg 'playground/chat"\|from "\./chat"' web/src` returns nothing |
| D3 | Every assertion in the five existing suites still passes | `cd web && npm test` |
| D4 | Streaming does not yank a reader back to the bottom | `cd web && npm test -- transcript` |
| D5 | The playground fills the viewport with no dead band | UAT at 1600×1000: the composer sits at the foot of the frame, the config pane border runs full height |
| D6 | The metrics strip does not shift the transcript when it appears | UAT: send a prompt; the transcript does not jump when the first reading lands |
| D7 | Deployed and serving the build under test | `docker ps` healthy, `/healthz` 200, and the served bundle byte-matches the local build per `CLAUDE.md` |

---

### Task 1: Pin the threads pool — DROPPED, do not perform

The premise was false. This task existed because the spec claimed the default fork pool silently
skips part of the console suite. Measured on 2026-08-29 at the lockfile's vitest 4.1.11, `npx vitest
run --pool=forks` and `--pool=threads` both run **51 files and 517 tests, all passing**. Pinning a
pool would add configuration that changes nothing, carrying a comment asserting something untrue.

Spec §12 is corrected to record the measurement, so the claim is not re-derived the next time a
suite looks short. D1 is void and removed from the Definition of Done. Task numbering is left alone
so earlier references stay valid.

---

### Task 2: Extract the pure functions

Five exported functions leave `chat.tsx` unchanged. Their existing tests are the gate: if all 23 still pass with only an import line edited, the move was clean.

**Files:**
- Create: `web/src/features/playground/lib/stream.ts`
- Create: `web/src/features/playground/lib/request.ts`
- Modify: `web/src/features/playground/chat.tsx` (delete the moved code, import it back)
- Modify: `web/src/features/playground/chat.test.ts:2` (import line only)
- Modify: `web/src/features/playground/config-pane.tsx:15` (import line only)
- Modify: `web/src/features/playground/compare.tsx:7` (import line only)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `lib/stream.ts`: `drainSSE(buffer: string, dialect?: PlaygroundDialect): { text: string; rest: string }`, `extractUnaryText(dialect: PlaygroundDialect, body: string): string`
  - `lib/request.ts`: `type ChatState = PlaygroundConfig & { messages: PlaygroundMessage[] }`, `parseTools(raw: string): { tools?: Record<string, unknown>[]; error?: string }`, `chatBody(state: ChatState): PlaygroundChatBody`, `seedFromTrace(trace: RequestTrace): Partial<ChatState>`

- [ ] **Step 1: Point the existing test at the modules that do not exist yet**

Replace line 2 of `web/src/features/playground/chat.test.ts`:

```ts
import { drainSSE, extractUnaryText } from "./lib/stream"
import { parseTools, chatBody, seedFromTrace } from "./lib/request"
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- chat.test
```

Expected: FAIL — cannot resolve `./lib/stream` and `./lib/request`.

- [ ] **Step 3: Create `lib/stream.ts`**

Move `chat.tsx` lines 20–129 verbatim — the three stream extractors, `STREAM_DELTA`, `drainSSE`, the three unary extractors, `UNARY_TEXT`, `extractUnaryText` — with every comment intact. Only the import path changes:

```ts
import type { PlaygroundDialect } from "../../../lib/api-types"
```

The file keeps the header comment that sits above `openaiStreamDelta` in `chat.tsx`:

```ts
// One extractor per dialect's streamed wire shape, verified against the edge
// writers rather than guessed: internal/edge/{openai,anthropic,gemini}/stream.go.
```

- [ ] **Step 4: Create `lib/request.ts`**

Move `ChatState` (chat.tsx:131), `parseTools` (135), `chatBody` (150) and `seedFromTrace` (165) verbatim, comments intact. Imports:

```ts
import { DIALECTS, type PlaygroundConfig } from "../config"
import type {
  PlaygroundChatBody,
  PlaygroundDialect,
  PlaygroundMessage,
  RequestTrace,
} from "../../../lib/api-types"
```

- [ ] **Step 5: Delete the moved code from `chat.tsx` and import it back**

Remove lines 20–176 (through `seedFromTrace`). Add to the import block:

```ts
import { drainSSE, extractUnaryText } from "./lib/stream"
import { chatBody, parseTools, seedFromTrace, type ChatState } from "./lib/request"
```

Drop any import in `chat.tsx` that is now unused — `PlaygroundChatBody` and `DIALECTS` among them. `npm run typecheck` names them if one is missed.

- [ ] **Step 6: Repoint the two other importers**

`config-pane.tsx` line 15: `import { parseTools } from "./chat"` → `from "./lib/request"`.

`compare.tsx` line 7: `import { chatBody, drainSSE } from "./chat"` → two lines:

```ts
import { chatBody } from "./lib/request"
import { drainSSE } from "./lib/stream"
```

- [ ] **Step 7: Run the full suite**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS, all 23 assertions in `chat.test.ts` among them, with no assertion edited.

- [ ] **Step 8: Commit**

```bash
git add web/src/features/playground/lib/stream.ts \
        web/src/features/playground/lib/request.ts \
        web/src/features/playground/chat.tsx \
        web/src/features/playground/chat.test.ts \
        web/src/features/playground/config-pane.tsx \
        web/src/features/playground/compare.tsx
git commit -m "refactor(web): split the playground wire helpers out"
```

---

### Task 3: Extract the send loop into a hook

`send()`, `appendToLastMessage`, the abort controller, the TTFT measurement and the `traceWhenWritten` follow-up are needed identically by every sending surface. They become `useChatRun`. This is the task most likely to lose behaviour, so it gets the most test.

**Files:**
- Create: `web/src/features/playground/lib/use-chat-run.ts`
- Create: `web/src/features/playground/lib/use-chat-run.test.tsx`
- Modify: `web/src/features/playground/chat.tsx`

**Interfaces:**
- Consumes: `chatBody`, `parseTools` from `lib/request`; `drainSSE`, `extractUnaryText` from `lib/stream`; `NO_METRICS`, `metricsFromTrace`, `traceWhenWritten` from `../metrics`; `routeFromTrace` from `../message`.
- Produces:

```ts
export type ChatRun = {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  error: string
  send: (prompt: string) => Promise<void>
  stop: () => void
  clear: () => void
}

export function useChatRun(
  config: PlaygroundConfig,
  onMetrics: (m: StreamMetrics) => void,
): ChatRun
```

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/lib/use-chat-run.test.tsx`:

```tsx
import { act, renderHook, waitFor } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { useChatRun } from "./use-chat-run"
import { emptyConfig } from "../config"

const frame = (text: string) =>
  `data: ${JSON.stringify({ choices: [{ delta: { content: text } }] })}\n\n`

const streamMock = vi.hoisted(() => vi.fn())
const traceMock = vi.hoisted(() => vi.fn())

vi.mock("../../../lib/api", () => ({
  stream: streamMock,
  api: { get: vi.fn() },
}))

// traceWhenWritten waits 300ms before its first attempt and retries six
// times, by design -- the log writer batches and the row is reliably absent
// when the stream ends. Left real, every test here would spend 1.8s inside it
// and outrun waitFor's default timeout, so the retry policy is stubbed and
// the rest of the module kept.
vi.mock("../metrics", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../metrics")>()),
  traceWhenWritten: traceMock,
}))

function yields(...chunks: string[]) {
  streamMock.mockImplementation(async function* (
    _path: string,
    _body: unknown,
    onStart?: (s: { requestId: string }) => void,
  ) {
    onStart?.({ requestId: "01TRACE" })
    for (const c of chunks) yield c
  })
}

describe("running one chat turn", () => {
  it("appends every streamed delta to the one assistant turn", async () => {
    // A version that read the turns its render closed over would append each
    // chunk to the same stale array, which renders as an empty transcript
    // above a request that plainly succeeded.
    yields(frame("Hel"), frame("lo "), frame("there"))
    traceMock.mockResolvedValue(null)

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))

    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(result.current.messages).toHaveLength(2)
    expect(result.current.messages[0]).toEqual({ role: "user", content: "hi" })
    expect(result.current.messages[1].content).toBe("Hello there")
  })

  it("refuses to send without a model", async () => {
    yields(frame("x"))
    const { result } = renderHook(() => useChatRun(emptyConfig(), () => {}))
    await act(() => result.current.send("hi"))
    expect(streamMock).not.toHaveBeenCalled()
    expect(result.current.messages).toHaveLength(0)
  })

  it("keeps the half answer when the operator stops", async () => {
    // Stopping is a decision, not a failure: the tokens were spent and the
    // partial answer is what they bought.
    streamMock.mockImplementation(async function* () {
      yield frame("par")
      throw Object.assign(new Error("aborted"), { name: "AbortError" })
    })
    traceMock.mockResolvedValue(null)

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))

    await waitFor(() => expect(result.current.busy).toBe(false))
    expect(result.current.messages[1].content).toBe("par")
    expect(result.current.error).toBe("")
  })

  it("files the route under the turn it served", async () => {
    // A transcript of six answers has to say which provider produced each,
    // not only the last.
    yields(frame("hi"))
    traceMock.mockResolvedValue({
      id: "01TRACE", tokens_in: 3, tokens_out: 5, total_ms: 120,
      cost_micros: null, model: "m", final_model: "m", provider: "groq",
      attempts: [{ provider: "groq", model: "m" }],
    })

    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))

    await waitFor(() => expect(result.current.routes[1]).toBeDefined())
    expect(result.current.routes[1].provider).toBe("groq")
  })

  it("clears the transcript and its routes together", async () => {
    yields(frame("hi"))
    traceMock.mockResolvedValue(null)
    const { result } = renderHook(() =>
      useChatRun({ ...emptyConfig(), model: "m" }, () => {}),
    )
    await act(() => result.current.send("hi"))
    await waitFor(() => expect(result.current.busy).toBe(false))

    act(() => result.current.clear())
    expect(result.current.messages).toHaveLength(0)
    expect(result.current.routes).toEqual({})
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- use-chat-run
```

Expected: FAIL — cannot resolve `./use-chat-run`.

- [ ] **Step 3: Write the hook**

Create `web/src/features/playground/lib/use-chat-run.ts`. The body is `chat.tsx` lines 229–323 moved verbatim — `appendToLastMessage`, `send`, `stop`, `clearConversation` — with their comments, wrapped in a hook that owns the state they used to read from the component. `send` takes the prompt as an argument instead of closing over `draft`, and the early return keeps its guards:

```ts
import { useRef, useState } from "react"
import { stream, type StreamStart } from "../../../lib/api"
import { chatBody, parseTools, type ChatState } from "./request"
import { drainSSE, extractUnaryText } from "./stream"
import {
  NO_METRICS,
  metricsFromTrace,
  traceWhenWritten,
  type StreamMetrics,
} from "../metrics"
import { routeFromTrace, type TurnRoute } from "../message"
import type { PlaygroundConfig } from "../config"
import type { PlaygroundMessage } from "../../../lib/api-types"

export type ChatRun = {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  error: string
  send: (prompt: string) => Promise<void>
  stop: () => void
  clear: () => void
}

/**
 * One chat turn, from send to the trace that explains it.
 *
 * Held as a hook rather than inside a component because every surface that
 * sends needs exactly this and none of them needs a second, slightly
 * different copy of it — two copies of a streaming loop drift, and the
 * drift shows up as one surface reporting timings the other does not.
 */
export function useChatRun(
  config: PlaygroundConfig,
  onMetrics: (m: StreamMetrics) => void,
): ChatRun {
  const [messages, setMessages] = useState<PlaygroundMessage[]>([])
  // Keyed by the index of the assistant turn it belongs to. Kept beside the
  // messages rather than inside them: `messages` is the wire body sent to the
  // gateway, and a field of ours in it would be a field the provider sees.
  const [routes, setRoutes] = useState<Record<number, TurnRoute>>({})
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const abort = useRef<AbortController | null>(null)
  const buffer = useRef("")

  // A functional update, and it has to be: a stream appends many times inside
  // one render, and a version that read the turns this render closed over
  // would append every chunk to the same stale array — which renders as a
  // transcript that stays empty while the request plainly succeeds.
  function appendToLastMessage(text: string) {
    if (!text) return
    setMessages((prev) => {
      const next = prev.slice()
      const lastIndex = next.length - 1
      const last = next[lastIndex]
      if (!last) return prev
      next[lastIndex] = { ...last, content: last.content + text }
      return next
    })
  }

  async function send(prompt: string) {
    const state: ChatState = { ...config, messages }
    const toolsError = parseTools(state.toolsRaw).error
    if (busy || state.model === "" || prompt === "" || toolsError !== undefined) return
    const dialect = state.dialect
    const doStream = state.stream
    const turns = [...state.messages, { role: "user", content: prompt } satisfies PlaygroundMessage]
    // The assistant turn this run will fill in, and the index its route lands
    // under when the trace arrives.
    const answerAt = turns.length
    setMessages([...turns, { role: "assistant", content: "" }])
    setError("")
    setBusy(true)
    onMetrics(NO_METRICS)
    buffer.current = ""
    const controller = new AbortController()
    abort.current = controller
    const startedAt = performance.now()
    let ttftMs: number | null = null
    let liveRequestId = ""
    try {
      for await (const chunk of stream(
        "/api/playground",
        chatBody({ ...state, messages: turns }),
        // The id arrives with the headers, before the body this is rendering.
        (s: StreamStart) => {
          liveRequestId = s.requestId
        },
        controller.signal,
      )) {
        buffer.current += chunk
        // With streaming off the executor answers with one JSON document and
        // no SSE framing at all, so there is nothing to drain until the body
        // is complete — handled after the loop instead.
        if (doStream) {
          const { text, rest } = drainSSE(buffer.current, dialect)
          buffer.current = rest
          // Measured on the first text, not the first chunk: a keep-alive or
          // a role-only frame is not the model answering.
          if (text && ttftMs === null) ttftMs = performance.now() - startedAt
          appendToLastMessage(text)
        }
      }
      if (!doStream) {
        appendToLastMessage(extractUnaryText(dialect, buffer.current))
      }
      const totalMs = performance.now() - startedAt
      const measured: StreamMetrics = { ...NO_METRICS, ttftMs, totalMs }
      onMetrics(measured)
      // The token counts are the gateway's, fetched after the fact rather
      // than guessed here: a client-side tokenisation would be a number that
      // looks authoritative and is not. A trace that has not landed yet
      // simply leaves the counts unknown.
      if (liveRequestId) {
        const trace = await traceWhenWritten(liveRequestId)
        if (trace) {
          onMetrics(metricsFromTrace(measured, trace))
          // The route lands under the turn it served, so a transcript of six
          // answers says which provider produced each rather than only the
          // last.
          setRoutes((prev) => ({ ...prev, [answerAt]: routeFromTrace(trace) }))
        }
      }
    } catch (err) {
      // Stopping is the operator's decision, not a failure to report at them.
      if ((err as Error).name !== "AbortError") setError((err as Error).message)
    } finally {
      abort.current = null
      setBusy(false)
    }
  }

  /** Ends the run in flight. The turns already written stay: a half answer is
   *  still an answer, and it is what the tokens were spent on. */
  function stop() {
    abort.current?.abort()
  }

  function clear() {
    setMessages([])
    setRoutes({})
    setError("")
    onMetrics(NO_METRICS)
  }

  return { messages, routes, busy, error, send, stop, clear }
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd web && npm test -- use-chat-run
```

Expected: PASS, five tests.

- [ ] **Step 5: Use the hook from `chat.tsx`**

Delete the state, `appendToLastMessage`, `send`, `stop` and `clearConversation` from `chat.tsx` and replace them with:

```ts
const { messages, routes, busy, error, send, stop, clear } = useChatRun(config, onMetrics)
```

`chat.tsx` keeps `draft`, the seed effect, the scroll effect and the markup. Its send handlers become `void send(draft)` followed by `setDraft("")`. `state` is now `{ ...config, messages }` built where it is read.

- [ ] **Step 6: Run the full suite**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/features/playground/lib/use-chat-run.ts \
        web/src/features/playground/lib/use-chat-run.test.tsx \
        web/src/features/playground/chat.tsx
git commit -m "refactor(web): hold one chat run in a hook"
```

---

### Task 4: Split the transcript and the composer

The two halves of `chat.tsx`'s markup become components. The transcript gains the scroll container the next task needs.

**Files:**
- Create: `web/src/features/playground/transcript.tsx`
- Create: `web/src/features/playground/transcript.test.tsx`
- Create: `web/src/features/playground/composer.tsx`
- Delete: `web/src/features/playground/chat.tsx`
- Create: `web/src/features/playground/chat-tab.tsx`
- Modify: `web/src/features/playground/playground-screen.tsx` (import path)

**Interfaces:**
- Consumes: `ChatRun` from `lib/use-chat-run`; `AssistantTurn`, `UserTurn`, `TurnRoute` from `./message`.
- Produces:

```ts
// transcript.tsx
export function Transcript(props: {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  model: string
  seedNote?: string
}): JSX.Element

// composer.tsx
export function Composer(props: {
  model: string
  busy: boolean
  error: string
  toolsError?: string
  canClear: boolean
  onSend: (prompt: string) => void
  onStop: () => void
  onClear: () => void
}): JSX.Element

// chat-tab.tsx
export function ChatTab(props: {
  config: PlaygroundConfig
  onConfigChange: (next: PlaygroundConfig) => void
  onMetrics: (m: StreamMetrics) => void
}): JSX.Element
```

**Note on naming.** §10 of the spec calls the composed component `lab/single.tsx`. It stays flat as `chat-tab.tsx` in this stage: the tab is still labelled "Chat", there is no Lab mode yet, and a `lab/` directory containing the only surface would name a structure that does not exist until stage 4. Stage 4 moves and renames it.

- [ ] **Step 1: Write the failing test**

Create `web/src/features/playground/transcript.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { Transcript } from "./transcript"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

describe("the transcript before anything is said", () => {
  it("says a model is what is missing", () => {
    // An empty panel cannot be told apart from one that failed to load.
    render(<Transcript messages={[]} routes={{}} busy={false} model="" />)
    expect(screen.getByText(/name a model to send to/i)).toBeInTheDocument()
  })

  it("says it is ready once one is named", () => {
    render(<Transcript messages={[]} routes={{}} busy={false} model="fast" />)
    expect(screen.getByText(/ready to send to fast/i)).toBeInTheDocument()
  })
})

describe("the transcript with turns", () => {
  it("renders each turn in order", () => {
    render(
      <Transcript
        messages={[
          { role: "user", content: "why did that fail over" },
          { role: "assistant", content: "the alias resolved" },
        ]}
        routes={{}}
        busy={false}
        model="fast"
      />,
    )
    expect(screen.getByText(/why did that fail over/)).toBeInTheDocument()
    expect(screen.getByText(/the alias resolved/)).toBeInTheDocument()
  })

  it("shows the seed note when a run was seeded from a trace", () => {
    // capture.bodies has a retention sweep and no writer, so a seeded run
    // restores model and dialect and nothing else. Left unsaid, the operator
    // discovers it from a transcript that is silently empty.
    render(
      <Transcript
        messages={[]} routes={{}} busy={false} model="fast"
        seedNote="Seeded from trace 01ABC: model and dialect carried over."
      />,
    )
    expect(screen.getByText(/model and dialect carried over/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- transcript
```

Expected: FAIL — cannot resolve `./transcript`.

- [ ] **Step 3: Write `transcript.tsx`**

The turn list, `EmptyChat` (moved verbatim from `chat.tsx:438`, comment intact) and the seed note, inside the scroll container the next task needs:

```tsx
import { useEffect, useRef } from "react"
import { AssistantTurn, UserTurn, type TurnRoute } from "./message"
import type { PlaygroundMessage } from "../../lib/api-types"

export function Transcript({
  messages,
  routes,
  busy,
  model,
  seedNote,
}: {
  messages: PlaygroundMessage[]
  routes: Record<number, TurnRoute>
  busy: boolean
  model: string
  seedNote?: string
}) {
  const scroller = useRef<HTMLDivElement | null>(null)
  const foot = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!busy) return
    const el = scroller.current
    if (!el) return
    if (nearBottom(el)) foot.current?.scrollIntoView({ block: "end" })
  }, [messages, busy])

  return (
    <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto px-6 py-4">
      {seedNote ? (
        <p className="pb-4 text-sm text-[hsl(var(--muted-foreground))]">{seedNote}</p>
      ) : null}

      {messages.length === 0 ? (
        <EmptyChat model={model} />
      ) : (
        <div className="flex flex-col gap-6">
          {messages.map((m, i) =>
            m.role === "user" ? (
              <UserTurn key={i} text={m.content} />
            ) : (
              <AssistantTurn
                key={i}
                text={m.content}
                route={routes[i]}
                // Only the last turn can still be arriving.
                streaming={busy && i === messages.length - 1}
              />
            ),
          )}
        </div>
      )}

      <div ref={foot} aria-hidden="true" />
    </div>
  )
}
```

`nearBottom` is Task 5. For now, so this task's tests pass on their own, define it in the same file as a placeholder that keeps today's behaviour. It is **exported from the start** — Task 5's test imports it, and a placeholder that is private would make that task fail on an unresolved import rather than on the assertion it is meant to fail on:

```tsx
export function nearBottom(_el: HTMLElement): boolean {
  return true
}
```

Move `EmptyChat` from `chat.tsx:438` verbatim, including its doc comment, and give it the height it now has room for:

```tsx
<div className="flex h-full flex-col items-center justify-center gap-2 text-center">
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd web && npm test -- transcript
```

Expected: PASS, four tests.

- [ ] **Step 5: Write `composer.tsx`**

Move `chat.tsx`'s sticky footer markup — the `Textarea`, the Send/Stop button, the error line and "New conversation" — verbatim, with every comment. Two changes, both forced by the move: the negative margins go (Task 6 negates the page padding once, at the screen root), and the draft state lives here rather than in the parent.

```tsx
<div className="flex shrink-0 flex-col gap-2 border-t bg-[hsl(var(--background))] px-6 pt-2 pb-4">
```

The `onKeyDown` guard keeps its comment and its conditions, calling `props.onSend(draft)` then clearing the draft.

- [ ] **Step 6: Write `chat-tab.tsx` and delete `chat.tsx`**

`ChatTab` holds what is left: `useChatRun`, the seed effect (moved verbatim from `chat.tsx:210-221`, including `seededFrom` and the three-state note wording), and the two children:

```tsx
return (
  <div className="flex h-full min-h-0 flex-col">
    <Transcript
      messages={messages} routes={routes} busy={busy}
      model={config.model} seedNote={seedNote}
    />
    <Composer
      model={config.model} busy={busy} error={error}
      toolsError={parseTools(config.toolsRaw).error}
      canClear={messages.length > 0}
      onSend={(p) => void send(p)} onStop={stop} onClear={clear}
    />
  </div>
)
```

Then `git rm web/src/features/playground/chat.tsx` and repoint `playground-screen.tsx`:

```ts
import { ChatTab } from "./chat-tab"
```

with `<Chat …/>` becoming `<ChatTab …/>`.

- [ ] **Step 7: Confirm nothing still imports the deleted file**

```bash
rg 'from "\./chat"|from "\.\./chat"' web/src
```

Expected: no output.

- [ ] **Step 8: Run the full suite**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add web/src/features/playground/transcript.tsx \
        web/src/features/playground/transcript.test.tsx \
        web/src/features/playground/composer.tsx \
        web/src/features/playground/chat-tab.tsx \
        web/src/features/playground/playground-screen.tsx
git rm web/src/features/playground/chat.tsx
git commit -m "refactor(web): split the chat surface in two"
```

---

### Task 5: Make the scroll-follow guard actually guard

The guard has never fired. `window.scrollY` is always 0 and `document.body.offsetHeight` always equals `window.innerHeight`, because darkraise-ui's layout root is `flex h-screen overflow-hidden` — so the test reduces to `innerHeight >= innerHeight - 160`, permanently true. Now that the transcript owns its scroll container, the check has something real to read.

**Files:**
- Modify: `web/src/features/playground/transcript.tsx`
- Modify: `web/src/features/playground/transcript.test.tsx`

**Interfaces:**
- Consumes: nothing new.
- Produces: `nearBottom(el: HTMLElement): boolean`, exported for its test.

- [ ] **Step 1: Write the failing test**

Extend the existing import at the top of `transcript.test.tsx` — a second `import` from the same module would trip `no-duplicate-imports`:

```tsx
import { Transcript, nearBottom } from "./transcript"
```

Then append:

```tsx
/** jsdom reports 0 for every layout measurement, so a scroll position has to
 *  be described rather than produced. */
function scrolled(el: HTMLElement, top: number, client: number, height: number) {
  Object.defineProperty(el, "scrollTop", { value: top, configurable: true })
  Object.defineProperty(el, "clientHeight", { value: client, configurable: true })
  Object.defineProperty(el, "scrollHeight", { value: height, configurable: true })
}

describe("following a streaming answer down", () => {
  it("follows while the reader is at the bottom", () => {
    const el = document.createElement("div")
    scrolled(el, 840, 400, 1240)
    expect(nearBottom(el)).toBe(true)
  })

  it("still follows inside the slack, so one wheel notch does not detach it", () => {
    const el = document.createElement("div")
    scrolled(el, 760, 400, 1240)
    expect(nearBottom(el)).toBe(true)
  })

  it("stops following once the reader has scrolled up to read", () => {
    // Yanking someone back to the bottom mid-sentence is the thing every chat
    // surface gets wrong, and the old guard could never decline.
    const el = document.createElement("div")
    scrolled(el, 200, 400, 1240)
    expect(nearBottom(el)).toBe(false)
  })

  it("follows an unscrollable transcript, which is always at its bottom", () => {
    const el = document.createElement("div")
    scrolled(el, 0, 400, 400)
    expect(nearBottom(el)).toBe(true)
  })
})
```

- [ ] **Step 2: Run it to verify it fails**

```bash
cd web && npm test -- transcript
```

Expected: FAIL — "stops following once the reader has scrolled up" gets `true` from the placeholder.

- [ ] **Step 3: Replace the placeholder**

```tsx
/** How far from the bottom still counts as reading the newest text. Roughly a
 *  few lines: enough that one wheel notch does not detach the follow, small
 *  enough that scrolling up to re-read does. */
const FOLLOW_SLACK_PX = 160

/**
 * Whether the reader is at the bottom of the transcript.
 *
 * Read off the scrolling element, which is this component's own container.
 * The window is not it and never was: darkraise-ui's layout root is
 * `h-screen overflow-hidden`, so `window.scrollY` is pinned at 0 and the
 * previous window-based test was true on every render — the follow could not
 * decline, and reading something earlier while an answer streamed pulled you
 * straight back down.
 */
export function nearBottom(el: HTMLElement): boolean {
  return el.scrollTop + el.clientHeight >= el.scrollHeight - FOLLOW_SLACK_PX
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
cd web && npm test -- transcript
```

Expected: PASS, eight tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/features/playground/transcript.tsx \
        web/src/features/playground/transcript.test.tsx
git commit -m "fix(web): stop yanking the reader back mid-answer"
```

---

### Task 6: Give the screen the whole frame

`main.dr-sidebar-layout-content` computes to `display: block`, `flex: 1 1 0%`, `overflow: auto` and a definite height inside a column that is `overflow: hidden` at viewport height. The height is already there; the playground has never asked for it.

**Files:**
- Modify: `web/src/features/playground/playground-screen.tsx`
- Modify: `web/src/features/playground/metrics.tsx`
- Modify: `web/src/features/playground/config-pane.tsx`
- Modify: `web/src/features/playground/aux-panels.tsx` (scroll container only)
- Modify: `web/src/features/playground/compare.tsx` (scroll container only)

**Interfaces:**
- Consumes: `ChatTab` from Task 4.
- Produces: a screen root that fills `main` and gives each region its own scroll.

- [ ] **Step 1: Make the screen a full-height column**

In `playground-screen.tsx`, the root becomes a flex column that fills `main` and cancels its `p-6` — the padding is what would otherwise leave the composer floating 24px above the foot:

```tsx
<div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col">
```

`PageHeader` keeps its own padding and stays `shrink-0`. The `Tabs` root becomes `flex min-h-0 flex-1 flex-col`, and the row holding the tab content and the config pane becomes:

```tsx
<div className="flex min-h-0 flex-1 flex-col lg:flex-row">
  <div className="flex min-h-0 min-w-0 flex-1 flex-col">
```

Each `TabsContent` needs `className="flex min-h-0 flex-1 flex-col data-[state=inactive]:hidden"` so the active panel inherits the height rather than collapsing to its content.

- [ ] **Step 2: Reserve the metrics strip's height**

The strip currently appears after the first run. In a fixed frame that shifts the transcript at exactly the moment the operator starts reading it. In `playground-screen.tsx` replace the `hasReadings` gate:

```tsx
{sends && <MetricsStrip metrics={metrics} />}
```

and in `metrics.tsx` add `shrink-0` to the strip's own class list so it cannot be squeezed:

```tsx
<div className="flex shrink-0 flex-wrap items-center gap-x-8 gap-y-3 border-b px-6 py-3">
```

`hasReadings` stays exported and tested — stage 4's Chat mode uses it to decide whether to show its quiet marker. Leave it and its comment alone.

- [ ] **Step 3: Let the config pane run the full height**

In `config-pane.tsx` the `aside` becomes a column that scrolls its own contents, so its border reaches the bottom of the frame instead of stopping where the controls end:

```tsx
<aside className="flex w-full shrink-0 flex-col gap-4 overflow-y-auto border-l p-4 lg:w-80">
```

- [ ] **Step 4: Give the other two sending surfaces their own scroll**

`compare.tsx` and `aux-panels.tsx` each wrap their content in `flex min-h-0 flex-1 flex-col overflow-y-auto`, replacing the outer `flex flex-col gap-4 p-6` with `flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-6`. Without this they scroll `main` and the layout above them moves.

- [ ] **Step 5: Run the full suite**

```bash
cd web && npm test && npm run typecheck
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/playground/playground-screen.tsx \
        web/src/features/playground/metrics.tsx \
        web/src/features/playground/config-pane.tsx \
        web/src/features/playground/aux-panels.tsx \
        web/src/features/playground/compare.tsx
git commit -m "fix(web): let the playground fill its frame"
```

---

### Task 7: Verify it in the running console, then deploy

A layout change cannot be verified from tests. `CLAUDE.md` requires both the look and the redeploy.

**Files:** none.

- [ ] **Step 1: Build and check the whole gate**

```bash
cd web && npm test && npm run typecheck && npm run build
```

Expected: all three clean.

- [ ] **Step 2: Build the image and deploy with the UAT overlay**

The overlay is required: `compose.prod.yml` alone sets `pull_policy: always` and would discard the local build.

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
```

- [ ] **Step 3: Confirm the deploy took**

```bash
docker ps --filter name=darkrouter --format '{{.Names}}\t{{.Status}}'
curl -s http://localhost:8091/healthz
```

Expected: healthy, and a 200. Use 8091 — 8081 belongs to another container on this machine and answers with a different service.

- [ ] **Step 4: Confirm the served bundle is this build**

Compare bytes, not filenames: Vite's asset hash differs between the image's build path and the repo path, so a filename check reports a false mismatch on a good deploy.

```bash
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
```

- [ ] **Step 5: Look at it**

Log in with the password from `.uat-credentials` — do not copy it into any tracked file. At **1600×1000** and **1280×800**, in **light and dark**, with the sidebar **collapsed and expanded**, on the **Chat** and **Compare** tabs:

| Check | What passing looks like |
|---|---|
| D5 | No dead band under the content. The composer sits at the foot of the frame; the config pane's left border runs the full height |
| D6 | Send a prompt: the transcript does not jump when the first reading lands |
| D4 | While an answer streams, scroll up — it stays where you put it. Scroll back down — it resumes following |
| Empty state | Centred in the region it has, not in a short band at the top |
| Narrow | At 1280×800 the config pane still fits beside the transcript without squeezing it below readable width |

Any row that fails is a defect in Tasks 4–6, not a new task: fix it there, re-run the suite, and redeploy before continuing.

- [ ] **Step 6: Record the stage as done**

Append a Stage 1 row to the plan's Definition of Done table with the date and the observed result of each check, then commit:

```bash
git add docs/superpowers/plans/2026-08-29-playground-stage1-frame.md
git commit -m "docs(playground): record the stage 1 gate"
```

---

## Notes for whoever picks this up

**The five suites are the safety net, and they are load-bearing.** Tasks 2 to 4 move roughly 300 lines between files. The only thing separating a clean extraction from a subtle behaviour change is that `chat.test.ts`'s 23 assertions, `message.test.tsx` and the rest still pass without being touched. If one needs editing, something was lost in the move — stop and say so rather than adjusting the assertion to match the new behaviour.

**Task 3 is where the risk is.** The streaming loop has four comments describing bugs it already survived: the functional `setMessages` update, TTFT measured on first text rather than first chunk, the trace fetched after the fact rather than tokenised client-side, and abort treated as a decision rather than an error. Every one of them is a defect someone already paid for. Move them with their code.

**Task 6 has no unit test and that is deliberate.** Asserting that an element carries `min-h-0` tests the implementation, not the behaviour, and would need rewriting the moment a class changed. Its gate is Task 7's live check, which is what `CLAUDE.md` requires for exactly this reason.
