import { useId, useRef, useState } from "react"
import {
  Button,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  Input,
  Label,
} from "darkraise-ui"
import { ModelCombobox } from "../shell/model-combobox"
import { targetFacts, type ChainContext } from "./chain-health"
import { STATE_PROSE, TargetDot } from "./target-pill"

/**
 * Why a name cannot be used, or null if it can.
 *
 * Returns null for the empty string too: nothing typed yet is not a mistake to
 * report, it is a form that has not been filled in. The Create button is
 * disabled on emptiness separately.
 */
export function aliasNameProblem(name: string, existing: string[]): string | null {
  const trimmed = name.trim()
  if (trimmed === "") return null
  if (existing.includes(trimmed)) return `There is already an alias called ${trimmed}.`
  // The router resolves an alias by exact match against the requested model
  // name, so a name with a space in it can be written here and never matched
  // by any client.
  if (/\s/.test(trimmed)) return "An alias name cannot contain spaces."
  return null
}

type Row = { id: string; value: string }

/** What the dialog would create: trimmed, with the rows nobody typed into
 *  dropped. An untouched blank row is not an empty target, it is a row that
 *  was added and not used. */
export function plannedTargets(rows: Row[]): string[] {
  return rows.map((r) => r.value.trim()).filter(Boolean)
}

/**
 * Creating an alias, name and targets together.
 *
 * A chain is an ordered fallback list, and an alias with no targets routes
 * nowhere — so the name alone was never enough to create anything useful. The
 * targets are typed here, in the order they will be tried.
 *
 * Nothing is sent from here. `PUT /api/aliases` replaces the whole map, so a
 * write from this dialog would carry along every other unsaved edit in the
 * editor behind it. The new chain joins the draft and one Save writes them
 * together.
 */
export function AddAliasDialog({
  open,
  onOpenChange,
  existingNames,
  candidates,
  context,
  onCreate,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  existingNames: string[]
  candidates: string[]
  context: ChainContext
  onCreate: (name: string, targets: string[]) => void
}) {
  // Unique to this dialog instance, so a row id can never collide with one
  // the editor behind it minted.
  const idPrefix = useId()
  // Past the first row, which is minted from the prefix alone. The dialog
  // opens with one row every time, so that row needs no counter to be
  // unique -- and the initial state is built during render, where a ref
  // must not be read.
  const counter = useRef(1)
  const firstRow = (): Row => ({ id: `${idPrefix}-first`, value: "" })
  const makeRow = (): Row => ({ id: `${idPrefix}${counter.current++}`, value: "" })
  const [name, setName] = useState("")
  const [rows, setRows] = useState<Row[]>(() => [firstRow()])

  function reset() {
    setName("")
    counter.current = 1
    setRows([firstRow()])
  }

  const problem = aliasNameProblem(name, existingNames)
  const targets = plannedTargets(rows)
  const canCreate = name.trim() !== "" && problem === null && targets.length > 0

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) reset()
        onOpenChange(next)
      }}
    >
      <DialogContent className="flex max-h-[85vh] max-w-2xl flex-col overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Add an alias</DialogTitle>
          <DialogDescription>
            A name clients can ask for, and the targets to try for it in order. The
            first one that can serve wins; the rest are the fallback.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="new-alias-name">Alias name</Label>
            <Input
              id="new-alias-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="sonnet"
              className="w-64 font-mono text-sm"
              autoFocus
            />
            {problem && (
              <span className="text-sm text-[hsl(var(--destructive))]">{problem}</span>
            )}
          </div>

          <div className="flex flex-col gap-1.5 border-t pt-4">
            <span className="text-sm font-medium">Targets</span>
            <ul className="flex flex-col gap-1.5">
              {rows.map((row, index) => {
                const facts = targetFacts(row.value, context)
                return (
                  <li key={row.id} className="flex flex-col gap-0.5">
                    <div className="flex items-center gap-2">
                      <span className="w-5 shrink-0 text-right font-mono text-sm text-[hsl(var(--legend))]">
                        {index + 1}
                      </span>
                      <TargetDot state={facts.state} />
                      <ModelCombobox
                        label={`New alias target ${index + 1}`}
                        value={row.value}
                        onChange={(value) =>
                          setRows((rs) =>
                            rs.map((r) => (r.id === row.id ? { ...r, value } : r)),
                          )
                        }
                        candidates={candidates}
                        placeholder="provider/model, or a model name"
                      />
                      <Button
                        size="sm"
                        variant="ghost"
                        // The last row is the one being typed into; removing it
                        // would leave a dialog with nothing to fill in.
                        disabled={rows.length === 1}
                        onClick={() => setRows((rs) => rs.filter((r) => r.id !== row.id))}
                      >
                        Remove
                      </Button>
                    </div>
                    {/* Said before the alias exists rather than after: a target
                        naming a provider that is not configured is worth
                        knowing about while it is still one keystroke to fix. */}
                    {facts.problem && (
                      <span className="ml-11 text-sm text-[hsl(var(--legend))]">
                        {STATE_PROSE[facts.state]} — {facts.problem}
                      </span>
                    )}
                  </li>
                )
              })}
            </ul>
            <Button
              size="sm"
              variant="ghost"
              className="self-start"
              onClick={() => setRows((rs) => [...rs, makeRow()])}
            >
              Add target
            </Button>
          </div>
        </div>

        <div className="mt-2 flex items-center gap-3 border-t pt-3">
          <span className="text-sm text-[hsl(var(--legend))]">
            Added to the draft. Save writes every pending change together.
          </span>
          <div className="ml-auto flex items-center gap-2">
            <Button
              variant="ghost"
              onClick={() => {
                reset()
                onOpenChange(false)
              }}
            >
              Cancel
            </Button>
            {/* Outline while it cannot act: a disabled filled button is still
                the loudest thing in the dialog. */}
            <Button
              variant={canCreate ? "default" : "outline"}
              disabled={!canCreate}
              onClick={() => {
                onCreate(name.trim(), targets)
                reset()
                onOpenChange(false)
              }}
            >
              {targets.length <= 1 ? "Create alias" : `Create with ${targets.length} targets`}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
