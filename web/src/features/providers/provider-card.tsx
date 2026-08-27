import { Badge, Card } from "darkraise-ui"
import type { BreakerEntry, Provider } from "../../lib/api-types"
import { ProviderIcon } from "./provider-icon"
import { STATE_VARIANT, providerState } from "./provider-state"

/**
 * One provider as a tile.
 *
 * The grid trades the table's columns for scanning by mark: at eight or eighty
 * providers the question "which one is cerebras" is answered by the logo
 * faster than by reading a name column, which is the only reason this view
 * exists alongside the list.
 */
export function ProviderCard({
  provider,
  cooling,
  onOpen,
}: {
  provider: Provider
  cooling: BreakerEntry[]
  onOpen: () => void
}) {
  const state = providerState(provider)
  return (
    <Card className="p-0">
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full flex-col gap-3 rounded-[inherit] p-4 text-left transition-colors hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))] focus-visible:-outline-offset-2"
      >
        <div className="flex items-start gap-3">
          <ProviderIcon preset={provider.preset} id={provider.id} name={provider.name} size={32} />
          <div className="min-w-0 flex-1">
            <p className="truncate font-medium">{provider.name}</p>
            <p className="truncate font-mono text-sm text-[hsl(var(--legend))]">{provider.id}</p>
          </div>
          <Badge variant={STATE_VARIANT[state]}>{state}</Badge>
        </div>

        <dl className="grid grid-cols-2 gap-y-1 text-sm">
          <dt className="text-[hsl(var(--legend))]">Priority</dt>
          <dd className="text-right tabular-nums">{provider.priority}</dd>
          <dt className="text-[hsl(var(--legend))]">Accounts</dt>
          <dd className="text-right tabular-nums">
            {provider.credentials.length}
            {cooling.length > 0 && (
              <span className="ml-1 text-[hsl(var(--warning))]">· {cooling.length} cooling</span>
            )}
          </dd>
          <dt className="text-[hsl(var(--legend))]">Kind</dt>
          <dd className="truncate text-right font-mono">{provider.kind}</dd>
        </dl>
      </button>
    </Card>
  )
}
