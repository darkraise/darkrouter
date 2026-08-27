import {
  Badge,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "darkraise-ui"
import { Fragment } from "react"
import { Link } from "@tanstack/react-router"
import { useTrace } from "../../lib/queries"
import type { RequestTrace, TraceAttempt, TraceBody } from "../../lib/api-types"
import { Ladder, type LadderRow, type RetrospectiveMark } from "../ladder/ladder"

/** The widest a latency bar may be drawn, in pixels. Bars are relative to the
 *  slowest attempt in this trace, so one trace's shape is readable rather than
 *  every trace sharing an absolute scale nothing fits. */
const MAX_BAR = 96

function markFor(a: TraceAttempt): RetrospectiveMark {
  if (a.outcome === "success") return "served"
  // Every mark here is filled: these attempts happened. A hollow mark would
  // claim the router only considered this provider.
  return "failed"
}

/** Attempts become ladder rows. The serving attempt is the one whose outcome
 *  says so, not the last one: a request can fail after something served. */
export function ladderRows(
  attempts: TraceAttempt[],
): LadderRow<RetrospectiveMark>[] {
  const slowest = Math.max(...attempts.map((a) => a.latency_ms), 1)
  const servedAt = attempts.findIndex((a) => a.outcome === "success")
  return attempts.map((a, i) => ({
    rank: a.seq + 1,
    mark: markFor(a),
    target: `${a.provider}/${a.model}`,
    reasonCode: a.status_code > 0 ? String(a.status_code) : a.outcome,
    reasonProse: [
      a.error || `${a.latency_ms.toLocaleString()}ms attempt`,
      a.path || "",
      // A failed attempt that burned tokens still cost money, and the ladder
      // row is the only place that spend is attributable to a provider.
      a.cost_micros === null ? "" : `$${(a.cost_micros / 1_000_000).toFixed(4)}`,
    ]
      .filter(Boolean)
      .join(" · "),
    latencyPx: Math.max(2, (a.latency_ms / slowest) * MAX_BAR),
    // Everything after the attempt that served was never sent.
    terminated: servedAt !== -1 && i > servedAt,
  }))
}

/**
 * The request-level waterfall.
 *
 * §6.3 describes connect, first-token and total per attempt. Only the request
 * carries a first-token measurement and nothing records connect timing at all,
 * so this draws the two facts that exist; per-attempt duration stays on the
 * ladder rows, which is where it already is.
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

export function BodiesPanel({ bodies }: { bodies?: TraceBody[] }) {
  if (bodies === undefined || bodies.length === 0) {
    return (
      <p className="text-sm text-[hsl(var(--muted-foreground))]">
        Bodies were not captured. <code>capture.bodies</code> has a retention
        sweep and no writer, so nothing in the gateway records them yet — this
        panel is empty for every request, not just this one.
      </p>
    )
  }
  return (
    <div className="flex flex-col gap-3">
      {bodies.map((b, i) => (
        // Keyed on index too: nothing writes bodies today, but two of the
        // same kind (e.g. two tool-call turns) would otherwise collide.
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
 * `capture.bodies`'s NOT NULL column forces an absent map to `{}` on write
 * (internal/store/log.go), so `surface_meta` is present-but-empty for the
 * overwhelming majority of requests. A truthy check on the object would pass
 * for `{}` and draw a heading over an empty list, the same rendering-fault
 * mistake the Bodies panel exists to avoid — so this checks key count and
 * renders nothing, heading included, when there is nothing to show.
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
            <dd className="font-mono">{String(value)}</dd>
          </Fragment>
        ))}
      </dl>
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
  const waterfall = trace.data ? waterfallRows(trace.data) : []

  return (
    <Sheet open={id !== null} onOpenChange={(open) => !open && onClose()}>
      <SheetContent className="w-full max-w-3xl overflow-y-auto">
        <SheetHeader>
          <SheetTitle className="flex items-center gap-4 font-mono text-sm">
            {id}
            {trace.data && (
              <Link
                to="/playground"
                search={{ seed: trace.data.id }}
                className="text-sm underline"
              >
                Open in playground
              </Link>
            )}
          </SheetTitle>
        </SheetHeader>

        {trace.isError && (
          <p className="mt-4 text-sm text-[hsl(var(--muted-foreground))]">
            This request is no longer in the log. Retention has passed it.
          </p>
        )}

        {trace.data && (
          <div className="mt-4 flex flex-col gap-6">
            <dl className="grid grid-cols-2 gap-x-6 gap-y-2 text-sm">
              <dt className="text-[hsl(var(--legend))]">Requested</dt>
              <dd className="font-mono">
                {trace.data.alias
                  ? `${trace.data.alias} → ${trace.data.model}`
                  : trace.data.model}
              </dd>
              <dt className="text-[hsl(var(--legend))]">Served by</dt>
              <dd className="font-mono">{trace.data.provider || "—"}</dd>
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
              <dd className="font-mono">
                {/* Unpriced is not free: a model with no catalog price cost an
                    unknown amount, and $0.0000 would be a different claim. */}
                {trace.data.cost_micros === null
                  ? "—"
                  : `$${(trace.data.cost_micros / 1_000_000).toFixed(4)}`}
              </dd>
              <dt className="text-[hsl(var(--legend))]">Tokens</dt>
              <dd className="font-mono">
                {trace.data.tokens_in}/{trace.data.tokens_out}
                {trace.data.cache_read_tokens > 0 && (
                  // Shown because tokens_in excludes cache reads: without it
                  // the number cannot be reconciled against an invoice.
                  <span className="ml-2 text-[hsl(var(--legend))]">
                    +{trace.data.cache_read_tokens} cached
                  </span>
                )}
              </dd>
            </dl>

            <section>
              <h3 className="mb-2 text-sm font-medium">Attempts</h3>
              <Ladder mode="retrospective" rows={ladderRows(trace.data.attempts)} />
            </section>

            {waterfall.length > 0 && (
              <section>
                <h3 className="mb-2 text-sm font-medium">Latency</h3>
                <div className="flex flex-col gap-2">
                  {waterfall.map((row) => (
                    <div key={row.label} className="flex flex-col gap-1">
                      <p className="text-sm text-[hsl(var(--legend))]">
                        {row.label} · {row.ms.toLocaleString()}ms
                      </p>
                      <div className="h-2 w-full rounded bg-[hsl(var(--muted))]">
                        <div
                          className="h-2 rounded bg-[hsl(var(--legend))]"
                          style={{ width: `${row.fraction * 100}%` }}
                        />
                      </div>
                    </div>
                  ))}
                </div>
              </section>
            )}

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
