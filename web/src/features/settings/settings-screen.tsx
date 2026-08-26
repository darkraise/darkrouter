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
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useConfig, useSessions } from "../../lib/queries"
import type { ConfigFieldMeta, ConfigResponse } from "../../lib/api-types"

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

  return (
    <>
      <PageHeader title="Settings" description="The knobs, and where each one lives" />

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
