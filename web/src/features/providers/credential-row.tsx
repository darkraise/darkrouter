import { useState } from "react"
import {
  Badge, Button,
  PasswordInput, PasswordInputControl, PasswordInputField,
  PasswordInputVisibilityTrigger,
  TableCell, TableRow,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"
import type { Credential } from "../../lib/api-types"
import { ConfirmButton } from "../shell/confirm-button"

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
    invalidates: [keys.providers, keys.health, keys.overview],
    onSuccess: () => {
      setDraftSecret("")
      setReplacing(false)
    },
  })

  const remove = useApiMutation({
    mutationFn: () => api.del(`/api/providers/${providerId}/keys/${credential.id}`),
    success: "Credential removed",
    invalidates: [keys.providers, keys.health, keys.overview],
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
              <PasswordInput className="w-40">
                <PasswordInputControl>
                  <PasswordInputField
                    placeholder="new secret"
                    autoFocus
                    value={draftSecret}
                    onChange={(e) => setDraftSecret(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Escape") {
                        setReplacing(false)
                        setDraftSecret("")
                      }
                    }}
                  />
                  <PasswordInputVisibilityTrigger />
                </PasswordInputControl>
              </PasswordInput>
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
              {/* Only the half that takes capacity away asks: putting a
                  credential back into service is not something to regret. */}
              {credential.enabled ? (
                <ConfirmButton
                  size="sm"
                  variant="ghost"
                  title={`Disable ${credential.label}?`}
                  description="The router stops dispatching to this credential. It keeps its secret, and enabling it again puts it straight back into service."
                  confirmLabel="Disable"
                  onConfirm={() => patch.mutate({ enabled: false })}
                >
                  Disable
                </ConfirmButton>
              ) : (
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => patch.mutate({ enabled: true })}
                >
                  Enable
                </Button>
              )}
              <Button size="sm" variant="ghost" onClick={() => setReplacing(true)}>
                Replace
              </Button>
              <ConfirmButton
                size="sm"
                variant="ghost"
                className="text-[hsl(var(--destructive))]"
                title={`Remove ${credential.label}?`}
                description="Requests that depended on this credential fail over to whatever else is configured, or stop routing here if nothing else is."
                confirmLabel="Remove"
                destructive
                onConfirm={() => remove.mutate(undefined)}
              >
                Remove
              </ConfirmButton>
            </>
          )}
        </div>
      </TableCell>
    </TableRow>
  )
}
