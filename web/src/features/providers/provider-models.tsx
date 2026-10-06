import { useState } from "react"
import {
  Badge, Card, Input,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "darkraise-ui"
import { EmptyState } from "../shell/empty-state"
import type { DiscoveryHealthRow, Model } from "../../lib/api-types"
import { pricePerMillion } from "../../lib/format"
import { discoveryFailing } from "./provider-state"

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

/** The surfaces worth printing on a row. Nothing for a plain chat model: llm
 *  on every row of a list that is mostly llm is noise. But the whole list once
 *  a model serves more than one, so "llm and embedding" is never read as
 *  "embedding" alone. */
export function surfaceBadges(surfaces: string[]): string[] {
  if (surfaces.length === 1 && surfaces[0] === "llm") return []
  return surfaces
}

/**
 * Three different nothings, with three different fixes: a provider no sweep
 * has reached, one whose sweeps fail, and one that answered with nothing.
 */
function EmptyCatalogue({ discovery }: { discovery?: DiscoveryHealthRow }) {
  if (discoveryFailing(discovery)) {
    const n = discovery?.consecutive_failures ?? 0
    return (
      <EmptyState
        title={`Discovery has failed ${n === 1 ? "once" : `${n} times`}`}
        hint={
          discovery?.last_error
            ? `The last sweep got: ${discovery.last_error}. Check the provider is reachable from the gateway, then run discovery again from Health below.`
            : "Check the provider is reachable from the gateway, then run discovery again from Health below."
        }
      />
    )
  }
  if (discovery) {
    return (
      <EmptyState
        title="The last sweep found nothing to import"
        hint={
          discovery.filtered_out > 0
            ? `It listed ${discovery.filtered_out} models and none of them is free. Turn off "Import free models only" in Settings to import them.`
            : "The provider answered with an empty model list. A local runtime lists only the models it has loaded; load one, then run discovery again from Health below."
        }
      />
    )
  }
  return (
    <EmptyState
      title="Nothing has asked this provider what it serves"
      hint="A discovery sweep asks the provider for its model list. Run one from Health below, or check that the release ships a catalogue entry for it."
    />
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
export function ProviderModels({
  models,
  loading,
  discovery,
}: {
  models: Model[]
  loading: boolean
  /** The provider's sweep reading, which is what tells the empty catalogue's
   *  three causes apart. */
  discovery?: DiscoveryHealthRow
}) {
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
        <EmptyCatalogue discovery={discovery} />
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
                <TableHead className="min-w-[16rem]">Model</TableHead>
                <TableHead>Capabilities</TableHead>
                <TableHead className="text-right">Context</TableHead>
                {/* The unit once, here; the cells carry only the numbers.
                    Input then output, the order every vendor quotes. */}
                <TableHead className="text-right" title="input / output">
                  $ / M tokens
                </TableHead>
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
                      {/* Only when it is not the default: embedding on the
                          one row that is not chat is the fact worth reading.
                          Every badge keeps its word whole -- the table cell
                          breaks anywhere, which split "embedding" in two. */}
                      {surfaceBadges(m.surfaces).map((surface) => (
                        <Badge key={surface} variant="secondary" className="whitespace-nowrap">
                          {surface}
                        </Badge>
                      ))}
                      {m.tools && <Badge variant="outline" className="whitespace-nowrap">tools</Badge>}
                      {m.vision && <Badge variant="outline" className="whitespace-nowrap">vision</Badge>}
                      {m.reasoning && (
                        <Badge variant="outline" className="whitespace-nowrap">reasoning</Badge>
                      )}
                      {/* Guessed rather than read: §6.4 routes these with a
                          warning, and an operator needs to know which. */}
                      {m.inferred && (
                        <Badge variant="amber" className="whitespace-nowrap">inferred</Badge>
                      )}
                    </span>
                  </TableCell>
                  <TableCell className="text-right font-mono text-[hsl(var(--legend))]">
                    {contextLabel(m.context_window)}
                  </TableCell>
                  <TableCell className="text-right font-mono whitespace-nowrap text-[hsl(var(--legend))]">
                    {m.pricing
                      ? `${pricePerMillion(m.pricing.input_micros)} / ${pricePerMillion(m.pricing.output_micros)}`
                      : "—"}
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
