import type { BreakerEntry, DiscoveryHealthRow, ProbeResult, Provider } from "../../lib/api-types"
import { duration } from "../../lib/format"

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

/**
 * Whether a credential added here would ever be sent.
 *
 * Narrower than `!isKeyless`: an `optional` gateway serves a key better and an
 * `anonymous` one accepts the operator's own in place of the published one.
 * `none` writes no header at all, so a key stored on it is a secret kept for
 * nothing — and every `none` preset is a runtime reached by its address.
 */
export function takesCredential(p: { auth_style?: string }): boolean {
  return p.auth_style !== "none"
}

/** Discovery has failed on its latest sweeps and nothing it found is live. A
 *  provider whose sweep fails while its models stay live is serving from what
 *  it last listed; one with nothing live has nothing to serve. */
export function discoveryFailing(row: DiscoveryHealthRow | undefined): boolean {
  return row !== undefined && (row.consecutive_failures ?? 0) > 0 && row.live === 0
}

/** The four states the overview emits. `degraded` is not a synonym for
 *  `cooling`: a credential cools, a provider degrades.
 *
 *  `discovery` is optional so a caller without the sweep readings still gets
 *  the credential-based answer. */
export function providerState(p: Provider, discovery?: DiscoveryHealthRow): ProviderState {
  if (!p.enabled) return "disabled"
  // A keyless provider has no credential to say it works, so the sweep is the
  // only evidence there is. Without this every unreachable free gateway read
  // "healthy" while its probe said Forbidden.
  const keylessState: ProviderState = discoveryFailing(discovery) ? "degraded" : "healthy"
  // Keyless first: a provider that needs no key is configured the moment it
  // exists, and calling it unconfigured sends an operator looking for a
  // credential that would do nothing.
  if (p.credentials.length === 0) return isKeyless(p) ? keylessState : "unconfigured"
  // The router drops a disabled credential, so only enabled ones decide
  // whether this provider can be sent to.
  const usable = p.credentials.filter((c) => c.enabled)
  if (usable.length === 0) return isKeyless(p) ? keylessState : "degraded"
  if (usable.some((c) => c.cooling)) return "degraded"
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

/** What one breaker entry has cooled: a credential, a model, or one model on
 *  one credential. A per-model breaker carries no key, and printing only the
 *  key left those rows reading "—" with nothing to say which model it was. */
export function coolingSubject(e: BreakerEntry, keyLabel?: string): string {
  const key = keyLabel || e.key_id
  const model = e.model || "all models"
  return key ? `${key}/${model}` : model
}

/** The heading over a set of breaker entries, named for what is cooling. A
 *  keyless provider's breakers are all per-model, and "2 credentials cooling"
 *  beside "none configured" contradicted itself. */
export function coolingTitle(entries: BreakerEntry[]): string {
  const n = entries.length
  const keyed = entries.filter((e) => e.key_id).length
  const noun =
    keyed === n
      ? n === 1 ? "credential" : "credentials"
      : keyed === 0
        ? n === 1 ? "model" : "models"
        : n === 1 ? "breaker" : "breakers"
  return `${n} ${noun} cooling`
}

/** One provider's discovery health, reduced to what the table cell shows. */
export function discoveryLine(row: DiscoveryHealthRow | undefined): string {
  // Absence is the signal: "0 of 0 live" would read as a sweep that ran and
  // found nothing, which is a different fact from one that never ran.
  if (!row) return "never discovered"
  // Before the counts: a provider whose every sweep fails has no models, and
  // "0 of 0 live" is the wording for one that answered with an empty list.
  if (discoveryFailing(row)) {
    return row.last_error ? `discovery failing · ${row.last_error}` : "discovery failing"
  }
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

export type ProbeOutcome = { kind: "success" | "error"; message: string }

/** What a probe proved, as the toast should say it. A refused credential
 *  arrives as a 200 with `ok:false` — the button exists to discover exactly
 *  that — so the verdict is read from the body, never from the status.
 *
 *  `subject` is the provider probed. Without a credential there is nothing to
 *  have been accepted, and "Credential accepted" on a keyless runtime claimed
 *  a check that never happened. */
export function probeOutcome(
  result: ProbeResult,
  subject?: { auth_style?: string; credentials?: readonly unknown[] },
): ProbeOutcome {
  const credentialled = subject === undefined || (subject.credentials?.length ?? 0) > 0
  if (!result.ok) {
    return {
      kind: "error",
      message:
        result.error ||
        (credentialled ? "the provider refused the credential" : "the provider refused the request"),
    }
  }
  const parts = [credentialled ? "Credential accepted" : "Endpoint answered"]
  if (result.model_count !== undefined) parts.push(`${result.model_count} models`)
  parts.push(duration(result.latency_ms))
  return { kind: "success", message: parts.join(" · ") }
}
