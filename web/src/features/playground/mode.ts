export type PlaygroundMode = "chat" | "compare" | "auxiliary"

const STORAGE_KEY = "darkrouter.playground.mode"

/** What "lab" used to name. Lab's Single surface and its request pane are
 *  Chat now, so a stored preference or a shared link still carrying the old
 *  word lands where its sender meant rather than being ignored. */
const RETIRED: Record<string, PlaygroundMode> = { lab: "chat", single: "chat", count: "auxiliary" }

export function isMode(value: unknown): value is PlaygroundMode {
  return value === "chat" || value === "compare" || value === "auxiliary"
}

/** A mode name this build understands, including the ones it has renamed. */
export function readMode(value: unknown): PlaygroundMode | undefined {
  if (isMode(value)) return value
  return typeof value === "string" ? RETIRED[value] : undefined
}

export function storedMode(): PlaygroundMode | undefined {
  try {
    return readMode(localStorage.getItem(STORAGE_KEY))
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
 * meant. A seed no longer chooses a mode of its own: the request pane a seeded
 * trace wants to land in is Chat's, so Chat is where it goes -- which is also
 * where a load with no preference at all starts.
 */
export function initialMode(search: { mode?: string; seed?: string }): PlaygroundMode {
  return readMode(search.mode) ?? storedMode() ?? "chat"
}
