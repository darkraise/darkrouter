import { Link } from "@tanstack/react-router"
import { Badge, Card } from "darkraise-ui"
import type { FailoverRow } from "../../lib/api-types"
import { dateTime, duration, zoneLabel } from "../../lib/format"
import { failoverLabel } from "./failover-label"

/**
 * The last handful of requests that took more than one attempt.
 *
 * A fleet-wide error rate hides one provider quietly degrading; this is the
 * early warning the rate cannot give.
 *
 * Laid out as columns rather than a sentence per row: five rows are read down
 * the time column and across to the route, which a run-on string of the same
 * facts makes impossible.
 */
export function Failovers({ rows }: { rows: FailoverRow[] }) {
  if (rows.length === 0) return null
  return (
    <section className="mt-6">
      <h2 className="mb-2 text-sm font-medium">
        Recent failovers{" "}
        <span className="font-normal text-[hsl(var(--legend))]">· times in {zoneLabel()}</span>
      </h2>
      <Card className="overflow-hidden">
        <ul className="divide-y divide-[hsl(var(--border))]">
          {rows.map((row) => (
            <li key={row.id}>
              <Link
                to="/requests/$id"
                params={{ id: row.id }}
                className="flex items-center gap-3 px-4 py-2 text-sm hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))] focus-visible:-outline-offset-2"
              >
                <span className="shrink-0 whitespace-nowrap font-mono text-sm text-[hsl(var(--legend))] tabular-nums">
                  {dateTime(row.ts)}
                </span>
                <span className="min-w-0 flex-1 truncate font-mono text-sm">
                  {failoverLabel(row)}
                </span>
                <span className="shrink-0">
                  <Badge variant="outline" size="sm" className="font-mono">
                    ×{row.attempts}
                  </Badge>
                </span>
                <span className="shrink-0 whitespace-nowrap text-right font-mono text-sm tabular-nums">
                  {duration(row.total_ms)}
                </span>
              </Link>
            </li>
          ))}
        </ul>
      </Card>
    </section>
  )
}
