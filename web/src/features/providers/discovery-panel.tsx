import { Card } from "darkraise-ui"
import { useDiscoveryHealth } from "../../lib/queries"
import { discoveryLine } from "./providers-screen"

/** This provider's row from the discovery sweep, or the fact that it has
 *  never run one — a missing-streak is what tells an operator a listing has
 *  gone quiet behind an otherwise healthy provider. */
export function DiscoveryPanel({ providerId }: { providerId: string }) {
  const discovery = useDiscoveryHealth()
  const row = discovery.data?.providers.find((d) => d.provider_id === providerId)
  const warning = row !== undefined && row.max_missing_streak > 0

  return (
    <Card className="mb-6 p-4">
      <h2 className="mb-2 text-sm font-medium">Discovery</h2>
      <p className={warning ? "text-sm text-[hsl(var(--warning))]" : "text-sm"}>
        {discoveryLine(row)}
      </p>
    </Card>
  )
}
