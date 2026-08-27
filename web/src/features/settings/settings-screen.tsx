import { useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Badge, Banner, Button, Card, toast } from "darkraise-ui"
import {
  AlertTriangle,
  Boxes,
  Clock,
  FileText,
  Server,
  ShieldAlert,
} from "lucide-react"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useConfig, useSessions } from "../../lib/queries"
import type { ConfigFieldMeta, ConfigResponse } from "../../lib/api-types"
import { AccountCard, passwordProblem, revokedText } from "./account-card"
import {
  SOURCE_LABEL,
  SOURCE_NOTE,
  settingGroups,
  type GroupId,
  type SettingRow,
} from "./settings-catalog"

export { passwordProblem, revokedText }

type ReloadResult = { valid: boolean; error?: string; serving?: string }
type SyncResult = { synced: boolean; error?: string; serving?: string }

/** Every field the config endpoint annotates, flattened for display. Kept for
 *  callers that want the raw pairs rather than the grouped view. */
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

const GROUP_ICON: Record<GroupId, typeof Clock> = {
  requests: Clock,
  failure: ShieldAlert,
  catalogue: Boxes,
  logging: FileText,
  server: Server,
}

/**
 * One setting: what it is called, what it does, and what it is set to.
 *
 * The key sits under the name in mono rather than replacing it. It is what
 * the YAML file and every error message use, so dropping it would break the
 * trail from this screen to the file being edited.
 */
function Setting({ row }: { row: SettingRow }) {
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
        {row.literal && (
          <span className="font-mono text-sm text-[hsl(var(--legend))]">{row.literal}</span>
        )}
        <span className="flex items-center gap-1">
          <Badge variant="outline" title={SOURCE_NOTE[row.source]}>
            {SOURCE_LABEL[row.source]}
          </Badge>
          {/* Shown as a fact rather than offered and refused: the endpoint
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

export function SettingsScreen() {
  const config = useConfig()
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
      // an outcome, not a failed request. Treated the same way as reload's
      // failure — a durable banner, not a toast that can vanish before it's
      // read — and the same reason not to refetch a catalog that didn't change.
      if (res.synced) {
        toast.success(syncMessage(res))
        void queryClient.invalidateQueries({ queryKey: keys.models })
      }
    },
  })

  const groups = config.data ? settingGroups(config.data) : []
  const warnings = config.data?.warnings ?? []

  return (
    <>
      <PageHeader
        title="Settings"
        description="What the gateway is set to, and where each setting comes from"
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
          <AlertTriangle
            className="size-5 shrink-0 text-[hsl(var(--warning))]"
            aria-hidden="true"
          />
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

      <div className="flex flex-col gap-4">
        {groups.map(({ group, rows }) => {
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
                  <Setting key={row.field} row={row} />
                ))}
              </div>
            </Card>
          )
        })}
      </div>

      <div className="mt-4 flex flex-col gap-4">
        <AccountCard />

        <Card className="p-4">
          <h2 className="mb-1 text-sm font-medium">Signed-in browsers</h2>
          <p className="mb-3 text-sm text-[hsl(var(--muted-foreground))]">
            Every session that can reach this console. Revoking one signs it out at its
            next request.
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
      </div>
    </>
  )
}
