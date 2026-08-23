import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Card } from "darkraise-ui/components/card"
import { Stat, StatLabel, StatValue } from "darkraise-ui/components/stat"
import { api, POLL } from "../lib/api"

type TileState = "healthy" | "degraded" | "disabled" | "unconfigured"

type Tile = {
  id: string
  name: string
  state: TileState
  cooling: number
  credentials: number
  enabled: boolean
  needs_reauth: boolean
}

type Overview = {
  providers: Tile[]
  requests_per_min: number
  error_rate: number
  window_sec: number
  today_spend: { micros: number; priced: boolean }
}

const stateVariant: Record<TileState, "green" | "amber" | "secondary" | "destructive"> = {
  healthy: "green",
  degraded: "amber",
  disabled: "secondary",
  unconfigured: "destructive",
}

export function OverviewScreen() {
  const overview = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.get<Overview>("/api/overview"),
    // Spec §5: 3s for the overview, so a provider entering or leaving cooldown
    // shows up within one interval.
    refetchInterval: POLL.fast,
  })

  if (!overview.data) return null
  const o = overview.data

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Card className="p-4">
          <Stat>
            <StatLabel>Requests / min</StatLabel>
            <StatValue>{o.requests_per_min.toFixed(1)}</StatValue>
          </Stat>
          <p className="text-muted-foreground mt-1 text-xs">
            over the last {Math.round(o.window_sec / 60)} min
          </p>
        </Card>
        <Card className="p-4">
          <Stat>
            <StatLabel>Error rate</StatLabel>
            <StatValue>{(o.error_rate * 100).toFixed(1)}%</StatValue>
          </Stat>
        </Card>
        <Card className="p-4">
          <Stat>
            <StatLabel>Today&rsquo;s spend</StatLabel>
            {/* An honest gap beats a confident $0.00 on a day that cost money.
                Nothing computes cost yet, and the API says so rather than
                reporting a zero. */}
            <StatValue>
              {o.today_spend.priced ? `$${(o.today_spend.micros / 1e6).toFixed(2)}` : "—"}
            </StatValue>
          </Stat>
          {!o.today_spend.priced ? (
            <p className="text-muted-foreground mt-1 text-xs">pricing is not wired yet</p>
          ) : null}
        </Card>
      </div>

      <div>
        <h2 className="text-muted-foreground mb-3 text-sm font-medium">Providers</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {o.providers.map((p) => (
            <Card key={p.id} className="flex flex-col gap-2 p-4">
              <div className="flex items-center justify-between">
                <span className="font-medium">{p.name || p.id}</span>
                {/* The state the API computed. Recomputing it here would be a
                    second place for the fold-forty-triples rule to drift. */}
                <Badge variant={stateVariant[p.state]}>{p.state}</Badge>
              </div>
              <div className="text-muted-foreground text-sm">
                {p.credentials} credential{p.credentials === 1 ? "" : "s"}
                {p.cooling > 0 ? ` · ${p.cooling} cooling` : null}
              </div>
              {p.needs_reauth ? (
                // The one state only the operator can fix, so it is called out
                // rather than folded into "degraded".
                <Badge variant="amber">needs reconnection</Badge>
              ) : null}
            </Card>
          ))}
          {o.providers.length === 0 ? (
            <Card className="text-muted-foreground p-4 text-sm">
              No providers configured yet. Add one from Settings.
            </Card>
          ) : null}
        </div>
      </div>
    </div>
  )
}
