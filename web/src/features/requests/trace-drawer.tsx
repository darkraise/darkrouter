import {
  Badge,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
} from "darkraise-ui"
import { useTrace } from "../../lib/queries"
import type { TraceAttempt } from "../../lib/api-types"
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
    reasonProse:
      a.error ||
      `${a.latency_ms.toLocaleString()}ms attempt${a.path ? ` · ${a.path}` : ""}`,
    latencyPx: Math.max(2, (a.latency_ms / slowest) * MAX_BAR),
    // Everything after the attempt that served was never sent.
    terminated: servedAt !== -1 && i > servedAt,
  }))
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

            {trace.data.skips.length > 0 && (
              <section>
                <h3 className="mb-2 text-sm font-medium">Skipped candidates</h3>
                {/* The skips are why the ladder starts where it does. Without
                    them a first attempt at the third provider looks arbitrary. */}
                <ul className="flex flex-col gap-1 font-mono text-xs text-[hsl(var(--legend))]">
                  {trace.data.skips.map((s) => (
                    <li key={s}>{s}</li>
                  ))}
                </ul>
              </section>
            )}

            {trace.data.warnings && trace.data.warnings.length > 0 && (
              <section>
                <h3 className="mb-2 text-sm font-medium">Warnings</h3>
                <ul className="flex flex-col gap-1 text-xs">
                  {trace.data.warnings.map((warning) => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              </section>
            )}
          </div>
        )}
      </SheetContent>
    </Sheet>
  )
}
