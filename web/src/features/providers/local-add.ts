import { committedButNotRouted } from "../../lib/api"
import type { ProbeResult } from "../../lib/api-types"

/** The slice of the api client this needs, so the orchestration can be tested
 *  against a fake network without standing one up. */
export type ProviderApi = {
  post: <T>(path: string, body?: unknown) => Promise<T>
  del: <T>(path: string) => Promise<T>
}

export type LocalDraft = {
  presetId: string
  baseUrl: string
  /** A token the runtime was started behind. Empty is the ordinary case: a
   *  model server on the operator's own machine usually asks for nothing. */
  apiKey?: string
}

export type LocalOutcome =
  | {
      ok: true
      modelCount?: number
      /** Set when a write committed but the gateway could not load it, so it
       *  is still routing without this runtime. The server's own words. */
      routingNotUpdated?: string
      /** Set when removing the row afterwards did not leave things as they
       *  were, so the operator knows what is still there. */
      leftBehind?: string
    }
  | { ok: false; error: string; leftBehind?: string }

/** What a create answers once the row is stored. */
type Created = { routing_updated?: boolean; warning?: string }

/** The server's warning when a committed create did not reach routing. */
function routingWarning(reply: Created | undefined): string | undefined {
  if (reply?.routing_updated !== false) return undefined
  return reply.warning || "the gateway did not load the change"
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

async function create(api: ProviderApi, d: LocalDraft, enabled: boolean): Promise<string | undefined> {
  const reply = await api.post<Created>("/api/providers", {
    id: d.presetId,
    preset: d.presetId,
    base_url: d.baseUrl,
    enabled,
    // Only when there is a key. Without one the preset's own `none` stands,
    // and overriding it would make a keyless runtime send an empty header.
    // Always bearer: every local server that reads a token at all -- vLLM and
    // llama.cpp behind --api-key -- reads it from Authorization: Bearer. The
    // x-api-key and api-key styles belong to hosted APIs, so offering them
    // here would be offering a choice with one right answer.
    ...(d.apiKey ? { auth_style: "bearer" } : {}),
  })
  return routingWarning(reply)
}

/** Stores the key, if there is one, before anything probes the endpoint: a
 *  probe that ran first would be refused by exactly the runtime the key is
 *  for, and report the address as unreachable. */
async function addKey(api: ProviderApi, d: LocalDraft): Promise<string | undefined> {
  if (!d.apiKey) return undefined
  const reply = await api.post<Created>(`/api/providers/${d.presetId}/keys`, {
    label: "default",
    secret: d.apiKey,
  })
  return routingWarning(reply)
}

async function probe(api: ProviderApi, id: string): Promise<LocalOutcome> {
  // A refused endpoint is a 200 carrying ok:false, not an error status, so the
  // verdict is read from the body rather than from a rejection.
  const result = await api.post<ProbeResult>(`/api/providers/${id}/test`)
  return result.ok
    ? { ok: true, modelCount: result.model_count }
    : { ok: false, error: result.error ?? "the provider did not answer" }
}

/** Removes the row, answering what is left behind if that did not fully
 *  work. Never throws: a failed rollback must not replace the reason the
 *  caller is rolling back, which is the fact the operator needs. */
async function remove(api: ProviderApi, id: string): Promise<string | undefined> {
  try {
    await api.del(`/api/providers/${id}`)
    return undefined
  } catch (err) {
    if (committedButNotRouted(err)) {
      return `${id} was removed, but the gateway is still routing to it until it reloads: ${messageOf(err)}`
    }
    return `${id} is still configured; removing it failed: ${messageOf(err)}`
  }
}

function withLeftBehind(outcome: LocalOutcome, leftBehind: string | undefined): LocalOutcome {
  return leftBehind ? { ...outcome, leftBehind } : outcome
}

/**
 * Proves an endpoint answers, leaving nothing behind either way.
 *
 * The probe endpoint needs a provider row to work from, so one is created for
 * the length of the check and removed again. It is created disabled: a row
 * that exists for a second should not be a row the router can pick.
 */
export async function testLocalRuntime(api: ProviderApi, d: LocalDraft): Promise<LocalOutcome> {
  try {
    await create(api, d, false)
  } catch (err) {
    // Nothing was created, so there is nothing to remove — and the id may
    // belong to a provider the operator configured earlier.
    return { ok: false, error: messageOf(err) }
  }
  let outcome: LocalOutcome
  try {
    await addKey(api, d)
    outcome = await probe(api, d.presetId)
  } catch (err) {
    outcome = { ok: false, error: messageOf(err) }
  }
  return withLeftBehind(outcome, await remove(api, d.presetId))
}

/**
 * Adds the runtime, keeping it only if it answers.
 *
 * The same add-then-probe-then-remove shape the credential flow uses: the
 * gateway can only reach an endpoint through a stored row, so the row has to
 * exist to be testable, and what survives is what works.
 */
export async function addLocalRuntime(api: ProviderApi, d: LocalDraft): Promise<LocalOutcome> {
  let routingNotUpdated: string | undefined
  try {
    routingNotUpdated = await create(api, d, true)
  } catch (err) {
    return { ok: false, error: messageOf(err) }
  }
  let outcome: LocalOutcome
  try {
    routingNotUpdated = (await addKey(api, d)) ?? routingNotUpdated
    outcome = await probe(api, d.presetId)
  } catch (err) {
    outcome = { ok: false, error: messageOf(err) }
  }
  // The delete cascades to the credential, so a rolled-back add leaves no
  // orphaned key behind.
  if (!outcome.ok) {
    return withLeftBehind(outcome, await remove(api, d.presetId))
  }
  return routingNotUpdated ? { ...outcome, routingNotUpdated } : outcome
}
