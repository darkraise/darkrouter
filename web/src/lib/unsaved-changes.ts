import { useBlocker } from "@tanstack/react-router"

const DEFAULT_MESSAGE = "You have unsaved changes. Leave this page and discard them?"

/**
 * Asks before an edit that has not been saved is thrown away.
 *
 * Two exits lose a draft: a route change inside the console, which the router
 * can hold until the operator answers, and a reload or tab close, which only
 * the browser's own beforeunload prompt can stop. One hook covers both so a
 * screen with a draft cannot guard one exit and forget the other.
 *
 * `dirty` is read when the exit happens rather than when the hook rendered,
 * so a save that clears the draft lets the very next navigation through.
 */
export function useUnsavedChangesGuard(dirty: boolean, message: string = DEFAULT_MESSAGE) {
  useBlocker({
    shouldBlockFn: () => dirty && !window.confirm(message),
    enableBeforeUnload: () => dirty,
  })
}
