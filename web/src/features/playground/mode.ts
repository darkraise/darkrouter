export type PlaygroundMode = "chat" | "lab"

const STORAGE_KEY = "darkrouter.playground.mode"

export function isMode(value: unknown): value is PlaygroundMode {
  return value === "chat" || value === "lab"
}

export function storedMode(): PlaygroundMode | undefined {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return isMode(raw) ? raw : undefined
  } catch {
    // A private window and a browser set to block site data both throw on
    // read. The mode is a convenience, so losing it is not an error state.
    return undefined
  }
}

export function rememberMode(mode: PlaygroundMode) {
  try {
    localStorage.setItem(STORAGE_KEY, mode)
  } catch {
    // Nothing to do. The choice still applies to this page.
  }
}

/**
 * The mode a fresh load opens in.
 *
 * The URL beats the stored preference because a link says where its sender
 * meant, and a seed beats both because a seed is a routing investigation --
 * the trace drawer's "Open in playground" wants the instrument, not a
 * conversation. An explicit ?mode= still wins over the seed, since nothing
 * puts one there by accident.
 */
export function initialMode(search: { mode?: string; seed?: string }): PlaygroundMode {
  if (isMode(search.mode)) return search.mode
  if (search.seed !== undefined) return "lab"
  return storedMode() ?? "lab"
}
