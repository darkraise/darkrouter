import { Badge, Card } from "darkraise-ui"
import type { BreakerEntry } from "../../lib/api-types"
import { ProviderIcon } from "./provider-icon"
import type { ProviderRow } from "./provider-rows"
import { STATE_VARIANT } from "./provider-state"

/**
 * One provider as a tile.
 *
 * The grid trades the table's columns for scanning by mark: across two hundred
 * providers the question "which one is cerebras" is answered by the logo faster
 * than by reading a name column, which is the only reason this view exists
 * alongside the list.
 */
export function ProviderCard({
  row,
  cooling,
  onOpen,
}: {
  row: ProviderRow
  cooling: BreakerEntry[]
  onOpen: () => void
}) {
  return (
    <Card className={row.configured ? "p-0" : "p-0 opacity-70"}>
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full flex-col gap-3 rounded-[inherit] p-4 text-left transition-colors hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))] focus-visible:-outline-offset-2"
      >
        <div className="flex items-start gap-3">
          <ProviderIcon preset={row.preset} id={row.id} name={row.name} size={36} />
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{row.name}</p>
            <p className="truncate font-mono text-sm text-[hsl(var(--legend))]">{row.id}</p>
          </div>
          <Badge variant={STATE_VARIANT[row.state]}>{row.state}</Badge>
        </div>

        <dl className="grid grid-cols-2 gap-y-1 text-sm">
          <dt className="text-[hsl(var(--legend))]">Priority</dt>
          <dd className="text-right tabular-nums">
            {row.priority ?? <span className="text-[hsl(var(--legend))]">—</span>}
          </dd>
          <dt className="text-[hsl(var(--legend))]">Accounts</dt>
          <dd className="text-right tabular-nums">
            {row.configured ? (
              <>
                {row.accounts}
                {cooling.length > 0 && (
                  <span className="ml-1 text-[hsl(var(--warning))]">· {cooling.length} cooling</span>
                )}
              </>
            ) : (
              <span className="text-[hsl(var(--legend))]">none</span>
            )}
          </dd>
          <dt className="text-[hsl(var(--legend))]">Kind</dt>
          <dd className="truncate text-right font-mono">{row.kind}</dd>
        </dl>
      </button>
    </Card>
  )
}
