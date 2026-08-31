import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { ChevronRight, RefreshCw } from "lucide-react"
import { Button } from "darkraise-ui"
import { useRequests } from "../../lib/queries"
import { formatLatency } from "../requests/requests-columns"
import { relativeTime } from "../../lib/time"
import type { RequestRow } from "../../lib/api-types"

/** The fields an expanded row shows, in the order an operator reads them. */
export function detailRows(r: RequestRow): { label: string; value: string; mono?: boolean }[] {
  return [
    { label: "Time", value: new Date(r.ts_ms).toLocaleString(), mono: true },
    { label: "Status", value: r.error_code ? `${r.status} · ${r.error_code}` : r.status },
    { label: "Latency", value: r.total_ms === null ? "—" : formatLatency(r.total_ms) },
    {
      label: "First token",
      value: r.ttft_ms === null ? "—" : formatLatency(r.ttft_ms),
    },
    { label: "Asked for", value: r.alias ? `${r.alias} → ${r.model}` : r.model, mono: true },
    { label: "Served", value: r.final_model ?? r.model, mono: true },
    { label: "Provider", value: r.provider ?? "—", mono: true },
    { label: "Attempts", value: String(r.attempts) },
    { label: "Tokens", value: `${r.tokens_in} in · ${r.tokens_out} out`, mono: true },
    { label: "Surface", value: r.surface, mono: true },
    { label: "Path", value: r.path ?? "—", mono: true },
  ]
}

/**
 * This provider's requests, from the log the gateway already keeps.
 *
 * Not a transcript of what this drawer has done: every run here goes through
 * the executor, so it is already written to the request log with its full
 * attempt trail — and so is every request a real client made. A second,
 * in-memory list would have shown less, forgotten itself on close, and
 * disagreed with the Requests screen about the same request.
 */
export function TestLogTab({ providerId }: { providerId: string }) {
  const [expanded, setExpanded] = useState<string | null>(null)
  // Console traffic only. Every run from this drawer goes through the real
  // executor and lands in the same log a client's request does, so without the
  // filter this panel would fill with production traffic an operator did not
  // come here to read — and the one test they just sent would be buried in it.
  const page = useRequests({ provider: providerId, source: "console", limit: "20" })
  const rows = page.data?.requests ?? []

  if (page.isPending) {
    return <p className="p-4 text-sm text-[hsl(var(--muted-foreground))]">Loading the log…</p>
  }

  if (rows.length === 0) {
    return (
      <div className="p-4">
        <p className="text-sm font-medium">Nothing tested yet</p>
        <p className="mt-1 text-sm text-[hsl(var(--muted-foreground))]">
          Runs from this drawer and from the playground are recorded here. Your
          clients' own traffic is not — that is on the Requests screen.
        </p>
      </div>
    )
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-center justify-between gap-2 px-4 py-2">
        <span className="text-sm text-[hsl(var(--legend))]">
          Last {rows.length} {rows.length === 1 ? "test" : "tests"}
        </span>
        <Button size="sm" variant="ghost" onClick={() => void page.refetch()}>
          <RefreshCw className="size-[var(--icon-size)]" />
          Refresh
        </Button>
      </div>

      <ul className="min-h-0 flex-1 divide-y overflow-y-auto">
        {rows.map((r) => {
          const open = expanded === r.id
          const failed = r.status !== "success"
          return (
            <li key={r.id}>
              <button
                type="button"
                onClick={() => setExpanded(open ? null : r.id)}
                aria-expanded={open}
                className="flex w-full items-center gap-3 px-4 py-2 text-left hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))] focus-visible:-outline-offset-2"
              >
                <span
                  className={
                    failed
                      ? "w-16 shrink-0 font-mono text-sm text-[hsl(var(--destructive))]"
                      : "w-16 shrink-0 font-mono text-sm text-[hsl(var(--success))]"
                  }
                >
                  {failed ? r.error_code ?? "error" : "ok"}
                </span>
                <span className="w-20 shrink-0 font-mono text-sm text-[hsl(var(--legend))]">
                  {relativeTime(r.ts_ms)}
                </span>
                <span className="min-w-0 flex-1 truncate font-mono text-sm" title={r.model}>
                  {r.model}
                </span>
                <span className="w-16 shrink-0 text-right font-mono text-sm tabular-nums text-[hsl(var(--legend))]">
                  {r.total_ms === null ? "—" : formatLatency(r.total_ms)}
                </span>
                <ChevronRight
                  className={
                    open
                      ? "size-[var(--icon-size,1rem)] shrink-0 rotate-90 transition-transform"
                      : "size-[var(--icon-size,1rem)] shrink-0 transition-transform"
                  }
                  aria-hidden="true"
                />
              </button>

              {open && (
                <div className="bg-[hsl(var(--muted))]/40 px-4 pb-3">
                  <dl className="grid grid-cols-[8rem_1fr] gap-x-3 gap-y-1 text-sm">
                    {detailRows(r).map((d) => (
                      <div key={d.label} className="contents">
                        <dt className="text-[hsl(var(--legend))]">{d.label}</dt>
                        <dd className={d.mono ? "truncate font-mono" : "truncate"}>{d.value}</dd>
                      </div>
                    ))}
                  </dl>
                  {/* The attempt trail, the candidates and the skips live on
                      the trace. This row is the summary of it. */}
                  <Link
                    to="/requests/$id"
                    params={{ id: r.id }}
                    className="mt-2 inline-block text-sm underline underline-offset-2"
                  >
                    Open the full trace
                  </Link>
                </div>
              )}
            </li>
          )
        })}
      </ul>

      <div className="border-t px-4 py-2 text-center">
        <Link
          to="/requests"
          search={{ provider: providerId }}
          className="text-sm underline underline-offset-2"
        >
          Open every request for {providerId}
        </Link>
      </div>
    </div>
  )
}
