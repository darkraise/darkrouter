import type { ReactNode } from "react"

/**
 * Classes for a gated control that still holds a value.
 *
 * The half-opacity disabled state drops the value to about 3.4:1 against the
 * field, where it reads as a placeholder rather than as something the pane
 * will resurrect the moment the dialect can carry it again. Dim the border
 * instead and leave the value at body contrast. An empty gated control gets
 * nothing: there is no value to protect, and the flat state is the honest one.
 */
export function retainedValueClass(value: string): string {
  if (value.trim() === "") return ""
  return "disabled:opacity-100 disabled:border-[hsl(var(--input)/0.5)]"
}

/**
 * A control the current dialect may not be able to carry.
 *
 * The reason renders as text beneath rather than only in a tooltip: a disabled
 * element fires no pointer events, so a tooltip bound to the control itself
 * would never open, and a wrapper that exists only to catch a hover is a
 * mechanism a reader has to know about to trust the control.
 */
export function GatedField({ reason, children }: { reason: string | null; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-1.5">
      {children}
      {reason ? <p className="text-sm text-[hsl(var(--legend))]">{reason}</p> : null}
    </div>
  )
}
