import { useParams } from "@tanstack/react-router"
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
import { breakersFor, providerState } from "./providers-screen"

export function ProviderDetail() {
  const { id } = useParams({ from: "/providers/$id" })
  const providers = useProviders()
  const health = useProviderHealth()
  const provider = providers.data?.find((p) => p.id === id)

  const patch = useApiMutation({
    mutationFn: (vars: { keyId: string; enabled: boolean }) =>
      api.patch(`/api/providers/${id}/keys/${vars.keyId}`, {
        enabled: vars.enabled,
      }),
    success: "Credential updated",
    invalidates: [keys.providers, keys.health],
  })

  if (providers.isSuccess && !provider) {
    return (
      <>
        <PageHeader title="Provider" />
        <Card className="p-6">
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            No provider named <span className="font-mono">{id}</span>. It may
            have been deleted.
          </p>
        </Card>
      </>
    )
  }
  if (!provider) return null

  const cooling = breakersFor(health.data ?? [], provider.id)

  return (
    <>
      <PageHeader
        title={provider.name}
        description={`${provider.kind} · priority ${provider.priority}`}
      />

      <Card className="mb-6 p-4">
        <dl className="grid grid-cols-[8rem_1fr] gap-y-2 text-sm">
          <dt className="text-[hsl(var(--legend))]">State</dt>
          <dd>
            <Badge>{providerState(provider)}</Badge>
          </dd>
          <dt className="text-[hsl(var(--legend))]">Base URL</dt>
          <dd className="font-mono text-xs">{provider.base_url}</dd>
          <dt className="text-[hsl(var(--legend))]">Preset</dt>
          <dd className="font-mono text-xs">{provider.preset || "—"}</dd>
          <dt className="text-[hsl(var(--legend))]">Auth</dt>
          <dd className="font-mono text-xs">{provider.auth_style}</dd>
        </dl>
      </Card>

      <h2 className="mb-2 text-sm font-medium">Credentials</h2>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Label</TableHead>
            <TableHead>Secret</TableHead>
            <TableHead>State</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {provider.credentials.map((c) => (
            <TableRow key={c.id}>
              <TableCell>{c.label}</TableCell>
              {/* Enough to recognise, never enough to use. */}
              <TableCell className="font-mono text-xs">{c.masked}</TableCell>
              <TableCell>
                {c.cooling ? (
                  <Badge variant="amber">cooling</Badge>
                ) : c.enabled ? (
                  <Badge variant="green">enabled</Badge>
                ) : (
                  <Badge variant="secondary">disabled</Badge>
                )}
              </TableCell>
              <TableCell>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() =>
                    patch.mutate({ keyId: c.id, enabled: !c.enabled })
                  }
                >
                  {c.enabled ? "Disable" : "Enable"}
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {cooling.length > 0 && (
        <Card className="mt-6 p-4">
          <h2 className="mb-2 text-sm font-medium">Cooling</h2>
          <ul className="flex flex-col gap-1 font-mono text-xs">
            {cooling.map((e) => (
              <li key={`${e.key_id}/${e.model}`}>
                {e.key_id || "—"} · backoff {e.backoff_level} ·{" "}
                {e.consecutive_failures} consecutive failures
              </li>
            ))}
          </ul>
        </Card>
      )}
    </>
  )
}
