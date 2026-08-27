import { useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import { PageHeader } from "darkraise-ui/layout"
import {
  Badge,
  Button,
  Card,
  Input,
  Label,
  Switch,
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
import {
  keys,
  useDiscoveryHealth,
  usePresets,
  useProviderHealth,
  useProviders,
} from "../../lib/queries"
import { FilterSelect } from "../requests/filter-select"
import { AddAccountsDialog } from "./add-accounts-dialog"
import { ProviderCard } from "./provider-card"
import { ProviderIcon } from "./provider-icon"
import {
  filterProviderRows,
  filterSummary,
  mergeProviderRows,
  type ProviderRow,
} from "./provider-rows"
import { STATE_VARIANT, breakersFor, discoveryLine } from "./provider-state"

export { breakersFor, discoveryLine, providerState } from "./provider-state"

export type ProviderView = "list" | "grid"

const VIEW_KEY = "providers-view"
const STATES = ["healthy", "degraded", "disabled", "unconfigured"]

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
  const presets = usePresets()
  const health = useProviderHealth()
  const discovery = useDiscoveryHealth()
  const navigate = useNavigate()
  const [addOpen, setAddOpen] = useState(false)
  const [view, setView] = useState<ProviderView>(() => readView(localStorage))
  const [q, setQ] = useState("")
  const [state, setState] = useState("")
  const [configuredOnly, setConfiguredOnly] = useState(false)
  const [freeTier, setFreeTier] = useState(false)

  function open(row: ProviderRow) {
    if (!row.configured) {
      // Nothing to show yet: the detail page reads a database row, and one
      // does not exist until the provider has an account. Sending an operator
      // to an empty page would be a dead end with the useful action one click
      // away.
      setAddOpen(true)
      return
    }
    void navigate({ to: "/providers/$id", params: { id: row.id } })
  }

  const reset = useApiMutation({
    mutationFn: (id: string) => api.post(`/api/providers/${id}/breaker/reset`, {}),
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

  const all = mergeProviderRows(presets.data?.presets ?? [], providers.data?.providers ?? [])
  const rows = filterProviderRows(all, { q, state, configuredOnly, freeTier })

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
              className="w-fit rounded-[var(--radius)] border bg-[hsl(var(--muted))] p-0.5"
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

      <AddAccountsDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        onDone={(id) => void navigate({ to: "/providers/$id", params: { id } })}
      />

      {/* The list is every provider the release supports, so it needs a way
          back down to the handful that carry traffic. */}
      <Card className="mb-4 flex flex-wrap items-center gap-3 p-3">
        <Input
          placeholder="Search providers"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          className="w-56"
          aria-label="Search providers"
        />
        <FilterSelect label="State" value={state} options={STATES} onChange={setState} />
        <div className="flex items-center gap-2">
          <Switch
            id="providers-configured"
            checked={configuredOnly}
            onCheckedChange={setConfiguredOnly}
          />
          <Label htmlFor="providers-configured">Configured only</Label>
        </div>
        <div className="flex items-center gap-2">
          <Switch id="providers-free-tier" checked={freeTier} onCheckedChange={setFreeTier} />
          <Label htmlFor="providers-free-tier">Free tier</Label>
        </div>
        <span className="ml-auto text-sm text-[hsl(var(--legend))]">
          {filterSummary(rows.length, all.length)}
        </span>
      </Card>

      {rows.length === 0 ? (
        <Card className="p-6">
          <p className="text-sm text-[hsl(var(--muted-foreground))]">
            No provider matches those filters.
          </p>
        </Card>
      ) : view === "grid" ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {rows.map((row) => (
            <ProviderCard
              key={row.id}
              row={row}
              cooling={breakersFor(health.data ?? [], row.id)}
              onOpen={() => open(row)}
            />
          ))}
        </div>
      ) : (
        // Scrolls rather than clips: the provider column carries two lines and
        // a mark, and the actions are three words wide.
        <Card className="overflow-x-auto p-0">
          {/* A minimum width so the table overflows the card and scrolls,
              rather than squeezing the actions column until its labels clip. */}
          <Table className="min-w-[56rem]">
          <TableHeader>
            <TableRow>
              <TableHead>Provider</TableHead>
              <TableHead>Priority</TableHead>
              <TableHead>Accounts</TableHead>
              <TableHead>Discovery</TableHead>
              <TableHead>State</TableHead>
              <TableHead />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => {
              const cooling = breakersFor(health.data ?? [], row.id)
              const discoveryRow = discovery.data?.providers.find((d) => d.provider_id === row.id)
              return (
                <TableRow
                  key={row.id}
                  onClick={() => open(row)}
                  tabIndex={0}
                  role="button"
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault()
                      open(row)
                    }
                  }}
                  className="cursor-pointer hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))] focus-visible:-outline-offset-2"
                >
                  {/* Two lines and a 36px mark: the row is the primary way into
                      a provider, and a single line of small text is a harder
                      target than it is a denser list. */}
                  <TableCell className="py-3">
                    <span className="flex items-center gap-3">
                      <ProviderIcon preset={row.preset} id={row.id} name={row.name} size={36} />
                      <span className="flex min-w-0 flex-col">
                        <span className="truncate font-medium">{row.name}</span>
                        <span className="truncate font-mono text-sm text-[hsl(var(--legend))]">
                          {row.id} · {row.kind}
                        </span>
                      </span>
                      {row.freeTier && <Badge variant="secondary">Free tier</Badge>}
                    </span>
                  </TableCell>
                  <TableCell className="tabular-nums">
                    {row.priority ?? <span className="text-[hsl(var(--legend))]">—</span>}
                  </TableCell>
                  <TableCell>
                    {row.configured ? (
                      <>
                        {row.accounts}
                        {cooling.length > 0 && (
                          <span className="ml-2 text-sm text-[hsl(var(--warning))]">
                            {cooling.length} cooling
                          </span>
                        )}
                      </>
                    ) : (
                      <span className="text-[hsl(var(--legend))]">none</span>
                    )}
                  </TableCell>
                  <TableCell
                    title={row.configured ? discoveryLine(discoveryRow) : undefined}
                    className={
                      discoveryRow && discoveryRow.max_missing_streak > 0
                        ? "max-w-[11rem] truncate text-sm text-[hsl(var(--warning))]"
                        : "max-w-[11rem] truncate text-sm text-[hsl(var(--legend))]"
                    }
                  >
                    {row.configured ? discoveryLine(discoveryRow) : "—"}
                  </TableCell>
                  <TableCell>
                    <Badge variant={STATE_VARIANT[row.state]}>{row.state}</Badge>
                  </TableCell>
                  {/* The row opens the provider; these act on it in place, so
                      they must not also open it. */}
                  <TableCell className="flex gap-2 py-3" onClick={(e) => e.stopPropagation()}>
                    {row.configured ? (
                      <>
                        <Button size="sm" variant="ghost" onClick={() => probe.mutate(row.id)}>
                          Probe
                        </Button>
                        <Button size="sm" variant="ghost" onClick={() => discover.mutate(row.id)}>
                          Discover
                        </Button>
                        {/* Only offered when there is something to clear: a
                            reset on a healthy provider invites a click that
                            does nothing and teaches the operator to distrust
                            it. */}
                        {cooling.length > 0 && (
                          <Button size="sm" variant="ghost" onClick={() => reset.mutate(row.id)}>
                            Reset breaker
                          </Button>
                        )}
                      </>
                    ) : (
                      <Button size="sm" variant="ghost" onClick={() => setAddOpen(true)}>
                        Add accounts
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
            </TableBody>
          </Table>
        </Card>
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
