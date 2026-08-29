import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { Check, Copy, CornerDownRight, TriangleAlert } from "lucide-react"
import { Badge } from "darkraise-ui"
import { ProviderIcon } from "../providers/provider-icon"
import { Markdown } from "./markdown"
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
  costMicros: number | null
  /** Providers tried before the one that answered, in the order tried. */
  failedOver: string[]
  /** What an adapter accepted and then dropped, in the gateway's own wording.
   *  Flat strings rather than a triple: ir.Warning.String() owns the format,
   *  and re-splitting it here would break on any reason containing " -> ". */
  warnings: string[]
}

export function routeFromTrace(trace: RequestTrace): TurnRoute {
  const attempts = trace.attempts ?? []
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
    costMicros: trace.cost_micros ?? null,
    failedOver: attempts.slice(0, -1).map((a) => a.provider),
    warnings: trace.warnings ?? [],
  }
}

/** A turn the operator typed. Right, tinted, and only as wide as it needs:
 *  the asymmetry is what lets someone scan who said what without reading. */
export function UserTurn({ text }: { text: string }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[42rem] rounded-[var(--radius)] rounded-br-sm bg-[hsl(var(--muted))] px-4 py-2.5">
        <p className="text-sm whitespace-pre-wrap">{text}</p>
      </div>
    </div>
  )
}

/**
 * A turn a provider answered.
 *
 * Full width on the page's own ground rather than in a bubble, with the
 * provider's mark in the gutter. The mark is the identity: an operator
 * running the same prompt across four providers is comparing answers, and
 * which one produced this is the first thing they need.
 */
export function AssistantTurn({
  text,
  route,
  streaming = false,
}: {
  text: string
  route?: TurnRoute
  streaming?: boolean
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
        ) : (
          // Before the trace lands there is nothing true to draw, so the
          // gutter keeps its width and stays empty rather than guessing.
          <div className="size-7 rounded-[var(--radius)] border border-dashed" aria-hidden="true" />
        )}
        </div>
      </div>

      <div className="flex min-w-0 flex-1 flex-col gap-2">
        {text === "" && streaming ? (
          <StreamingHint />
        ) : (
          <div className="min-w-0">
            <Markdown text={text} />
            {streaming ? <Caret /> : null}
          </div>
        )}
        {route ? <RouteLine route={route} /> : null}
        {route && route.warnings.length > 0 ? <TurnWarnings warnings={route.warnings} /> : null}
      </div>

      {!streaming && text !== "" ? <CopyTurn text={text} /> : null}
    </div>
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
 */
function RouteLine({ route }: { route: TurnRoute }) {
  const parts: string[] = []
  if (route.totalMs !== null) {
    parts.push(route.totalMs >= 1000 ? `${(route.totalMs / 1000).toFixed(1)}s` : `${route.totalMs}ms`)
  }
  if (route.tokensIn !== null && route.tokensOut !== null) {
    parts.push(`${route.tokensIn} in · ${route.tokensOut} out`)
  }
  if (route.costMicros !== null && route.costMicros > 0) {
    parts.push(formatCost(route.costMicros))
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

/** Cost lands in micros. Below a cent the useful reading is the fraction, not
 *  a rounded zero that says the call was free when it was not. */
export function formatCost(micros: number): string {
  const dollars = micros / 1_000_000
  if (dollars >= 0.01) return `$${dollars.toFixed(2)}`
  if (dollars >= 0.0001) return `$${dollars.toFixed(4)}`
  return "<$0.0001"
}

function CopyTurn({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      aria-label="Copy this answer"
      className="mt-0.5 h-fit shrink-0 rounded-sm p-1 text-[hsl(var(--legend))] transition-colors hover:text-[hsl(var(--foreground))]"
      onClick={() => {
        void navigator.clipboard?.writeText(text)
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1200)
      }}
    >
      {copied ? (
        <Check className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
      ) : (
        <Copy className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
      )}
    </button>
  )
}
