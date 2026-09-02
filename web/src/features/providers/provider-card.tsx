import { MessageSquare } from "lucide-react"
import { Badge, Button, Card } from "darkraise-ui"
import { AccountStrip, ShareMeter, type AccountMix } from "../shell/measures"
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
  mix,
  share,
  onOpen,
  onTest,
}: {
  row: ProviderRow
  /** The accounts by what the router can do with them, taken once per row
   *  set by the screen rather than again here. */
  mix: AccountMix
  /** Share of the window's requests, or undefined for a provider that served
   *  none — an empty meter and no meter say different things. */
  share?: number
  onOpen: () => void
  /** Absent for a provider with nothing to test — the router cannot reach one
   *  that has no account and needs one. */
  onTest?: () => void
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
          <dt className="text-[hsl(var(--legend))]">Credentials</dt>
          <dd className="flex justify-end">
            {row.accounts > 0 ? (
              <AccountStrip mix={mix} label={`${mix.usable}/${row.accounts}`} />
            ) : (
              <span className="text-[hsl(var(--legend))]">none</span>
            )}
          </dd>
          {share !== undefined && (
            <>
              <dt className="text-[hsl(var(--legend))]">Traffic</dt>
              <dd className="flex justify-end">
                <ShareMeter fraction={share} label={`${Math.round(share * 100)}%`} />
              </dd>
            </>
          )}
          <dt className="text-[hsl(var(--legend))]">Kind</dt>
          <dd className="truncate text-right font-mono">{row.kind}</dd>
        </dl>
      </button>

      {/* Outside the button: a button inside a button is invalid markup and
          the browser resolves it by dropping one of them. */}
      {onTest && row.configured && (
        <div className="border-t px-4 py-2">
          <Button size="sm" variant="ghost" onClick={onTest}>
            <MessageSquare className="size-[var(--icon-size)]" />
            Test
          </Button>
        </div>
      )}
    </Card>
  )
}
