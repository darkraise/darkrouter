import { useEffect, useState } from "react"
import { Card } from "darkraise-ui"
import { Link } from "@tanstack/react-router"
import { count, duration, money } from "../../../lib/format"
import { traceWhenWritten } from "../metrics"
import type { AuxRun } from "./surfaces"
import type { RequestTrace } from "../../../lib/api-types"

function Reading({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-sm text-[hsl(var(--legend))]">{label}</span>
      <span className="text-sm tabular-nums">{value}</span>
    </div>
  )
}

/**
 * What the last run cost, from the gateway's own record of it.
 *
 * The aux endpoints answer in one response with no stream and no usage
 * headers, so unlike Chat there is nothing to count on the way past. The
 * figures come from the trace the request wrote, fetched the same way Chat
 * fetches a turn's — the writer's timer means the row is not there the
 * instant the response lands, and `traceWhenWritten` is the retry that
 * already knows that.
 *
 * Reading the trace rather than tokenising in the browser is the same rule
 * the rest of the playground follows: a number that looks authoritative and
 * is not is worse than no number.
 */
export function RunReadings({ run }: { run: AuxRun | undefined }) {
  const [trace, setTrace] = useState<RequestTrace | null>(null)
  const [waiting, setWaiting] = useState(false)

  useEffect(() => {
    if (!run || run.requestId === "") {
      setTrace(null)
      return
    }
    // Aborted on the way out, so the retry stops with the panel rather than
    // running on and writing into a run that has been superseded or a
    // surface that has been left.
    const controller = new AbortController()
    setWaiting(true)
    setTrace(null)
    void traceWhenWritten(run.requestId, controller.signal).then((t) => {
      if (controller.signal.aborted) return
      setTrace(t)
      setWaiting(false)
    })
    return () => controller.abort()
  }, [run?.requestId, run])

  return (
    <Card className="flex shrink-0 flex-col gap-3 p-4">
      <h2 className="text-sm font-medium">Last run</h2>

      {run === undefined ? (
        <p className="text-sm text-[hsl(var(--muted-foreground))]">
          Nothing has been run on this tool yet.
        </p>
      ) : (
        <>
          <div className="flex flex-col gap-1">
            <Reading label="served by" value={trace?.provider ?? (waiting ? "…" : "—")} />
            <Reading
              label="model"
              value={trace?.final_model ?? trace?.model ?? (waiting ? "…" : "—")}
            />
            <Reading label="total" value={trace ? duration(trace.total_ms) : "—"} />
          </div>

          <div className="flex flex-col gap-1 border-t pt-3">
            <Reading label="tokens in" value={count(trace?.tokens_in)} />
            <Reading label="tokens out" value={count(trace?.tokens_out)} />
            <Reading label="cost" value={money(trace?.cost_micros)} />
          </div>

          {/* Said rather than left as a row of dashes an operator reads as a
              free request. Log retention sweeps traces, and a run whose trace
              never arrived has no figures to show rather than zero ones. */}
          {!waiting && trace === null && (
            <p className="text-sm text-[hsl(var(--legend))]">
              This run wrote no trace, so there is nothing to count.
            </p>
          )}

          {run.requestId !== "" && (
            <Link
              to="/requests/$id"
              params={{ id: run.requestId }}
              className="text-sm underline underline-offset-2"
            >
              Open the full trace
            </Link>
          )}
        </>
      )}
    </Card>
  )
}
