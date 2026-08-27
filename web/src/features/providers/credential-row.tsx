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

  const patch = useApiMutation({
    mutationFn: (vars: { enabled?: boolean; secret?: string }) =>
      api.patch(`/api/providers/${providerId}/keys/${credential.id}`, vars),
    success: "Credential updated",
    invalidates: [keys.providers, keys.health],
    onSuccess: () => setDraftSecret(""),
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
      <TableCell className="font-mono text-xs">{credential.masked}</TableCell>
      <TableCell>
        {credential.cooling ? (
          <Badge variant="amber">cooling</Badge>
        ) : credential.enabled ? (
          <Badge variant="green">enabled</Badge>
        ) : (
          <Badge variant="secondary">disabled</Badge>
        )}
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="outline">{credential.kind}</Badge>
          {credential.expires_at !== undefined && (
            <span className="text-xs text-[hsl(var(--legend))]">
              expires {new Date(credential.expires_at * 1000).toLocaleDateString()}
            </span>
          )}
          {credential.scope && (
            <span className="font-mono text-xs text-[hsl(var(--legend))]">{credential.scope}</span>
          )}
        </div>
      </TableCell>
      <TableCell>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant="ghost"
            onClick={() => patch.mutate({ enabled: !credential.enabled })}
          >
            {credential.enabled ? "Disable" : "Enable"}
          </Button>
          <Input
            placeholder="new secret"
            type="password"
            value={draftSecret}
            onChange={(e) => setDraftSecret(e.target.value)}
            className="w-32"
          />
          <Button
            size="sm"
            variant="ghost"
            disabled={draftSecret === ""}
            onClick={() => patch.mutate({ secret: draftSecret })}
          >
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
        </div>
      </TableCell>
    </TableRow>
  )
}
