import { useRef, useState } from "react"
import { PageHeader } from "darkraise-ui/layout"
import { Button, Card, Input } from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useAliases, useProviders } from "../../lib/queries"
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

/** Reorder one target. Returns a new array: the draft is React state, and a
 *  mutation in place would not re-render. */
export function moveTarget(chain: string[], from: number, to: number): string[] {
  if (from === to) return chain
  if (from < 0 || to < 0 || from >= chain.length || to >= chain.length) return chain
  const next = [...chain]
  const moved = next[from]
  // Unreachable given the bounds check above; narrows the type rather than
  // asserting past it.
  if (moved === undefined) return chain
  next.splice(from, 1)
  next.splice(to, 0, moved)
  return next
}

/**
 * Problems a browser can see without asking the server.
 *
 * The server validates on PUT and stays the authority; this exists so a typo
 * is caught before a round trip rather than instead of one.
 */
export function validateChain(targets: string[], knownProviders: string[]): string[] {
  if (targets.length === 0) return ["an alias with no targets routes nowhere"]
  const problems: string[] = []
  for (const target of targets) {
    const slash = target.indexOf("/")
    // A bare model name is not qualified, so any provider offering it may
    // serve — there is nothing to check.
    if (slash < 0) continue
    const provider = target.slice(0, slash)
    if (!knownProviders.includes(provider)) {
      problems.push(`${target}: no provider named ${provider} is configured`)
    }
  }
  return problems
}

/** One target, tagged with an id that survives both a reorder and an edit.
 *  The target text alone cannot key the row: two targets can hold the same
 *  text, and reliably do while one is a blank the operator hasn't typed into
 *  yet — keying on text there would collapse two rows onto one DOM node. */
type DraftRow = { id: string; value: string }

function toDraftRows(aliases: Aliases, makeId: () => string): Record<string, DraftRow[]> {
  return Object.fromEntries(
    Object.entries(aliases).map(([name, targets]) => [
      name,
      targets.map((value) => ({ id: makeId(), value })),
    ]),
  )
}

/** Reorders rows by id rather than by splicing values in place, so the row a
 *  reorder carries past the operator's cursor is still the row they were
 *  looking at — not whatever text a plain index-keyed list would have swapped
 *  into that screen position. Delegates the actual reordering to moveTarget,
 *  operating on the id list rather than the values. */
function reorderRows(rows: DraftRow[], from: number, to: number): DraftRow[] {
  const valueById = new Map(rows.map((r) => [r.id, r.value]))
  return moveTarget(
    rows.map((r) => r.id),
    from,
    to,
  ).map((id) => ({ id, value: valueById.get(id) ?? "" }))
}

