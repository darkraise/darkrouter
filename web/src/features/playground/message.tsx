import { memo, useState } from "react"
import { Link } from "@tanstack/react-router"
import { Brain, Check, ChevronRight, Copy, CornerDownRight, TriangleAlert } from "lucide-react"
import { Badge, Collapsible, CollapsibleContent, CollapsibleTrigger } from "darkraise-ui"
import { count, duration, money } from "../../lib/format"
import { ProviderIcon } from "../providers/provider-icon"
import { Markdown } from "./markdown"
import type { TurnThinking } from "./lib/use-chat-run"
import type { RequestTrace } from "../../lib/api-types"

/**
 * What served one turn.
 *
 * A chat transcript anywhere else is a record of what was said. This is a
 * router, so a turn here is also a record of who said it and what the routing
 * cost — the thing an operator opened the playground to find out, and the
 * thing a bare bubble throws away.
 */
export type TurnRoute = {
  requestId: string
  provider: string
  model: string
  totalMs: number | null
  tokensIn: number | null
  tokensOut: number | null
  /** Of `tokensOut`, how many the model spent reasoning. Zero for a model
   *  that does not reason, and the one signal that a turn reasoned which does
   *  not depend on what the provider's wire called the reasoning text. */
  reasoningTokens: number
  costMicros: number | null
  /** Whether `costMicros` covers every provider attempt, only the priced
   * attempts, or none of them. */
  costCoverage: "complete" | "partial" | "unknown"
  /** Providers tried before the one that answered, in the order tried. */
  failedOver: string[]
  /** What an adapter accepted and then dropped, in the gateway's own wording.
   *  Flat strings rather than a triple: ir.Warning.String() owns the format,
   *  and re-splitting it here would break on any reason containing " -> ". */
  warnings: string[]
}

export function routeFromTrace(trace: RequestTrace): TurnRoute {
  const attempts = trace.attempts ?? []
  const pricedAttempts = attempts.filter((attempt) => typeof attempt.cost_micros === "number")
  const attemptCost = pricedAttempts.reduce((sum, attempt) => sum + attempt.cost_micros!, 0)
  const costMicros = attempts.length === 0
    ? (trace.cost_micros ?? null)
    : pricedAttempts.length === 0
      ? null
      : attemptCost
  const costCoverage: TurnRoute["costCoverage"] = attempts.length === 0
    ? trace.cost_micros === null ? "unknown" : "complete"
    : pricedAttempts.length === 0
      ? "unknown"
      : pricedAttempts.length === attempts.length ? "complete" : "partial"
  // The last attempt is the one that answered; everything before it failed
  // over. Reading the served provider off the trace rather than the request
  // is the point: an alias or a bare model name does not say who answered.
  const served = attempts[attempts.length - 1]
  return {
    requestId: trace.id,
    provider: served?.provider ?? trace.provider ?? "",
    model: trace.final_model || served?.model || trace.model,
    totalMs: trace.total_ms ?? null,
    tokensIn: trace.tokens_in ?? null,
    tokensOut: trace.tokens_out ?? null,
    reasoningTokens: trace.reasoning_tokens ?? 0,
    costMicros,
    costCoverage,
    failedOver: attempts.slice(0, -1).map((a) => a.provider),
    warnings: trace.warnings ?? [],
  }
}

/** A turn the operator typed. Right, tinted, and only as wide as it needs:
 *  the asymmetry is what lets someone scan who said what without reading.
 *
 *  Memoised, as is the assistant turn: a streaming answer re-renders the
 *  transcript on every chunk, and every earlier turn would otherwise be
 *  re-rendered — and its markdown re-parsed — for text that has not changed. */
export const UserTurn = memo(function UserTurn({ text }: { text: string }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[42rem] rounded-[var(--radius)] rounded-br-sm bg-[hsl(var(--muted))] px-4 py-2.5">
        <p className="text-sm whitespace-pre-wrap">{text}</p>
      </div>
    </div>
  )
})

/**
 * A turn a provider answered.
 *
 * Full width on the page's own ground rather than in a bubble, with the
 * provider's mark in the gutter. The mark is the identity: an operator
 * running the same prompt across four providers is comparing answers, and
 * which one produced this is the first thing they need.
 */
