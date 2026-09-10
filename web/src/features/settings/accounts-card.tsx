import { useState } from "react"
import {
  Badge,
  Button,
  Card,
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
  PasswordInput,
  PasswordInputControl,
  PasswordInputField,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "darkraise-ui"
import { Users } from "lucide-react"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { MIN_PASSWORD, passwordConfirmationProblem } from "../../lib/password-rules"
import { keys } from "../../lib/queries"
import type { UserAccount } from "../../lib/api-types"
import { ConfirmButton } from "../shell/confirm-button"
import { PasswordToggle } from "../shell/password-toggle"

/** What the server also checks -- this is a courtesy that saves a round trip
 *  on an obvious typo, never the authority on either rule. */
export function addAccountPasswordProblem(password: string, confirm: string): string | null {
  return passwordConfirmationProblem(password, confirm, {
    tooShort: `The password must be at least ${MIN_PASSWORD} characters.`,
    mismatch: "The two passwords do not match.",
  })
}

function roleLabel(role: string): string {
  return role === "admin" ? "Administrator" : "Member"
}

/**
 * Adding an account, from the Accounts card.
 *
 * A dialog rather than an inline row: creating an account asks for more than
 * any row edits, and a role nobody chose deliberately is worse than a form
 * that makes the choice visible.
 */
function AddAccountDialog({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [role, setRole] = useState<"admin" | "member">("member")

  // Nothing is typed yet, so nothing is wrong yet -- see change-password-dialog.
  const touched = password !== "" || confirm !== ""
  const problem = touched ? addAccountPasswordProblem(password, confirm) : null

  function clear() {
    setUsername("")
    setPassword("")
    setConfirm("")
    setRole("member")
    create.reset()
  }

  const create = useApiMutation({
    mutationFn: () => api.post<UserAccount>("/api/users", { username, password, role }),
    invalidates: [keys.users],
    success: (created) => `${created.username} can now sign in.`,
    onSuccess: () => {
      clear()
      onOpenChange(false)
    },
  })

  // One close path, so every way out clears the form: Escape, the overlay,
  // the X, and Cancel.
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
          <DialogTitle>Add an account</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-account-username">Username</Label>
            <Input
              id="new-account-username"
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-account-password">Password</Label>
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="new-account-password"
                  autoComplete="new-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-account-confirm-password">Confirm password</Label>
            <PasswordInput>
              <PasswordInputControl>
                <PasswordInputField
                  id="new-account-confirm-password"
                  autoComplete="new-password"
                  value={confirm}
                  onChange={(e) => setConfirm(e.target.value)}
                />
                <PasswordToggle />
              </PasswordInputControl>
            </PasswordInput>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-account-role">Role</Label>
            <Select value={role} onValueChange={(v) => setRole(v as "admin" | "member")}>
              {/* Label association only, no aria-label -- see reasoning.tsx's
                  select for the same convention. */}
              <SelectTrigger id="new-account-role">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="member">Member</SelectItem>
                <SelectItem value="admin">Administrator</SelectItem>
              </SelectContent>
            </Select>
          </div>

          {problem && <p className="text-sm text-[hsl(var(--destructive))]">{problem}</p>}

          <div className="flex items-center gap-2 border-t pt-3">
            <Button
              size="sm"
              disabled={username === "" || !touched || problem !== null || create.isPending}
              onClick={() => create.mutate()}
            >
              Add account
            </Button>
            <Button size="sm" variant="ghost" onClick={() => close()}>
              Cancel
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Every account that can sign in to this console, and the administrator's
 * controls over them.
 *
 * `me` marks the caller's own row so they are not offered a Remove button
 * for it. The server allows a non-last administrator to remove themselves
 * regardless -- this is a UI convenience against a misclick, not the safety
 * mechanism; the last-administrator refusal lives in the store and answers
 * with its own 409 however the request reaches it.
 */
export function AccountsCard({ users, me }: { users: UserAccount[]; me: string }) {
  const [addOpen, setAddOpen] = useState(false)

  const remove = useApiMutation({
    mutationFn: (id: string) => api.del<void>(`/api/users/${id}`),
    invalidates: [keys.users],
    success: "Account removed",
  })

  const solo = users.length === 1

  return (
    <Card className="mt-4 p-4">
      <div className="flex flex-wrap items-start gap-3">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-[var(--radius)] bg-[hsl(var(--muted))]">
          <Users className="size-5" aria-hidden="true" />
        </span>
        <div className="min-w-0 flex-1">
          <h2 className="font-medium">Accounts</h2>
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            Every account can sign in and use every screen. An account with the admin role can
            also add and remove accounts; a removed account is signed out immediately.
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => setAddOpen(true)}>
          Add an account
        </Button>
      </div>

      {solo && (
        <p className="mt-3 text-sm text-[hsl(var(--muted-foreground))]">
          This is the only account, so there is no one else to remove.
        </p>
      )}

      <ul className="mt-3 flex flex-col gap-2">
        {users.map((u) => (
          <li key={u.id} className="flex items-center gap-3 text-sm">
            <span className="font-medium">{u.username}</span>
            <Badge variant={u.role === "admin" ? "secondary" : "outline"} className="font-medium">
              {roleLabel(u.role)}
            </Badge>
            {u.id === me && <Badge variant="green">This is you</Badge>}
            {!solo && u.id !== me && (
              <ConfirmButton
                size="sm"
                variant="ghost"
                className="ml-auto text-[hsl(var(--destructive))]"
                title="Remove this account?"
                description={`${u.username} can no longer sign in, and any session of theirs ends right away.`}
                confirmLabel="Remove"
                destructive
                onConfirm={() => remove.mutate(u.id)}
              >
                Remove
              </ConfirmButton>
            )}
          </li>
        ))}
      </ul>

      <AddAccountDialog open={addOpen} onOpenChange={setAddOpen} />
    </Card>
  )
}
