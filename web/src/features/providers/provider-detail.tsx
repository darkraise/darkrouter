import { useState } from "react"
import { useNavigate, useParams } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
  Badge,
  Button,
  Card,
  Input,
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
  toast,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useProviderHealth, useProviders } from "../../lib/queries"
import { breakersFor, providerState } from "./providers-screen"
import { CredentialRow } from "./credential-row"
import { DiscoveryPanel } from "./discovery-panel"
import { ProbePanel } from "./probe-panel"

export function ProviderDetail() {
  const { id } = useParams({ from: "/providers/$id" })
  const navigate = useNavigate()
  const providers = useProviders()
  const health = useProviderHealth()
  const provider = providers.data?.providers.find((p) => p.id === id)
  const [draftName, setDraftName] = useState<string | null>(null)
  const [draftPriority, setDraftPriority] = useState<string | null>(null)
  const [draftRegion, setDraftRegion] = useState("")
  const [draftProject, setDraftProject] = useState("")
  const [newCredLabel, setNewCredLabel] = useState("")
  const [newCredSecret, setNewCredSecret] = useState("")

  const rename = useApiMutation({
    mutationFn: (vars: { name: string; priority: number }) =>
      api.patch(`/api/providers/${id}`, vars),
    success: "Provider updated",
    invalidates: [keys.providers],
  })
  const toggle = useApiMutation({
    mutationFn: (enabled: boolean) => api.patch(`/api/providers/${id}`, { enabled }),
    success: "Provider updated",
    invalidates: [keys.providers, keys.overview],
  })
  const saveLocation = useApiMutation({
    mutationFn: (vars: { region: string; project: string }) =>
      api.patch(`/api/providers/${id}`, vars),
    success: "Provider updated",
    invalidates: [keys.providers],
  })
  const addCredential = useApiMutation({
    mutationFn: (vars: { label: string; secret: string }) =>
      api.post(`/api/providers/${id}/keys`, vars),
    success: "Credential added",
    invalidates: [keys.providers, keys.health],
    onSuccess: () => {
      setNewCredLabel("")
      setNewCredSecret("")
    },
  })
  const del = useApiMutation({
    mutationFn: () => api.del<{ id: string; dangling_aliases: string[] }>(`/api/providers/${id}`),
    invalidates: [keys.providers],
    onSuccess: (data) => {
      // An alias pointing at nothing is the consequence the operator needs
      // to see at the moment they cause it, not buried in a log they were
      // not looking at.
      if (data.dangling_aliases.length > 0) {
        toast.warning(
          `Deleted ${data.id}. Now-dangling aliases: ${data.dangling_aliases.join(", ")}`,
        )
      } else {
        toast.success("Provider deleted")
      }
      void navigate({ to: "/providers" })
    },
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

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Rename and reprioritise</h2>
        <div className="flex flex-wrap items-center gap-2">
          <Input
            value={draftName ?? provider.name}
            onChange={(e) => setDraftName(e.target.value)}
            placeholder="display name"
            className="w-64"
          />
          <Input
            value={draftPriority ?? String(provider.priority)}
            onChange={(e) => setDraftPriority(e.target.value)}
            placeholder="priority"
            className="w-28"
            inputMode="numeric"
          />
          <Button
            size="sm"
            onClick={() =>
              rename.mutate({
                name: draftName ?? provider.name,
                priority: Number(draftPriority ?? provider.priority) || 0,
              })
            }
          >
            Save
          </Button>
          {/* Disabling is a routing decision, not a deletion: the provider and
              its credentials stay, and the router stops choosing it. */}
          <Button
            size="sm"
            variant="ghost"
            onClick={() => toggle.mutate(!provider.enabled)}
          >
            {provider.enabled ? "Disable" : "Enable"}
          </Button>
        </div>
      </Card>

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Region and project</h2>
        {/* Blank rather than prefilled: GET /api/providers does not return
            either field, so there is no current value here to show. */}
        <div className="flex flex-wrap items-center gap-2">
          <Input
            value={draftRegion}
            onChange={(e) => setDraftRegion(e.target.value)}
            placeholder="region"
            className="w-40"
          />
          <Input
            value={draftProject}
            onChange={(e) => setDraftProject(e.target.value)}
            placeholder="project"
            className="w-40"
          />
          <Button
            size="sm"
            onClick={() => saveLocation.mutate({ region: draftRegion, project: draftProject })}
          >
            Save
          </Button>
        </div>
      </Card>

      <h2 className="mb-2 text-sm font-medium">Credentials</h2>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Label</TableHead>
            <TableHead>Secret</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Auth</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {provider.credentials.map((c) => (
            <CredentialRow key={c.id} providerId={provider.id} credential={c} />
          ))}
        </TableBody>
      </Table>

      <Card className="mb-6 mt-3 p-4">
        <h2 className="mb-3 text-sm font-medium">Add credential</h2>
        <div className="flex flex-wrap items-center gap-2">
          <Input
            value={newCredLabel}
            onChange={(e) => setNewCredLabel(e.target.value)}
            placeholder="label"
            className="w-40"
          />
          <Input
            value={newCredSecret}
            onChange={(e) => setNewCredSecret(e.target.value)}
            placeholder="secret"
            type="password"
            className="w-64"
          />
          <Button
            size="sm"
            disabled={newCredSecret === ""}
            onClick={() => addCredential.mutate({ label: newCredLabel, secret: newCredSecret })}
          >
            Add
          </Button>
        </div>
      </Card>

      <ProbePanel providerId={provider.id} />

      <DiscoveryPanel providerId={provider.id} />

      {cooling.length > 0 && (
        <Card className="mb-6 p-4">
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

      <Card className="p-4">
        <h2 className="mb-3 text-sm font-medium">Danger zone</h2>
        <AlertDialog>
          <AlertDialogTrigger asChild>
            <Button size="sm" variant="destructive">
              Delete provider
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Delete {provider.name}?</AlertDialogTitle>
              <AlertDialogDescription>
                Its credentials go with it. Any alias that routes here is left dangling — the
                delete still completes, and the console will say which aliases those were.
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction onClick={() => del.mutate(undefined)}>Delete</AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </Card>
    </>
  )
}
