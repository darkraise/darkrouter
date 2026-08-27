import type { SavedView } from "../../lib/api-types"

const KEY = "darkrouter_saved_views"

export type { SavedView }

export function loadSavedViews(): SavedView[] {
  try {
    const raw = localStorage.getItem(KEY)
    const parsed: unknown = raw ? JSON.parse(raw) : []
    return Array.isArray(parsed) ? (parsed as SavedView[]) : []
  } catch {
    // Anything can write here, including an older build of this app. Losing
    // the views beats throwing on every render of the screen.
    return []
  }
}

function write(views: SavedView[]): SavedView[] {
  try {
    localStorage.setItem(KEY, JSON.stringify(views))
  } catch {
    // A full or blocked store is not a reason to refuse the filter change the
    // operator actually asked for.
  }
  return views
}

export function saveView(name: string, filters: Record<string, string>): SavedView[] {
  const kept = Object.fromEntries(Object.entries(filters).filter(([, v]) => v !== ""))
  const rest = loadSavedViews().filter((v) => v.name !== name)
  return write([...rest, { name, filters: kept }])
}

export function deleteView(name: string): SavedView[] {
  return write(loadSavedViews().filter((v) => v.name !== name))
}
