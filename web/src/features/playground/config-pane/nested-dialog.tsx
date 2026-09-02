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

/** Reports whether this component currently has a dialog open, to whatever
 *  dialog is above it. Nothing happens outside one. */
export function useReportNestedDialog(open: boolean) {
  const report = useContext(NestedDialogContext)
  useEffect(() => {
    report(open)
    return () => report(false)
  }, [open, report])
}
