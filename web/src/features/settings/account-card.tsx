import { useState } from "react"
import { Button, Card, Input, Label } from "darkraise-ui"
import { api, ApiError } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"

export function passwordProblem(next: string, confirm: string): string | null {
  // The server's floor, checked here as a courtesy. It remains the authority:
  // this is about not spending a round trip on a typo.
  if (next.length < 12) return "The new password must be at least 12 characters."
  if (next !== confirm) return "The two entries do not match."
  return null
}

export function revokedText(revoked: number): string {
  // The operator has just logged every other browser out. Not saying so makes
  // the next login failure elsewhere look like a fault.
  if (revoked === 0) return "Password changed. There were no other sessions to revoke."
  const plural = revoked === 1 ? "session" : "sessions"
  return `Password changed, and ${revoked} other ${plural} revoked.`
}

/**
 * The current password, even though the caller already holds a session:
 * the server refuses the write without it, so asking for it here is not
 * optional friction.
 */
export function AccountCard() {
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")

  const problem = passwordProblem(next, confirm)

  const change = useApiMutation({
    mutationFn: () =>
      // A wrong current password is a legitimate rejection, not a dead
      // session — naming it here keeps the operator on this screen instead
      // of bouncing them to login over their own typo.
      api.post<{ revoked: number }>(
        "/api/auth/password",
        { current, new: next },
        { expectedRejection: "invalid password" },
      ),
    invalidates: [keys.sessions],
    success: (res) => revokedText(res.revoked),
    onSuccess: () => {
      // Plaintext in state past the moment it's needed is a liability with
      // no upside; clearing it also leaves the form ready for another change.
      setCurrent("")
      setNext("")
      setConfirm("")
    },
  })

  // useApiMutation's own onError suppresses the toast for every 401 on the
  // assumption that the global logout listener is already explaining itself.
  // For this one rejection that assumption is false, so the message has to
  // be read straight from the mutation's error state instead.
  const wrongPassword =
    change.error instanceof ApiError && change.error.status === 401 ? change.error.message : null

  return (
    <Card className="mb-6 p-4">
      <h2 className="mb-3 text-sm font-medium">Account</h2>
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-3 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-current-password">Current password</Label>
            <Input
              id="account-current-password"
              type="password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-new-password">New password</Label>
            <Input
              id="account-new-password"
              type="password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-confirm-password">Confirm new password</Label>
            <Input
              id="account-confirm-password"
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </div>
        </div>
        {problem && (
          <p className="text-sm text-[hsl(var(--destructive))]">{problem}</p>
        )}
        {!problem && wrongPassword && (
          <p className="text-sm text-[hsl(var(--destructive))]">{wrongPassword}</p>
        )}
        <div>
          <Button
            size="sm"
            disabled={problem !== null || change.isPending}
            onClick={() => change.mutate()}
          >
            Change password
          </Button>
        </div>
      </div>
    </Card>
  )
}
