import { useId, useMemo, useRef, useState } from "react"
import { ChevronDown, ChevronUp, Plus } from "lucide-react"
import { Button, Card, ToggleGroup, ToggleGroupItem } from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useAliases, useModels, usePolicy, useProviders } from "../../lib/queries"
import { useSearchFilters } from "../../lib/search-filters"
import type { Aliases, RouteCandidate, RoutePreview, RouteSkip } from "../../lib/api-types"
import { Ladder, type LadderRow, type PredictiveMark } from "../ladder/ladder"
import { ConfirmButton } from "../shell/confirm-button"
import { EmptyState, GhostChain } from "../shell/empty-state"
import { AddAliasDialog } from "./add-alias-dialog"
import { ChainGraph } from "./chain-graph"
import { isFatal, targetFacts, type ChainContext } from "./chain-health"
import { StrategyCard } from "./strategy-card"
import { TargetPill } from "./target-pill"
import { ModelCombobox, modelCandidates } from "../shell/model-combobox"

/**
 * Preview rows, in the order the endpoint returned them.
 *
 * Not re-sorted. §12 requires the preview to show the same ordered candidate
 * list a real request would produce, and the endpoint already guarantees that
 * by sharing the executor's snapshot — sorting here for display would undo it
 * and misreport failover order.
 */
export function previewRows(p: RoutePreview): LadderRow<PredictiveMark>[] {
  const candidates = collapse(p.candidates, (c) => `${c.provider_id}/${c.model}`).map(
    ({ first: c, count }): Omit<LadderRow<PredictiveMark>, "rank"> => ({
      // Hollow: nothing has been sent. This is what the router would do.
      mark: "skipped",
      target: `${c.provider_id}/${c.model}`,
      reasonCode: c.inferred ? "inferred" : undefined,
      reasonProse: prose(c.inferred ? "capabilities were guessed" : undefined, count),
    }),
  )
  // Keyed on the reason as well: two cooling keys are one fact, but a
  // cooling key and a disabled one are two, and folding them would hide the
  // reason an operator can act on.
  const skipped = collapse(p.skips, (s) => `${s.provider_id}/${s.model}\u0000${s.reason}`).map(
    ({ first: s, count }): Omit<LadderRow<PredictiveMark>, "rank"> => ({
      mark: s.reason === "cooling" ? "cooling" : "skipped",
      target: `${s.provider_id}/${s.model}`,
      reasonCode: s.reason,
      reasonProse: prose(undefined, count),
      terminated: true,
    }),
  )
  return [...candidates, ...skipped].map((row, i) => ({ ...row, rank: i + 1 }))
}

/**
 * One rung per (provider, model), however many credentials the router would
 * walk through on it.
 *
 * The endpoint lists every (provider, credential, model) it would try, so
 * three keys on one provider came back as three rows saying the same thing —
 * which reads as three providers until the operator notices the keys differ.
 * The first key decides where the rung sits, since that is where the router
 * would arrive first.
 */
function collapse<T extends RouteCandidate | RouteSkip>(
  items: T[],
  keyOf: (item: T) => string,
): { first: T; count: number }[] {
  const groups = new Map<string, { first: T; count: number }>()
  for (const item of items) {
    const key = keyOf(item)
    const group = groups.get(key)
    if (group) group.count++
    else groups.set(key, { first: item, count: 1 })
  }
  return [...groups.values()]
}

