import { toast } from "darkraise-ui"
import { api } from "../../lib/api"
import { type AccountDraft, parseBulkSecrets } from "./account-fields"

export type AddResult = { added: number; failed: { label: string; error: string }[] }

/** How many accounts the draft would create. */
export function countAccounts(draft: AccountDraft): number {
  return draft.mode === "single"
    ? draft.secret.trim() === ""
      ? 0
      : 1
    : parseBulkSecrets(draft.bulk).length
}

/** The label on the button that submits the draft, so it says what will
 *  happen rather than what the form is called. */
export function addAccountsLabel(n: number): string {
  return n <= 1 ? "Add account" : `Add ${n} accounts`
}

/**
 * Posts one credential per secret.
 *
 * `POST /keys` takes a single secret, so a bulk paste is N calls. They run in
 * sequence rather than at once: the handler reloads the provider set on every
 * success, and twenty concurrent reloads is a self-inflicted thundering herd.
 * A failure does not abort the rest — one rejected key out of twenty should
 * cost that key, not the other nineteen.
 */
export async function addCredentials(
  providerId: string,
  draft: AccountDraft,
): Promise<AddResult> {
  const items =
    draft.mode === "single"
      ? [{ label: draft.label.trim() || "default", secret: draft.secret.trim() }]
      : parseBulkSecrets(draft.bulk).map((secret, i) => ({
          label: `${draft.label.trim() || "key"}-${i + 1}`,
          secret,
        }))

  const failed: AddResult["failed"] = []
  let added = 0
  for (const item of items) {
    try {
      await api.post(`/api/providers/${providerId}/keys`, item)
      added++
    } catch (err) {
      failed.push({ label: item.label, error: err instanceof Error ? err.message : "failed" })
    }
  }
  return { added, failed }
}

export function reportAdded(result: AddResult) {
  if (result.failed.length === 0) {
    toast.success(result.added === 1 ? "Account added" : `${result.added} accounts added`)
    return
  }
  // Naming the ones that failed, because "18 of 20" without saying which two
  // leaves the operator to diff the list by hand.
  const names = result.failed.map((f) => f.label).join(", ")
  if (result.added === 0) {
    toast.error(`No accounts added. ${names} failed: ${result.failed[0]?.error}`)
    return
  }
  toast.warning(`${result.added} added, ${result.failed.length} failed: ${names}`)
}
