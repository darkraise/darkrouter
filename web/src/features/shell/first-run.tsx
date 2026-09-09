import { useRef, useState, type FormEvent } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import {
  PasswordInput,
  PasswordInputControl,
  PasswordInputField,
} from "darkraise-ui/components/password-input"
import { api, ApiError, setCsrfToken } from "../../lib/api"
import { MIN_PASSWORD, passwordConfirmationProblem } from "../../lib/password-rules"
import { IdentityMark } from "./identity-mark"
import { PasswordToggle } from "./password-toggle"

/** What the server also checks — this is a courtesy that saves a round trip
 *  on an obvious typo, never the authority on either rule. */
export function claimProblem(password: string, confirm: string): string | null {
  return passwordConfirmationProblem(password, confirm, {
    tooShort: `The password must be at least ${MIN_PASSWORD} characters.`,
    mismatch: "The two passwords do not match.",
  })
}

/**
 * What a console nobody has claimed yet shows instead of a login it cannot
 * pass.
 *
 * The claim is trust-on-first-use: whoever reaches this screen first picks a
 * username and password and owns the console from then on, no token
 * involved. Setup still mints no session, so the password just set is spent
 * immediately on a real login — one code path issues cookies, and the
 * stored hash is exercised before the operator relies on it.
 */
export function FirstRun({ onClaimed }: { onClaimed: () => void }) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const usernameField = useRef<HTMLInputElement>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setError("")
    const problem = claimProblem(password, confirm)
    if (problem) {
      setError(problem)
      return
    }
    setBusy(true)
    try {
      await api.post("/api/auth/setup", { username, password, confirm })
    } catch (err) {
      // Someone claimed it between this page loading and this submit. The
      // console has a password now, so what this operator needs is the login
      // form; re-reading auth status is what puts them there.
      if (err instanceof ApiError && err.status === 409) {
        onClaimed()
        return
      }
      setError((err as Error).message || "setup failed")
      usernameField.current?.focus()
      usernameField.current?.select()
      return
    } finally {
      setBusy(false)
    }
    const res = await api.post<{ csrf_token: string }>("/api/auth/login", { username, password })
    setCsrfToken(res.csrf_token)
    onClaimed()
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-sm p-6">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col items-center gap-2">
            <IdentityMark size={72} />
            <h1 className="text-xl font-medium">Claim this console</h1>
          </div>
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            No account exists yet. The first username and password set here
            become the console&apos;s admin account — trust on first use, no
            setup token involved.
          </p>

          <div className="flex flex-col gap-2">
            <label htmlFor="setup-username" className="text-sm font-medium">
              Username
            </label>
            <Input
              id="setup-username"
              ref={usernameField}
              autoFocus
              autoComplete="username"
              aria-invalid={error !== "" || undefined}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            <label htmlFor="setup-password" className="text-sm font-medium">
              Password
            </label>
            {/* Revealable: this password is being chosen, not recalled, and a
                masked field makes a typo in a new password undiscoverable
                until the next login refuses it. */}
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="setup-password"
                  autoComplete="new-password"
                  aria-invalid={error !== "" || undefined}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
            <p className="text-sm text-[hsl(var(--muted-foreground))]">
              At least {MIN_PASSWORD} characters.
            </p>
          </div>

          <div className="flex flex-col gap-2">
            <label htmlFor="setup-confirm" className="text-sm font-medium">
              Confirm password
            </label>
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="setup-confirm"
                  autoComplete="new-password"
                  aria-invalid={error !== "" || undefined}
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
          </div>

          {error ? (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          ) : null}

          <Button
            type="submit"
            disabled={busy || username === "" || password === "" || confirm === ""}
          >
            {busy ? "Claiming…" : "Claim console"}
          </Button>
        </form>
      </Card>
    </div>
  )
}
