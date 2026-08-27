import { PageHeader } from "darkraise-ui/layout"
import {
  Badge,
  Button,
  Card,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  toast,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useConfig, useSessions } from "../../lib/queries"
import type { ConfigFieldMeta, ConfigResponse } from "../../lib/api-types"
import { AccountCard, passwordProblem, revokedText } from "./account-card"

export { passwordProblem, revokedText }

type ReloadResult = { valid: boolean; error?: string; serving?: string }
type SyncResult = { synced: boolean; error?: string; serving?: string }

/** Every field the config endpoint annotates, flattened for display. */
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

const SOURCE_NOTE = {
  file: "from darkrouter.yaml",
  // §8.1 requires the config view to say this at the point of display: after
  // the first run, editing the file has no effect on these.
  database: "edited here — the file is no longer read for this",
  default: "not set; this is the built-in default",
} as const

export function SettingsScreen() {
  const config = useConfig()
  const sessions = useSessions()

  const revoke = useApiMutation({
    mutationFn: (id: string) => api.del(`/api/sessions/${id}`),
    success: "Session revoked",
    invalidates: [keys.sessions],
  })

  const reload = useApiMutation({
    mutationFn: () => api.post<ReloadResult>("/api/config/reload"),
    invalidates: [keys.config],
    onSuccess: (res) => {
      // Only the good outcome toasts. A toast for a config that is still
      // broken disappears before it can be acted on — that one gets the
      // banner below instead, which stays up until the next reload attempt.
      if (res.valid) toast.success(reloadMessage(res))
    },
  })

  const sync = useApiMutation({
    mutationFn: () => api.post<SyncResult>("/api/catalog/sync"),
    invalidates: [keys.models],
    onSuccess: (res) => {
      // Sync shares the reload endpoint's shape: a 200 with synced:false is
      // an outcome, not a failed request, so a flat success toast would lie.
      if (res.synced) toast.success("Catalog sync started.")
      else toast.error(`Catalog sync failed: ${res.error ?? "unknown error"}`)
    },
  })

  return (
    <>
      <PageHeader
        title="Settings"
        description="The knobs, and where each one lives"
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
        <Card className="mb-6 border-[hsl(var(--destructive))] p-4">
          <p className="text-sm font-medium">The reloaded configuration is invalid.</p>
          <p className="mt-1 text-sm text-[hsl(var(--muted-foreground))]">
            {reloadMessage(reload.data)}
          </p>
        </Card>
      )}

      {config.data && !config.data.valid && (
        <Card className="mb-6 border-[hsl(var(--destructive))] p-4">
          <p className="text-sm font-medium">The configuration file is invalid.</p>
          <p className="mt-1 text-sm text-[hsl(var(--muted-foreground))]">
            {config.data.error} — {config.data.serving}
          </p>
        </Card>
      )}

      {config.data && config.data.warnings.length > 0 && (
        <Card className="mb-6 p-4">
          <h2 className="mb-2 text-sm font-medium">Warnings</h2>
          <ul className="flex flex-col gap-1 text-xs">
            {config.data.warnings.map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </Card>
      )}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Field</TableHead>
            <TableHead>Value</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>Reload</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {config.data &&
            configRows(config.data).map(({ field, value, meta }) => (
              <TableRow key={field}>
                <TableCell className="font-mono text-xs">{field}</TableCell>
                <TableCell className="font-mono text-xs">{value}</TableCell>
                <TableCell>
                  <span title={SOURCE_NOTE[meta.source]}>{meta.source}</span>
                </TableCell>
                <TableCell>
                  {meta.hot_reloadable ? (
                    <Badge variant="green">hot</Badge>
                  ) : (
                    // Shown as a fact rather than offered and refused: the
                    // endpoint will not accept a write to one of these.
                    <Badge variant="secondary">restart</Badge>
                  )}
                </TableCell>
              </TableRow>
            ))}
        </TableBody>
      </Table>

      <AccountCard />

      <Card className="mt-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Sessions</h2>
        <ul className="flex flex-col gap-2">
          {(sessions.data ?? []).map((s) => (
            <li key={s.id} className="flex items-center gap-3 text-xs">
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
