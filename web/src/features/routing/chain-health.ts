import type { Credential, Model, Provider } from "../../lib/api-types"

/**
 * What the router would make of one target, right now.
 *
 * `routable` and `any-provider` are the two ways a target works; everything
 * else is a reason it would not be chosen, ordered from "deliberate" to
 * "broken". They are distinguished because they call for different actions:
 * a cooling provider needs waiting for, a disabled one needs enabling, and a
 * missing one needs fixing in the chain.
 */
export type TargetState =
  | "routable"
  | "any-provider"
  | "cooling"
  | "provider-disabled"
  | "provider-unconfigured"
  | "model-missing"
  | "provider-missing"
  | "model-retired"
  | "unresolved"
  | "unknown"
  | "blank"

export type TargetFacts = {
  raw: string
  /** The provider the router would pin this to, when the target names one. */
  providerId: string | null
  /** What is left after the provider prefix, or the whole string. */
  model: string
  state: TargetState
  /** Providers that offer it, when the target names none. */
  offeredBy: string[]
  /** One line, in an operator's terms, or "" when the target is fine. */
  problem: string
}

/** Everything a target is judged against. Cooling is read from each
 *  credential's own flag rather than from the breaker endpoint: the provider
 *  rows already carry it, and a second source would be a second thing to keep
 *  in step. */
export type ChainCredential = Pick<Credential, "enabled" | "cooling">
export type ChainProvider = Pick<Provider, "id" | "enabled"> & { credentials: ChainCredential[] }
export type ChainModel = Pick<Model, "model" | "providers" | "state">

/** Only the fields the judgement reads. Named narrowly so a caller holding
 *  the provider ids alone can stand rows in without fabricating the rest of
 *  a Provider, and the live rows still fit as they are. */
export type ChainContext = {
  providers: ChainProvider[]
  models: ChainModel[]
}

/**
 * Split a target the way `router.resolveDirect` does.
 *
 * On the FIRST slash, and only when the prefix names a configured provider.
 * Model identifiers legitimately contain slashes
 * (`meta-llama/Llama-3.3-70B-Instruct-Turbo`), so a non-matching prefix has to
 * fall through with the full string intact rather than be read as a provider
 * nobody has heard of.
 */
export function splitTarget(
  raw: string,
  providerIds: string[],
): { providerId: string | null; model: string } {
  const slash = raw.indexOf("/")
  if (slash < 0) return { providerId: null, model: raw }
  const prefix = raw.slice(0, slash)
  if (!providerIds.includes(prefix)) return { providerId: null, model: raw }
  return { providerId: prefix, model: raw.slice(slash + 1) }
}

/** Credentials the router would actually dispatch to. A disabled one is not a
 *  quiet one: `provider.enabledOnly` drops it, and a provider left with none is
 *  dropped from the provider set entirely, so it can never serve. */
function usableCredentials(p: ChainProvider) {
  return p.credentials.filter((c) => c.enabled)
}

/** A provider the router could dispatch to at all: switched on, and holding at
 *  least one enabled credential that is not cooling. */
function live(p: ChainProvider): boolean {
  return p.enabled && usableCredentials(p).some((c) => !c.cooling)
}

/** The catalogue rows for one model on one provider that the router would
 *  still consider. `catalog.Model.Routable()` is `State != "removed_upstream"`,
 *  so `stale` still routes and only a model withdrawn upstream does not. */
function routableRow(models: ChainModel[], model: string, providerID: string) {
  return models.find(
    (m) => m.model === model && m.providers.includes(providerID) && m.state !== "removed_upstream",
  )
}

