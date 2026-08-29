import { Tooltip, TooltipContent, TooltipTrigger } from "darkraise-ui"
import type { TargetFacts, TargetState } from "./chain-health"

/** The dot's colour, and nothing else's. A pill is neutral: colouring the
 *  whole chip by state would make a chain of three fine targets read as three
 *  competing badges rather than as an order. */
const DOT: Record<TargetState, string> = {
  routable: "bg-[hsl(var(--success))]",
  "any-provider": "bg-[hsl(var(--success))]",
  cooling: "bg-[hsl(var(--warning))]",
  "provider-disabled": "bg-[hsl(var(--muted-foreground))]",
  "provider-unconfigured": "bg-[hsl(var(--warning))]",
  "model-missing": "bg-[hsl(var(--destructive))]",
  "model-retired": "bg-[hsl(var(--warning))]",
  "provider-missing": "bg-[hsl(var(--destructive))]",
  unresolved: "bg-[hsl(var(--destructive))]",
  unknown: "bg-[hsl(var(--border))]",
  blank: "bg-[hsl(var(--border))]",
}

export const STATE_PROSE: Record<TargetState, string> = {
  routable: "routable",
  "any-provider": "routable",
  cooling: "cooling",
  "provider-disabled": "provider disabled",
  "provider-unconfigured": "no accounts",
  "model-missing": "model not offered",
  "model-retired": "withdrawn upstream",
  "provider-missing": "provider not configured",
  unresolved: "nothing offers this",
  unknown: "not checked — the catalogue has not loaded",
  blank: "empty",
}

/** The state alone, for places that already show the target's text in a field
 *  of their own and only need the verdict beside it. */
export function TargetDot({ state }: { state: TargetState }) {
  return (
    <span
      className={`size-2 shrink-0 rounded-full ${DOT[state]}`}
      role="img"
      aria-label={STATE_PROSE[state]}
    />
  )
}

/**
 * One target in a chain, as the router sees it right now.
 *
 * The rank is on the chip rather than implied by position: a chain is a
 * fallback order, and an operator scanning a wrapped row needs to know which
 * one is tried first without counting from the left edge.
 */
export function TargetPill({ facts, rank }: { facts: TargetFacts; rank: number }) {
  const detail =
    facts.problem ||
    (facts.state === "any-provider" && facts.offeredBy.length > 0
      ? `served by ${facts.offeredBy.join(", ")}, in priority order`
      : facts.providerId
        ? `pinned to ${facts.providerId}`
        : STATE_PROSE[facts.state])

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="inline-flex max-w-full items-center gap-1.5 rounded-full border px-2 py-0.5 font-mono text-sm">
          <span className="text-[hsl(var(--legend))]">{rank}</span>
          <TargetDot state={facts.state} />
          <span className="truncate">{facts.raw}</span>
        </span>
      </TooltipTrigger>
      <TooltipContent>
        <span className="text-sm">
          {STATE_PROSE[facts.state]} · {detail}
        </span>
      </TooltipContent>
    </Tooltip>
  )
}
