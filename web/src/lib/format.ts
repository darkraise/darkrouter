/** One place for every reading the console prints. Four screens had grown
 *  four rounding rules for the same cost, five duration formatters, and
 *  three date conventions; an operator comparing a trace to the usage table
 *  should never have to wonder whether the numbers disagree or the
 *  formatting does. */

/** Cost in micro-dollars. `null` is "unpriced": a model with no catalog
 *  price has an unknown cost, and any dollar string would claim otherwise.
 *  Zero on a priced model is genuinely free, and says so. Below a cent the
 *  fraction is the reading, because `$0.00` for a call that cost $0.004 is
 *  the exact string that would claim it cost nothing. */
export function money(micros: number | null | undefined): string {
  if (micros === null || micros === undefined) return "—"
  if (micros === 0) return "free"
  const dollars = micros / 1_000_000
  if (dollars >= 0.01) return `$${dollars.toFixed(2)}`
  if (dollars >= 0.0001) return `$${dollars.toFixed(4)}`
  return "<$0.0001"
}

/** Catalog prices are quoted per million tokens and are much smaller than
 *  a request's cost, so they keep four places rather than rounding to a
 *  cent. The unit is the caller's to print once in a header, not per cell. */
export function pricePerMillion(micros: number | null | undefined): string {
  if (micros === null || micros === undefined) return "—"
  if (micros === 0) return "free"
  return `$${(micros / 1_000_000).toFixed(4)}`
}

/** Seconds past the thousand, because `8100 ms` makes the reader do the
 *  division. The unit is separated by a thin space so it wraps as a unit. */
export function duration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined) return "—"
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`
}

/** Same reading as `duration`, split for layouts that style the unit. */
export function durationParts(ms: number): { value: string; unit: string } {
  if (ms >= 1000) return { value: (ms / 1000).toFixed(1), unit: "s" }
  return { value: `${Math.round(ms)}`, unit: "ms" }
}

/** Token and request counts, grouped. The locale is pinned so a table does
 *  not change shape between an operator's browsers. */
export function count(n: number | null | undefined): string {
  if (n === null || n === undefined) return "—"
  return n.toLocaleString("en-US")
}

/** Counts where the column is narrow and the magnitude is the reading:
 *  a context window, a tile. Full precision belongs in `count`. */
export function compact(n: number | null | undefined): string {
  if (n === null || n === undefined) return "—"
  if (n >= 1_000_000) return `${trimZero((n / 1_000_000).toFixed(1))}M`
  if (n >= 1_000) return `${trimZero((n / 1_000).toFixed(1))}k`
  return `${n}`
}

function trimZero(s: string): string {
  return s.endsWith(".0") ? s.slice(0, -2) : s
}

/** A percentage from a fraction, one place, for rates like error share. */
export function percent(fraction: number | null | undefined): string {
  if (fraction === null || fraction === undefined || Number.isNaN(fraction)) return "—"
  return `${(fraction * 100).toFixed(1)}%`
}

/** Timestamps are shown in the browser's zone, always with the date, because
 *  a request log spans days and a bare clock time cannot say which. The
 *  zone itself is printed once per screen with `zoneLabel`, never per row. */
export function dateTime(ms: number | string | Date): string {
  const d = toDate(ms)
  const date = d.toLocaleDateString("en-CA")
  const time = d.toLocaleTimeString("en-GB", { hour12: false })
  return `${date} ${time}`
}

/** Date alone, ISO order so it sorts by eye. */
export function dateOnly(ms: number | string | Date): string {
  return toDate(ms).toLocaleDateString("en-CA")
}

/** The browser's zone as an offset, for the one label a screen prints. */
export function zoneLabel(now = new Date()): string {
  const offset = -now.getTimezoneOffset()
  if (offset === 0) return "UTC"
  const sign = offset > 0 ? "+" : "−"
  const abs = Math.abs(offset)
  const h = Math.floor(abs / 60)
  const m = abs % 60
  return `UTC${sign}${h}${m ? `:${String(m).padStart(2, "0")}` : ""}`
}

/** Server rollups are keyed on UTC calendar days (`YYYY-MM-DD`). They are
 *  shown as given; the screen says "UTC day" once so the reader knows the
 *  bucket is not their local midnight. */
export function utcDay(day: string): string {
  return day
}

function toDate(v: number | string | Date): Date {
  return v instanceof Date ? v : new Date(v)
}
