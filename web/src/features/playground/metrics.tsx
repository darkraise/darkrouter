import { api } from "../../lib/api"
import { duration } from "../../lib/format"
import type { RequestTrace } from "../../lib/api-types"

/**
 * What the last run cost, in time and tokens.
 *
 * Time is measured here because only the client knows when it asked: time to
 * first token is the reading a streaming surface is judged on, and no server
 * field carries it. Tokens come from the trace instead — the gateway counted
 * them, and a client-side guess at tokenisation would be a number that looks
 * authoritative and is not.
 */
export type StreamMetrics = {
  /** Milliseconds to the first token of the reply. Null until one arrives. */
  ttftMs: number | null
  /** Milliseconds from send to the end of the stream. */
  totalMs: number | null
  tokensIn: number | null
  tokensOut: number | null
}

export const NO_METRICS: StreamMetrics = {
  ttftMs: null,
  totalMs: null,
  tokensIn: null,
  tokensOut: null,
}

/** Output tokens per second of wall clock. Null unless both halves are known,
 *  because a rate computed from a missing count is a fabricated number. */
export function tokensPerSecond(m: StreamMetrics): number | null {
  if (m.tokensOut === null || m.totalMs === null || m.totalMs <= 0) return null
  return (m.tokensOut / m.totalMs) * 1000
}

/** The counts the gateway recorded for this run. */
export function metricsFromTrace(m: StreamMetrics, trace: RequestTrace): StreamMetrics {
  return { ...m, tokensIn: trace.tokens_in, tokensOut: trace.tokens_out }
}

/** One reading and its name. Wide enough that a number appearing does not
 *  shift the strip, because a row that reflows on every run is unreadable. */
function Cell({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-24 flex-col">
      <span className="text-sm text-[hsl(var(--legend))]">{label}</span>
      <span className="text-sm tabular-nums">{value}</span>
    </div>
  )
}

/**
 * The last run, read at a glance.
 *
 * The numbers were always here; what was missing is their shape. Time to
 * first token against total is the reading that separates a slow provider
 * from a slow generation, and it is a proportion — so it is drawn as one.
 * The bare figures stay beside it, because a bar cannot be quoted in a bug
 * report.
 */
export function MetricsStrip({ metrics }: { metrics: StreamMetrics }) {
  const tps = tokensPerSecond(metrics)
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-x-8 gap-y-3 border-b px-6 py-3">
      <LatencySplit ttftMs={metrics.ttftMs} totalMs={metrics.totalMs} />
      <TokenSplit tokensIn={metrics.tokensIn} tokensOut={metrics.tokensOut} />
      <Cell label="tokens/s" value={tps === null ? "—" : tps.toFixed(1)} />
    </div>
  )
}

/**
 * Waiting against generating.
 *
 * One bar, split where the first token arrived. A provider that took two
 * seconds to start and one to answer, and one that started at once and took
 * three, are the same total and completely different problems.
 */
function LatencySplit({ ttftMs, totalMs }: { ttftMs: number | null; totalMs: number | null }) {
  const known = ttftMs !== null && totalMs !== null && totalMs > 0
  const waitPct = known ? Math.min(100, (ttftMs / totalMs) * 100) : 0
  return (
    <div className="flex min-w-56 flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-4">
        <span className="text-sm text-[hsl(var(--legend))]">first token</span>
        <span className="text-sm tabular-nums">{duration(ttftMs)}</span>
      </div>
      <div
        className="flex h-1.5 overflow-hidden rounded-full bg-[hsl(var(--muted))]"
        role="img"
        aria-label={
          known
            ? `${duration(ttftMs)} waiting, then ${duration(totalMs - ttftMs)} generating`
            : "No timing yet"
        }
      >
        {known ? (
          <>
            <span className="bg-[hsl(var(--primary))]" style={{ width: `${waitPct}%` }} />
            <span className="bg-[hsl(var(--legend))] opacity-40" style={{ width: `${100 - waitPct}%` }} />
          </>
        ) : null}
      </div>
      <div className="flex items-baseline justify-between gap-4">
        <span className="text-sm text-[hsl(var(--legend))]">total</span>
        <span className="text-sm tabular-nums">{duration(totalMs)}</span>
      </div>
    </div>
  )
}

/** What went up against what came back. The ratio is the thing an operator
 *  is watching when a prompt grows: context in, answer out. */
function TokenSplit({ tokensIn, tokensOut }: { tokensIn: number | null; tokensOut: number | null }) {
  const known = tokensIn !== null && tokensOut !== null && tokensIn + tokensOut > 0
  const inPct = known ? (tokensIn / (tokensIn + tokensOut)) * 100 : 0
  return (
    <div className="flex min-w-56 flex-col gap-1.5">
      <div className="flex items-baseline justify-between gap-4">
        <span className="text-sm text-[hsl(var(--legend))]">tokens in</span>
        <span className="text-sm tabular-nums">{tokensIn === null ? "—" : tokensIn}</span>
      </div>
      <div
        className="flex h-1.5 overflow-hidden rounded-full bg-[hsl(var(--muted))]"
        role="img"
        aria-label={known ? `${tokensIn} tokens in, ${tokensOut} out` : "No token counts yet"}
      >
        {known ? (
          <>
            <span className="bg-[hsl(var(--legend))] opacity-40" style={{ width: `${inPct}%` }} />
            <span className="bg-[hsl(var(--success))]" style={{ width: `${100 - inPct}%` }} />
          </>
        ) : null}
      </div>
      <div className="flex items-baseline justify-between gap-4">
        <span className="text-sm text-[hsl(var(--legend))]">tokens out</span>
        <span className="text-sm tabular-nums">{tokensOut === null ? "—" : tokensOut}</span>
      </div>
    </div>
  )
}

/** How long to keep asking for the trace of a request that just finished.
 *  The log writer batches on a 250 ms timer, so the row is reliably absent at
 *  the moment the stream ends and reliably present a beat later. */
const TRACE_ATTEMPTS = 6
const TRACE_RETRY_MS = 300

/**
 * The trace for a request that has just completed, once it has been written.
 *
 * Asking immediately is a race the client loses: the executor answers from
 * memory while the record is still in the log writer's batch, so a single
 * fetch 404s and the token counts read as unknown for every run. Giving up
 * after a second and a half is deliberate — the timings are already shown, and
 * a spinner that never resolves would be worse than two em dashes.
 */
export async function traceWhenWritten(
  id: string,
  signal?: AbortSignal,
): Promise<RequestTrace | null> {
  for (let attempt = 0; attempt < TRACE_ATTEMPTS; attempt++) {
    if (signal?.aborted) return null
    // Waits first, deliberately. The writer's timer means the very first
    // attempt is not merely likely to miss, it is certain to — and a fetch
    // that 404s still prints in the browser console, so trying immediately
    // buys nothing and leaves an error beside every successful run.
    await new Promise((resolve) => setTimeout(resolve, TRACE_RETRY_MS))
    if (signal?.aborted) return null
    try {
      return await api.get<RequestTrace>(`/api/requests/${id}`)
    } catch {
      // Not written yet, or never will be. The next pass decides.
    }
  }
  return null
}
