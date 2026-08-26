import { PageHeader } from "darkraise-ui/layout"
import {
  Badge,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui"
import { useModels } from "../../lib/queries"
import { useSearchFilters } from "../../lib/search-filters"
import type { Model } from "../../lib/api-types"
import { Ladder, type LadderRow, type PredictiveMark } from "../ladder/ladder"

const FIELDS = ["model", "provider"] as const

/**
 * The compressed ladder for one model: every provider that serves it, in
 * catalog order, with hollow marks.
 *
 * Nothing has been sent, so no mark may be filled. This is what the router
 * *would* consider, which is a different statement from what it did.
 */
export function compressedRows(m: Model): LadderRow<PredictiveMark>[] {
  return m.providers.map((provider, i) => ({
    rank: i + 1,
    mark: "skipped",
    target: `${provider}/${m.model}`,
  }))
}

export function matches(m: Model, filters: Record<string, string>): boolean {
  const model = filters.model?.toLowerCase() ?? ""
  const provider = filters.provider?.toLowerCase() ?? ""
  if (model && !m.model.toLowerCase().includes(model)) return false
  if (provider && !m.providers.some((p) => p.toLowerCase().includes(provider)))
    return false
  return true
}

export function ModelsScreen() {
  const [filters, setFilter] = useSearchFilters(FIELDS)
  const catalog = useModels()
  const models = (catalog.data?.models ?? []).filter((m) => matches(m, filters))

  return (
    <>
      <PageHeader
        title="Models"
        description="What it can route to, and which providers serve each one"
      />

      <div className="mb-4 flex flex-wrap gap-2">
        {FIELDS.map((field) => (
          <Input
            key={field}
            placeholder={field}
            value={filters[field]}
            onChange={(e) => setFilter(field, e.target.value)}
            className="w-48"
          />
        ))}
      </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Model</TableHead>
            <TableHead>Serves</TableHead>
            <TableHead>Context</TableHead>
            <TableHead>Capabilities</TableHead>
            <TableHead>State</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {models.map((m) => (
            <TableRow key={m.model}>
              <TableCell className="font-mono text-xs">{m.model}</TableCell>
              <TableCell>
                <Ladder mode="compressed" catalog rows={compressedRows(m)} />
              </TableCell>
              <TableCell className="tabular-nums">
                {m.context_window ? m.context_window.toLocaleString() : "—"}
              </TableCell>
              <TableCell className="flex flex-wrap gap-1">
                {m.tools && <Badge variant="secondary">tools</Badge>}
                {m.vision && <Badge variant="secondary">vision</Badge>}
                {m.reasoning && <Badge variant="secondary">reasoning</Badge>}
                {/* Guessed rather than read. Master design §6.4 routes these
                    with a warning, so the row has to say which they are. */}
                {m.inferred && <Badge variant="amber">inferred</Badge>}
              </TableCell>
              <TableCell className="font-mono text-xs">{m.state}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {models.length === 0 && (
        <p className="mt-4 text-sm text-[hsl(var(--muted-foreground))]">
          No models match these filters.
        </p>
      )}
    </>
  )
}
