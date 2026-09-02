import type { Preset, Provider } from "../../lib/api-types"
import { isKeyless, providerState, type ProviderState } from "./provider-state"

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
  /** How the provider is reached — the axis an operator filters by when they
   *  are looking for "the ones I run myself" or "the ones with a browser
   *  flow" rather than for a name. */
  connection: ConnectionType
  /** Whether it serves a request with no credential. Carried on the row
   *  because the actions turn on it: there is nothing to add to a provider
   *  that asks for nothing, so the row offers the test instead. */
  keyless: boolean
  /** Present when the provider has a database row, with or without accounts.
   *  The detail page and the per-row actions need the real thing. */
  provider?: Provider
}

function rowFromPreset(p: Preset): ProviderRow {
  return {
    connection: connectionType(p),
    keyless: isKeyless({ auth_style: p.auth_kind }),
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
    // The provider's own base URL, not the preset's: an operator who pointed
    // a preset at their own machine has a local provider, whatever the
    // release shipped.
    connection: connectionType({ base_url: p.base_url, auth_kind: p.auth_style }),
    keyless: isKeyless(p),
    id: p.id,
    name: p.name,
    preset: p.preset,
    kind: p.kind,
    state: providerState(p),
    accounts: p.credentials.length,
    priority: p.priority,
    // A keyless provider is configured with no accounts at all: there is
    // nothing an operator could add that would change how it is reached.
    configured: p.credentials.length > 0 || isKeyless(p),
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
  connection?: string
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
    if (f.connection && r.connection !== f.connection) return false
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

/**
 * How a provider is reached, as an operator thinks of it.
 *
 * Derived rather than stored: every fact it needs is already on the preset,
 * and a second field would be one more thing to keep in step with the first.
 *
 *  - `local` is a program the operator runs themselves. Read off the address
 *    rather than a flag, because that is what actually makes it local — a
 *    loopback URL cannot be anybody else's machine, and a base URL that names
 *    no host at all (auggie://cli/v1) is a command on this box.
 *  - `oauth` and `signed` are the two that are not a secret you paste once:
 *    one mints short-lived tokens through a browser flow, the other signs
 *    every request from a key pair.
 *  - `none` covers the three styles that need nothing pasted: one that asks
 *    for nothing, one that answers without a key and answers better with one,
 *    and one that demands a key the vendor publishes and the release ships. To
 *    an operator scanning for "what can I just add", they are the same answer.
 *  - `key` is everything else, which is most of the catalogue.
 */
export type ConnectionType = "key" | "oauth" | "signed" | "none" | "local"

export const CONNECTION_LABEL: Record<ConnectionType, string> = {
  key: "API key",
  oauth: "OAuth",
  signed: "Signed",
  none: "No auth",
  local: "Local",
}

/** What each chip means, for the reader the one-word label does not reach.
 *  "Signed" in particular names two schemes an operator knows by their own
 *  names, not by what they have in common. */
export const CONNECTION_DESCRIPTION: Record<ConnectionType, string> = {
  key: "A key pasted once and sent with every request",
  oauth: "A browser sign-in that mints short-lived tokens",
  signed: "SigV4 and service-account credentials",
  none: "Reached with nothing pasted",
  local: "A program running on this machine",
}

const LOOPBACK = /^https?:\/\/(localhost|127\.0\.0\.1|\[::1\]|0\.0\.0\.0)(:|\/|$)/i

/** Anything darkrouter reaches over the network. A base URL that is not one of
 *  these names a transport of its own — today the local-CLI scheme. */
const HTTP_URL = /^https?:\/\//i

export function connectionType(p: {
  base_url?: string
  auth_kind?: string
}): ConnectionType {
  if (p.base_url && (LOOPBACK.test(p.base_url) || !HTTP_URL.test(p.base_url))) return "local"
  switch (p.auth_kind) {
    case "oauth":
      return "oauth"
    case "sigv4":
    case "gcp-sa":
      return "signed"
    case "none":
    case "optional":
    case "anonymous":
      return "none"
    default:
      return "key"
  }
}

/** The counts behind the quick filters, so a chip can say how many rows it
 *  would leave and an empty one is visibly empty before it is clicked. */
export function connectionCounts(rows: ProviderRow[]): Record<ConnectionType, number> {
  const out: Record<ConnectionType, number> = { key: 0, oauth: 0, signed: 0, none: 0, local: 0 }
  for (const r of rows) out[r.connection]++
  return out
}
