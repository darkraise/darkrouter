import { Link } from "@tanstack/react-router"
import { Card } from "darkraise-ui"
import type { FailoverRow } from "../../lib/api-types"
import { failoverLabel } from "./overview-screen"

/**
 * The last handful of requests that took more than one attempt.
 *
 * A fleet-wide error rate hides one provider quietly degrading; this is the
 * early warning the rate cannot give.
 */
export function Failovers({ rows }: { rows: FailoverRow[] }) {
  if (rows.length === 0) return null
  return (
    <Card className="mt-6 p-4">
      <h2 className="mb-2 text-sm font-medium">Recent failovers</h2>
      <ul className="flex flex-col gap-1 font-mono text-xs">
        {rows.map((row) => (
          <li key={row.id}>
            <Link to="/requests/$id" params={{ id: row.id }} className="underline">
              {failoverLabel(row)}
            </Link>
            <span className="ml-2 text-[hsl(var(--legend))]">
              {new Date(row.ts).toLocaleTimeString()} · {row.total_ms}ms
            </span>
          </li>
        ))}
      </ul>
    </Card>
  )
}
