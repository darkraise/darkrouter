import { useState } from "react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  Label,
  PasswordInput,
  PasswordInputControl,
  PasswordInputField,
} from "darkraise-ui"
import { PasswordToggle } from "../shell/password-toggle"
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
 * Changing the console password, from the user menu.
 *
 * It asks for the current password even though the caller already holds a
 * session: the server refuses the write without it, so this is not optional
 * friction.
 *
 * A dialog rather than a panel on a screen. It belongs to whoever is signed
 * in rather than to the gateway's configuration, and it is reached from the
 * same menu as signing out.
 */
export function ChangePasswordDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [current, setCurrent] = useState("")
  const [next, setNext] = useState("")
  const [confirm, setConfirm] = useState("")

  // Nothing is typed yet, so nothing is wrong yet: showing "at least 12
  // characters" over an untouched form scolds before the operator has done
  // anything.
  const touched = next !== "" || confirm !== ""
  const problem = touched ? passwordProblem(next, confirm) : null

  function clear() {
    // Plaintext in state past the moment it is needed is a liability with no
    // upside; clearing also leaves the form ready for another change.
    setCurrent("")
    setNext("")
    setConfirm("")
    // The rejection belongs to the attempt that produced it. Left standing, a
    // reopened and empty form carries "invalid password" over fields nobody
    // has typed into yet.
    change.reset()
  }

  const change = useApiMutation({
    mutationFn: () =>
      // A wrong current password is a legitimate rejection, not a dead
      // session — naming it here keeps the operator on this dialog instead
      // of bouncing them to login over their own typo.
      api.post<{ revoked: number }>(
        "/api/auth/password",
        { current, new: next },
        { expectedRejection: "invalid password" },
      ),
    invalidates: [keys.sessions],
    success: (res) => revokedText(res.revoked),
    onSuccess: () => {
      clear()
      onOpenChange(false)
    },
  })

  // useApiMutation's own onError suppresses the toast for every 401 on the
  // assumption that the global logout listener is already explaining itself.
  // For this one rejection that assumption is false, so the message has to
  // be read straight from the mutation's error state instead.
  const wrongPassword =
    change.error instanceof ApiError && change.error.status === 401 ? change.error.message : null

  // One close path, so every way out of the dialog clears the form: Escape,
  // the overlay, the X, and Cancel.
  function close() {
    clear()
    onOpenChange(false)
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(nextOpen) => {
        if (nextOpen) {
          onOpenChange(true)
          return
        }
        close()
      }}
    >
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>Change password</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            Every other signed-in browser is signed out when the password changes.
          </p>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-current-password">Current password</Label>
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="account-current-password"
                  autoComplete="current-password"
                  value={current}
                  onChange={(e) => setCurrent(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-new-password">New password</Label>
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="account-new-password"
                  autoComplete="new-password"
                  value={next}
                  onChange={(e) => setNext(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="account-confirm-password">Confirm new password</Label>
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="account-confirm-password"
                  autoComplete="new-password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
          </div>

          {problem && <p className="text-sm text-[hsl(var(--destructive))]">{problem}</p>}
          {!problem && wrongPassword && (
            <p className="text-sm text-[hsl(var(--destructive))]">{wrongPassword}</p>
          )}

          <div className="flex items-center gap-2 border-t pt-3">
            <Button
              size="sm"
              disabled={current === "" || !touched || problem !== null || change.isPending}
              onClick={() => change.mutate()}
            >
              Change password
            </Button>
            {/* Routed through the same close path as Escape and the overlay.
                Calling onOpenChange directly skipped clear(), and because the
                dialog stays mounted the typed plaintext survived until the
                next successful change. */}
            <Button size="sm" variant="ghost" onClick={() => close()}>
              Cancel
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
