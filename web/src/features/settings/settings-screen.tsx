import { useState, type ChangeEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Badge, Banner, Button, Card, Input, Label, toast } from "darkraise-ui"
import { AlertTriangle, Clock, ShieldAlert } from "lucide-react"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useConfig, usePolicy, useSessions } from "../../lib/queries"
import type { ConfigFieldMeta, ConfigResponse, PolicyBlock } from "../../lib/api-types"
import { EDITABLE, type EditableSetting } from "./settings-catalog"

export { passwordProblem, revokedText } from "./change-password-dialog"

type ReloadResult = { valid: boolean; error?: string; serving?: string }
type SyncResult = { synced: boolean; error?: string; serving?: string }

/** Every field the config endpoint annotates, flattened. Kept for callers
 *  that want the raw pairs rather than the edited view. */
export function configRows(cfg: ConfigResponse): {
  field: string
  value: string
  meta: ConfigFieldMeta
}[] {
  return Object.entries(cfg.fields)
    .map(([field, meta]) => ({ field, value: readValue(cfg, field), meta }))
    .sort((a, b) => a.field.localeCompare(b.field))
}

/** Walks a dotted field name into the blocks payload. */
export function readValue(cfg: ConfigResponse, field: string): string {
  let node: unknown = cfg.blocks
  for (const part of field.split(".")) {
    if (typeof node !== "object" || node === null) return "—"
    node = (node as Record<string, unknown>)[part]
  }
  if (node === undefined || node === null) return "—"
  if (typeof node === "object") return JSON.stringify(node)
  return String(node)
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
  retry: { max_attempts: number }
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
  return {
    cooldown: {
      max: draft["policy.cooldown.max"] ?? "",
      ...(tripAfter !== "" ? { trip_after: Number(tripAfter) } : {}),
    },
    retry: { max_attempts: Number(draft["policy.retry.max_attempts"] ?? 0) },
    timeout: {
      total: draft["policy.timeout.total"] ?? "",
      idle: draft["policy.timeout.idle"] ?? "",
    },
  }
}

const GROUP_ICON = { requests: Clock, failure: ShieldAlert } as const

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

function PolicySettings({ policy }: { policy: PolicyBlock }) {
  const [draft, setDraft] = useState<Draft>(() => toDraft(policy))
  const clean = toDraft(policy)
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

  const warnings = config.data?.warnings ?? []

  return (
    <>
      <PageHeader
        title="Settings"
        description="The routing knobs an operator can change here"
        actions={
          <div className="flex gap-2">
            <Button size="sm" variant="outline" disabled={sync.isPending} onClick={() => sync.mutate()}>
              Sync catalog now
            </Button>
            <Button size="sm" variant="outline" disabled={reload.isPending} onClick={() => reload.mutate()}>
              Reload config
            </Button>
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
                <Button size="sm" variant="ghost" onClick={() => revoke.mutate(s.id)}>
                  Revoke
                </Button>
              )}
            </li>
          ))}
        </ul>
      </Card>
    </>
  )
}