export function AliasEditor({
  aliases,
  knownProviders,
}: {
  aliases: Aliases
  knownProviders: string[]
}) {
  const idCounter = useRef(0)
  const makeId = () => `row-${idCounter.current++}`

  const [draft, setDraft] = useState<Record<string, DraftRow[]>>(() =>
    toDraftRows(aliases, makeId),
  )
  const [newChainName, setNewChainName] = useState("")
  const [dragTarget, setDragTarget] = useState<{ name: string; index: number } | null>(null)

  const save = useApiMutation({
    mutationFn: (next: Aliases) => api.put("/api/aliases", next),
    success: "Aliases saved",
    invalidates: [keys.aliases, keys.config],
  })

  // Trimmed and stripped of in-progress blanks: what would actually be sent,
  // and what validateChain should judge — an empty row mid-edit is not yet a
  // chain with no targets, it is a chain with one target not typed yet.
  const cleaned: Aliases = Object.fromEntries(
    Object.entries(draft).map(([name, rows]) => [
      name,
      rows.map((r) => r.value.trim()).filter(Boolean),
    ]),
  )
  const problemsByChain = Object.fromEntries(
    Object.entries(cleaned).map(([name, targets]) => [
      name,
      validateChain(targets, knownProviders),
    ]),
  )
  const hasProblems = Object.values(problemsByChain).some((p) => p.length > 0)

  function updateTarget(name: string, id: string, value: string) {
    setDraft((d) => ({
      ...d,
      [name]: (d[name] ?? []).map((r) => (r.id === id ? { ...r, value } : r)),
    }))
  }

  function removeTarget(name: string, id: string) {
    setDraft((d) => ({ ...d, [name]: (d[name] ?? []).filter((r) => r.id !== id) }))
  }

  function drop(name: string, toIndex: number) {
    if (dragTarget && dragTarget.name === name) {
      setDraft((d) => ({
        ...d,
        [name]: reorderRows(d[name] ?? [], dragTarget.index, toIndex),
      }))
    }
    setDragTarget(null)
  }

  return (
    <Card className="p-4">
      <h2 className="mb-3 text-sm font-medium">Alias chains</h2>
      <div className="flex flex-col gap-4">
        {Object.entries(draft).map(([name, rows]) => (
          <div key={name} className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <span className="w-32 shrink-0 font-mono text-sm">{name}</span>
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
                Remove chain
              </Button>
            </div>
            {/* The chain order is the fallback order, so it is edited as an
                ordered, draggable list rather than as a set. Keyed by the
                row's own id, not its position — a reorder moves the id's DOM
                node with it, so an in-progress edit or focus stays on the
                target the operator was looking at. */}
            <ul className="flex flex-col gap-1 pl-4">
              {rows.map((row, index) => (
                <li
                  key={row.id}
                  draggable
                  onDragStart={() => setDragTarget({ name, index })}
                  onDragOver={(e) => e.preventDefault()}
                  onDrop={() => drop(name, index)}
                  className="flex items-center gap-2"
                >
                  <span className="cursor-grab text-[hsl(var(--muted-foreground))]" aria-hidden>
                    ⠿
                  </span>
                  <Input
                    aria-label={`${name} target ${index + 1}`}
                    value={row.value}
                    onChange={(e) => updateTarget(name, row.id, e.target.value)}
                    className="flex-1 font-mono text-sm"
                  />
                  <Button size="sm" variant="ghost" onClick={() => removeTarget(name, row.id)}>
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
            <Button
              size="sm"
              variant="ghost"
              className="ml-4 self-start"
              onClick={() =>
                setDraft((d) => ({
                  ...d,
                  [name]: [...(d[name] ?? []), { id: makeId(), value: "" }],
                }))
              }
            >
              Add target
            </Button>
            {(problemsByChain[name] ?? []).length > 0 && (
              <ul className="ml-4 flex flex-col gap-0.5 text-sm text-[hsl(var(--destructive))]">
                {(problemsByChain[name] ?? []).map((problem) => (
                  <li key={problem}>{problem}</li>
                ))}
              </ul>
            )}
          </div>
        ))}
      </div>
      <div className="mt-4 flex gap-2">
        <Input
          placeholder="new alias name"
          value={newChainName}
          onChange={(e) => setNewChainName(e.target.value)}
          className="w-48 font-mono text-sm"
        />
        <Button
          size="sm"
          variant="secondary"
          onClick={() => {
            // An operator with no aliases has to be able to make their first
            // one here; an editor that only edits what exists cannot be the
            // only way in. An inline input replaces window.prompt: a prompt
            // cannot be styled, cannot be driven by a test, and some
            // embeddings block it outright.
            const name = newChainName.trim()
            if (!name || name in draft) return
            setDraft((d) => ({ ...d, [name]: [] }))
            setNewChainName("")
          }}
        >
          Add chain
        </Button>
        <Button size="sm" disabled={hasProblems} onClick={() => save.mutate(cleaned)}>
          Save
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={() => setDraft(toDraftRows(aliases, makeId))}
        >
          Revert
        </Button>
      </div>
    </Card>
  )
}

export function RoutingScreen() {
  const [filters, setFilter] = useSearchFilters(["alias"] as const)
  const aliases = useAliases()
  const providers = useProviders()
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
            className="w-72 font-mono text-sm"
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

      {aliases.data && (
        <AliasEditor
          aliases={aliases.data}
          knownProviders={(providers.data?.providers ?? []).map((p) => p.id)}
        />
      )}

      <div className="mt-6">
      </div>
    </>
  )
}
