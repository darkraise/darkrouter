import { useCallback, useEffect, useRef, useState, type FormEvent } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import {
  PasswordInput,
  PasswordInputControl,
  PasswordInputField,
} from "darkraise-ui/components/password-input"
import { api, ApiError, setCsrfToken } from "../lib/api"
import { IdentityMark } from "../features/shell/identity-mark"
import { PasswordToggle } from "../features/shell/password-toggle"

/** The server's exact wording for a refused login. Naming it opts this one
 *  401 out of the global logout, which would otherwise treat a mistyped
 *  credential as a dead session and remount the screen the operator is
 *  already on.
 *
 *  It is also, deliberately, the one message for a wrong username, a wrong
 *  password, and an unclaimed console alike — the screen must not add
 *  wording that tells the three apart. */
const REJECTED = "invalid username or password"

/** The tab's name while the form is up. Set here because the shell that
 *  names every other screen is not mounted, and a tab left reading
 *  "Requests · Darkrouter" over a sign-in form looks like a working page. */
export const LOGIN_TITLE = "Sign in · Darkrouter"

/**
 * Counts down to the moment the server said sign-in reopens: the whole
 * seconds left, zero once it has or when there is no wait, and a function
 * that starts a new wait.
 */
function useCountdown(): [number, (secs: number | undefined) => void] {
  const [wait, setWait] = useState<{ until: number; now: number } | null>(null)
  useEffect(() => {
    if (wait === null || wait.now >= wait.until) return
    const id = setTimeout(() => setWait((w) => w && { ...w, now: Date.now() }), 1000)
    return () => clearTimeout(id)
  }, [wait])
  const start = useCallback((secs: number | undefined) => {
    const now = Date.now()
    setWait(secs === undefined ? null : { until: now + secs * 1000, now })
  }, [])
  const left = wait === null ? 0 : Math.max(0, Math.ceil((wait.until - wait.now) / 1000))
  return [left, start]
}

export function LoginScreen({
  onAuthenticated,
  reason,
}: {
  onAuthenticated: () => void
  /** Why the operator is here, when it is not a fresh visit: "expired" when
   *  a session that was in use stopped being accepted. */
  reason?: "expired"
}) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  // A refusal of the attempt rather than of the credential: the password may
  // well be right, so neither field is marked wrong.
  const [limited, setLimited] = useState(false)
  const [wait, startWait] = useCountdown()
  const [busy, setBusy] = useState(false)
  const field = useRef<HTMLInputElement>(null)

  useEffect(() => {
    document.title = LOGIN_TITLE
  }, [])

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError("")
    setLimited(false)
    try {
      const res = await api.post<{ authenticated: boolean; csrf_token: string }>(
        "/api/auth/login",
        { username, password },
        { expectedRejection: REJECTED },
      )
      setCsrfToken(res.csrf_token)
      onAuthenticated()
    } catch (err) {
      if (err instanceof ApiError && err.status === 429) {
        // The limiter's wait is what the operator needs, and the server
        // sends it; "try again later" left them to guess, and retrying early
        // only earns another 429. Sign in stays disabled until it passes.
        const secs = err.retryAfter
        setLimited(true)
        startWait(secs)
        setError(
          secs === undefined
            ? "Too many sign-in attempts. Wait a minute, then try again."
            : `Too many sign-in attempts. Try again in ${secs} ${secs === 1 ? "second" : "seconds"}.`,
        )
        return
      }
      const refused = err instanceof ApiError && err.status === 401 && err.message === REJECTED
      setError(refused ? REJECTED : (err as Error).message || "login failed")
      field.current?.focus()
      field.current?.select()
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-sm p-6">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col items-center gap-2">
            <IdentityMark size={72} />
            <h1 className="text-xl font-medium">darkrouter</h1>
          </div>

          <div className="flex flex-col gap-2">
            <label htmlFor="login-username" className="text-sm font-medium">
              Username
            </label>
            <Input
              id="login-username"
              autoFocus
              autoComplete="username"
              aria-invalid={(error !== "" && !limited) || undefined}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            <label htmlFor="login-password" className="text-sm font-medium">
              Password
            </label>
            {/* Revealable. A masked field with no way to check what is in it
                makes a mistyped password indistinguishable from a wrong one,
                and this form has exactly one field to get right. */}
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="login-password"
                  ref={field}
                  autoComplete="current-password"
                  aria-invalid={(error !== "" && !limited) || undefined}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
          </div>

          {/* Announced, not just drawn. A refused login stays on the page
              with the password field selected, so a typo can be corrected
              without retyping the whole thing; the live region says why it
              was refused. */}
          {error ? (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          ) : reason === "expired" ? (
            // Said, rather than dropping the operator onto a blank form with
            // no clue why the screen they were on went away.
            <p role="status" className="text-sm text-[hsl(var(--muted-foreground))]">
              Your session has ended. Sign in again to carry on where you were.
            </p>
          ) : null}
          {/* The countdown is on the button rather than in the alert, so a
              screen reader hears the wait once instead of every second. */}
          <Button type="submit" disabled={busy || wait > 0 || username === "" || password === ""}>
            {busy ? "Signing in…" : wait > 0 ? `Try again in ${wait} s` : "Sign in"}
          </Button>
        </form>
      </Card>
    </div>
  )
}
