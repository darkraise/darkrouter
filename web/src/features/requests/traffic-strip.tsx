import type { TableRow } from "./requests-columns"

/**
 * The page of requests, as a shape.
 *
 * A table answers "what happened to this request". It cannot answer "what has
 * the last few minutes looked like" — that question is about the rows
 * together, and reading two hundred timestamps is not how anyone answers it.
 *
 * One bar per bucket, height by count, and a bar holding a failure keeps the
 * reserved destructive tone: a burst of errors is a shape an operator sees
 * before they have read a single row.
 */
export type Bucket = { at: number; total: number; failed: number }

/** Buckets over the span the loaded rows actually cover, so the strip is a
 *  picture of the page rather than of a clock. Fewer rows than buckets is not
 *  a problem: an empty bucket is a real gap in traffic and reads as one. */
export function bucketRequests(rows: TableRow[], count = 40): Bucket[] {
  if (rows.length === 0) return []
  const times = rows.map((r) => r.ts_ms)
  const from = Math.min(...times)
  const to = Math.max(...times)
  // A page spanning no time at all is one bucket, not a division by zero.
  const span = Math.max(to - from, 1)
  const width = span / count
  const out: Bucket[] = Array.from({ length: count }, (_, i) => ({
    at: from + i * width,
    total: 0,
    failed: 0,
  }))
  for (const r of rows) {
    const i = Math.min(count - 1, Math.floor((r.ts_ms - from) / width))
    const bucket = out[i]
    if (!bucket) continue
    bucket.total++
    if (r.status !== "success") bucket.failed++
  }
  return out
}

/**
 * How to label the ends of the strip.
 *
 * A page of requests can span minutes or days, and the clock alone cannot
 * tell them apart: "7:10 PM → 7:14 PM" reads as four minutes whether the
 * requests are four minutes or four days apart, which is exactly the mistake
 * the strip exists to prevent. The date appears only when it changes, because
 * on the common case — a busy few minutes — it is noise.
 */
export function edgeLabels(fromMs: number, toMs: number): [string, string] {
  const from = new Date(fromMs)
  const to = new Date(toMs)
  const sameDay = from.toDateString() === to.toDateString()
  const fmt = (d: Date) =>
    sameDay ? d.toLocaleTimeString() : `${d.toLocaleDateString()} ${d.toLocaleTimeString()}`
  return [fmt(from), fmt(to)]
}

export function TrafficStrip({ rows }: { rows: TableRow[] }) {
  const buckets = bucketRequests(rows)
  if (buckets.length === 0) return null
  const peak = Math.max(...buckets.map((b) => b.total), 1)
  // The rows' own extremes, not the buckets': the last bucket's `at` is where
  // it starts, so labelling with it puts the right-hand caption one bucket
  // before the newest request it is describing.
  const [fromLabel, toLabel] = edgeLabels(
    Math.min(...rows.map((r) => r.ts_ms)),
    Math.max(...rows.map((r) => r.ts_ms)),
  )

  return (
    <figure className="mb-4">
      <div className="flex h-12 items-end gap-px" role="img" aria-label={`${rows.length} requests over the loaded page`}>
        {buckets.map((b) => (
          <span
            key={b.at}
            title={
              b.total === 0
                ? "no requests"
                : `${b.total} request${b.total === 1 ? "" : "s"}${b.failed > 0 ? `, ${b.failed} failed` : ""}`
            }
            className={
              b.failed > 0
                ? "flex-1 rounded-t-[2px] bg-[hsl(var(--destructive))]"
                : "flex-1 rounded-t-[2px] bg-[hsl(var(--primary))]"
            }
            // A floor of two pixels: a bucket that served one request must not
            // round to the same nothing as a bucket that served none.
            style={{ height: b.total === 0 ? 1 : `${Math.max((b.total / peak) * 100, 8)}%` }}
          />
        ))}
      </div>
      <figcaption className="mt-1 flex justify-between text-sm text-[hsl(var(--legend))]">
        <span>{fromLabel}</span>
        <span>
          {rows.length} loaded · peak {peak}
        </span>
        <span>{toLabel}</span>
      </figcaption>
    </figure>
  )
}
