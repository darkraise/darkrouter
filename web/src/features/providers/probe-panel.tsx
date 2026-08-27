import { Badge, Button, Card } from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys } from "../../lib/queries"
import type { ProbeResult } from "../../lib/api-types"

/**
 * Sends a real upstream request and shows what came back.
 *
 * No success toast: the result renders in the card below the button. A
 * rejected credential is a 200 with ok:false — the toast path in
 * useApiMutation only fires on the request itself failing, so this mutation
 * exists to keep the payload rather than to announce it a second time.
 */
export function ProbePanel({ providerId }: { providerId: string }) {
  const probe = useApiMutation({
    mutationFn: () => api.post<ProbeResult>(`/api/providers/${providerId}/test`, {}),
    invalidates: [keys.providers, keys.health],
  })

  return (
    <Card className="p-4">
      <h3 className="mb-3 text-sm font-medium">Probe</h3>
      <Button size="sm" onClick={() => probe.mutate(undefined)} disabled={probe.isPending}>
        Send test request
      </Button>
      {probe.data && (
        <dl className="mt-3 grid grid-cols-[8rem_1fr] gap-y-1 text-sm">
          <dt className="text-[hsl(var(--legend))]">Result</dt>
          <dd>
            <Badge variant={probe.data.ok ? "green" : "destructive"}>
              {probe.data.ok ? "ok" : "failed"}
            </Badge>
          </dd>
          <dt className="text-[hsl(var(--legend))]">Probe</dt>
          <dd className="font-mono text-sm">{probe.data.probe}</dd>
          <dt className="text-[hsl(var(--legend))]">Latency</dt>
          <dd className="tabular-nums">{probe.data.latency_ms} ms</dd>
          {probe.data.model_count !== undefined && (
            <>
              <dt className="text-[hsl(var(--legend))]">Models</dt>
              <dd className="tabular-nums">{probe.data.model_count}</dd>
            </>
          )}
          {probe.data.error && (
            <>
              <dt className="text-[hsl(var(--legend))]">Error</dt>
              <dd className="text-[hsl(var(--destructive))]">{probe.data.error}</dd>
            </>
          )}
        </dl>
      )}
    </Card>
  )
}
