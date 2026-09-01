import { useState, type FormEvent } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import {
  PasswordInput,
  PasswordInputControl,
  PasswordInputField,
  PasswordInputVisibilityTrigger,
} from "darkraise-ui/components/password-input"
import { api, setCsrfToken } from "../lib/api"
import { IdentityMark } from "../features/shell/identity-mark"

export function LoginScreen({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError("")
    try {
      const res = await api.post<{ authenticated: boolean; csrf_token: string }>(
        "/api/auth/login",
        { password },
      )
      setCsrfToken(res.csrf_token)
      onAuthenticated()
    } catch (err) {
      // One message for a wrong password and an unconfigured hash, matching the
      // server: an operator reading "no password is set" learns the port is
      // open.
      setError((err as Error).message || "login failed")
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
          {/* Revealable. A masked field with no way to check what is in it
              makes a mistyped password indistinguishable from a wrong one,
              and this form has exactly one field to get right. */}
          <PasswordInput>
            <PasswordInputControl>
              <PasswordInputField
                autoFocus
                autoComplete="current-password"
                placeholder="Admin password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <PasswordInputVisibilityTrigger />
            </PasswordInputControl>
          </PasswordInput>
          {/* Announced, not just drawn. A refused password clears the field
              and prints here; without a live region the only feedback a
              screen-reader user gets is that what they typed is gone. */}
          {error ? (
            <p role="alert" className="text-destructive text-sm">
              {error}
            </p>
          ) : null}
          <Button type="submit" disabled={busy || password === ""}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </Card>
    </div>
  )
}
