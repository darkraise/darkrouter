import { useState } from "react"
import { PageHeader } from "darkraise-ui/layout"
import { Button, Card, Input } from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useAliases } from "../../lib/queries"
import { useSearchFilters } from "../../lib/search-filters"
import type { Aliases, RoutePreview } from "../../lib/api-types"
import { Ladder, type LadderRow, type PredictiveMark } from "../ladder/ladder"

/**
 * Preview rows, in the order the endpoint returned them.
 *
 * Not re-sorted. §12 requires the preview to show the same ordered candidate
 * list a real request would produce, and the endpoint already guarantees that
 * by sharing the executor's snapshot — sorting here for display would undo it
 * and misreport failover order.
 */
export function previewRows(p: RoutePreview): LadderRow<PredictiveMark>[] {
  const candidates: LadderRow<PredictiveMark>[] = p.candidates.map((c, i) => ({
    rank: i + 1,
    // Hollow: nothing has been sent. This is what the router would do.
    mark: "skipped",
    target: `${c.provider_id}/${c.model}`,
    reasonCode: c.inferred ? "inferred" : undefined,
    reasonProse: c.inferred ? "capabilities were guessed" : undefined,
  }))
  const skipped: LadderRow<PredictiveMark>[] = p.skips.map((s, i) => ({
    rank: candidates.length + i + 1,
    mark: s.includes("cooling") ? "cooling" : "skipped",
    target: s,
    terminated: true,
  }))
  return [...candidates, ...skipped]
}

function AliasEditor({ aliases }: { aliases: Aliases }) {
  const [draft, setDraft] = useState<Aliases>(aliases)

  const save = useApiMutation({
    mutationFn: (next: Aliases) => api.put("/api/aliases", next),
    success: "Aliases saved",
    invalidates: [keys.aliases, keys.config],
  })

  return (
    <Card className="p-4">
      <h2 className="mb-3 text-sm font-medium">Alias chains</h2>
      <div className="flex flex-col gap-3">
        {Object.entries(draft).map(([name, targets]) => (
          <div key={name} className="flex items-center gap-2">
            <span className="w-32 shrink-0 font-mono text-xs">{name}</span>
            <Input
              // The chain order is the fallback order, so it is edited as an
              // ordered list rather than as a set.
              value={targets.join(", ")}
              onChange={(e) =>
                setDraft((d) => ({
                  ...d,
                  [name]: e.target.value.split(",").map((t) => t.trim()).filter(Boolean),
                }))
              }
              className="flex-1 font-mono text-xs"
            />
            <Button
              size="sm"
              variant="ghost"
              onClick={() =>
                setDraft((d) => {
                  const next = { ...d }
                  delete next[name]
                  return next
                })
              }
            >
              Remove
            </Button>
          </div>
        ))}
      </div>
      <div className="mt-4 flex gap-2">
        <Button
          size="sm"
          variant="secondary"
          onClick={() => {
            // An operator with no aliases has to be able to make their first
            // one here; an editor that only edits what exists cannot be the
            // only way in.
            const name = window.prompt("Alias name")?.trim()
            if (!name) return
            setDraft((d) => (name in d ? d : { ...d, [name]: [] }))
          }}
        >
          Add chain
        </Button>
        <Button size="sm" onClick={() => save.mutate(draft)}>
          Save
        </Button>
        <Button size="sm" variant="ghost" onClick={() => setDraft(aliases)}>
          Revert
        </Button>
      </div>
    </Card>
  )
}

export function RoutingScreen() {
  const [filters, setFilter] = useSearchFilters(["alias"] as const)
  const aliases = useAliases()
  const [preview, setPreview] = useState<RoutePreview | null>(null)

  const run = useApiMutation({
    mutationFn: (model: string) =>
      api.post<RoutePreview>("/api/route/preview", { model }),
    onSuccess: (data) => setPreview(data),
  })

  return (
    <>
      <PageHeader
        title="Routing"
        description="How it chooses, and what it would choose right now"
      />

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Preview</h2>
        <div className="flex gap-2">
          <Input
            placeholder="alias or model"
            value={filters.alias}
            onChange={(e) => setFilter("alias", e.target.value)}
            className="w-72 font-mono text-xs"
          />
          <Button size="sm" onClick={() => run.mutate(filters.alias)}>
            Preview
          </Button>
        </div>

        {preview && (
          <div className="mt-4">
            {preview.error && (
              <p className="mb-2 text-sm text-[hsl(var(--muted-foreground))]">
                {preview.error}
              </p>
            )}
            {/* Even with no candidates the skips are shown: they are the only
                account of why nothing routed. */}
            <Ladder mode="predictive" rows={previewRows(preview)} />
          </div>
        )}
      </Card>

      {aliases.data && <AliasEditor aliases={aliases.data} />}
    </>
  )
}
