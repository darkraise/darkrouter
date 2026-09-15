import { useState } from "react"
import { Link } from "@tanstack/react-router"
import { RefreshCw } from "lucide-react"
import {
  Accordion, AccordionContent, AccordionItem, AccordionTrigger, Button,
} from "darkraise-ui"
import { useRequests } from "../../lib/queries"
import { duration } from "@/lib/format"
import { relativeTime } from "../../lib/time"
import { LoadError } from "../shell/screen-state"
import type { RequestRow } from "../../lib/api-types"

/** The fields an expanded row shows, in the order an operator reads them. */
export function detailRows(r: RequestRow): { label: string; value: string; mono?: boolean }[] {
  return [
    { label: "Time", value: new Date(r.ts_ms).toLocaleString(), mono: true },
    { label: "Status", value: r.error_code ? `${r.status} · ${r.error_code}` : r.status },
    { label: "Latency", value: r.total_ms === null ? "—" : duration(r.total_ms) },
    {
      label: "First token",
      value: r.ttft_ms === null ? "—" : duration(r.ttft_ms),
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

/** A cancelled run was stopped by whoever sent it, so it is neither served nor
 *  failed, and wears the same neutral tone RequestStatus gives it. */
function outcomeOf(r: RequestRow): { word: string; tone: string } {
  if (r.status === "success") return { word: "ok", tone: "text-[hsl(var(--success))]" }
  if (r.status === "cancelled") return { word: "cancelled", tone: "text-[hsl(var(--muted-foreground))]" }
  return { word: r.error_code ?? "error", tone: "text-[hsl(var(--destructive))]" }
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
  // Any attempt on the provider rather than the one that served: a test that
  // failed on every attempt names no serving provider at all.
  const page = useRequests({ attempted_provider: providerId, source: "console", limit: "20" })
  const rows = page.data?.requests ?? []

  if (page.isPending) {
    return <p className="p-4 text-sm text-[hsl(var(--muted-foreground))]">Loading the log…</p>
  }

  if (page.isError && !page.data) {
    return (
      <LoadError
        what="The log"
        error={page.error}
        onRetry={() => void page.refetch()}
        className="m-4"
      />
    )
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

      {/* An Accordion, not a <ul> of aria-expanded buttons. The hand-rolled
          version told a screen reader that something had expanded without
          ever saying what: the trigger carried aria-expanded and no
          aria-controls, and the panel had no role of its own. The component
          wires both, and brings arrow-key movement between rows.

          Single-open with `collapsible`, which is the behaviour the row state
          already implemented by hand. */}
      <Accordion
        type="single"
        collapsible
        value={expanded ?? ""}
        onValueChange={(next) => setExpanded(next === "" ? null : next)}
        className="min-h-0 flex-1 divide-y overflow-y-auto"
      >
        {rows.map((r) => {
          const outcome = outcomeOf(r)
          const when = relativeTime(r.ts_ms)
          const latency = r.total_ms === null ? "—" : duration(r.total_ms)
          return (
            <AccordionItem key={r.id} value={r.id}>
              <AccordionTrigger className="gap-3 px-4 py-2 hover:bg-[hsl(var(--muted))]">
                {/* Widths in ch, not rem: the font-size axis grows the text
                    without growing the root, so a rem column spills into
                    its neighbour at the larger steps. */}
                <span
                  className={`w-[12ch] shrink-0 truncate text-left font-mono ${outcome.tone}`}
                  title={outcome.word}
                >
                  {outcome.word}
                </span>
                <span
                  className="w-[10ch] shrink-0 truncate text-left font-mono text-[hsl(var(--legend))]"
                  title={when}
                >
                  {when}
                </span>
                <span className="min-w-0 flex-1 truncate text-left font-mono" title={r.model}>
                  {r.model}
                </span>
                <span
                  className="w-[7ch] shrink-0 truncate text-right font-mono tabular-nums text-[hsl(var(--legend))]"
                  title={latency}
                >
                  {latency}
                </span>
              </AccordionTrigger>

              <AccordionContent className="bg-[hsl(var(--muted))]/40 px-4 pb-3">
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
              </AccordionContent>
            </AccordionItem>
          )
        })}
      </Accordion>

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
