import { useState } from "react"
import { Badge, Card, Input } from "darkraise-ui"
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
        <ul className="flex max-h-96 flex-col divide-y divide-[hsl(var(--border))] overflow-y-auto">
          {shown.map((m) => (
            <li key={m.model} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
              <span className="min-w-0 flex-1 truncate font-mono text-sm" title={m.model}>
                {m.model}
              </span>
              <span className="flex shrink-0 flex-wrap items-center gap-1">
                {/* Only when it is not the default: llm on every row of a list
                    that is mostly llm is noise, and embedding on the one row
                    that is not is the fact worth reading. */}
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
                {/* Guessed rather than read: §6.4 routes these with a warning,
                    and an operator needs to know which they are. */}
                {m.inferred && <Badge variant="amber">inferred</Badge>}
              </span>
              <span className="w-12 shrink-0 text-right font-mono text-sm text-[hsl(var(--legend))]">
                {contextLabel(m.context_window)}
              </span>
              <span
                className="w-40 shrink-0 text-right font-mono text-sm text-[hsl(var(--legend))]"
                title="input / output per million tokens"
              >
                {priceLabel(m.pricing)}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Card>
  )
}
