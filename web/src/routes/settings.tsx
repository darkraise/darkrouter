import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "darkraise-ui/components/alert-dialog"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "darkraise-ui/components/select"
import { api, POLL } from "../lib/api"

type Credential = {
  id: string
  label: string
  masked: string
  enabled: boolean
  cooling: boolean
}

type Provider = {
  id: string
  name: string
  preset: string
  kind: string
  base_url: string
  priority: number
  enabled: boolean
  credentials: Credential[]
}

type Preset = { id: string; name: string; kind: string; base_url: string }

type Config = {
  valid: boolean
  error?: string
  serving?: string
  warnings: string[]
  aliases: Record<string, string[]> | null
}

type ProbeResult = {
  ok: boolean
  probe: string
  model_count?: number
  latency_ms: number
  error?: string
}

export function SettingsScreen() {
  const qc = useQueryClient()
  const invalidate = () => void qc.invalidateQueries()

  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.get<{ providers: Provider[] }>("/api/providers"),
    refetchInterval: POLL.slow,
  })
  const presets = useQuery({
    queryKey: ["presets"],
    queryFn: () => api.get<{ presets: Preset[] }>("/api/presets"),
  })
  const config = useQuery({
    queryKey: ["config"],
    queryFn: () => api.get<Config>("/api/config"),
    refetchInterval: POLL.slow,
  })

  const [newID, setNewID] = useState("")
  const [newPreset, setNewPreset] = useState("")
  const [deleting, setDeleting] = useState<Provider | null>(null)
  const [probeResult, setProbeResult] = useState<Record<string, string>>({})
  const [error, setError] = useState("")

  const create = useMutation({
    mutationFn: () => api.post("/api/providers", { id: newID, preset: newPreset }),
    onSuccess: () => {
      setNewID("")
      setNewPreset("")
      setError("")
      invalidate()
    },
    onError: (e: Error) => setError(e.message),
  })
  const remove = useMutation({
    mutationFn: (id: string) =>
      api.del<{ dangling_aliases: string[] }>(`/api/providers/${id}`),
    onSuccess: () => {
      setDeleting(null)
      invalidate()
    },
    onError: (e: Error) => setError(e.message),
  })
  const probe = useMutation({
    mutationFn: (id: string) => api.post<ProbeResult>(`/api/providers/${id}/test`),
  })
  const reload = useMutation({
    mutationFn: () => api.post("/api/config/reload"),
    onSuccess: invalidate,
  })

  const aliases = config.data?.aliases ?? {}

  return (
    <div className="flex flex-col gap-6 p-6">
      {config.data && !config.data.valid ? (
        <Card className="border-destructive p-4">
          <h2 className="text-destructive font-medium">Configuration is invalid</h2>
          <p className="mt-1 font-mono text-xs">{config.data.error}</p>
          {/* The difference between a calm fix and a panicked restart. */}
          <p className="text-muted-foreground mt-2 text-sm">
            {config.data.serving ?? "The previous configuration is still serving."}
          </p>
        </Card>
      ) : null}

      {error ? <p className="text-destructive text-sm">{error}</p> : null}

      <section>
        <h2 className="text-muted-foreground mb-3 text-sm font-medium">Providers</h2>
        <div className="flex flex-col gap-3">
          {(providers.data?.providers ?? []).map((p) => (
            <Card key={p.id} className="flex flex-col gap-3 p-4">
              <div className="flex items-center justify-between">
                <div>
                  <div className="font-medium">{p.name}</div>
                  <div className="text-muted-foreground font-mono text-xs">
                    {p.kind} · {p.base_url}
                    {p.preset ? ` · preset ${p.preset}` : " · no preset"}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    onClick={() =>
                      probe.mutate(p.id, {
                        onSuccess: (r) =>
                          setProbeResult((s) => ({
                            ...s,
                            [p.id]: r.ok
                              ? `OK · ${r.model_count} models · ${r.latency_ms} ms`
                              : `Failed · ${r.error}`,
                          })),
                      })
                    }
                  >
                    Test
                  </Button>
                  <Button variant="destructive" onClick={() => setDeleting(p)}>
                    Delete
                  </Button>
                </div>
              </div>
              {probeResult[p.id] ? (
                <p className="text-muted-foreground text-sm">{probeResult[p.id]}</p>
              ) : null}

              <div className="flex flex-col gap-1">
                {p.credentials.map((c) => (
                  <div key={c.id} className="flex items-center gap-2 text-sm">
                    <span>{c.label}</span>
                    {/* Never the secret. Spec §4.1: replacing one means adding
                        a new one and deleting the old, so there is nothing to
                        show and no edit to offer. */}
                    <span className="text-muted-foreground font-mono text-xs">
                      {c.masked}
                    </span>
                    {c.cooling ? <Badge variant="amber">cooling</Badge> : null}
                    {!c.enabled ? <Badge variant="amber">needs reconnection</Badge> : null}
                    <Button
                      variant="ghost"
                      onClick={() =>
                        void api
                          .del(`/api/providers/${p.id}/keys/${c.id}`)
                          .then(invalidate)
                          .catch((e: Error) => setError(e.message))
                      }
                    >
                      Remove
                    </Button>
                  </div>
                ))}
                <AddCredential
                  providerID={p.id}
                  onAdded={invalidate}
                  onError={setError}
                />
              </div>
            </Card>
          ))}
          {(providers.data?.providers ?? []).length === 0 ? (
            <Card className="text-muted-foreground p-4 text-sm">
              No providers yet. Add one below.
            </Card>
          ) : null}
        </div>
      </section>

      <section>
        <h2 className="text-muted-foreground mb-3 text-sm font-medium">Add a provider</h2>
        <Card className="flex flex-wrap items-end gap-2 p-4">
          <Input
            placeholder="id"
            value={newID}
            onChange={(e) => setNewID(e.target.value)}
          />
          <Select value={newPreset} onValueChange={setNewPreset}>
            <SelectTrigger className="w-64">
              <SelectValue placeholder="preset" />
            </SelectTrigger>
            <SelectContent>
              {(presets.data?.presets ?? []).map((p) => (
                <SelectItem key={p.id} value={p.id}>
                  {p.name} ({p.id})
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button onClick={() => create.mutate()} disabled={!newID || !newPreset}>
            Create
          </Button>
        </Card>
      </section>

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-muted-foreground text-sm font-medium">Configuration</h2>
          <Button variant="secondary" onClick={() => reload.mutate()}>
            Reload
          </Button>
        </div>
        <Card className="p-4">
          {(config.data?.warnings ?? []).map((w) => (
            <p key={w} className="text-muted-foreground mb-1 text-sm">
              {w}
            </p>
          ))}
          <h3 className="mt-3 text-sm font-medium">Aliases</h3>
          <pre className="mt-1 overflow-x-auto text-xs">
            {JSON.stringify(aliases, null, 2)}
          </pre>
          <p className="text-muted-foreground mt-2 text-xs">
            Aliases and policy are edited in darkrouter.yaml. This view is read-only.
          </p>
        </Card>
      </section>

      <AlertDialog
        open={deleting !== null}
        onOpenChange={(o) => (o ? undefined : setDeleting(null))}
      >
        <AlertDialogContent>
          {deleting ? (
            <>
              <AlertDialogHeader>
                <AlertDialogTitle>Delete {deleting.name}?</AlertDialogTitle>
                <AlertDialogDescription>
                  Its {deleting.credentials.length} credential
                  {deleting.credentials.length === 1 ? "" : "s"} and its discovered
                  models go with it.
                </AlertDialogDescription>
              </AlertDialogHeader>
              <DanglingWarning providerID={deleting.id} aliases={aliases} />
              <AlertDialogFooter>
                <AlertDialogCancel onClick={() => setDeleting(null)}>
                  Cancel
                </AlertDialogCancel>
                <AlertDialogAction onClick={() => remove.mutate(deleting.id)}>
                  Delete
                </AlertDialogAction>
              </AlertDialogFooter>
            </>
          ) : null}
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

/**
 * DanglingWarning names the aliases a delete will strand.
 *
 * Master design §7 makes a dangling alias a warning rather than a validation
 * error precisely so this delete cannot brick the reload button. The operator
 * still has to edit the file, which is why the file is named: that edit cannot
 * be made here.
 */
function DanglingWarning({
  providerID,
  aliases,
}: {
  providerID: string
  aliases: Record<string, string[]>
}) {
  const stranded = Object.entries(aliases)
    // The provider half only counts when the target actually names one:
    // "gpt-4o" with no slash is a bare model, not a reference to this provider.
    .filter(([, targets]) =>
      targets.some((t) => t.includes("/") && t.slice(0, t.indexOf("/")) === providerID),
    )
    .map(([name]) => name)
    .sort()

  if (stranded.length === 0) return null
  return (
    <div className="rounded border p-3 text-sm">
      <p>
        These aliases will point at nothing: <strong>{stranded.join(", ")}</strong>
      </p>
      <p className="text-muted-foreground mt-1">
        The gateway keeps serving — a dangling alias is a warning, not an error — but
        edit darkrouter.yaml to remove them.
      </p>
    </div>
  )
}

function AddCredential({
  providerID,
  onAdded,
  onError,
}: {
  providerID: string
  onAdded: () => void
  onError: (m: string) => void
}) {
  const [label, setLabel] = useState("")
  const [secret, setSecret] = useState("")
  return (
    <div className="mt-2 flex flex-wrap items-end gap-2">
      <Input
        placeholder="label"
        value={label}
        onChange={(e) => setLabel(e.target.value)}
      />
      <Input
        type="password"
        placeholder="secret"
        value={secret}
        onChange={(e) => setSecret(e.target.value)}
      />
      <Button
        variant="secondary"
        disabled={!secret}
        onClick={() =>
          void api
            .post(`/api/providers/${providerID}/keys`, { label, secret })
            .then(() => {
              // Cleared immediately: the value has left the browser and keeping
              // it in a controlled input serves nothing.
              setLabel("")
              setSecret("")
              onAdded()
            })
            .catch((e: Error) => onError(e.message))
        }
      >
        Add key
      </Button>
    </div>
  )
}
