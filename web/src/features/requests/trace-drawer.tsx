import {
  Badge,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "darkraise-ui"
import { Fragment } from "react"
import { Link } from "@tanstack/react-router"
import { ApiError } from "../../lib/api"
import { count, duration, money } from "../../lib/format"
import { useTrace } from "../../lib/queries"
import type { RequestTrace, TraceAttempt, TraceBody } from "../../lib/api-types"
import { Ladder, type LadderRow, type RetrospectiveMark } from "../ladder/ladder"

/** The widest a latency bar may be drawn, in pixels. */
const MAX_BAR = 96

function markFor(a: TraceAttempt): RetrospectiveMark {
  if (a.outcome === "success") return "served"
  // Every mark here is filled: these attempts happened. A hollow mark would
  // claim the router only considered this provider.
  return "failed"
}

/**
 * Attempts become ladder rows. The serving attempt is the one whose outcome
 * says so, not the last one: a request can fail after something served.
 *
 * Bars are scaled to the request total so they agree with the waterfall
 * below; a trace with no total falls back to the slowest attempt so the shape
 * is still readable.
 */
export function ladderRows(
  attempts: TraceAttempt[],
  totalMs: number | null = null,
): LadderRow<RetrospectiveMark>[] {
  const scale =
    totalMs !== null && totalMs > 0 ? totalMs : Math.max(...attempts.map((a) => a.latency_ms), 1)
  const servedAt = attempts.findIndex((a) => a.outcome === "success")
  return attempts.map((a, i) => ({
    rank: a.seq + 1,
    mark: markFor(a),
    target: `${a.provider}/${a.model}`,
    reasonCode: a.status_code > 0 ? String(a.status_code) : a.outcome,
    reasonProse: [
      duration(a.latency_ms),
      a.path,
      // A failed attempt that burned tokens still cost money, and the ladder
      // row is the only place that spend is attributable to a provider.
      a.cost_micros === null ? "" : money(a.cost_micros),
      a.key_label === "" ? "" : `key ${a.key_label}`,
      a.error,
    ]
      .filter(Boolean)
      .join(" · "),
    latencyPx: Math.max(2, Math.min(1, a.latency_ms / scale) * MAX_BAR),
    // Everything after the attempt that served was never sent.
    terminated: servedAt !== -1 && i > servedAt,
  }))
}

/**
 * The request-level waterfall.
 *
 * §6.3 describes connect, first-token and total per attempt. Only the request
 * carries a first-token measurement and nothing records connect timing at all,
 * so this draws the two facts that exist.
 */
export function waterfallRows(
  trace: Pick<RequestTrace, "ttft_ms" | "total_ms">,
): { label: string; ms: number; fraction: number }[] {
  const total = trace.total_ms
  if (total === null || total <= 0) return []
  const rows: { label: string; ms: number; fraction: number }[] = []
  if (trace.ttft_ms !== null) {
    rows.push({
      label: "time to first token",
      ms: trace.ttft_ms,
      // Clamped: the two measurements can skew, and a fraction above one
      // draws a bar outside its own track.
      fraction: Math.min(1, trace.ttft_ms / total),
    })
  }
  rows.push({ label: "total", ms: total, fraction: 1 })
  return rows
}

export type AttemptSegment = {
  seq: number
  label: string
  /** Where on the total track this attempt began, as a fraction. */
  start: number
  fraction: number
  served: boolean
}

/** Attempts laid end to end along the request total. They ran in sequence,
 *  so each starts where the one before ended; the last is clamped so clock
 *  skew between the two measurements cannot draw past the track. */
export function attemptSegments(
  attempts: TraceAttempt[],
  totalMs: number | null,
): AttemptSegment[] {
  if (totalMs === null || totalMs <= 0) return []
  let start = 0
  const out: AttemptSegment[] = []
  for (const a of attempts) {
    const fraction = Math.max(0, Math.min(1 - start, a.latency_ms / totalMs))
    out.push({
      seq: a.seq,
      label: `${a.provider}/${a.model}`,
      start,
      fraction,
      served: a.outcome === "success",
    })
    start += fraction
  }
  return out
}

/** What to tell the operator when the trace did not load. A 404 is a request
 *  the log does not hold — swept by retention, or a stale link — which is a
 *  different situation from the gateway failing to answer. */
export function traceErrorMessage(error: unknown): string {
  if (error instanceof ApiError && error.status === 404) {
    return "No request with that id. Retention may have swept it, or the link is stale."
  }
  const detail = error instanceof Error ? error.message : String(error)
  return `Could not load this trace: ${detail}`
}

export function BodiesPanel({ bodies }: { bodies?: TraceBody[] }) {
  if (bodies === undefined || bodies.length === 0) {
    return (
      <p className="text-sm text-[hsl(var(--muted-foreground))]">
        Bodies are not stored for this request.
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-3">
      {bodies.map((b, i) => (
        // Keyed on index too: two of the same kind (e.g. two tool-call
        // turns) would otherwise collide.
        <div key={`${b.kind}-${i}`}>
          <p className="text-sm text-[hsl(var(--legend))]">{b.kind}</p>
          <pre className="mt-1 overflow-x-auto rounded bg-[hsl(var(--muted))] p-3 font-mono text-sm">
            {b.content}
          </pre>
        </div>
      ))}
    </div>
  )
}

/**
 * One metadata value, rendered so a reader can act on it.
 *
 * `String(value)` prints `[object Object]` for anything that is not a scalar,
 * and `surface_meta` is typed `Record<string, unknown>` — every writer in
 * internal/exec emits scalars today, but nothing in the type or the column
 * says they must. JSON is the fallback for the shape that has no better
 * rendering.
 */
export function metaValue(value: unknown): string {
  if (value === null) return "null"
  if (typeof value === "object") return JSON.stringify(value)
  return String(value)
}

/**
 * `surface_meta` is written as `{}` rather than null for a NOT NULL column,
 * so a truthy check on the object would draw a heading over an empty list.
 * This checks the key count and renders nothing, heading included, when
 * there is nothing to show.
 */
export function SurfaceMetaSection({
  meta,
}: {
  meta?: Record<string, unknown>
}) {
  const entries = Object.entries(meta ?? {})
  if (entries.length === 0) return null
  return (
    <section>
      <h3 className="mb-2 text-sm font-medium">Surface metadata</h3>
      <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
        {entries.map(([key, value]) => (
          <Fragment key={key}>
            <dt className="text-[hsl(var(--legend))]">{key}</dt>
            <dd className="font-mono break-all">{metaValue(value)}</dd>
          </Fragment>
        ))}
      </dl>
    </section>
  )
}

/**
 * One track for the whole request: attempts laid end to end, and a tick where
 * the first token arrived. A separate bar per phase said the same total twice
 * and could not show that the first token came from the second attempt.
 */
function AttemptWaterfall({ trace }: { trace: RequestTrace }) {
  const segments = attemptSegments(trace.attempts, trace.total_ms)
  const ttft = waterfallRows(trace).find((r) => r.label !== "total")
  if (segments.length === 0 && ttft === undefined) return null
  return (
    <section>
      <h3 className="mb-2 text-sm font-medium">Latency</h3>
      <p className="mb-1 text-sm text-[hsl(var(--legend))]">
        {ttft ? `first token at ${duration(ttft.ms)} · ` : ""}
        total {duration(trace.total_ms)}
      </p>
      <div
        className="relative h-3 w-full overflow-hidden rounded bg-[hsl(var(--muted))]"
        role="img"
        aria-label={segments
          .map((s) => `${s.label} ${Math.round(s.fraction * 100)}% of the total`)
          .join(", ")}
      >
        {segments.map((s) => (
          <div
            key={s.seq}
            title={s.label}
            className={
              s.served
                ? "absolute inset-y-0 bg-[hsl(var(--legend))]"
                : "absolute inset-y-0 bg-[hsl(var(--destructive)/0.6)]"
            }
            style={{ left: `${s.start * 100}%`, width: `${s.fraction * 100}%` }}
          />
        ))}
        {ttft ? (
          <div
            title={`first token at ${duration(ttft.ms)}`}
            className="absolute inset-y-0 w-0.5 bg-[hsl(var(--primary))]"
            style={{ left: `calc(${ttft.fraction * 100}% - 1px)` }}
          />
        ) : null}
      </div>
    </section>
  )
}

export function TraceDrawer({
  id,
  onClose,
}: {
  id: string | null
  onClose: () => void
}) {
  const trace = useTrace(id ?? "", { enabled: id !== null })

  return (
    <Sheet open={id !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-full max-w-3xl overflow-y-auto">
        <SheetHeader>
          <SheetTitle className="font-mono text-sm">{id}</SheetTitle>
        </SheetHeader>

        {trace.isError && (
          <p className="mt-4 text-sm text-[hsl(var(--muted-foreground))]">
            {traceErrorMessage(trace.error)}
          </p>
        )}

        {trace.data && (
          <div className="mt-4 flex flex-col gap-6">
            <Link
              to="/playground"
              search={{ mode: "chat", seed: trace.data.id }}
              className="w-fit text-sm underline underline-offset-2"
            >
              Open in playground
            </Link>

            <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
              <dt className="text-[hsl(var(--legend))]">Requested</dt>
              <dd className="font-mono">
                {trace.data.alias && trace.data.final_model
                  ? `${trace.data.alias} → ${trace.data.final_model}`
                  : trace.data.model}
              </dd>
              <dt className="text-[hsl(var(--legend))]">Served by</dt>
              <dd className="font-mono">
                {trace.data.provider || "—"}
                {trace.data.final_model ? `/${trace.data.final_model}` : ""}
              </dd>
              {trace.data.source && (
                <>
                  <dt className="text-[hsl(var(--legend))]">Source</dt>
                  <dd>{trace.data.source}</dd>
                </>
              )}
              {trace.data.path && (
                <>
                  <dt className="text-[hsl(var(--legend))]">Path</dt>
                  <dd>{trace.data.path}</dd>
                </>
              )}
              <dt className="text-[hsl(var(--legend))]">Status</dt>
              <dd>
                <Badge
                  variant={
                    trace.data.status === "success" ? "green" : "destructive"
                  }
                >
                  {trace.data.status}
                </Badge>
              </dd>
              <dt className="text-[hsl(var(--legend))]">Cost</dt>
              <dd className="font-mono">{money(trace.data.cost_micros)}</dd>
              <dt className="text-[hsl(var(--legend))]">Tokens</dt>
              <dd className="font-mono">
                {count(trace.data.tokens_in)}/{count(trace.data.tokens_out)}
                {trace.data.cache_read_tokens > 0 && (
                  // Shown because tokens_in excludes cache reads: without it
                  // the number cannot be reconciled against an invoice.
                  <span className="ml-2 text-[hsl(var(--legend))]">
                    +{count(trace.data.cache_read_tokens)} cached
                  </span>
                )}
                {(trace.data.reasoning_tokens ?? 0) > 0 && (
                  <span className="block text-[hsl(var(--legend))]">
                    of which reasoning {count(trace.data.reasoning_tokens)}
                  </span>
                )}
              </dd>
              <dt className="text-[hsl(var(--legend))]">Total</dt>
              <dd className="font-mono">{duration(trace.data.total_ms)}</dd>
            </dl>

            <section>
              <h3 className="mb-2 text-sm font-medium">Attempts</h3>
              <Ladder
                mode="retrospective"
                rows={ladderRows(trace.data.attempts, trace.data.total_ms)}
              />
            </section>

            <AttemptWaterfall trace={trace.data} />

            {trace.data.skips.length > 0 && (
              <section>
                <h3 className="mb-2 text-sm font-medium">Skipped candidates</h3>
                {/* The skips are why the ladder starts where it does. Without
                    them a first attempt at the third provider looks arbitrary. */}
                <ul className="flex flex-col gap-1 font-mono text-sm text-[hsl(var(--legend))]">
                  {trace.data.skips.map((s) => (
                    <li key={s}>{s}</li>
                  ))}
                </ul>
              </section>
            )}

            {trace.data.warnings && trace.data.warnings.length > 0 && (
              <section>
                <h3 className="mb-2 text-sm font-medium">Warnings</h3>
                <ul className="flex flex-col gap-1 text-sm">
                  {trace.data.warnings.map((warning) => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              </section>
            )}

            <section>
              <h3 className="mb-2 text-sm font-medium">Bodies</h3>
              <BodiesPanel bodies={trace.data.bodies} />
            </section>

            <SurfaceMetaSection meta={trace.data.surface_meta} />
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
