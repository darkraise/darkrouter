import { toast } from "darkraise-ui"
import { api } from "../../lib/api"
import { type AccountDraft, draftAccounts } from "./account-fields"
import type { ProbeResult } from "../../lib/api-types"

export type AddFailure = { label: string; error: string }
export type AddResult = { added: number; failed: AddFailure[]; rejected: AddFailure[] }

/** How many accounts the draft would create. */
export function countAccounts(draft: AccountDraft): number {
  return draftAccounts(draft).length
}

/** The label on the button that submits the draft, so it says what will
 *  happen rather than what the form is called. */
export function addAccountsLabel(n: number): string {
  return n <= 1 ? "Add account" : `Add ${n} accounts`
}

/**
 * Posts one credential per account, and optionally proves each one works.
 *
 * `POST /keys` takes a single secret, so a paste of twenty is twenty calls.
 * They run in sequence rather than at once: the handler reloads the provider
 * set on every success, and twenty concurrent reloads is a self-inflicted
 * thundering herd. A failure does not abort the rest — one rejected key out of
 * twenty should cost that key, not the other nineteen.
 *
 * Verification is add-then-probe-then-remove rather than probe-then-add: the
 * gateway can only reach a provider through a stored credential, so a key has
 * to exist for a moment to be testable. A key that fails is deleted again, so
 * what survives is what works.
 */
export async function addCredentials(
  providerId: string,
  draft: AccountDraft,
): Promise<AddResult> {
  const failed: AddFailure[] = []
  const rejected: AddFailure[] = []
  let added = 0

  for (const item of draftAccounts(draft)) {
    let created: { id: string }
    try {
      created = await api.post<{ id: string }>(`/api/providers/${providerId}/keys`, item)
    } catch (err) {
      failed.push({ label: item.label, error: err instanceof Error ? err.message : "failed" })
      continue
    }

    if (!draft.verifyKeys) {
      added++
      continue
    }

    try {
      const probe = await api.post<ProbeResult>(
        `/api/providers/${providerId}/test?key=${encodeURIComponent(created.id)}`,
        {},
      )
      if (probe.ok) {
        added++
        continue
      }
      // The provider answered and refused it. Keeping it would leave a key
      // that fails every request it is ever chosen for.
      await api.del(`/api/providers/${providerId}/keys/${created.id}`)
      rejected.push({ label: item.label, error: probe.error || "the provider refused it" })
    } catch (err) {
      // The probe itself could not run. The key is kept: an unreachable
      // gateway is not evidence the key is bad, and deleting it would lose a
      // secret the operator may not have anywhere else.
      added++
      failed.push({
        label: item.label,
        error: `kept unverified: ${err instanceof Error ? err.message : "probe failed"}`,
      })
    }
  }
  return { added, failed, rejected }
}

export function reportAdded(result: AddResult) {
  const { added, failed, rejected } = result
  if (failed.length === 0 && rejected.length === 0) {
    toast.success(added === 1 ? "Account added" : `${added} accounts added`)
    return
  }
  // Naming the ones that did not make it, because "18 of 20" without saying
  // which two leaves the operator to diff the list by hand.
  const names = (list: AddFailure[]) => list.map((f) => f.label).join(", ")
  if (rejected.length > 0 && added === 0 && failed.length === 0) {
    toast.error(`No account kept. ${names(rejected)} — ${rejected[0]?.error}`)
    return
  }
  const parts = [`${added} added`]
  if (rejected.length > 0) parts.push(`${rejected.length} refused (${names(rejected)})`)
  if (failed.length > 0) parts.push(`${failed.length} failed (${names(failed)})`)
  toast.warning(parts.join(", "))
}