export const AssistantTurn = memo(function AssistantTurn({
  text,
  route,
  thinking,
  streaming = false,
  quiet = false,
}: {
  text: string
  route?: TurnRoute
  /** The model's own working, when it sent any. Absent for a model that does
   *  not reason, and for every turn read back from the store. */
  thinking?: TurnThinking
  streaming?: boolean
  /** Chat mode's reading: the duration only, until the operator asks. */
  quiet?: boolean
}) {
  return (
    <div className="flex gap-3">
      {/* Sticky inside a column that runs the height of the turn: on a long
          answer the mark would otherwise scroll away, and whose answer you
          are reading is exactly what you lose track of halfway down. */}
      <div className="shrink-0">
        <div className="sticky top-4 mt-0.5">
        {route?.provider ? (
          <ProviderIcon preset={route.provider} id={route.provider} name={route.provider} size={28} />
        ) : route ? (
          // Settled, but the trace could not be fetched -- swept by log
          // retention, most likely. Solid rather than dashed: the turn is
          // finished, and a dashed mark reads as one still on its way.
          <div
            className="size-7 rounded-[var(--radius)] border bg-[hsl(var(--muted))]"
            title="This answer's trace is no longer in the request log"
          />
        ) : (
          // Before the trace lands there is nothing true to draw, so the
          // gutter keeps its width and stays empty rather than guessing.
          <div className="size-7 rounded-[var(--radius)] border border-dashed" aria-hidden="true" />
        )}
        </div>
      </div>

      <div className="flex min-w-0 flex-1 flex-col gap-2">
        {thinking ? (
          <Thinking thinking={thinking} />
        ) : route && route.reasoningTokens > 0 ? (
          <UnreadableThinking tokens={route.reasoningTokens} />
        ) : null}
        {text === "" && streaming && !thinking ? (
          <StreamingHint />
        ) : text === "" && streaming ? null : (
          <div className="min-w-0">
            <Markdown text={text} streaming={streaming} />
            {streaming ? <Caret /> : null}
          </div>
        )}
        {route ? <RouteLine route={route} quiet={quiet} /> : null}
        {route && route.warnings.length > 0 ? <TurnWarnings warnings={route.warnings} /> : null}
      </div>

      {!streaming && text !== "" ? <CopyTurn text={text} /> : null}
    </div>
  )
})

/**
 * The model's own working, folded away.
 *
 * Collapsed by default and on every turn, because reasoning is not the answer:
 * a transcript that opens six of these is six screens of a model talking to
 * itself in front of the thing that was asked for. The header carries what an
 * operator actually wants at a glance -- that it thought, and for how long --
 * and one click gets the rest.
 *
 * The duration is the reading that makes it worth showing at all. Reasoning is
 * billed and it is slow, so "thought for 8.4s" explains a turn whose answer
 * arrived late without the operator opening a trace to find out why.
 */
