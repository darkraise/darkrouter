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
import { IdentityMark } from "./identity-mark"
import { PasswordToggle } from "./password-toggle"

/** The server's exact wording for a refused token. Naming it opts this 401
 *  out of the global logout, which would otherwise treat a mistyped token as
 *  a dead session and remount the screen the operator is already on. */
const REJECTED = "invalid setup token"

/** The floor the server enforces; repeated here only to say so before the
 *  round trip, never instead of it. */
const MIN_PASSWORD = 12

/**
 * What a console nobody has claimed yet shows instead of a login it cannot
 * pass.
 *
 * The token comes from the startup log, which is what keeps the claim to
 * whoever can read the host rather than whoever can reach the port. Setup
 * mints no session, so the password just set is spent immediately on a real
 * login — one code path issues cookies, and the stored hash is exercised
 * before the operator relies on it.
 */
export function FirstRun({ onClaimed }: { onClaimed: () => void }) {
  const [token, setToken] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const tokenField = useRef<HTMLInputElement>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError("")
    try {
      await api.post("/api/auth/setup", { token, password }, { expectedRejection: REJECTED })
    } catch (err) {
      // Someone claimed it between this page loading and this submit. The
      // console has a password now, so what this operator needs is the login
      // form; re-reading auth status is what puts them there.
      if (err instanceof ApiError && err.status === 409) {
        onClaimed()
        return
      }
      const refused = err instanceof ApiError && err.status === 401 && err.message === REJECTED
      setError(refused ? "That setup token is not right." : (err as Error).message || "setup failed")
      tokenField.current?.focus()
      tokenField.current?.select()
      return
    } finally {
      setBusy(false)
    }
    const res = await api.post<{ csrf_token: string }>("/api/auth/login", { password })
    setCsrfToken(res.csrf_token)
    onClaimed()
  }

  const tooShort = password !== "" && password.length < MIN_PASSWORD

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-sm p-6">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <div className="flex flex-col items-center gap-2">
            <IdentityMark size={72} />
            <h1 className="text-xl font-medium">Claim this console</h1>
          </div>
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            No admin password is set yet. The setup token was printed to the
            startup log — <code className="font-mono">docker compose logs</code>{" "}
            — and setting a password here closes it for good.
          </p>

          <div className="flex flex-col gap-2">
            <label htmlFor="setup-token" className="text-sm font-medium">
              Setup token
            </label>
            <Input
              id="setup-token"
              ref={tokenField}
              autoFocus
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
              aria-invalid={error !== "" || undefined}
              value={token}
              onChange={(e) => setToken(e.target.value)}
            />
          </div>

          <div className="flex flex-col gap-2">
            <label htmlFor="setup-password" className="text-sm font-medium">
              Admin password
            </label>
            {/* Revealable: this password is being chosen, not recalled, and a
                masked field makes a typo in a new password undiscoverable
                until the next login refuses it. */}
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="setup-password"
                  autoComplete="new-password"
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

          {error ? (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          ) : null}

          <Button type="submit" disabled={busy || token === "" || tooShort || password === ""}>
            {busy ? "Setting…" : "Set password"}
          </Button>
        </form>
      </Card>
    </div>
  )
}
