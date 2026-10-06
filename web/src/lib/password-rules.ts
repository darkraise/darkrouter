/** The floor the server enforces (setupapi.go, sessionapi.go); repeated here
 *  only so a typo costs no round trip, never as the authority — the server
 *  checks both the length and the match again on every call that carries a
 *  new password. */
export const MIN_PASSWORD = 12

/** The server's username limit (setupapi.go, `maxUsernameChars`), as the
 *  username inputs' `maxLength`, so a name the server would refuse cannot be
 *  typed at all. maxLength counts UTF-16 units where the server counts
 *  characters, which only makes the field stricter, for names built from
 *  characters outside the Basic Multilingual Plane. */
export const MAX_USERNAME = 64

/**
 * A password's length as the server counts it: in characters, which in Go is
 * runes and here is code points. `.length` counts UTF-16 units, so an emoji is
 * two of them; the two sides must agree on what "12 characters" means.
 */
export function characterCount(text: string): number {
  return [...text].length
}

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
  if (characterCount(password) < MIN_PASSWORD) return wording.tooShort
  if (password !== confirm) return wording.mismatch
  return null
}
