import { useState } from "react"
import { PageHeader } from "darkraise-ui/layout"
import {
  Badge,
  Button,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useRequests } from "../../lib/queries"
import { useSearchFilters, filterQuery } from "../../lib/search-filters"
import type { RequestPage, RequestRow } from "../../lib/api-types"
import { TraceDrawer } from "./trace-drawer"

const FIELDS = ["provider", "model", "status", "surface"] as const

export function RequestsScreen() {
  const [filters, setFilter, clear] = useSearchFilters(FIELDS)
  // Pages accumulate: the operator is scrolling a log, and a "next page" that
  // swapped the table would lose their place and make the cursor pointless.
  const [older, setOlder] = useState<RequestRow[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)

  const first = useRequests({ ...filters, limit: "50" })

  function onFilter(key: (typeof FIELDS)[number], value: string) {
    // The cursor is rejected under different filters by design; resetting it
    // here is what keeps that rejection invisible in normal use.
    setOlder([])
    setCursor(null)
    setFilter(key, value)
  }

  async function loadMore() {
    const from = cursor ?? first.data?.next_cursor
    if (!from) return
    const page = await api.get<RequestPage>(
      `/api/requests${filterQuery({ ...filters, limit: "50", cursor: from })}`,
    )
    setOlder((p) => [...p, ...page.requests])
    setCursor(page.next_cursor ?? null)
  }

  const rows = [...(first.data?.requests ?? []), ...older]
  const more = cursor ?? first.data?.next_cursor
  const filtered = Object.values(filters).some((v) => v !== "")

  return (
    <>
      <PageHeader
        title="Requests"
        description="What it just did, and which provider actually served"
      />

      <div className="mb-4 flex flex-wrap items-center gap-2">
        {FIELDS.map((field) => (
          <Input
            key={field}
            placeholder={field}
            value={filters[field]}
            onChange={(e) => onFilter(field, e.target.value)}
            className="w-40"
          />
        ))}
        {filtered && (
          <Button variant="ghost" size="sm" onClick={clear}>
            Clear
          </Button>
        )}
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
                {r.attempts > 1 ? (
                  <Badge variant="amber">{r.attempts}</Badge>
                ) : (
                  r.attempts
                )}
              </TableCell>
              <TableCell>
                {r.tokens_in}/{r.tokens_out}
              </TableCell>
              <TableCell>{r.total_ms ?? "—"} ms</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {rows.length === 0 && (
        <p className="mt-4 text-sm text-[hsl(var(--muted-foreground))]">
          No requests match these filters.
        </p>
      )}

      {more && (
        <Button variant="secondary" className="mt-4" onClick={() => void loadMore()}>
          Load more
        </Button>
      )}

      <TraceDrawer id={selected} onClose={() => setSelected(null)} />
    </>
  )
}
