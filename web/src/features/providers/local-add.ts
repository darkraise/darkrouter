import type { ProbeResult } from "../../lib/api-types"

/** The slice of the api client this needs, so the orchestration can be tested
 *  against a fake network without standing one up. */
export type ProviderApi = {
  post: <T>(path: string, body?: unknown) => Promise<T>
  del: <T>(path: string) => Promise<T>
}

export type LocalDraft = { presetId: string; baseUrl: string }

export type LocalOutcome =
  | { ok: true; modelCount?: number }
  | { ok: false; error: string }

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

async function create(api: ProviderApi, d: LocalDraft, enabled: boolean): Promise<void> {
  await api.post("/api/providers", {
    id: d.presetId,
    preset: d.presetId,
    base_url: d.baseUrl,
    enabled,
  })
}

async function probe(api: ProviderApi, id: string): Promise<LocalOutcome> {
  // A refused endpoint is a 200 carrying ok:false, not an error status, so the
  // verdict is read from the body rather than from a rejection.
  const result = await api.post<ProbeResult>(`/api/providers/${id}/test`)
  return result.ok
    ? { ok: true, modelCount: result.model_count }
    : { ok: false, error: result.error ?? "the provider did not answer" }
}

async function remove(api: ProviderApi, id: string): Promise<void> {
  try {
    await api.del(`/api/providers/${id}`)
  } catch {
    // A failed rollback must not replace the reason the caller is rolling
    // back, which is the fact the operator needs.
  }
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
    outcome = await probe(api, d.presetId)
  } catch (err) {
    outcome = { ok: false, error: messageOf(err) }
  }
  await remove(api, d.presetId)
  return outcome
}

/**
 * Adds the runtime, keeping it only if it answers.
 *
 * The same add-then-probe-then-remove shape the credential flow uses: the
 * gateway can only reach an endpoint through a stored row, so the row has to
 * exist to be testable, and what survives is what works.
 */
export async function addLocalRuntime(api: ProviderApi, d: LocalDraft): Promise<LocalOutcome> {
  try {
    await create(api, d, true)
  } catch (err) {
    return { ok: false, error: messageOf(err) }
  }
  let outcome: LocalOutcome
  try {
    outcome = await probe(api, d.presetId)
  } catch (err) {
    outcome = { ok: false, error: messageOf(err) }
  }
  if (!outcome.ok) await remove(api, d.presetId)
  return outcome
}
