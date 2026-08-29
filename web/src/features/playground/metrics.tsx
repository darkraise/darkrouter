import { api } from "../../lib/api"
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

function reading(value: number | null, unit: string, digits = 0): string {
  if (value === null) return "—"
  return `${value.toFixed(digits)} ${unit}`
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

export function MetricsStrip({ metrics }: { metrics: StreamMetrics }) {
  const tps = tokensPerSecond(metrics)
  return (
    <div className="flex flex-wrap items-baseline gap-x-6 gap-y-2 border-b px-6 py-3">
      <Cell
        label="first token"
        value={
          metrics.ttftMs === null
            ? "—"
            : metrics.ttftMs >= 1000
              ? reading(metrics.ttftMs / 1000, "s", 1)
              : reading(metrics.ttftMs, "ms")
        }
      />
      <Cell
        label="total"
        value={
          metrics.totalMs === null
            ? "—"
            : metrics.totalMs >= 1000
              ? reading(metrics.totalMs / 1000, "s", 1)
              : reading(metrics.totalMs, "ms")
        }
      />
      <Cell label="tokens in" value={metrics.tokensIn === null ? "—" : String(metrics.tokensIn)} />
      <Cell
        label="tokens out"
        value={metrics.tokensOut === null ? "—" : String(metrics.tokensOut)}
      />
      <Cell label="tokens/s" value={tps === null ? "—" : tps.toFixed(1)} />
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
export async function traceWhenWritten(id: string): Promise<RequestTrace | null> {
  for (let attempt = 0; attempt < TRACE_ATTEMPTS; attempt++) {
    // Waits first, deliberately. The writer's timer means the very first
    // attempt is not merely likely to miss, it is certain to — and a fetch
    // that 404s still prints in the browser console, so trying immediately
    // buys nothing and leaves an error beside every successful run.
    await new Promise((resolve) => setTimeout(resolve, TRACE_RETRY_MS))
    try {
      return await api.get<RequestTrace>(`/api/requests/${id}`)
    } catch {
      // Not written yet, or never will be. The next pass decides.
    }
  }
  return null
}