function prose(note: string | undefined, count: number): string | undefined {
  const parts = [note, count > 1 ? `× ${count} credentials` : undefined].filter(Boolean)
  return parts.length > 0 ? parts.join(" · ") : undefined
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
 * Problems that stop a save, as opposed to problems worth showing.
 *
 * The server validates on PUT and stays the authority; this exists so a typo
 * is caught before a round trip rather than instead of one.
 *
 * Only the states nothing can fix by waiting block. A target on a disabled or
 * cooling provider is a chain doing its job — the whole point of a fallback
 * order is that some of it is not currently serving — so those are drawn on
 * the pill and left alone here.
 */
export function validateChain(
  targets: string[],
  knownProviders: string[],
  ctx?: ChainContext,
): string[] {
  if (targets.length === 0) return ["an alias with no targets routes nowhere"]
  // A context with no providers in it cannot judge anything, so the ids fall
  // back to standing in for the full rows. Testing `ctx` for presence alone
  // was not enough: the editor always passes one, so the fallback was
  // unreachable and a caller that knew the provider ids was told none existed.
  const context: ChainContext =
    ctx && ctx.providers.length > 0
      ? ctx
      : {
          providers: knownProviders.map((id) => ({
            id,
            enabled: true,
            credentials: [{ enabled: true, cooling: false }],
          })),
          models: ctx?.models ?? [],
        }
  const problems: string[] = []
  for (const target of targets) {
    const facts = targetFacts(target, context)
    if (isFatal(facts.state)) problems.push(`${target}: ${facts.problem}`)
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

const EMPTY_CONTEXT: ChainContext = { providers: [], models: [] }

export function AliasEditor({
  aliases,
  knownProviders,
  context = EMPTY_CONTEXT,
  candidates = [],
  onPreview,
}: {
  aliases: Aliases
  knownProviders: string[]
  /** Live provider, catalogue and breaker state, so each target can say what
   *  the router would make of it right now rather than only whether it parses. */
  context?: ChainContext
  candidates?: string[]
  onPreview?: (name: string) => void
}) {
  // The prefix is this editor's alone, so two editors on one page cannot
  // mint the same row id; the counter only has to be unique within it.
  const idPrefix = useId()
  const idCounter = useRef(0)
  const makeId = () => `${idPrefix}${idCounter.current++}`

  const [draft, setDraft] = useState<Record<string, DraftRow[]>>(() =>
    toDraftRows(aliases, makeId),
  )
  // What the draft was seeded from. `PUT /api/aliases` replaces the whole map
  // rather than merging, so a draft that never notices an alias added
  // elsewhere will delete it on the next Save and report success. Adopting
  // chains the draft has never seen keeps the write additive without
  // discarding whatever is being typed.
  const [seededFrom, setSeededFrom] = useState(aliases)
  if (aliases !== seededFrom) {
    setSeededFrom(aliases)
    setDraft((d) => {
      const next = { ...d }
      for (const [name, targets] of Object.entries(aliases)) {
        if (!(name in next)) next[name] = targets.map((value) => ({ id: makeId(), value }))
      }
      return next
    })
  }
  const [addOpen, setAddOpen] = useState(false)
  const [editing, setEditing] = useState<string | null>(null)
  const [dragTarget, setDragTarget] = useState<{ name: string; index: number } | null>(null)

  const save = useApiMutation({
    mutationFn: (next: Aliases) => api.put("/api/aliases", next),
    success: "Aliases saved",
    // The catalogue too: its alias column is read from the same map.
    invalidates: [keys.aliases, keys.config, keys.models],
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
      validateChain(targets, knownProviders, context),
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

  function move(name: string, from: number, to: number) {
    setDraft((d) => ({ ...d, [name]: reorderRows(d[name] ?? [], from, to) }))
  }

  function drop(name: string, toIndex: number) {
    if (dragTarget && dragTarget.name === name) move(name, dragTarget.index, toIndex)
    setDragTarget(null)
  }

  const names = Object.keys(draft)

  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center justify-between gap-2">
        <h2 className="text-sm font-medium">Alias chains</h2>
        <span className="text-sm text-[hsl(var(--legend))]">
          {names.length === 0
            ? "none yet"
            : `${names.length} ${names.length === 1 ? "chain" : "chains"}`}
        </span>
      </div>

      {names.length === 0 && (
        <EmptyState
          title="An alias is a name your clients ask for"
          hint="Point one at a list of providers and the router walks it in order, moving to the next when one refuses. Clients keep asking for the alias; you change what it means."
          preview={<GhostChain />}
        />
      )}

      <div className="flex flex-col gap-2">
        {names.map((name) => {
          const rows = draft[name] ?? []
          const open = editing === name
          const problems = problemsByChain[name] ?? []
          const saved = aliases[name]
          const pending = cleaned[name] ?? []
          const unsaved =
            saved === undefined ||
            saved.length !== pending.length ||
            saved.some((t, i) => t !== pending[i])
          return (
            <div key={name} className="rounded-[var(--radius)] border p-3">
              {/* The chain at rest: its name, and where it would go, in order.
                  This is the view an operator spends their time in — editing
                  is the exception, so it is the thing behind a click. */}
              <div className="flex flex-wrap items-center gap-2">
                <span className="w-32 shrink-0 truncate font-mono text-sm" title={name}>
                  {name}
                </span>
                <span className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
                  {rows.length === 0 ? (
                    <span className="text-sm text-[hsl(var(--muted-foreground))]">
                      no targets yet
                    </span>
                  ) : (
                    rows.map((row, i) => (
                      <TargetPill
                        key={row.id}
                        rank={i + 1}
                        facts={targetFacts(row.value, context)}
                      />
                    ))
                  )}
                </span>
                {onPreview && (
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => onPreview(name)}
                    // The endpoint resolves what is stored, so a chain with
                    // unsaved edits would be previewed as it was, beside pills
                    // drawn from the draft.
                    title={
                      unsaved
                        ? "Previews the saved chain — this one has unsaved changes"
                        : undefined
                    }
                  >
                    Preview{unsaved ? " (saved)" : ""}
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setEditing(open ? null : name)}
                  aria-expanded={open}
                >
                  {open ? "Done" : "Edit"}
                </Button>
                {/* A whole chain is worth asking about; a single target row is
                    not — that is one click of Add target to put back, and a
                    prompt per row would make the editor unusable. */}
                <ConfirmButton
                  size="sm"
                  variant="ghost"
                  className="text-[hsl(var(--destructive))]"
                  title={`Remove the ${name} chain?`}
                  description={`Requests asking for ${name} stop resolving through it and fall back to whatever a bare model name of that spelling finds. Nothing is written until you save.`}
                  confirmLabel="Remove chain"
                  destructive
                  onConfirm={() =>
                    setDraft((d) => {
                      const next = { ...d }
                      delete next[name]
                      return next
                    })
                  }
                >
                  Remove
                </ConfirmButton>
              </div>

              {open && (
                <div className="mt-3 border-t pt-3">
                  {/* The chain order is the fallback order, so it is edited as
                      an ordered, draggable list rather than as a set. Keyed by
                      the row's own id, not its position — a reorder moves the
                      id's DOM node with it, so an in-progress edit or focus
                      stays on the target the operator was looking at. */}
                  <ul className="flex flex-col gap-1.5">
                    {rows.map((row, index) => (
                      <li
                        key={row.id}
                        draggable
                        onDragStart={() => setDragTarget({ name, index })}
                        onDragOver={(e) => e.preventDefault()}
                        onDrop={() => drop(name, index)}
                        className="flex items-center gap-2"
                      >
                        <span
                          className="cursor-grab text-[hsl(var(--muted-foreground))]"
                          aria-hidden
                        >
                          ⠿
                        </span>
                        <span className="w-5 shrink-0 text-right font-mono text-sm text-[hsl(var(--legend))]">
                          {index + 1}
                        </span>
                        <ModelCombobox
                          label={`${name} target ${index + 1}`}
                          value={row.value}
                          onChange={(value) => updateTarget(name, row.id, value)}
                          candidates={candidates}
                          placeholder="provider/model, or a model name"
                        />
                        {/* Drag is pointer-only, and the order is the whole
                            point of a chain: the buttons are how it is changed
                            from the keyboard. */}
                        <Button
                          size="icon"
                          variant="ghost"
                          aria-label={`Move ${name} target ${index + 1} up`}
                          disabled={index === 0}
                          onClick={() => move(name, index, index - 1)}
                        >
                          <ChevronUp className="size-[var(--icon-size)]" />
                        </Button>
                        <Button
                          size="icon"
                          variant="ghost"
                          aria-label={`Move ${name} target ${index + 1} down`}
                          disabled={index === rows.length - 1}
                          onClick={() => move(name, index, index + 1)}
                        >
                          <ChevronDown className="size-[var(--icon-size)]" />
                        </Button>
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => removeTarget(name, row.id)}
                        >
                          Remove
                        </Button>
                      </li>
                    ))}
                  </ul>
                  <Button
                    size="sm"
                    variant="ghost"
                    className="mt-1.5"
                    onClick={() =>
                      setDraft((d) => ({
                        ...d,
                        [name]: [...(d[name] ?? []), { id: makeId(), value: "" }],
                      }))
                    }
                  >
                    Add target
                  </Button>
                </div>
              )}

              {problems.length > 0 && (
                <ul className="mt-2 flex flex-col gap-0.5 text-sm text-[hsl(var(--destructive))]">
                  {problems.map((problem) => (
                    <li key={problem}>{problem}</li>
                  ))}
                </ul>
              )}
            </div>
          )
        })}
      </div>

      <div className="mt-4 flex flex-wrap gap-2 border-t pt-4">
        {/* An operator with no aliases has to be able to make their first one
            here; an editor that only edits what exists cannot be the only way
            in. */}
        <Button size="sm" variant="secondary" onClick={() => setAddOpen(true)}>
          <Plus className="size-[var(--icon-size)]" />
          Add alias
        </Button>
        <AddAliasDialog
          open={addOpen}
          onOpenChange={setAddOpen}
          existingNames={names}
          candidates={candidates}
          context={context}
          onCreate={(name, targets) => {
            setDraft((d) => ({
              ...d,
              [name]: targets.map((value) => ({ id: makeId(), value })),
            }))
            // Opened rather than left collapsed: the operator was just editing
            // this chain, and the next thing they do is usually to it.
            setEditing(name)
          }}
        />
        <div className="ml-auto flex gap-2">
          <Button size="sm" disabled={hasProblems} onClick={() => save.mutate(cleaned)}>
            Save
          </Button>
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setDraft(toDraftRows(aliases, makeId))
              setEditing(null)
            }}
          >
            Revert
          </Button>
        </div>
      </div>
    </Card>
  )
}

/** The filter this screen keeps in the URL. A module constant so the hook
 *  sees the same array each render rather than a fresh literal. */
const ROUTING_FIELDS = ["alias"] as const

export function RoutingScreen() {
  const [filters, setFilter] = useSearchFilters(ROUTING_FIELDS)
  const aliases = useAliases()
  const providers = useProviders()
  const models = useModels()
  const policy = usePolicy()
  // The request and its result move together. Kept apart, a preview that 404s
  // or one that arrives out of order leaves the graph labelling the previous
  // candidate list with the new request's name.
  const [preview, setPreview] = useState<{ request: string; result: RoutePreview } | null>(null)
  const [view, setView] = useState<"ladder" | "graph">("ladder")

  const run = useApiMutation({
    mutationFn: async (model: string) => ({
      request: model,
      result: await api.post<RoutePreview>("/api/route/preview", { model }),
    }),
    onSuccess: setPreview,
  })

  // Memoised on the query results, not on the `?? []` defaults: those build a
  // fresh array on every render while the data is undefined, so every memo
  // below them would recompute each time and hand a new object to the editor.
  const providerRows = useMemo(() => providers.data?.providers ?? [], [providers.data])
  const modelRows = useMemo(() => models.data?.models ?? [], [models.data])
  const context: ChainContext = useMemo(
    () => ({ providers: providerRows, models: modelRows }),
    [providerRows, modelRows],
  )
  // Two lists, because they are two different questions. Inside a chain the
  // router expands targets through rules 2 and 3 only, so an alias suggested
  // there could never resolve; the preview box answers rule 1 as well.
  const chainCandidates = useMemo(() => modelCandidates({ models: modelRows }), [modelRows])
  const aliasNames = useMemo(() => Object.keys(aliases.data ?? {}), [aliases.data])
  const previewCandidates = useMemo(
    () => modelCandidates({ models: modelRows, aliases: aliasNames }),
    [modelRows, aliasNames],
  )

  function previewChain(name: string) {
    setFilter("alias", name)
    run.mutate(name)
  }

  // Memoised: the graph rebuilds its nodes when the rows change identity.
  const rows = useMemo(() => (preview ? previewRows(preview.result) : []), [preview])

  return (
    <>
      <StrategyCard policy={policy.data} providers={providerRows} />

      {aliases.data && (
        <AliasEditor
          aliases={aliases.data}
          knownProviders={providerRows.map((p) => p.id)}
          context={context}
          candidates={chainCandidates}
          onPreview={previewChain}
        />
      )}

      <Card className="mt-6 p-4">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-sm font-medium">Preview</h2>
          <ToggleGroup
            type="single"
            value={view}
            onValueChange={(next) => {
              if (next === "ladder" || next === "graph") setView(next)
            }}
            aria-label="Preview layout"
            className="w-fit rounded-[var(--radius)] border bg-[hsl(var(--muted))] p-0.5"
          >
            <ToggleGroupItem value="ladder">Ladder</ToggleGroupItem>
            <ToggleGroupItem value="graph">Graph</ToggleGroupItem>
          </ToggleGroup>
        </div>
        <div className="flex gap-2">
          <ModelCombobox
            label="Alias or model to preview"
            value={filters.alias}
            onChange={(next) => setFilter("alias", next)}
            candidates={previewCandidates}
            placeholder="alias or model"
            className="w-72"
          />
          <Button
            size="sm"
            disabled={run.isPending}
            onClick={() => run.mutate(filters.alias)}
          >
            Preview
          </Button>
        </div>

        {preview && (
          <div className="mt-4">
            {preview.result.error && (
              <p className="mb-2 text-sm text-[hsl(var(--muted-foreground))]">
                {preview.result.error}
              </p>
            )}
            {/* Even with no candidates the skips are shown: they are the only
                account of why nothing routed. */}
            {view === "ladder" ? (
              <Ladder mode="predictive" rows={rows} />
            ) : (
              <ChainGraph request={preview.request} rows={rows} />
            )}
          </div>
        )}
      </Card>
    </>
  )
}
