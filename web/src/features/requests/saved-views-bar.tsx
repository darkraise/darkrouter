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

  function confirmSave() {
    if (!savingName) return
    setViews(saveView(savingName, filters))
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
        <>
          <Input
            autoFocus
            placeholder="View name"
            value={savingName}
            onChange={(e) => setSavingName(e.target.value)}
            className="w-40"
          />
          <Button size="sm" onClick={confirmSave}>
            Save
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setSavingName(null)}>
            Cancel
          </Button>
        </>
      )}
    </div>
  )
}
