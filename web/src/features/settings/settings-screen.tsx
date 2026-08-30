import { useState, type ChangeEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Badge, Banner, Button, Card, Input, Label, toast } from "darkraise-ui"
import { AlertTriangle, Boxes, Clock, FileText, Server, ShieldAlert } from "lucide-react"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { ConfirmButton } from "../shell/confirm-button"
import { usePurgeConversations } from "../playground/lib/conversations"
import { keys, useConfig, usePolicy, useSessions } from "../../lib/queries"
import type { ConfigResponse, PolicyBlock } from "../../lib/api-types"
import {
  EDITABLE,
  SOURCE_LABEL,
  SOURCE_NOTE,
  settingGroups,
  type EditableSetting,
  type GroupId,
  type SettingRow,
} from "./settings-catalog"

export { passwordProblem, revokedText } from "./change-password-dialog"

type ReloadResult = { valid: boolean; error?: string; serving?: string }
type SyncResult = { synced: boolean; error?: string; serving?: string }

/**
 * The settings this screen shows but cannot change, grouped for reading.
 *
 * The editable fields are removed rather than repeated. Listing a setting as a
 * live input above and as a read-only row below is what made the previous
 * version of this screen show the same five values twice under two different
 * names; the source and restart facts they would have carried belong to the
 * field they are already displayed as.
 *
 * A group emptied by that removal drops out, so "Requests" does not appear as
 * a heading with nothing under it.
 */
export function readOnlyGroups(cfg: ConfigResponse) {
  const editable = new Set(EDITABLE.map((e) => e.field))
  return settingGroups(cfg)
    .map((section) => ({
      ...section,
      rows: section.rows.filter((row) => !editable.has(row.field)),
    }))
    .filter((section) => section.rows.length > 0)
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
  // SyncOnce runs synchronously and this response is its result, not an
  // acknowledgement — the sync is done, not started.
  if (res.synced) return "Catalog synced."
  return [res.error, res.serving ?? "the previous metadata is still serving"]
    .filter(Boolean)
    .join(" — ")
}

type Draft = Record<string, string>

/**
 * The form's starting values.
 *
 * Every block is read defensively. A policy response missing one is not
 * something the gateway sends, but reading through it unguarded took the
 * whole screen down with it -- including the banners that would have said
 * what was wrong.
 */
export function toDraft(policy: PolicyBlock): Draft {
  const trip = policy.cooldown?.trip_after
  return {
    "policy.cooldown.trip_after": trip !== undefined ? String(trip) : "",
    "policy.cooldown.max": policy.cooldown?.max ?? "",
    "policy.retry.max_attempts":
      policy.retry?.max_attempts !== undefined ? String(policy.retry.max_attempts) : "",
    "policy.timeout.total": policy.timeout?.total ?? "",
    "policy.timeout.idle": policy.timeout?.idle ?? "",
  }
}

export type PolicyWrite = {
  cooldown: { trip_after?: number; max: string }
  /** Optional, mirroring the Go `policyWrite` where every block is a pointer:
   *  an omitted block leaves the setting alone, which is what an emptied or
   *  unparseable field should do rather than writing a value nobody typed. */
  retry?: { max_attempts: number }
  timeout: { total: string; idle: string }
}

/**
 * The write, built from the draft.
 *
 * `connect` and `first_byte` never enter it. Both configure the one shared
 * transport built at startup, so no reload can apply them and `PUT
 * /api/policy` refuses a write that touches either — they are omitted rather
 * than sent and rejected.
 */
export function toWrite(draft: Draft): PolicyWrite {
  const tripAfter = (draft["policy.cooldown.trip_after"] ?? "").trim()
  const attempts = (draft["policy.retry.max_attempts"] ?? "").trim()
  return {
    cooldown: {
      max: draft["policy.cooldown.max"] ?? "",
      ...(tripAfter !== "" ? { trip_after: Number(tripAfter) } : {}),
    },
    // Left out rather than sent as 0. `Number("")` is 0, and the store reads
    // 0 as "no override" (`ok = p.Retry.MaxAttempts != 0`), so an emptied box
    // would delete the setting and silently fall back to the file default
    // under a toast reporting success. NaN is worse: it serialises to null and
    // the field is ignored with no complaint either.
    ...(Number.isInteger(Number(attempts)) && attempts !== ""
      ? { retry: { max_attempts: Number(attempts) } }
      : {}),
    timeout: {
      total: draft["policy.timeout.total"] ?? "",
      idle: draft["policy.timeout.idle"] ?? "",
    },
  }
}

