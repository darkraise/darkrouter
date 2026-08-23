import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Input } from "darkraise-ui/components/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui/components/table"
import { api, POLL } from "../lib/api"
import { TraceDrawer } from "../components/trace-drawer"

type Row = {
  id: string
  ts_ms: number
  dialect: string
  surface: string
  model: string
  alias?: string
  provider?: string
  status: string
  tokens_in: number
  tokens_out: number
  total_ms: number | null
  attempts: number
}

type Page = { requests: Row[]; next_cursor?: string }

type Filters = { provider: string; model: string; status: string; surface: string }

function queryString(f: Filters, cursor: string | null) {
  const p = new URLSearchParams({ limit: "50" })
  if (f.provider) p.set("provider", f.provider)
  if (f.model) p.set("model", f.model)
  if (f.status) p.set("status", f.status)
  if (f.surface) p.set("surface", f.surface)
  if (cursor) p.set("cursor", cursor)
  return p.toString()
}

export function RequestsScreen() {
  const [filters, setFilters] = useState<Filters>({
    provider: "",
    model: "",
    status: "",
    surface: "",
  })
  // Pages accumulate: the operator is scrolling a log, and a "next page" that
  // swapped the table would lose their place and make the cursor pointless.
  const [older, setOlder] = useState<Row[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)

  const first = useQuery({
    queryKey: ["requests", filters],
    queryFn: () => api.get<Page>(`/api/requests?${queryString(filters, null)}`),
    // Spec §5: 3s on the first page. Later pages are history and do not move.
    refetchInterval: POLL.fast,
  })

  function setFilter(k: keyof Filters, v: string) {
    // The cursor is rejected under different filters, by design. Resetting it
    // here is what keeps that rejection invisible in normal use.
    setFilters((f) => ({ ...f, [k]: v }))
    setOlder([])
    setCursor(null)
  }

  async function loadMore() {
    const from = cursor ?? first.data?.next_cursor
    if (!from) return
    const page = await api.get<Page>(`/api/requests?${queryString(filters, from)}`)
    setOlder((p) => [...p, ...page.requests])
    setCursor(page.next_cursor ?? null)
  }

  const rows = [...(first.data?.requests ?? []), ...older]
  const more = cursor ?? first.data?.next_cursor

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex flex-wrap gap-2">
        <Input
          placeholder="provider"
          value={filters.provider}
          onChange={(e) => setFilter("provider", e.target.value)}
        />
        <Input
          placeholder="model"
          value={filters.model}
          onChange={(e) => setFilter("model", e.target.value)}
        />
        <Input
          placeholder="status"
          value={filters.status}
          onChange={(e) => setFilter("status", e.target.value)}
        />
        <Input
          placeholder="surface"
          value={filters.surface}
          onChange={(e) => setFilter("surface", e.target.value)}
        />
      </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Time</TableHead>
            <TableHead>Surface</TableHead>
            <TableHead>Model</TableHead>
            <TableHead>Provider</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Attempts</TableHead>
            <TableHead>Tokens</TableHead>
            <TableHead>Latency</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((r) => (
            <TableRow
              key={r.id}
              className="cursor-pointer"
              onClick={() => setSelected(r.id)}
            >
              <TableCell className="whitespace-nowrap">
                {new Date(r.ts_ms).toLocaleTimeString()}
              </TableCell>
              <TableCell>{r.surface}</TableCell>
              <TableCell className="font-mono text-xs">
                {r.alias ? `${r.alias} → ${r.model}` : r.model}
              </TableCell>
              <TableCell>{r.provider || "—"}</TableCell>
              <TableCell>
                <Badge variant={r.status === "success" ? "green" : "destructive"}>
                  {r.status}
                </Badge>
              </TableCell>
              {/* More than one attempt means a failover, which is the row an
                  operator is usually looking for. */}
              <TableCell>
                {r.attempts > 1 ? <Badge variant="amber">{r.attempts}</Badge> : r.attempts}
              </TableCell>
              <TableCell>
                {r.tokens_in}/{r.tokens_out}
              </TableCell>
              <TableCell>{r.total_ms ?? "—"} ms</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {rows.length === 0 ? (
        <p className="text-muted-foreground text-sm">No requests match these filters.</p>
      ) : null}

      {more ? (
        <Button variant="secondary" onClick={() => void loadMore()}>
          Load more
        </Button>
      ) : null}

      <TraceDrawer id={selected} onClose={() => setSelected(null)} />
    </div>
  )
}
