import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
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
  ToggleGroup,
  ToggleGroupItem,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useDiscoveryHealth, useProviderHealth, useProviders } from "../../lib/queries"
import { AddAccountsDialog } from "./add-accounts-dialog"
import { ProviderCard } from "./provider-card"
import { ProviderIcon } from "./provider-icon"
import { STATE_VARIANT, breakersFor, discoveryLine, providerState } from "./provider-state"

export { breakersFor, discoveryLine, providerState } from "./provider-state"

export type ProviderView = "list" | "grid"

const VIEW_KEY = "providers-view"

/** Which layout an operator last chose.
 *
 *  Persisted because it is a preference about how they read this screen, not
 *  a filter: coming back tomorrow to the other layout would be the console
 *  forgetting something it was told. */
export function readView(store: Pick<Storage, "getItem">): ProviderView {
  return store.getItem(VIEW_KEY) === "grid" ? "grid" : "list"
}

export function ProvidersScreen() {
  const providers = useProviders()
  const health = useProviderHealth()
  const discovery = useDiscoveryHealth()
  const navigate = useNavigate()
  const [addOpen, setAddOpen] = useState(false)
  const [view, setView] = useState<ProviderView>(() => readView(localStorage))

  // The row is the way into a provider. It had none before: the detail page
  // existed but only the command palette and the overview's routing graph
  // ever linked to it.
  function open(id: string) {
    void navigate({ to: "/providers/$id", params: { id } })
  }

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

  const rows = providers.data?.providers ?? []

  return (
    <>
      <PageHeader
        title="Providers"
        description="What it can route to, and whether it is answering"
        actions={
          <div className="flex items-center gap-2">
            <ToggleGroup
              type="single"
              value={view}
              onValueChange={(next) => {
                if (next !== "list" && next !== "grid") return
                setView(next)
                localStorage.setItem(VIEW_KEY, next)
              }}
              aria-label="Provider layout"
            >
              <ToggleGroupItem value="list" aria-label="List view">
                <ListGlyph />
                List
              </ToggleGroupItem>
              <ToggleGroupItem value="grid" aria-label="Grid view">
                <GridGlyph />
                Grid
              </ToggleGroupItem>
            </ToggleGroup>
            <Button size="sm" onClick={() => setAddOpen(true)}>
              Add accounts
            </Button>
          </div>
        }
      />

      <AddAccountsDialog open={addOpen} onOpenChange={setAddOpen} onDone={(id) => open(id)} />

      {view === "grid" ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {rows.map((p) => (
            <ProviderCard
              key={p.id}
              provider={p}
              cooling={breakersFor(health.data ?? [], p.id)}
              onOpen={() => open(p.id)}
            />
          ))}
        </div>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Provider</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Priority</TableHead>
              <TableHead>Accounts</TableHead>
              <TableHead>Discovery</TableHead>
              <TableHead>State</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((p) => {
              const state = providerState(p)
              const cooling = breakersFor(health.data ?? [], p.id)
              const discoveryRow = discovery.data?.providers.find((d) => d.provider_id === p.id)
              return (
                <TableRow
                  key={p.id}
                  onClick={() => open(p.id)}
                  tabIndex={0}
                  role="button"
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      open(p.id)
                    }
                  }}
                  className="cursor-pointer hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))] focus-visible:-outline-offset-2"
                >
                  <TableCell>
                    <span className="flex items-center gap-2">
                      <ProviderIcon preset={p.preset} id={p.id} name={p.name} size={24} />
                      <span className="font-medium">{p.name}</span>
                      <span className="font-mono text-sm text-[hsl(var(--legend))]">{p.id}</span>
                    </span>
                  </TableCell>
                  <TableCell className="font-mono text-sm">{p.kind}</TableCell>
                  <TableCell className="tabular-nums">{p.priority}</TableCell>
                  <TableCell>
                    {p.credentials.length}
                    {cooling.length > 0 && (
                      <span className="ml-2 text-sm text-[hsl(var(--warning))]">
                        {cooling.length} cooling
                      </span>
                    )}
                  </TableCell>
                  {/* One line with the rest on hover: the full sentence wraps
                      to four lines in this column and makes the row taller
                      than the eight around it. The Health tab carries it in
                      full. */}
                  <TableCell
                    title={discoveryLine(discoveryRow)}
                    className={
                      discoveryRow && discoveryRow.max_missing_streak > 0
                        ? "max-w-[11rem] truncate text-sm text-[hsl(var(--warning))]"
                        : "max-w-[11rem] truncate text-sm text-[hsl(var(--legend))]"
                    }
                  >
                    {discoveryLine(discoveryRow)}
                  </TableCell>
                  <TableCell>
                    <Badge variant={STATE_VARIANT[state]}>{state}</Badge>
                  </TableCell>
                  {/* The row opens the provider; these act on it in place, so
                      they must not also open it. */}
                  <TableCell
                    className="flex gap-2"
                    onClick={(e) => e.stopPropagation()}
                  >
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
      )}

      {(health.data ?? []).some((e) => e.cooling_until) && (
        <Card className="mt-6 p-4">
          <h2 className="mb-2 text-sm font-medium">Cooling credentials</h2>
          <ul className="flex flex-col gap-1 font-mono text-sm">
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

function ListGlyph() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <path
        d="M2 4h12M2 8h12M2 12h12"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  )
}

function GridGlyph() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <rect x="2" y="2" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="9" y="2" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="2" y="9" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
      <rect x="9" y="9" width="5" height="5" rx="1" stroke="currentColor" strokeWidth="1.5" />
    </svg>
  )
}
