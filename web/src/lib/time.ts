const DAY_MS = 24 * 60 * 60 * 1000

/** Every UTC calendar day from `first` through `last`, as YYYY-MM-DD. The
 *  usage rollup keys days by UTC date and serves only days that had traffic,
 *  so a daily chart needs this to draw an idle day at all. */
export function utcDays(first: string, last: string): string[] {
  const out: string[] = []
  const end = Date.parse(`${last}T00:00:00Z`)
  for (let t = Date.parse(`${first}T00:00:00Z`); t <= end; t += DAY_MS) {
    out.push(new Date(t).toISOString().slice(0, 10))
  }
  return out
}

/** Relative until it stops being useful. "3 min ago" is the reading an
 *  operator wants while tailing; a wall clock is the one they want an hour
 *  later, and past a day the date is the only part that carries. */
export function relativeTime(ms: number, now = Date.now()): string {
  const seconds = Math.round((now - ms) / 1000)
  if (seconds < 5) return "just now"
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.round(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  return new Date(ms).toLocaleDateString()
}