export function targetFacts(raw: string, ctx: ChainContext): TargetFacts {
  const trimmed = raw.trim()
  const providerIds = ctx.providers.map((p) => p.id)
  const { providerId, model } = splitTarget(trimmed, providerIds)
  const base = { raw: trimmed, providerId, model, offeredBy: [] as string[] }

  if (trimmed === "") {
    return { ...base, state: "blank", problem: "" }
  }

  // Nothing can be judged before the provider set has arrived, and judging it
  // anyway condemns every target on a cold load. An empty set is also what a
  // brand-new gateway has, where the server is a better authority than a guess
  // made here.
  if (ctx.providers.length === 0) {
    return { ...base, state: "unknown", problem: "" }
  }

  if (providerId !== null) {
    const provider = ctx.providers.find((p) => p.id === providerId)
    // splitTarget only returns a providerId it found in the same list, so
    // this cannot miss; the check narrows the type rather than guarding.
    if (!provider) return { ...base, state: "provider-missing", problem: "" }
    if (!provider.enabled) {
      return { ...base, state: "provider-disabled", problem: `${providerId} is disabled` }
    }
    const usable = usableCredentials(provider)
    if (usable.length === 0) {
      return {
        ...base,
        state: "provider-unconfigured",
        problem:
          provider.credentials.length === 0
            ? `${providerId} has no credentials`
            : `every ${providerId} credential is disabled`,
      }
    }
    // Only judged against a catalogue that has loaded: an empty one means
    // discovery has not run, not that the model does not exist.
    if (ctx.models.length > 0 && !routableRow(ctx.models, model, providerId)) {
      const retired = ctx.models.some(
        (m) => m.model === model && m.providers.includes(providerId),
      )
      return retired
        ? {
            ...base,
            state: "model-retired",
            problem: `${providerId} no longer offers ${model} — it was withdrawn upstream`,
          }
        : {
            ...base,
            state: "model-missing",
            problem: `${providerId} does not offer ${model}`,
          }
    }
    if (!live(provider)) {
      return { ...base, state: "cooling", problem: `every ${providerId} credential is cooling` }
    }
    return { ...base, state: "routable", problem: "" }
  }

  // Any remaining slash is fatal, whatever the catalogue says. The alias API
  // is stricter than the router here: `aliasTargetsExist` splits every target
  // on the first slash unconditionally and rejects the write when the prefix
  // is not a configured provider, so a catalogued model id that merely
  // contains a slash (`meta-llama/Llama-3.3-70B`) cannot be an alias target
  // however happily the router would resolve it. Saying so here is the
  // difference between an inline message and a 400 on Save.
  if (trimmed.includes("/")) {
    const prefix = trimmed.slice(0, trimmed.indexOf("/"))
    return {
      ...base,
      state: "provider-missing",
      problem: `no provider named ${prefix} is configured, and an alias target containing a slash has to name one`,
    }
  }

  const offeredBy = ctx.models.find((m) => m.model === trimmed && m.state !== "removed_upstream")
    ?.providers ?? []
  if (offeredBy.length === 0) {
    // Nothing can be said about a bare name until discovery has imported
    // something. Calling it unresolved would condemn every chain on a gateway
    // whose first sweep has not finished.
    if (ctx.models.length === 0) {
      return { ...base, offeredBy, state: "unknown", problem: "" }
    }
    return {
      ...base,
      offeredBy,
      state: "unresolved",
      problem: `no configured provider offers ${trimmed}`,
    }
  }
  const usable = ctx.providers.filter((p) => offeredBy.includes(p.id) && live(p))
  if (usable.length === 0) {
    return {
      ...base,
      offeredBy,
      state: "cooling",
      problem: `nothing offering ${trimmed} can be dispatched to right now`,
    }
  }
  return { ...base, offeredBy, state: "any-provider", problem: "" }
}

/**
 * States that block a save.
 *
 * Only what the server itself would reject. `aliasTargetsExist` checks one
 * thing — that a slash-qualified target names a configured provider — and
 * accepts every bare name without looking, so `unresolved` is a warning to
 * show rather than a write to prevent. Blocking it would strand an operator
 * naming a model the next discovery sweep will import, and would disable Save
 * for every other chain on the page along with it.
 */
const FATAL: TargetState[] = ["provider-missing"]

export function isFatal(state: TargetState): boolean {
  return FATAL.includes(state)
}
