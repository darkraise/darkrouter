import type { BreakerEntry, Credential, Model, Provider } from "../../lib/api-types"
import { isKeyless } from "../providers/provider-state"

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

/** Everything a target is judged against. A credential's own flag covers a
 *  breaker tripped for the whole key; `id` is what ties it to the per-model
 *  breakers, and is optional only so a caller standing rows in can omit it. */
export type ChainCredential = Pick<Credential, "enabled" | "cooling"> & { id?: string }
/** `auth_style` is optional for the same reason. Absent reads as keyed: the
 *  stricter assumption, and the one a stood-in row has always had. */
export type ChainProvider = Pick<Provider, "id" | "enabled"> &
  Partial<Pick<Provider, "auth_style">> & { credentials: ChainCredential[] }
export type ChainModel = Pick<Model, "model" | "providers" | "state">
export type ChainBreaker = Pick<BreakerEntry, "provider_id" | "key_id" | "model" | "cooling_until">

/** Only the fields the judgement reads. Named narrowly so a caller holding
 *  the provider ids alone can stand rows in without fabricating the rest of
 *  a Provider, and the live rows still fit as they are. */
export type ChainContext = {
  providers: ChainProvider[]
  models: ChainModel[]
  /** `/api/health/providers`. Breakers are keyed per (provider, key, model),
   *  which a credential flag cannot express: a keyless provider has no
   *  credential row to carry one, and one model cooling on a provider leaves
   *  its other models routable. Absent means nothing is known to be cooling
   *  beyond what the credential flags say. */
  breakers?: ChainBreaker[]
  /** The instant `cooling_until` is compared against. Defaults to now; a
   *  field so a test does not depend on the clock. */
  now?: number
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

/**
 * The credential slots the router would walk on one provider.
 *
 * Its enabled credentials -- or, for a keyless provider holding none, the one
 * attempt `router/filter.go` makes with no credential, keyed on the empty id.
 * Leaving that slot out drew every local runtime as unusable while it was
 * serving traffic.
 */
function slots(p: ChainProvider): { id: string; cooling: boolean }[] {
  const usable = usableCredentials(p)
  if (usable.length > 0) return usable.map((c) => ({ id: c.id ?? "", cooling: c.cooling }))
  return isKeyless(p) ? [{ id: "", cooling: false }] : []
}

/** Whether a breaker holds this slot shut for this model. The credential-wide
 *  entry (model "") gates every model the key serves, as
 *  `health.Breaker.Available` checks it first; the model's own entry gates
 *  only that model. */
function breakerCooling(ctx: ChainContext, providerId: string, keyId: string, model: string) {
  const now = ctx.now ?? Date.now()
  return (ctx.breakers ?? []).some(
    (b) =>
      b.provider_id === providerId &&
      b.key_id === keyId &&
      (b.model === "" || b.model === model) &&
      b.cooling_until !== undefined &&
      Date.parse(b.cooling_until) > now,
  )
}

/** A provider the router could dispatch this model to right now: switched on,
 *  and holding at least one slot cooling neither as a whole nor for this
 *  model. */
function live(p: ChainProvider, model: string, ctx: ChainContext): boolean {
  return p.enabled && slots(p).some((s) => !s.cooling && !breakerCooling(ctx, p.id, s.id, model))
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
    // The sqlsource rule and its exception together: a provider left with no
    // enabled credential is dropped, unless it needs none.
    const usable = usableCredentials(provider)
    if (usable.length === 0 && !isKeyless(provider)) {
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
    if (!live(provider, model, ctx)) {
      return {
        ...base,
        state: "cooling",
        problem:
          usable.length === 0
            ? `${providerId} is cooling for ${model} after recent failures`
            : `every ${providerId} credential is cooling for ${model}`,
      }
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
  const usable = ctx.providers.filter((p) => offeredBy.includes(p.id) && live(p, trimmed, ctx))
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
