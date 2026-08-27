import type { BreakerEntry, DiscoveryHealthRow, Model, Provider, UsageRow } from "../../lib/api-types"

/** Daily request totals for one provider, in day order so the sparkline's
 *  x-axis is time. Days the provider served nothing are absent from the
 *  rollup rather than zero, and dropping them would compress the shape. */
export function requestsByDay(rows: UsageRow[], providerId: string): number[] {
  const acc = new Map<string, number>()
  for (const row of rows) {
    if (row.key !== providerId) continue
    acc.set(row.day, (acc.get(row.day) ?? 0) + row.requests)
  }
  return [...acc.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([, v]) => v)
}

export function totalRequests(rows: UsageRow[], providerId: string): number {
  return requestsByDay(rows, providerId).reduce((n, v) => n + v, 0)
}

/** The models this provider serves. The catalog is one row per model listing
 *  every provider that offers it, so this is a filter rather than a lookup. */
export function modelsFor(models: Model[], providerId: string): Model[] {
  return models
    .filter((m) => m.providers.includes(providerId))
    .sort((a, b) => a.model.localeCompare(b.model))
}

export type CapabilityCount = { tools: number; vision: number; reasoning: number; total: number }

/** How much of what this provider offers can do each thing. A count without
 *  its denominator cannot answer "can I route tools here", which is the
 *  question the number is on the page to answer. */
export function capabilityCount(models: Model[]): CapabilityCount {
  return {
    tools: models.filter((m) => m.tools).length,
    vision: models.filter((m) => m.vision).length,
    reasoning: models.filter((m) => m.reasoning).length,
    total: models.length,
  }
}

export type AccountSummary = { total: number; usable: number; cooling: number; disabled: number }

/**
 * Accounts by what the router can currently do with them.
 *
 * `usable` is the one that matters: an enabled credential that is cooling is
 * not one the router can send to right now, so it counts as neither enabled
 * nor gone.
 */
export function accountSummary(p: Provider, cooling: BreakerEntry[]): AccountSummary {
  const coolingIds = new Set(cooling.map((c) => c.key_id))
  const disabled = p.credentials.filter((c) => !c.enabled).length
  const cool = p.credentials.filter((c) => c.enabled && (c.cooling || coolingIds.has(c.id))).length
  return {
    total: p.credentials.length,
    usable: p.credentials.length - disabled - cool,
    cooling: cool,
    disabled,
  }
}

/** The discovery reading as a fraction, or null when no sweep has ever run —
 *  which is a different fact from a sweep that found nothing. */
export function discoveryFraction(row: DiscoveryHealthRow | undefined): string | null {
  if (!row) return null
  return `${row.live}/${row.total}`
}

/** What the discovery stat says beneath its number. The filtered case comes
 *  first: it is the one an operator would otherwise misread as a fault. */
export function discoveryNote(row: DiscoveryHealthRow | undefined): string {
  if (!row) return "never discovered"
  if (row.total === 0 && row.filtered_out > 0) {
    return `none free of ${row.filtered_out} listed`
  }
  if (row.filtered_out > 0) return `${row.filtered_out} paid, not imported`
  if (row.max_missing_streak > 0) return `missing for ${row.max_missing_streak} sweeps`
  return "live of known"
}
