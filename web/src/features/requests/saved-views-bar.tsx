import { useState } from "react"
import { Button, Input } from "darkraise-ui"
import type { SavedView } from "../../lib/api-types"
import { deleteView, loadSavedViews, saveView } from "./saved-views"

/** The saved-views row: apply, delete, and the inline "save this view" flow.
 *  Filter writing itself is not this component's job — it hands the merged
 *  filter set up to `onApply`, which is the screen's one atomic URL writer,
 *  shared with every other control that can change more than one field. */
export function SavedViewsBar({
  fields,
  filters,
  onApply,
}: {
  fields: readonly string[]
  filters: Record<string, string>
  onApply: (filters: Record<string, string>) => void
}) {
  // localStorage is not reactive, so the list is mirrored into state and
  // refreshed from what saveView/deleteView return, rather than re-read on
  // every render.
  const [views, setViews] = useState<SavedView[]>(() => loadSavedViews())
  const [savingName, setSavingName] = useState<string | null>(null)

  function applyView(view: SavedView) {
    // A saved view carries only what it filters (empties were dropped on
    // save), so applying it must clear every other field explicitly rather
    // than merge on top of whatever happens to be active already.
    const merged = Object.fromEntries(fields.map((f) => [f, view.filters[f] ?? ""]))
    onApply(merged)
  }

  // Trimmed, so "  " is not a name and "errors " does not sit beside "errors".
  const name = savingName?.trim() ?? ""

  function confirmSave() {
    if (!name) return
    setViews(saveView(name, filters))
    setSavingName(null)
  }

  return (
    <div className="mb-4 flex flex-wrap items-center gap-2">
      {views.map((v) => (
        <div key={v.name} className="flex items-center gap-1">
          <Button variant="outline" size="sm" onClick={() => applyView(v)}>
            {v.name}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            aria-label={`Delete saved view ${v.name}`}
            onClick={() => setViews(deleteView(v.name))}
          >
            ×
          </Button>
        </div>
      ))}
      {savingName === null ? (
        <Button variant="ghost" size="sm" onClick={() => setSavingName("")}>
          Save this view
        </Button>
      ) : (
        // A form, so Enter saves the way it does in every other name field;
        // Save is disabled until there is a name rather than ignoring the
        // click without a word.
        <form
          className="flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            confirmSave()
          }}
        >
          <Input
            autoFocus
            aria-label="View name"
            placeholder="View name"
            value={savingName}
            onChange={(e) => setSavingName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape") setSavingName(null)
            }}
            className="w-40"
          />
          <Button type="submit" size="sm" disabled={!name}>
            Save
          </Button>
          <Button type="button" variant="ghost" size="sm" onClick={() => setSavingName(null)}>
            Cancel
          </Button>
        </form>
      )}
    </div>
  )
}
