import type { BreakerEntry, DiscoveryHealthRow, Provider } from "../../lib/api-types"

export type ProviderState = "healthy" | "degraded" | "disabled" | "unconfigured"

/**
 * A provider that serves a request with no credential the operator supplies.
 *
 * Three styles qualify and they are not the same offer. `none` asks for
 * nothing and would ignore a key. `optional` answers without one and answers
 * better with one — a free gateway whose limits rise when it knows who is
 * calling — so it is keyless without being credential-free. `anonymous`
 * insists on a key and publishes it, so the release ships the string and the
 * operator still pastes nothing.
 */
export function isKeyless(p: { auth_style?: string }): boolean {
  return (
    p.auth_style === "none" || p.auth_style === "optional" || p.auth_style === "anonymous"
  )
}

/** The four states the overview emits. `degraded` is not a synonym for
 *  `cooling`: a credential cools, a provider degrades. */
export function providerState(p: Provider): ProviderState {
  if (!p.enabled) return "disabled"
  // Keyless first: a provider that needs no key is configured the moment it
  // exists, and calling it unconfigured sends an operator looking for a
  // credential that would do nothing.
  if (p.credentials.length === 0) return isKeyless(p) ? "healthy" : "unconfigured"
  if (p.credentials.some((c) => c.cooling)) return "degraded"
  return "healthy"
}

export const STATE_VARIANT = {
  healthy: "green",
  degraded: "amber",
  disabled: "secondary",
  // Neutral, not destructive. The list holds every provider the release
  // supports, so most rows are unconfigured at any moment -- and red across
  // two hundred of them says something is broken when nothing is. Red is for
  // a provider that failed, not for one nobody has set up.
  unconfigured: "outline",
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
  // A sweep that imported nothing because the free filter dropped everything
  // is not an empty provider. Saying "0 of 0 live" for it sends an operator
  // to look at a listing endpoint that is working perfectly.
  if (row.total === 0 && row.filtered_out > 0) {
    return `no free models · ${row.filtered_out} paid, not imported`
  }
  const parts = [`${row.live} of ${row.total} live`]
  if (row.filtered_out > 0) parts.push(`${row.filtered_out} filtered out`)
  if (row.stale > 0) parts.push(`${row.stale} stale`)
  if (row.removed_upstream > 0) parts.push(`${row.removed_upstream} removed upstream`)
  if (row.max_missing_streak > 0) {
    parts.push(`missing for ${row.max_missing_streak} sweeps`)
  }
  return parts.join(" · ")
}