function Thinking({ thinking }: { thinking: TurnThinking }) {
  const settled = thinking.ms !== null
  return (
    <Collapsible className="w-fit max-w-full">
      <CollapsibleTrigger className="group flex items-center gap-1.5 rounded-[var(--radius)] border px-2 py-1 text-sm text-[hsl(var(--muted-foreground))] hover:text-[hsl(var(--foreground))]">
        <ChevronRight
          className="size-[var(--icon-size,1rem)] transition-transform group-data-[state=open]:rotate-90"
          aria-hidden="true"
        />
        <Brain
          className={`size-[var(--icon-size,1rem)] ${settled ? "" : "motion-safe:animate-pulse"}`}
          aria-hidden="true"
        />
        {settled ? `Thinking in ${duration(thinking.ms)}` : "Thinking…"}
      </CollapsibleTrigger>
      <CollapsibleContent>
        {/* Its own quiet ground and a capped height: the working is often
            longer than the answer, and a turn whose reasoning pushes the reply
            off the screen has buried the thing it was asked for. */}
        <div className="mt-2 max-h-64 overflow-y-auto rounded-[var(--radius)] border-l-2 bg-[hsl(var(--muted)/0.4)] px-3 py-2 text-sm whitespace-pre-wrap text-[hsl(var(--muted-foreground))]">
          {thinking.text}
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

/**
 * A turn that reasoned and whose working never arrived.
 *
 * The token count comes off the trace, so it is true whatever the reply's wire
 * shape was — which is the point. Two different situations land here and the
 * client cannot tell them apart, so the wording claims neither: some providers
 * bill reasoning and deliberately withhold the text, and a passthrough reply
 * carries the upstream's own field name for it, which the extractor may not
 * know yet. Either way the honest statement is that it reasoned and this is
 * what it cost.
 *
 * Drawn at all because the alternative is what shipped before: a turn that
 * spent most of its output budget thinking, showing nothing to say so.
 */
function UnreadableThinking({ tokens }: { tokens: number }) {
  return (
    <p
      className="flex w-fit items-center gap-1.5 text-sm text-[hsl(var(--legend))]"
      title="The gateway counted these tokens from the provider's usage. The reasoning text itself was not in the reply."
    >
      <Brain className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
      Reasoned for {count(tokens)} tokens · working not returned
    </p>
  )
}

/** The moment between sending and the first token, which is exactly what the
 *  time-to-first-token reading measures. Three dots rather than a spinner:
 *  a spinner says "working", this says "about to speak". */
function StreamingHint() {
  return (
    <p className="flex items-center gap-1 py-1" aria-label="Waiting for the first token">
      {[0, 1, 2].map((i) => (
        <span
          key={i}
          className="size-1.5 rounded-full bg-[hsl(var(--legend))] motion-safe:animate-pulse"
          style={{ animationDelay: `${i * 160}ms` }}
        />
      ))}
    </p>
  )
}

/** Sits at the end of the text while it is still arriving. */
function Caret() {
  return (
    <span
      aria-hidden="true"
      className="ml-0.5 inline-block h-4 w-[2px] translate-y-0.5 bg-[hsl(var(--primary))] motion-safe:animate-pulse"
    />
  )
}

/**
 * The route, under the answer it explains.
 *
 * One line, in the quiet colour, holding what the operator came for: who
 * answered, how long it took, what it spent. A failover is the exception and
 * is drawn as one — it is the most interesting thing that can happen to a
 * request, and a number in a row would bury it.
 *
 * Quiet is Chat mode's reading of the same line. Twenty turns each carrying
 * cost, tokens and a trace link is an instrument panel under a conversation;
 * the duration alone still says the answer was routed, and one click gets the
 * rest back. Lab never quiets, because measurement is what Lab is for.
 */
function RouteLine({ route, quiet = false }: { route: TurnRoute; quiet?: boolean }) {
  const [expanded, setExpanded] = useState(false)

  if (quiet && !expanded) {
    return (
      <button
        type="button"
        aria-label="Show routing detail"
        onClick={() => setExpanded(true)}
        className="w-fit text-sm text-[hsl(var(--legend))] underline-offset-2 hover:underline"
      >
        {route.totalMs === null ? "routed" : duration(route.totalMs)}
      </button>
    )
  }

  const parts: string[] = []
  if (route.totalMs !== null) {
    parts.push(duration(route.totalMs))
  }
  if (route.tokensIn !== null && route.tokensOut !== null) {
    parts.push(`${count(route.tokensIn)} in · ${count(route.tokensOut)} out`)
  }
  if (route.costMicros !== null) {
    parts.push(money(route.costMicros))
  }

  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm text-[hsl(var(--legend))]">
      {route.provider ? (
        <span className="font-mono">
          {route.provider}
          {route.model ? <span className="text-[hsl(var(--muted-foreground))]">/{route.model}</span> : null}
        </span>
      ) : null}
      {parts.length > 0 ? <span>{parts.join(" · ")}</span> : null}
      {route.failedOver.length > 0 ? (
        <Badge variant="secondary" className="gap-1">
          <CornerDownRight className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
          failed over from {route.failedOver.join(", ")}
        </Badge>
      ) : null}
      {route.requestId ? (
        <Link
          to="/requests/$id"
          params={{ id: route.requestId }}
          className="underline underline-offset-2 hover:text-[hsl(var(--foreground))]"
        >
          trace
        </Link>
      ) : null}
      {/* Only where there is a quiet state to go back to. Lab never quiets,
          so collapsing there would hide what the screen exists to show. */}
      {quiet ? (
        <button
          type="button"
          aria-label="Hide routing detail"
          onClick={() => setExpanded(false)}
          className="underline-offset-2 hover:underline"
        >
          less
        </button>
      ) : null}
    </div>
  )
}

/**
 * Parameters the provider would not take.
 *
 * Distinct from an error: the request succeeded, and this is the part of it
 * that did not survive the trip. Drawn quietly, because a run with warnings is
 * still a run that worked.
 */
function TurnWarnings({ warnings }: { warnings: string[] }) {
  return (
    <ul className="flex flex-col gap-1">
      {warnings.map((w, i) => (
        <li key={i} className="flex items-start gap-1.5 text-sm text-[hsl(var(--legend))]">
          <TriangleAlert
            className="mt-0.5 size-[var(--icon-size,1rem)] shrink-0"
            aria-hidden="true"
          />
          <span className="font-mono">{w}</span>
        </li>
      ))}
    </ul>
  )
}

function CopyTurn({ text }: { text: string }) {
  const [state, setState] = useState<"idle" | "copied" | "error">("idle")
  const [error, setError] = useState("")

  async function copy() {
    if (!navigator.clipboard) {
      setError("Clipboard is unavailable in this browser context.")
      setState("error")
      return
    }
    try {
      await navigator.clipboard.writeText(text)
      setError("")
      setState("copied")
      window.setTimeout(() => setState("idle"), 1200)
    } catch {
      setError("Could not copy this answer.")
      setState("error")
    }
  }

  return (
    <div className="flex shrink-0 flex-col items-end gap-1">
    <button
      type="button"
      aria-label={state === "copied" ? "Copied" : "Copy this answer"}
      className="mt-0.5 h-fit shrink-0 rounded-sm p-1 text-[hsl(var(--legend))] transition-colors hover:text-[hsl(var(--foreground))]"
      onClick={() => void copy()}
    >
      {state === "copied" ? (
        <Check className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
      ) : (
        <Copy className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
      )}
    </button>
    {state === "error" ? (
      <span role="alert" className="max-w-48 text-right text-sm text-[hsl(var(--destructive))]">
        {error}
      </span>
    ) : null}
    </div>
  )
}
