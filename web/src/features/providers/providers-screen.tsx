import { PageHeader } from "darkraise-ui/layout"
import {
  Badge,
  Button,
  Card,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useProviderHealth, useProviders } from "../../lib/queries"
import type { BreakerEntry, Provider } from "../../lib/api-types"

/** The four states the overview emits. `degraded` is not a synonym for
 *  `cooling`: a credential cools, a provider degrades. */
export function providerState(
  p: Provider,
): "healthy" | "degraded" | "disabled" | "unconfigured" {
  if (!p.enabled) return "disabled"
  if (p.credentials.length === 0) return "unconfigured"
  if (p.credentials.some((c) => c.cooling)) return "degraded"
  return "healthy"
}

const VARIANT = {
  healthy: "green",
  degraded: "amber",
  disabled: "secondary",
  unconfigured: "destructive",
} as const

/** Breaker rows for one provider, so the panel sits beside its subject rather
 *  than on a destination of its own. */
export function breakersFor(
  entries: BreakerEntry[],
  providerID: string,
): BreakerEntry[] {
  return entries.filter((e) => e.provider_id === providerID && e.cooling_until)
}

export function ProvidersScreen() {
  const providers = useProviders()
  const health = useProviderHealth()

  const reset = useApiMutation({
    mutationFn: (id: string) =>
      api.post(`/api/providers/${id}/breaker/reset`, {}),
    success: "Cooldown cleared",
    invalidates: [keys.health, keys.providers],
  })
  const discover = useApiMutation({
    mutationFn: (id: string) => api.post(`/api/providers/${id}/discover`, {}),
    success: "Discovery sweep queued",
    invalidates: [keys.models],
  })
  const probe = useApiMutation({
    mutationFn: (id: string) => api.post(`/api/providers/${id}/test`, {}),
    success: "Probe sent",
    invalidates: [keys.providers, keys.health],
  })

  return (
    <>
      <PageHeader
        title="Providers"
        description="What it can route to, and whether it is answering"
      />

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Provider</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Priority</TableHead>
            <TableHead>Credentials</TableHead>
            <TableHead>State</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {(providers.data ?? []).map((p) => {
            const state = providerState(p)
            const cooling = breakersFor(health.data ?? [], p.id)
            return (
              <TableRow key={p.id}>
                <TableCell>
                  <span className="font-medium">{p.name}</span>
                  <span className="ml-2 font-mono text-xs text-[hsl(var(--legend))]">
                    {p.id}
                  </span>
                </TableCell>
                <TableCell className="font-mono text-xs">{p.kind}</TableCell>
                <TableCell className="tabular-nums">{p.priority}</TableCell>
                <TableCell>
                  {p.credentials.length}
                  {cooling.length > 0 && (
                    <span className="ml-2 text-xs text-[hsl(var(--warning))]">
                      {cooling.length} cooling
                    </span>
                  )}
                </TableCell>
                <TableCell>
                  <Badge variant={VARIANT[state]}>{state}</Badge>
                </TableCell>
                <TableCell className="flex gap-2">
                  <Button size="sm" variant="ghost" onClick={() => probe.mutate(p.id)}>
                    Probe
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => discover.mutate(p.id)}
                  >
                    Discover
                  </Button>
                  {/* Only offered when there is something to clear: a reset
                      button on a healthy provider invites a click that does
                      nothing and teaches the operator to distrust it. */}
                  {cooling.length > 0 && (
                    <Button size="sm" variant="ghost" onClick={() => reset.mutate(p.id)}>
                      Reset breaker
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>

      {(health.data ?? []).some((e) => e.cooling_until) && (
        <Card className="mt-6 p-4">
          <h2 className="mb-2 text-sm font-medium">Cooling credentials</h2>
          <ul className="flex flex-col gap-1 font-mono text-xs">
            {(health.data ?? [])
              .filter((e) => e.cooling_until)
              .map((e) => (
                <li key={`${e.provider_id}/${e.key_id}/${e.model}`}>
                  {e.provider_id}/{e.key_id || "—"} · backoff {e.backoff_level} ·{" "}
                  {e.consecutive_failures} consecutive failures · until{" "}
                  {new Date(e.cooling_until as string).toLocaleTimeString()}
                </li>
              ))}
          </ul>
        </Card>
      )}
    </>
  )
}
