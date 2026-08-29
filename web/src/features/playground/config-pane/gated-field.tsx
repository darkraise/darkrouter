import type { ReactNode } from "react"

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