const GROUP_ICON: Record<GroupId, typeof Clock> = {
  requests: Clock,
  failure: ShieldAlert,
  catalogue: Boxes,
  logging: FileText,
  server: Server,
}

function SettingField({
  setting,
  value,
  onChange,
}: {
  setting: EditableSetting
  value: string
  onChange: (next: string) => void
}) {
  return (
    <div className="flex flex-wrap items-start gap-4 border-t py-3 first:border-t-0 first:pt-0">
      <div className="min-w-0 flex-1">
        <Label htmlFor={setting.field} className="font-medium">
          {setting.name}
        </Label>
        <p className="text-sm text-[hsl(var(--muted-foreground))]">{setting.description}</p>
        <p className="font-mono text-sm text-[hsl(var(--legend))]">{setting.field}</p>
      </div>
      <Input
        id={setting.field}
        value={value}
        onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.target.value)}
        inputMode={setting.kind === "count" ? "numeric" : undefined}
        placeholder={setting.placeholder}
        className="w-40 shrink-0 font-mono"
      />
    </div>
  )
}

/**
 * One setting this screen shows but cannot change.
 *
 * The key sits under the name in mono rather than replacing it. It is what
 * the YAML file and every error message use, so dropping it would break the
 * trail from this screen to the file being edited.
 */
function ReadOnlySetting({ row }: { row: SettingRow }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1 border-t py-3 first:border-t-0 first:pt-0">
      <div className="min-w-0 flex-1">
        <p className="font-medium">{row.meta.name}</p>
        {row.meta.description && (
          <p className="text-sm text-[hsl(var(--muted-foreground))]">{row.meta.description}</p>
        )}
        <p className="font-mono text-sm text-[hsl(var(--legend))]">{row.field}</p>
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1">
        <span className="font-mono text-base font-medium tabular-nums">{row.display}</span>
        {/* The file's own spelling, when the humanised form differs from it:
            720h0m0s reads as 30 days here and still says the first in YAML. */}
        {row.literal && (
          <span className="font-mono text-sm text-[hsl(var(--legend))]">{row.literal}</span>
        )}
        <span className="flex items-center gap-1">
          <Badge variant="outline" title={SOURCE_NOTE[row.source]}>
            {SOURCE_LABEL[row.source]}
          </Badge>
          {/* Stated as a fact rather than offered and refused: PUT /api/policy
              will not accept a write to a restart-only field. */}
          {row.hotReloadable ? (
            <Badge variant="green">hot</Badge>
          ) : (
            <Badge variant="secondary">restart</Badge>
          )}
        </span>
      </div>
    </div>
  )
}

/** Everything the gateway is set to that this console cannot change, and
 *  where each value came from. §8.1 requires the source to be said at the
 *  point of display: after the first run a database value means editing the
 *  file has no effect. */
function ReadOnlySettings({ cfg }: { cfg: ConfigResponse }) {
  const sections = readOnlyGroups(cfg)
  if (sections.length === 0) return null
  return (
    <div className="mt-4 flex flex-col gap-4">
      <div>
        <h2 className="text-sm font-medium">Read-only configuration</h2>
        <p className="text-sm text-[hsl(var(--muted-foreground))]">
          Set in <span className="font-mono">darkrouter.yaml</span> or left at its default.
          There is no write endpoint for these — change the file and reload, or restart
          where the badge says so.
        </p>
      </div>
      {sections.map(({ group, rows }) => {
        const Icon = GROUP_ICON[group.id]
        return (
          <Card key={group.id} className="p-4">
            <div className="mb-3 flex items-start gap-3">
              <span className="flex size-9 shrink-0 items-center justify-center rounded-[var(--radius)] bg-[hsl(var(--muted))]">
                <Icon className="size-5" aria-hidden="true" />
              </span>
              <div>
                <h3 className="font-medium">{group.title}</h3>
                <p className="text-sm text-[hsl(var(--muted-foreground))]">{group.blurb}</p>
              </div>
            </div>
            <div className="flex flex-col">
              {rows.map((row) => (
                <ReadOnlySetting key={row.field} row={row} />
              ))}
            </div>
          </Card>
        )
      })}
    </div>
  )
}

