/** The floor the server enforces (setupapi.go, sessionapi.go); repeated here
 *  only so a typo costs no round trip, never as the authority — the server
 *  checks both the length and the match again on every call that carries a
 *  new password. */
export const MIN_PASSWORD = 12

/**
 * The one guard behind every password-confirmation pair in the console: the
 * same floor and the same match check, worded per screen. A claim ("The
 * password…") and a change ("The new password…") read differently on
 * purpose; the rule they enforce must not drift the same way the server's
 * wording and the console's `expectedRejection` once did.
 */
export function passwordConfirmationProblem(
  password: string,
  confirm: string,
  wording: { tooShort: string; mismatch: string },
): string | null {
  if (password.length < MIN_PASSWORD) return wording.tooShort
  if (password !== confirm) return wording.mismatch
  return null
}
