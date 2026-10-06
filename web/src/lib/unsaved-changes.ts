import { useEffect } from "react"
import { useBlocker } from "@tanstack/react-router"

const DEFAULT_MESSAGE = "You have unsaved changes. Leave this page and discard them?"

/** One entry per mounted guard whose screen holds a draft right now. */
const drafts = new Set<object>()

/** Set once the operator has agreed to drop every draft for an exit the
 *  console makes itself, so the reload that exit ends in does not ask again. */
let released = false

/**
 * Asks before an edit that has not been saved is thrown away.
 *
 * Two exits lose a draft: a route change inside the console, which the router
 * can hold until the operator answers, and a reload or tab close, which only
 * the browser's own beforeunload prompt can stop. One hook covers both so a
 * screen with a draft cannot guard one exit and forget the other. A third,
 * signing out, is neither, and asks through `confirmDiscardingDrafts`.
 *
 * `dirty` is read when the exit happens rather than when the hook rendered,
 * so a save that clears the draft lets the very next navigation through.
 */
export function useUnsavedChangesGuard(dirty: boolean, message: string = DEFAULT_MESSAGE) {
  useEffect(() => {
    if (!dirty) return
    const draft = {}
    drafts.add(draft)
    return () => {
      drafts.delete(draft)
    }
  }, [dirty])
  useBlocker({
    shouldBlockFn: () => dirty && !window.confirm(message),
    enableBeforeUnload: () => dirty && !released,
  })
}

/**
 * Asks before an exit the router cannot see throws a draft away.
 *
 * Signing out is a POST and then a reload. The reload is what beforeunload
 * catches, and by then the POST has ended the session: "Stay" kept a page that
 * could no longer save, and the draft went with the next 401 anyway. So the
 * question is asked here, before anything is sent. True when there is nothing
 * to lose or the operator agreed to lose it, and in that case the reload that
 * follows goes through without asking a second time.
 */
export function confirmDiscardingDrafts(message: string): boolean {
  if (drafts.size === 0) return true
  if (!window.confirm(message)) return false
  released = true
  return true
}
