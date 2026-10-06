import { Button, Card } from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useDiscoveryHealth } from "../../lib/queries"
import { discoveryFailing, discoveryLine } from "./provider-state"

/** This provider's row from the discovery sweep, or the fact that it has
 *  never run one — a missing-streak is what tells an operator a listing has
 *  gone quiet behind an otherwise healthy provider.
 *
 *  It carries the sweep's trigger as well. The empty Models card sends an
 *  operator here to run one, and the only other way to was an icon on the
 *  list row, a page away. */
export function DiscoveryPanel({ providerId }: { providerId: string }) {
  const discovery = useDiscoveryHealth()
  const row = discovery.data?.providers.find((d) => d.provider_id === providerId)
  const warning = row !== undefined && (row.max_missing_streak > 0 || discoveryFailing(row))
  const discover = useApiMutation({
    mutationFn: () => api.post(`/api/providers/${providerId}/discover`, {}),
    success: "Discovery sweep queued",
    invalidates: [keys.models, keys.discovery],
  })

  return (
    <Card className="flex flex-col gap-3 p-4">
      <h3 className="text-sm font-medium">Discovery</h3>
      <p className={warning ? "text-sm text-[hsl(var(--warning))]" : "text-sm"}>
        {discoveryLine(row)}
      </p>
      <div>
        <Button
          size="sm"
          variant="secondary"
          onClick={() => discover.mutate(undefined)}
          disabled={discover.isPending}
        >
          Run discovery
        </Button>
      </div>
    </Card>
  )
}
