import type { BreakerEntry, DiscoveryHealthRow, Provider } from "../../lib/api-types"

/** The four states the overview emits. `degraded` is not a synonym for
 *  `cooling`: a credential cools, a provider degrades. */
export function providerState(
  p: Provider,
): "healthy" | "degraded" | "disabled" | "unconfigured" {
  if (!p.enabled) return "disabled"
  if (p.credentials.length === 0) return "unconfigured"
  if (p.credentials.some((c) => c.cooling)) return "degraded"
  return "healthy"
}

export const STATE_VARIANT = {
  healthy: "green",
  degraded: "amber",
  disabled: "secondary",
  unconfigured: "destructive",
} as const

/** Breaker rows for one provider, so the panel sits beside its subject rather
 *  than on a destination of its own. */
export function breakersFor(
  entries: BreakerEntry[],
  providerID: string,
): BreakerEntry[] {
  return entries.filter((e) => e.provider_id === providerID && e.cooling_until)
}

/** One provider's discovery health, reduced to what the table cell shows. */
export function discoveryLine(row: DiscoveryHealthRow | undefined): string {
  // Absence is the signal: "0 of 0 live" would read as a sweep that ran and
  // found nothing, which is a different fact from one that never ran.
  if (!row) return "never discovered"
  const parts = [`${row.live} of ${row.total} live`]
  if (row.stale > 0) parts.push(`${row.stale} stale`)
  if (row.removed_upstream > 0) parts.push(`${row.removed_upstream} removed upstream`)
  if (row.max_missing_streak > 0) {
    parts.push(`missing for ${row.max_missing_streak} sweeps`)
  }
  return parts.join(" · ")
}