function PolicySettings({ policy }: { policy: PolicyBlock }) {
  const [draft, setDraft] = useState<Draft>(() => toDraft(policy))
  const clean = toDraft(policy)
  // Reseeded whenever the server's answer changes, which is what a successful
  // save produces. Without this the bar never clears: Go normalises durations
  // on the way out (`Total.String()` turns a typed "10m" into "10m0s"), so the
  // draft and the saved value compare unequal forever and a live Save button
  // sits under a toast saying the settings were saved.
  const [seededFrom, setSeededFrom] = useState(policy)
  if (policy !== seededFrom) {
    setSeededFrom(policy)
    setDraft(toDraft(policy))
  }
  const dirty = Object.keys(clean).some((k) => draft[k] !== clean[k])

  const save = useApiMutation({
    mutationFn: (body: PolicyWrite) => api.put("/api/policy", body),
    success: "Settings saved",
    invalidates: [keys.policy, keys.config],
  })

  const groups = [
    { id: "requests" as const, title: "Requests", blurb: "How long the router waits, and how many providers it will try." },
    { id: "failure" as const, title: "Failure handling", blurb: "When a credential is taken out of rotation, and for how long." },
  ]

  return (
    <>
      <div className="flex flex-col gap-4">
        {groups.map((group) => {
          const Icon = GROUP_ICON[group.id]
          const fields = EDITABLE.filter((s) => s.group === group.id)
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
                {fields.map((setting) => (
                  <SettingField
                    key={setting.field}
                    setting={setting}
                    value={draft[setting.field] ?? ""}
                    onChange={(next) =>
                      setDraft((d) => ({ ...d, [setting.field]: next }))
                    }
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
            <Button size="sm" variant="ghost" onClick={() => setDraft(clean)}>
              Discard
            </Button>
            <Button size="sm" disabled={save.isPending} onClick={() => save.mutate(toWrite(draft))}>
              Save
            </Button>
          </div>
        </div>
      )}
    </>
  )
}

export function SettingsScreen() {
  const config = useConfig()
  const policy = usePolicy()
  const sessions = useSessions()
  const queryClient = useQueryClient()

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
      // Sync shares the reload endpoint's shape: a 200 with synced:false is
      // an outcome, not a failed request.
      if (res.synced) {
        toast.success(syncMessage(res))
        void queryClient.invalidateQueries({ queryKey: keys.models })
      }
    },
  })

  const purgeConversations = usePurgeConversations()

  const warnings = config.data?.warnings ?? []

  return (
    <>
      <PageHeader
        title="Settings"
        description="What the gateway is set to, and where each setting comes from"
        actions={
          <div className="flex gap-2">
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
              title="Reload the config file?"
              description="The file on disk becomes what the gateway serves. Anything changed in the console that the file still contradicts goes back to the file's version."
              confirmLabel="Reload"
              onConfirm={() => reload.mutate()}
            >
              Reload config
            </ConfirmButton>
          </div>
        }
      />

      {reload.data && !reload.data.valid && (
        <Banner variant="destructive" className="mb-4">
          <p className="text-sm font-medium">The reloaded configuration is invalid</p>
          <p className="mt-1 text-sm">{reloadMessage(reload.data)}</p>
        </Banner>
      )}

      {sync.data && !sync.data.synced && (
        <Banner variant="destructive" className="mb-4">
          <p className="text-sm font-medium">The catalog sync failed</p>
          <p className="mt-1 text-sm">{syncMessage(sync.data)}</p>
        </Banner>
      )}

      {config.data && !config.data.valid && (
        <Banner variant="destructive" className="mb-4">
          <p className="text-sm font-medium">The configuration file is invalid</p>
          <p className="mt-1 font-mono text-sm break-words">{config.data.error}</p>
          {config.data.serving && <p className="mt-1 text-sm">{config.data.serving}</p>}
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

      {policy.data && <PolicySettings policy={policy.data} />}

      {config.data && <ReadOnlySettings cfg={config.data} />}

      <Card className="mt-4 p-4">
        <h2 className="mb-1 text-sm font-medium">Signed-in browsers</h2>
        <p className="mb-3 text-sm text-[hsl(var(--muted-foreground))]">
          Every session that can reach this console. Revoking one signs it out at its next
          request.
        </p>
        <ul className="flex flex-col gap-2">
          {(sessions.data ?? []).map((s) => (
            <li key={s.id} className="flex items-center gap-3 text-sm">
              <span className="font-mono">{s.prefix}…</span>
              <span className="text-[hsl(var(--legend))]">
                since {new Date(s.created_at).toLocaleString()}
              </span>
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
    </>
  )
}
