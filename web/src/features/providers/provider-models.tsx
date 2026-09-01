import { useState } from "react"
import {
  Badge, Card, Input,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "darkraise-ui"
import { EmptyState } from "../shell/empty-state"
import type { Model } from "../../lib/api-types"
import { priceLabel } from "../models/models-screen"

/** Tokens read as thousands, because 131072 is a number nobody holds in their
 *  head and 131k is the one on the vendor's own page. */
export function contextLabel(tokens: number): string {
  if (tokens <= 0) return "—"
  if (tokens >= 1_000_000) return `${(tokens / 1_000_000).toFixed(tokens % 1_000_000 === 0 ? 0 : 1)}M`
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}k`
  return String(tokens)
}

export function filterModels(models: Model[], q: string): Model[] {
  const needle = q.trim().toLowerCase()
  if (needle === "") return models
  return models.filter(
    (m) =>
      m.model.toLowerCase().includes(needle) ||
      m.surfaces.some((s) => s.toLowerCase().includes(needle)),
  )
}

/**
 * What this provider can actually be routed to.
 *
 * The catalog is the answer to "why did my alias not resolve here", and until
 * now the only way to ask it was the Models screen filtered by hand. Scrolls
 * rather than paginates: the list is bounded by one provider's catalogue, and
 * a pager over forty rows is furniture.
 */
export function ProviderModels({ models, loading }: { models: Model[]; loading: boolean }) {
  const [q, setQ] = useState("")
  const shown = filterModels(models, q)

  return (
    <Card className="flex flex-col gap-3 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <Input
          placeholder="Filter models"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          className="w-56"
          aria-label="Filter models"
        />
        <span className="text-sm text-[hsl(var(--legend))]">
          {q.trim() === ""
            ? `${models.length} ${models.length === 1 ? "model" : "models"}`
            : `${shown.length} of ${models.length}`}
        </span>
      </div>

      {loading ? (
        <p className="text-sm text-[hsl(var(--muted-foreground))]">Loading the catalogue…</p>
      ) : models.length === 0 ? (
        // Two different nothings: a provider whose catalogue has never been
        // fetched reads the same as one that offers nothing, and the fix for
        // each is different.
        <EmptyState
          title="Nothing has asked this provider what it serves"
          hint="A discovery sweep lists its models with one of its own keys. Run one from Health below, or check that the release ships a catalogue entry for it."
        />
      ) : shown.length === 0 ? (
        <p className="text-sm text-[hsl(var(--muted-foreground))]">
          No model here matches “{q.trim()}”.
        </p>
      ) : (
        // A table, because it is one: four columns, and until now none of
        // them was named. The context and price columns were explained only
        // by a title attribute, which never appears on touch and never on
        // keyboard focus -- so two of the four columns were unlabelled for
        // most readers. Scrolls rather than paginates, as before: the list is
        // bounded by one provider's catalogue.
        <div className="max-h-96 overflow-y-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Model</TableHead>
                <TableHead>Capabilities</TableHead>
                <TableHead className="text-right">Context</TableHead>
                <TableHead className="text-right">$ / M tokens</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {shown.map((m) => (
                <TableRow key={m.model}>
                  <TableCell className="max-w-0 truncate font-mono" title={m.model}>
                    {m.model}
                  </TableCell>
                  <TableCell>
                    <span className="flex flex-wrap items-center gap-1">
                      {/* Only when it is not the default: llm on every row of
                          a list that is mostly llm is noise, and embedding on
                          the one row that is not is the fact worth reading. */}
                      {m.surfaces
                        .filter((surface) => surface !== "llm")
                        .map((surface) => (
                          <Badge key={surface} variant="secondary">
                            {surface}
                          </Badge>
                        ))}
                      {m.tools && <Badge variant="outline">tools</Badge>}
                      {m.vision && <Badge variant="outline">vision</Badge>}
                      {m.reasoning && <Badge variant="outline">reasoning</Badge>}
                      {/* Guessed rather than read: §6.4 routes these with a
                          warning, and an operator needs to know which. */}
                      {m.inferred && <Badge variant="amber">inferred</Badge>}
                    </span>
                  </TableCell>
                  <TableCell className="text-right font-mono text-[hsl(var(--legend))]">
                    {contextLabel(m.context_window)}
                  </TableCell>
                  <TableCell className="text-right font-mono whitespace-nowrap text-[hsl(var(--legend))]">
                    {priceLabel(m.pricing)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </Card>
  )
}
