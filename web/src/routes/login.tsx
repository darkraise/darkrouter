import { useRef, useState, type FormEvent } from "react"
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

export function LoginScreen({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const field = useRef<HTMLInputElement>(null)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError("")
    try {
      const res = await api.post<{ authenticated: boolean; csrf_token: string }>(
        "/api/auth/login",
        { username, password },
        { expectedRejection: REJECTED },
      )
      setCsrfToken(res.csrf_token)
      onAuthenticated()
    } catch (err) {
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
              aria-invalid={error !== "" || undefined}
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
                  aria-invalid={error !== "" || undefined}
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
          ) : null}
          <Button type="submit" disabled={busy || username === "" || password === ""}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </Card>
    </div>
  )
}
