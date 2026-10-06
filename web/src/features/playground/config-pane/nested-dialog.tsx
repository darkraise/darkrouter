import { createContext, useContext, useEffect } from "react"

/**
 * How a dialog learns that one of its descendants has opened a dialog of its
 * own.
 *
 * darkraise-ui's dismissable layers defer to a layer nested inside their DOM
 * node, but every dialog portals to the body, so a dialog opened from inside
 * another is a sibling there and both treat Escape as their own. The outer
 * one has to be told to stand down; this is the channel it is told through.
 */
export const NestedDialogContext = createContext<(open: boolean) => void>(() => {})

/**
 * Whether an Escape about to close a dialog is really aimed at a popup inside
 * it -- a model suggestion list or a Select's options.
 *
 * Those popups portal to the body too, so the dialog's layer hears the same
 * Escape and closes, taking every value typed into it along. A dialog passes
 * this to its content's onEscapeKeyDown and stands down when it is true.
 * Read off the focused element rather than reported by each control, so it
 * holds for any combobox or select the pane gains later: an open Select keeps
 * focus inside its listbox, and an open combobox keeps it on an input marked
 * expanded. An expanded accordion trigger is not a popup, which is why the
 * popup roles are required alongside aria-expanded.
 */
export function escapeBelongsToPopup(): boolean {
  const focused = document.activeElement
  if (!(focused instanceof HTMLElement)) return false
  if (focused.closest('[role="listbox"], [role="menu"]')) return true
  if (focused.getAttribute("aria-expanded") !== "true") return false
  const popup = focused.getAttribute("aria-haspopup")
  return focused.getAttribute("role") === "combobox" || (popup !== null && popup !== "false")
}

/** Reports whether this component currently has a dialog open, to whatever
 *  dialog is above it. Nothing happens outside one. */
export function useReportNestedDialog(open: boolean) {
  const report = useContext(NestedDialogContext)
  useEffect(() => {
    report(open)
    return () => report(false)
  }, [open, report])
}
