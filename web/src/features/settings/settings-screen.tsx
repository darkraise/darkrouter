import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Badge, Banner, Button, Card, toast } from "darkraise-ui"
import { AlertTriangle, Boxes, Clock, FileText, KeyRound, Server, ShieldAlert } from "lucide-react"
import { ApiError, api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { ConfirmButton } from "../shell/confirm-button"
import { LoadError, LoadingRows } from "../shell/screen-state"
import { usePurgeConversations } from "../playground/lib/conversations"
import { keys, useConfig, useSessions, useUsers } from "../../lib/queries"
import { dateTime, zoneLabel } from "../../lib/format"
import type { ConfigResponse, Session } from "../../lib/api-types"
import { AccountsCard } from "./accounts-card"
import { ChangePasswordDialog } from "./change-password-dialog"
import { SettingField } from "./setting-field"
import { displayOf, sameSetting, settingGroups, type GroupId } from "./settings-catalog"

export { passwordProblem, revokedText } from "./change-password-dialog"

type ReloadResult = { valid: boolean; error?: string; serving?: string }
type SyncResult = { triggered: boolean }

export type ConfigPatch = { set?: Record<string, string>; reset?: string[] }

export type SaveResult = {
  valid: boolean
  restart_required?: string[]
  error?: string
  serving?: string
}

/**
 * The save, built from the draft.
 *
 * Only what changed. The previous screen sent three policy keys on every save
 * whatever the operator touched, so those three reported as stored until the
 * next start reconciled them back to default -- the flip retiring the old
 * policy write path was meant to end.
 *
 * An emptied box is a reset, because the store cannot hold "" as a value
 * distinct from absent. It resets the one key that was emptied.
 */
export function settingsPatch(
  draft: Record<string, string>,
  reset: Set<string>,
  cfg: ConfigResponse,
): ConfigPatch {
  const set: Record<string, string> = {}
  const clear: string[] = []

  for (const [field, typed] of Object.entries(draft)) {
    const meta = cfg.fields[field]
    // An environment key is not ours to write, and a key the gateway does not
    // report is not one this build knows how to send.
    if (!meta || meta.source === "env") continue
    if (reset.has(field)) continue
    // Trimmed once, for the comparison as well as the send. Comparing the
    // untrimmed text made " 10m0s " a change from "10m0s" and then sent the
    // padding along with it.
    const next = typed.trim()
    const current = cfg.values[field] ?? ""
    if (next === "") {
      // Only if there is a row to delete. Resetting a key already on its
      // default writes nothing and would still be named in restart_required.
      if (meta.source === "database") clear.push(field)
      continue
    }
    if (!sameSetting(next, current, meta.kind)) set[field] = next
  }

  for (const field of reset) {
    const meta = cfg.fields[field]
    if (!meta || meta.source !== "database") continue
    if (!clear.includes(field)) clear.push(field)
  }

  const patch: ConfigPatch = {}
  if (Object.keys(set).length > 0) patch.set = set
  if (clear.length > 0) patch.reset = clear
  return patch
}

/**
 * The server's refusal, against the fields it names.
 *
 * One refusal can belong to several keys: a cross-key rule reverts its whole
 * set together and its message lists them, so the operator sees the complaint
 * on every field that has to move for it to pass rather than on one of them.
 */
export function fieldErrors(message: string, fields: string[]): Record<string, string> {
  const out: Record<string, string> = {}
  for (const field of fields) {
    if (message.includes(field)) out[field] = message
  }
  return out
}

/** Every key one patch touches, which is the set a refusal can name. */
function patchedKeys(patch: ConfigPatch): string[] {
  return [...Object.keys(patch.set ?? {}), ...(patch.reset ?? [])]
}

export function reloadMessage(res: ReloadResult): string {
  if (res.valid) return "Configuration reloaded."
  // A 200 with valid:false is the honest shape: the reload was performed and
  // this is its outcome, not a failed request.
  return [res.error, res.serving ?? "the previous configuration is still serving"]
    .filter(Boolean)
    .join(" — ")
}

export function syncMessage(res: SyncResult): string {
  // The gateway answers 202 and syncs in the background; the models list
  // refetches on its own once the run has landed.
  return res.triggered ? "Catalog sync started." : "Catalog sync was not started."
}

/**
 * What is stored but not yet running.
 *
 * Measured against the snapshot this process booted on, not against the
 * previous reload, so it survives the next unrelated save. The transient
 * warning in `warnings` does not, and that is why this is a separate notice
 * rather than one more line in that list.
 */
export function pendingRestartMessage(fields: string[]): string {
  const list = fields.join(", ")
  return fields.length === 1
    ? `${list} is stored but the gateway is still running the value it started with.`
    : `${list} are stored but the gateway is still running the values it started with.`
}

const GROUP_ICON: Record<GroupId, typeof Clock> = {
  requests: Clock,
  failure: ShieldAlert,
  catalogue: Boxes,
  logging: FileText,
  server: Server,
}

/** Every editable key's stored spelling, which is what a save submits. */
function seedDraft(cfg: ConfigResponse): Record<string, string> {
  const draft: Record<string, string> = {}
  for (const { rows } of settingGroups(cfg)) {
    for (const row of rows) {
      if (row.editable) draft[row.field] = row.value
    }
  }
  return draft
}

/**
 * Every stored setting, in one form with one Save.
 *
 * A save per field would be a way to leave half the visit applied; and the
 * gateway validates the configuration as a whole, so a cross-key rule can only
 * be satisfied by the keys that break it moving together.
 */
function SettingsForm({ cfg }: { cfg: ConfigResponse }) {
  const queryClient = useQueryClient()
  const [draft, setDraft] = useState<Record<string, string>>(() => seedDraft(cfg))
  const [reset, setReset] = useState<Set<string>>(() => new Set())
  const [errors, setErrors] = useState<Record<string, string>>({})

  const reseedFrom = (from: ConfigResponse) => {
    setDraft(seedDraft(from))
    setReset(new Set())
    setErrors({})
  }

  /** Moves the rows the operator has not touched to a newer answer, keeping
   *  every edit. An edit whose row also moved underneath it says so on the
   *  row, since saving will overwrite what the other change stored. */
  const rebaseOnto = (from: ConfigResponse, to: ConfigResponse) => {
    const before = seedDraft(from)
    const after = seedDraft(to)
    const next = { ...after }
    const kept: Record<string, string> = {}
    for (const [f, typed] of Object.entries(draft)) {
      const stored = after[f]
      if (stored === undefined || typed === before[f]) continue
      next[f] = typed
      const error = errors[f]
      if (error) kept[f] = error
      const kind = to.fields[f]?.kind ?? "string"
      if (stored !== before[f] && !sameSetting(stored, typed, kind)) {
        const shown = displayOf(stored, kind)
        kept[f] = `Changed elsewhere to ${shown} while you were editing. Saving replaces it with the value here.`
      }
    }
    setDraft(next)
    setReset((r) => new Set([...r].filter((f) => to.fields[f]?.source === "database")))
    setErrors(kept)
  }

  const [seededFrom, setSeededFrom] = useState(cfg)

  const save = useApiMutation({
    // The toast is this screen's to raise: a refusal naming a key is already
    // shown on that key's row, and saying it twice reads as two complaints
    // about one refusal.
    quietError: true,
    mutationFn: async (patch: ConfigPatch) => {
      try {
        return await api.put<SaveResult>("/api/config", patch)
      } catch (err) {
        // A 401 is handled globally by the unauthorized listener.
        if (err instanceof ApiError && err.status === 401) throw err
        // A 400 is the registry's verdict on a value, and it names the keys it
        // is about. Put it on those rows: a toast alone leaves the operator
        // hunting the field across five cards.
        const mapped =
          err instanceof ApiError && err.status === 400
            ? fieldErrors(err.message, patchedKeys(patch))
            : {}
        setErrors(mapped)
        // A refusal that named no key -- a database failure, an alias problem
        // -- landed on no row, so the toast is the only place it can be said.
        if (Object.keys(mapped).length === 0) toast.error((err as Error).message)
        throw err
      }
    },
    onSuccess: async (res) => {
      // Only the good outcome toasts. A committed write the gateway could not
      // republish is invalid configuration, and the refetch below reports it
      // as such: the screen's own `config.valid` banner says so, with the same
      // error and the same note about what is still serving.
      if (res.valid) toast.success(savedMessage(res))
      // The rows are durable in both 200 shapes, so the served answer is stale
      // either way.
      await queryClient.invalidateQueries({ queryKey: keys.config })
      await queryClient.invalidateQueries({ queryKey: keys.policy })
      if (!res.valid) return
      // Seeded from the refetched answer rather than from the `cfg` this
      // render closed over, which is the pre-save one: reseeding from that
      // flips every saved box back to its old value until the refetch lands,
      // and leaves it there for good if the refetch fails.
      //
      // Reseeded at all -- rather than left to the guard above -- because a
      // save can leave the answer byte-identical: /api/config reports the
      // typed config, so a saved "10m" reads back as "10m0s" and Query shares
      // that response structurally with the old one. The reference never
      // changes, the guard never fires, and the draft would stay dirty
      // against a value that is in fact stored.
      reseedFrom(queryClient.getQueryData<ConfigResponse>(keys.config) ?? cfg)
    },
  })

  // A changed answer during a save is that save's own refetch, and the editors
  // are gated, so the whole draft is reseeded: Go normalises durations on the
  // way out (`Total.String()` turns a typed "10m" into "10m0s"), and a draft
  // kept against that would compare unequal forever under a toast saying the
  // settings were saved. Any other change is a background refetch bringing
  // someone else's write, which must not take the operator's unsaved edits.
  if (cfg !== seededFrom) {
    setSeededFrom(cfg)
    if (save.isPending) reseedFrom(cfg)
    else rebaseOnto(seededFrom, cfg)
  }

  const patch = settingsPatch(draft, reset, cfg)
  const dirty = Object.keys(patch).length > 0

  const change = (field: string, next: string) => {
    setDraft((d) => ({ ...d, [field]: next }))
    setErrors((e) => (field in e ? Object.fromEntries(Object.entries(e).filter(([k]) => k !== field)) : e))
  }

  /** In or out of the save's reset list. The draft is left alone: what the
   *  row will do is said on the row, and emptying the box to say it turned a
   *  stored `true` into an unchecked switch -- a different write entirely. */
  const toggleReset = (field: string) =>
    setReset((r) => {
      const out = new Set(r)
      if (!out.delete(field)) out.add(field)
      return out
    })

  return (
    <>
      <div className="flex flex-col gap-4">
        {settingGroups(cfg).map(({ group, rows }) => {
          const Icon = GROUP_ICON[group.id]
          return (
            <Card key={group.id} className="p-4">
              <div className="mb-3 flex items-start gap-3">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-[var(--radius)] bg-[hsl(var(--muted))]">
                  <Icon className="size-5" aria-hidden="true" />
                </span>
                <div>
                  <h2 className="font-medium">{group.title}</h2>
                  <p className="text-sm text-[hsl(var(--muted-foreground))]">{group.blurb}</p>
                </div>
              </div>
              <div className="flex flex-col">
                {rows.map((row) => (
                  <SettingField
                    key={row.field}
                    row={row}
                    value={draft[row.field] ?? ""}
                    onChange={(next) => change(row.field, next)}
                    // Only a stored row has anything to delete; a key already
                    // on its default would be a no-op the answer still reports
                    // as a write.
                    onReset={row.source === "database" ? () => toggleReset(row.field) : null}
                    resetting={reset.has(row.field)}
                    disabled={save.isPending}
                    error={errors[row.field]}
                  />
                ))}
              </div>
            </Card>
          )
        })}
      </div>

      {/* Only once something has changed: a Save that is always live invites a
          click that writes back what is already there. */}
      {dirty && (
        <div className="sticky bottom-4 mt-4 flex items-center gap-2 rounded-[var(--radius)] border bg-[hsl(var(--card))] p-3 shadow-lg">
          <span className="text-sm">Unsaved changes</span>
          <div className="ml-auto flex gap-2">
            <Button size="sm" variant="ghost" onClick={() => reseedFrom(cfg)}>
              Discard
            </Button>
            <Button size="sm" disabled={save.isPending} onClick={() => save.mutate(patch)}>
              Save
            </Button>
          </div>
        </div>
      )}
    </>
  )
}

/** A restart-only key is accepted rather than refused, so the toast has to say
 *  the value is stored but not yet serving; silence there reads as applied. */
function savedMessage(res: SaveResult): string {
  const pending = res.restart_required ?? []
  if (pending.length === 0) return "Settings saved"
  return `Settings saved. ${pending.join(", ")} ${
    pending.length === 1 ? "takes" : "take"
  } effect after a restart.`
}

/** The caller's own session first, so the row that must not be revoked is
 *  the first one read; the rest in the order the gateway lists them. */
export function orderSessions(sessions: Session[]): Session[] {
  return [...sessions].sort((a, b) => Number(b.current) - Number(a.current))
}

/** GET /api/users answers 403 for a caller who is not an administrator --
 *  every account can still use every other part of this screen, so that is
 *  not a load failure and must not read as one. */
export function accountsForbidden(error: unknown): boolean {
  return error instanceof ApiError && error.status === 403
}

export function SettingsScreen() {
  const config = useConfig()
  const sessions = useSessions()
  const users = useUsers()
  const queryClient = useQueryClient()
  const [passwordOpen, setPasswordOpen] = useState(false)

  const revoke = useApiMutation({
    mutationFn: (id: string) => api.del(`/api/sessions/${id}`),
    success: "Session revoked",
    invalidates: [keys.sessions],
  })

  const reload = useApiMutation({
    mutationFn: () => api.post<ReloadResult>("/api/config/reload"),
    onSuccess: (res) => {
      // Only the good outcome toasts. A toast for a config that is still
      // broken disappears before it can be acted on — that one gets the
      // banner below instead, which stays up until the next reload attempt.
      if (res.valid) {
        toast.success(reloadMessage(res))
        // Refetching on failure would pull back the same invalid config this
        // response already describes, stacking a second banner beside this
        // one for no new information.
        void queryClient.invalidateQueries({ queryKey: keys.config })
      }
    },
  })

  const sync = useApiMutation({
    mutationFn: () => api.post<SyncResult>("/api/catalog/sync"),
    onSuccess: (res) => {
      toast.success(syncMessage(res))
      void queryClient.invalidateQueries({ queryKey: keys.models })
    },
  })

  const purgeConversations = usePurgeConversations()

  const warnings = config.data?.warnings ?? []
  const pendingRestart = config.data?.pending_restart ?? []

  return (
    <>
      <div className="mb-4 flex flex-wrap justify-end gap-2">
        <ConfirmButton
          size="sm"
          variant="outline"
          destructive
          disabled={purgeConversations.isPending}
          title="Delete every saved conversation?"
          description="Every conversation the playground has kept, and every message in them, is removed. This cannot be undone. Turning the setting off stops new ones being saved; this is what removes the ones already there."
          confirmLabel="Delete"
          onConfirm={() => purgeConversations.mutate()}
        >
          Delete saved conversations
        </ConfirmButton>
        <ConfirmButton
          size="sm"
          variant="outline"
          disabled={sync.isPending}
          title="Sync the catalogue now?"
          description="Model definitions are refetched and merged over what is stored. A model the upstream source has dropped stops being offered, which can take routing targets with it."
          confirmLabel="Sync"
          onConfirm={() => sync.mutate()}
        >
          Sync catalog now
        </ConfirmButton>
        <ConfirmButton
          size="sm"
          variant="outline"
          disabled={reload.isPending}
          title="Reload the configuration?"
          description="Settings are re-read from the database and become what the gateway serves. Values that need a restart keep the ones the process started with."
          confirmLabel="Reload"
          onConfirm={() => reload.mutate()}
        >
          Reload config
        </ConfirmButton>
      </div>

      {reload.data && !reload.data.valid && (
        <Banner variant="destructive" className="mb-4">
          <p className="text-sm font-medium">The reloaded configuration is invalid</p>
          <p className="mt-1 text-sm">{reloadMessage(reload.data)}</p>
        </Banner>
      )}

      {config.data && !config.data.valid && (
        <Banner variant="destructive" className="mb-4">
          <p className="text-sm font-medium">The configuration is invalid</p>
          <p className="mt-1 font-mono text-sm break-words">{config.data.error}</p>
          {config.data.serving && <p className="mt-1 text-sm">{config.data.serving}</p>}
        </Banner>
      )}

      {pendingRestart.length > 0 && (
        <Banner className="mb-4">
          <p className="text-sm font-medium">Waiting for a restart</p>
          <p className="mt-1 text-sm">{pendingRestartMessage(pendingRestart)}</p>
        </Banner>
      )}

      {warnings.length > 0 && (
        <Card className="mb-4 flex gap-3 p-4">
          <AlertTriangle className="size-5 shrink-0 text-[hsl(var(--warning))]" aria-hidden="true" />
          <div>
            <h2 className="text-sm font-medium">
              {warnings.length === 1 ? "One warning" : `${warnings.length} warnings`}
            </h2>
            <ul className="mt-1 flex flex-col gap-1 text-sm text-[hsl(var(--muted-foreground))]">
              {warnings.map((warning) => (
                <li key={warning}>{warning}</li>
              ))}
            </ul>
          </div>
        </Card>
      )}

      {config.isError && (
        <LoadError
          what="The configuration"
          error={config.error}
          onRetry={() => void config.refetch()}
          className="mb-4"
        />
      )}
      {config.isPending && !config.isError && <LoadingRows rows={6} />}

      {config.data && <SettingsForm cfg={config.data} />}

      <Card className="mt-4 p-4">
        <div className="flex flex-wrap items-start gap-3">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-[var(--radius)] bg-[hsl(var(--muted))]">
            <KeyRound className="size-5" aria-hidden="true" />
          </span>
          <div className="min-w-0 flex-1">
            <h2 className="font-medium">Password</h2>
            <p className="text-sm text-[hsl(var(--muted-foreground))]">
              The password for your own account. Changing it signs your other browsers out.
            </p>
          </div>
          <Button size="sm" variant="outline" onClick={() => setPasswordOpen(true)}>
            Change password
          </Button>
        </div>
        <ChangePasswordDialog open={passwordOpen} onOpenChange={setPasswordOpen} />
      </Card>

      <Card className="mt-4 p-4">
        <h2 className="mb-1 text-sm font-medium">Signed-in browsers</h2>
        <p className="mb-3 text-sm text-[hsl(var(--muted-foreground))]">
          Where your own account is signed in. Other accounts have sessions of their own,
          which are not listed here. Revoking one signs it out at its next request.
          Times in {zoneLabel()}.
        </p>
        {sessions.isError && (
          <LoadError
            what="The session list"
            error={sessions.error}
            onRetry={() => void sessions.refetch()}
          />
        )}
        {sessions.isPending && <LoadingRows rows={2} />}
        <ul className="flex flex-col gap-2">
          {orderSessions(sessions.data ?? []).map((s) => (
            <li key={s.id} className="flex items-center gap-3 text-sm">
              <span className="font-mono">{s.prefix}…</span>
              <span className="text-[hsl(var(--legend))]">since {dateTime(s.created_at)}</span>
              {s.current ? (
                // Naming the caller's row is what stops an operator revoking
                // the session they are using and wondering what broke.
                <Badge variant="green">this browser</Badge>
              ) : (
                <ConfirmButton
                  size="sm"
                  variant="ghost"
                  className="text-[hsl(var(--destructive))]"
                  title="Revoke this session?"
                  description={`Whoever is signed in at ${s.prefix}… is signed out at their next request and has to log in again.`}
                  confirmLabel="Revoke"
                  destructive
                  onConfirm={() => revoke.mutate(s.id)}
                >
                  Revoke
                </ConfirmButton>
              )}
            </li>
          ))}
        </ul>
      </Card>

      {users.isError && !accountsForbidden(users.error) && (
        <LoadError
          what="The account list"
          error={users.error}
          onRetry={() => void users.refetch()}
          className="mt-4"
        />
      )}
      {users.isError && accountsForbidden(users.error) && (
        // Not a load failure: every account reaches every other part of this
        // screen, and a member simply has no accounts to manage. A destructive
        // banner here would contradict the sentence the accounts card itself
        // uses to explain that.
        <Card className="mt-4 p-4">
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            Every account can sign in and use every screen. Managing accounts is limited to
            administrators.
          </p>
        </Card>
      )}
      {users.isPending && <LoadingRows rows={2} className="mt-4 flex flex-col gap-2" />}
      {users.data && <AccountsCard users={users.data.users} me={users.data.me} />}
    </>
  )
}
