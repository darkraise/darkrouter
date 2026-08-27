import type { Preset, Provider } from "../../lib/api-types"
import { providerState, type ProviderState } from "./provider-state"

/**
 * One line of the providers list: a provider the release supports, and what
 * this gateway has done with it.
 *
 * The list is the supported set, not the database. A provider with no account
 * reads the same whether it has never been configured or was configured once
 * and emptied — those are the same fact about routing, and showing them
 * differently was the inconsistency this merge exists to remove.
 */
export type ProviderRow = {
  id: string
  name: string
  /** The preset that carries the brand mark. Empty for a provider whose
   *  preset the running build does not ship. */
  preset: string
  kind: string
  state: ProviderState
  accounts: number
  /** Null until the provider has a row, because priority is a property of
   *  the configuration rather than of the catalogue. */
  priority: number | null
  /**
   * Whether the router can pick this provider — that is, whether it has an
   * account. NOT whether it has a database row: a provider keeps its row
   * after its last key is deleted, and calling that "configured" made the
   * filter disagree with the state badge beside it on the same line.
   *
   * `provider` is what says a row exists, and it is what the detail page and
   * the per-row actions key on.
   */
  configured: boolean
  freeTier: boolean
  /** Present when the provider has a database row, with or without accounts.
   *  The detail page and the per-row actions need the real thing. */
  provider?: Provider
}

function rowFromPreset(p: Preset): ProviderRow {
  return {
    id: p.id,
    name: p.name,
    preset: p.id,
    kind: p.kind,
    state: "unconfigured",
    accounts: 0,
    priority: null,
    configured: false,
    freeTier: p.free_tier,
  }
}

function rowFromProvider(p: Provider, preset: Preset | undefined): ProviderRow {
  return {
    id: p.id,
    name: p.name,
    preset: p.preset,
    kind: p.kind,
    state: providerState(p),
    accounts: p.credentials.length,
    priority: p.priority,
    configured: p.credentials.length > 0,
    freeTier: preset?.free_tier ?? false,
    provider: p,
  }
}

/**
 * Every supported provider, with the configured ones filled in.
 *
 * Configured first, in the order the router walks them, because those are the
 * handful that carry traffic; the rest follow alphabetically. Sorting the two
 * hundred purely by name would bury the four that matter.
 *
 * A configured provider whose preset this build does not ship still appears:
 * it is serving requests, and dropping it from the list because the catalogue
 * moved would hide the one row an operator most needs to find.
 */
export function mergeProviderRows(presets: Preset[], providers: Provider[]): ProviderRow[] {
  const byId = new Map(presets.map((p) => [p.id, p]))
  const configured = providers.map((p) => rowFromProvider(p, byId.get(p.preset || p.id)))
  const taken = new Set(configured.map((r) => r.id))
  const rest = presets.filter((p) => !taken.has(p.id)).map(rowFromPreset)

  configured.sort((a, b) => (b.priority ?? 0) - (a.priority ?? 0) || a.name.localeCompare(b.name))
  rest.sort((a, b) => a.name.localeCompare(b.name))
  return [...configured, ...rest]
}

export type ProviderFilter = {
  q?: string
  state?: string
  configuredOnly?: boolean
  freeTier?: boolean
}

/** Narrowing only. Every filter absent means every row, which is what an
 *  operator arriving at the screen should see. */
export function filterProviderRows(rows: ProviderRow[], f: ProviderFilter): ProviderRow[] {
  const q = (f.q ?? "").trim().toLowerCase()
  return rows.filter((r) => {
    if (q && !r.id.toLowerCase().includes(q) && !r.name.toLowerCase().includes(q)) return false
    if (f.state && r.state !== f.state) return false
    if (f.configuredOnly && !r.configured) return false
    if (f.freeTier && !r.freeTier) return false
    return true
  })
}

/** What the count beside the filters reads. Naming the total is what tells an
 *  operator a filter is on rather than that the catalogue is small. */
export function filterSummary(shown: number, total: number): string {
  return shown === total ? `${total} providers` : `${shown} of ${total}`
}
