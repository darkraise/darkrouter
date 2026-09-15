import { toast } from "darkraise-ui"
import { api, committedButNotRouted } from "../../lib/api"
import {
  type AccountDraft,
  type ParsedAccount,
  draftAccounts,
  parseBulkLines,
} from "./account-fields"
import type { ProbeResult } from "../../lib/api-types"

export type AddFailure = { label: string; error: string }
export type AddResult = {
  added: number
  failed: AddFailure[]
  rejected: AddFailure[]
  /** The accounts that were not stored and why, so the operator can fix and
   *  resend them without resending the ones that were. */
  retry: (AddFailure & { account: ParsedAccount })[]
  /** Set when a write committed but the gateway could not load it, so it is
   *  still routing with what it had before this run. The server's own words. */
  routingNotUpdated?: string
}

/** What POST /keys answers once the credential is stored. */
type CreatedCredential = {
  id: string
  label: string
  /** False when the key is stored but the gateway did not load it. */
  routing_updated?: boolean
  warning?: string
}

/** Where a run has got to. `done` counts accounts finished, so it is the
 *  index of the one named — the bar and the sentence never disagree. */
export type AddProgress = {
  done: number
  total: number
  label: string
  step: "adding" | "checking"
}

/** What the progress line reads while a run is in flight. Probing a key takes
 *  a round trip to the provider per account, so a paste of twenty is a wait
 *  long enough that silence reads as a hang. */
export function progressLabel(p: AddProgress): string {
  const position = `${Math.min(p.done + 1, p.total)} of ${p.total}`
  return p.step === "checking"
    ? `Checking ${p.label} · ${position}`
    : `Adding ${p.label} · ${position}`
}

/** How many accounts the draft would create. */
export function countAccounts(draft: AccountDraft, needsAccount = false): number {
  return draftAccounts(draft, needsAccount).length
}

/** The label on the button that submits the draft, so it says what will
 *  happen rather than what the form is called. */
export function addAccountsLabel(n: number): string {
  return n <= 1 ? "Add credential" : `Add ${n} credentials`
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
  needsAccount: boolean,
  onProgress?: (p: AddProgress) => void,
): Promise<AddResult> {
  const failed: AddFailure[] = []
  const rejected: AddFailure[] = []
  const retry: AddResult["retry"] = []
  let added = 0
  let routingNotUpdated: string | undefined

  const items = draftAccounts(draft, needsAccount)
  for (const [done, item] of items.entries()) {
    const report = (step: AddProgress["step"]) =>
      onProgress?.({ done, total: items.length, label: item.label, step })
    report("adding")
    let created: CreatedCredential
    try {
      created = await api.post<CreatedCredential>(`/api/providers/${providerId}/keys`, item)
    } catch (err) {
      const failure = { label: item.label, error: err instanceof Error ? err.message : "failed" }
      failed.push(failure)
      retry.push({ ...failure, account: item })
      continue
    }
    if (created.routing_updated === false) {
      routingNotUpdated = created.warning || "the gateway did not load the new credential"
    }

    if (!draft.verifyKeys) {
      added++
      continue
    }

    report("checking")
    try {
      const probe = await api.post<ProbeResult>(
        `/api/providers/${providerId}/test?key=${encodeURIComponent(created.id)}`,
        {},
      )
      if (probe.ok) {
        added++
        continue
      }
      if (!probe.rejected) {
        // The check could not finish -- a timeout, a rate limit, an outage.
        // That is no evidence against the key, so it stays, as below.
        added++
        failed.push({
          label: item.label,
          error: `kept unverified: ${probe.error || "the check did not complete"}`,
        })
        continue
      }
      // The provider answered and refused it. Keeping it would leave a key
      // that fails every request it is ever chosen for.
      try {
        await api.del(`/api/providers/${providerId}/keys/${created.id}`)
      } catch (err) {
        // Deleted, but still in the gateway's routing until it reloads.
        if (!committedButNotRouted(err)) throw err
        routingNotUpdated = (err as Error).message
      }
      const refusal = { label: item.label, error: probe.error || "the provider refused it" }
      rejected.push(refusal)
      retry.push({ ...refusal, account: item })
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
  return { added, failed, rejected, retry, routingNotUpdated }
}

/**
 * The draft to leave in the form after a run that did not store everything:
 * only the accounts still to add, so sending it again cannot duplicate the
 * ones that went in. A single credential is already exactly that.
 */
export function retryDraft(
  draft: AccountDraft,
  retry: AddResult["retry"],
  needsAccount: boolean,
): AccountDraft {
  if (draft.mode === "single") return draft
  // The operator's own lines rather than lines rebuilt from what they parsed
  // to: a rebuilt line names every auto-named key, and a label prefix holding
  // a pipe would split differently when it is read back. Parsing drops
  // duplicate secrets, so a secret identifies its line.
  const failed = new Set(retry.map((r) => r.account.secret))
  const bulk = parseBulkLines(draft.bulk, draft.label.trim() || "key", needsAccount)
    .filter((l) => failed.has(l.account.secret))
    .map((l) => l.line)
    .join("\n")
  return { ...draft, bulk }
}

export function reportAdded(result: AddResult) {
  const { added, failed, rejected, routingNotUpdated } = result
  if (failed.length === 0 && rejected.length === 0) {
    const done = added === 1 ? "Credential added" : `${added} credentials added`
    if (routingNotUpdated) toast.warning(`${done}, but not yet in use: ${routingNotUpdated}`)
    else toast.success(done)
    return
  }
  // Naming the ones that did not make it, and why, because "18 of 20" without
  // saying which two leaves the operator to diff the list by hand, and a name
  // without its reason leaves them guessing at the fix.
  const names = (list: AddFailure[]) => list.map((f) => `${f.label}: ${f.error}`).join("; ")
  const routing = routingNotUpdated ? `. Routing not updated: ${routingNotUpdated}` : ""
  if (added === 0) {
    toast.error(`No credential kept. ${names([...rejected, ...failed])}${routing}`)
    return
  }
  const parts = [`${added} added`]
  if (rejected.length > 0) parts.push(`${rejected.length} refused (${names(rejected)})`)
  if (failed.length > 0) parts.push(`${failed.length} failed (${names(failed)})`)
  toast.warning(parts.join(", ") + routing)
}
