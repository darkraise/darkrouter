import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import {
  Drawer,
  DrawerContent,
  DrawerHeader,
  DrawerTitle,
} from "darkraise-ui/components/drawer"
import { JsonTreeView } from "darkraise-ui/components/json-tree-view"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui/components/table"
import { api } from "../lib/api"

type Attempt = {
  seq: number
  provider: string
  key_label: string
  model: string
  outcome: string
  status_code: number
  latency_ms: number
  error?: string
  path: string
}

export type Trace = {
  id: string
  ts_ms: number
  dialect: string
  surface: string
  model: string
  alias?: string
  provider?: string
  final_model?: string
  status: string
  error_code?: string
  tokens_in: number
  tokens_out: number
  cost_micros: number | null
  ttft_ms: number | null
  total_ms: number | null
  candidates: string[]
  skips: string[]
  attempts: Attempt[]
  warnings: string[]
  surface_meta: Record<string, unknown> | null
  response_bytes: number
  response_content_type: string
  bodies: { kind: string; content: string }[]
}

export function TraceDrawer({ id, onClose }: { id: string | null; onClose: () => void }) {
  const trace = useQuery({
    queryKey: ["trace", id],
    queryFn: () => api.get<Trace>(`/api/requests/${id}`),
    enabled: id !== null,
  })

  const t = trace.data
  return (
    <Drawer open={id !== null} onOpenChange={(open) => (open ? undefined : onClose())}>
      <DrawerContent>
        {!t ? null : (
          <div className="flex max-h-[85vh] flex-col gap-6 overflow-y-auto p-6">
            <DrawerHeader className="p-0">
              <DrawerTitle className="flex items-center gap-2">
                <Badge variant={t.status === "success" ? "green" : "destructive"}>
                  {t.status}
                </Badge>
                <span className="text-muted-foreground font-mono text-xs">{t.id}</span>
              </DrawerTitle>
              <p className="text-muted-foreground text-sm">
                {t.dialect} · {t.surface} · {t.alias ? `${t.alias} → ` : ""}
                {t.provider ? `${t.provider}/${t.final_model}` : t.model}
              </p>
            </DrawerHeader>

            <section>
              <h3 className="mb-2 text-sm font-medium">Attempts</h3>
              {/* In order, one row each. A failover that took three tries reads
                  as three labelled rows, which is spec §6's criterion. */}
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>#</TableHead>
                    <TableHead>Provider</TableHead>
                    <TableHead>Model</TableHead>
                    <TableHead>Outcome</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Latency</TableHead>
                    <TableHead>Path</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {t.attempts.map((a) => (
                    <TableRow key={a.seq}>
                      {/* seq is 0-indexed at the source (exec.recordAttempt
                          uses len(attempts)); "Attempt 0" reads as a bug to an
                          operator, so it is displayed 1-based. */}
                      <TableCell>{a.seq + 1}</TableCell>
                      <TableCell>{a.provider}</TableCell>
                      <TableCell className="font-mono text-xs">{a.model}</TableCell>
                      <TableCell>
                        <Badge variant={a.outcome === "success" ? "green" : "amber"}>
                          {a.outcome}
                        </Badge>
                        {a.error ? (
                          <div className="text-muted-foreground text-xs">{a.error}</div>
                        ) : null}
                      </TableCell>
                      <TableCell>{a.status_code || "—"}</TableCell>
                      <TableCell>{a.latency_ms} ms</TableCell>
                      <TableCell>{a.path}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </section>

            <section className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div>
                <h3 className="mb-2 text-sm font-medium">Candidates</h3>
                {/* What the router produced, in order. Separate from attempts:
                    a candidate that was never tried is not an attempt. */}
                <ul className="flex flex-col gap-1 text-sm">
                  {t.candidates.map((c) => (
                    <li key={c} className="font-mono text-xs">
                      {c}
                    </li>
                  ))}
                  {t.candidates.length === 0 ? (
                    <li className="text-muted-foreground">none</li>
                  ) : null}
                </ul>
              </div>
              <div>
                <h3 className="mb-2 text-sm font-medium">Skipped</h3>
                {/* Spec §6: "the four candidates that were never tried must say
                    why". Each entry is target:reason from the router's trace. */}
                <ul className="flex flex-col gap-1 text-sm">
                  {t.skips.map((s) => {
                    const cut = s.lastIndexOf(":")
                    const target = cut < 0 ? s : s.slice(0, cut)
                    const reason = cut < 0 ? "skipped" : s.slice(cut + 1)
                    return (
                      <li key={s} className="flex items-center gap-2">
                        <span className="font-mono text-xs">{target}</span>
                        <Badge variant="secondary">{reason}</Badge>
                      </li>
                    )
                  })}
                  {t.skips.length === 0 ? (
                    <li className="text-muted-foreground">none</li>
                  ) : null}
                </ul>
              </div>
            </section>

            {t.warnings.length > 0 ? (
              <section>
                <h3 className="mb-2 text-sm font-medium">Warnings</h3>
                {/* Phase 4's dropped-field warnings. This is where a vanished
                    cache_control marker becomes visible. */}
                <ul className="text-muted-foreground flex flex-col gap-1 text-sm">
                  {t.warnings.map((wn) => (
                    <li key={wn}>{wn}</li>
                  ))}
                </ul>
              </section>
            ) : null}

            <section className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
              <div>
                <span className="text-muted-foreground">Tokens in</span>
                <div>{t.tokens_in}</div>
              </div>
              <div>
                <span className="text-muted-foreground">Tokens out</span>
                <div>{t.tokens_out}</div>
              </div>
              <div>
                <span className="text-muted-foreground">Cost</span>
                {/* null, not zero: nothing computes cost yet. */}
                <div>
                  {t.cost_micros === null ? "—" : `$${(t.cost_micros / 1e6).toFixed(4)}`}
                </div>
              </div>
              <div>
                <span className="text-muted-foreground">TTFT</span>
                <div>{t.ttft_ms ?? "—"} ms</div>
              </div>
            </section>

            {t.response_bytes > 0 ? (
              <section className="text-sm">
                <span className="text-muted-foreground">Response</span>
                <div>
                  {t.response_bytes} bytes · {t.response_content_type || "unknown type"}
                </div>
              </section>
            ) : null}

            {t.surface_meta && Object.keys(t.surface_meta).length > 0 ? (
              <section>
                <h3 className="mb-2 text-sm font-medium">Surface detail</h3>
                <JsonTreeView data={t.surface_meta} />
              </section>
            ) : null}

            <section>
              <h3 className="mb-2 text-sm font-medium">Bodies</h3>
              {t.bodies.length === 0 ? (
                // capture.bodies has a retention sweep and no writer, so this is
                // always empty today. Saying so beats an empty panel that looks
                // like a rendering bug.
                <p className="text-muted-foreground text-sm">Not captured.</p>
              ) : (
                t.bodies.map((b) => (
                  <div key={b.kind} className="mb-3">
                    <div className="text-muted-foreground text-xs">{b.kind}</div>
                    <pre className="bg-muted overflow-x-auto rounded p-2 text-xs">
                      {b.content}
                    </pre>
                  </div>
                ))
              )}
            </section>
          </div>
        )}
      </DrawerContent>
    </Drawer>
  )
}
