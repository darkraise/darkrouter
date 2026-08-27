import { useState } from "react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
  Badge,
  Button,
  Input,
  TableCell,
  TableRow,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"
import type { Credential } from "../../lib/api-types"

/**
 * One credential: its masked secret, its state, its OAuth metadata when it
 * has any, and the three ways an operator changes it — enable/disable,
 * replace the secret, or remove it entirely.
 *
 * The replace-secret input never carries a value in from the credential: the
 * API never returns one, and there is nothing to prefill it with that would
 * not be a fabrication.
 */
export function CredentialRow({ providerId, credential }: { providerId: string; credential: Credential }) {
  const [draftSecret, setDraftSecret] = useState("")
  const [replacing, setReplacing] = useState(false)

  const patch = useApiMutation({
    mutationFn: (vars: { enabled?: boolean; secret?: string }) =>
      api.patch(`/api/providers/${providerId}/keys/${credential.id}`, vars),
    success: "Credential updated",
    invalidates: [keys.providers, keys.health],
    onSuccess: () => {
      setDraftSecret("")
      setReplacing(false)
    },
  })

  const remove = useApiMutation({
    mutationFn: () => api.del(`/api/providers/${providerId}/keys/${credential.id}`),
    success: "Credential removed",
    invalidates: [keys.providers, keys.health],
  })

  return (
    <TableRow>
      <TableCell>{credential.label}</TableCell>
      {/* Enough to recognise, never enough to use. */}
      <TableCell className="whitespace-nowrap font-mono text-sm">{credential.masked}</TableCell>
      <TableCell>
        <div className="flex items-center gap-1.5 whitespace-nowrap">
          {credential.cooling ? (
            <Badge variant="amber">cooling</Badge>
          ) : credential.enabled ? (
            <Badge variant="green">enabled</Badge>
          ) : (
            <Badge variant="secondary">disabled</Badge>
          )}
          {/* The kind is shown only where it differs from a plain key. It is
              identical on every row of an ordinary provider, and the
              Connection panel already names the auth style once. */}
          {credential.kind !== "static" && <Badge variant="outline">{credential.kind}</Badge>}
          {credential.expires_at !== undefined && (
            <span className="text-sm text-[hsl(var(--legend))]">
              expires {new Date(credential.expires_at * 1000).toLocaleDateString()}
            </span>
          )}
          {credential.scope && (
            <span className="font-mono text-sm text-[hsl(var(--legend))]">{credential.scope}</span>
          )}
        </div>
      </TableCell>
      <TableCell>
        {/* Replacing a secret is a deliberate act, so its field appears when
            it is asked for. Standing open on every row, it put an empty
            password box beside every key on the screen. */}
        <div className="flex items-center justify-end gap-1.5 whitespace-nowrap">
          {replacing ? (
            <>
              <Input
                placeholder="new secret"
                type="password"
                autoFocus
                value={draftSecret}
                onChange={(e) => setDraftSecret(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Escape") {
                    setReplacing(false)
                    setDraftSecret("")
                  }
                }}
                className="w-40"
              />
              <Button
                size="sm"
                disabled={draftSecret === ""}
                onClick={() => patch.mutate({ secret: draftSecret })}
              >
                Save
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  setReplacing(false)
                  setDraftSecret("")
                }}
              >
                Cancel
              </Button>
            </>
          ) : (
            <>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => patch.mutate({ enabled: !credential.enabled })}
              >
                {credential.enabled ? "Disable" : "Enable"}
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setReplacing(true)}>
                Replace
              </Button>
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button size="sm" variant="ghost" className="text-[hsl(var(--destructive))]">
                    Remove
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>Remove {credential.label}?</AlertDialogTitle>
                    <AlertDialogDescription>
                      Requests that depended on this credential fail over to whatever else is
                      configured, or stop routing here if nothing else is.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <AlertDialogFooter>
                    <AlertDialogCancel>Cancel</AlertDialogCancel>
                    <AlertDialogAction onClick={() => remove.mutate(undefined)}>Remove</AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            </>
          )}
        </div>
      </TableCell>
    </TableRow>
  )
}
