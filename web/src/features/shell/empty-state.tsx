import type { ReactNode } from "react"

/**
 * The two kinds of nothing, and why they are two components rather than one
 * with a flag.
 *
 * `EmptyState` is a well that has never filled: no request has arrived, no
 * sweep has run, nobody has added an account. The fix is upstream of this
 * screen, so the panel says what will appear here, what produces it, and
 * offers that action.
 *
 * `NoMatch` is a filter excluding data that exists. The fix is right here and
 * the operator has already seen the shape of what they are missing, so it is
 * one compact line and a way back — a full well would restate a layout that
 * is still on screen and read as though the data had been lost.
 *
 * Telling them apart is the whole point: "no requests" and "no requests
 * matching these filters" send an operator to different places, and a screen
 * that renders the same grey sentence for both sends them to the wrong one.
 */
export function EmptyState({
  title,
  hint,
  action,
  preview,
}: {
  /** What appears here, stated as the fact it is rather than as an absence. */
  title: string
  /** What produces it. The sentence that tells an operator where to go. */
  hint: string
  /** The one thing to do about it, when there is one. */
  action?: ReactNode
  /** A dimmed wireframe of the artifact this well will hold. */
  preview?: ReactNode
}) {
  return (
    <div className="rounded-[var(--radius)] border border-dashed p-8 text-center">
      <p className="text-sm font-medium">{title}</p>
      <p className="mx-auto mt-1 max-w-prose text-sm text-[hsl(var(--muted-foreground))]">
        {hint}
      </p>
      {action && <div className="mt-4 flex justify-center gap-2">{action}</div>}
      {/* Below the action, not above it: the shape is context for a decision
          already made, and leading with it puts scenery between the operator
          and the button. */}
      {preview && (
        <div className="mt-6 opacity-30" aria-hidden="true">
          {preview}
        </div>
      )}
    </div>
  )
}

/** A filter that excludes everything. `what` is the plural noun the screen
 *  lists, so the sentence reads as the screen's own. */
export function NoMatch({ what, onClear }: { what: string; onClear?: () => void }) {
  return (
    <div className="flex flex-wrap items-baseline justify-center gap-x-2 gap-y-1 rounded-[var(--radius)] border border-dashed p-6 text-center">
      <p className="text-sm text-[hsl(var(--muted-foreground))]">
        No {what} match these filters.
      </p>
      {onClear && (
        // A link rather than a button: it undoes a choice the operator made
        // rather than acting on the gateway, and the buttons on these screens
        // all do the second thing.
        <button
          type="button"
          onClick={onClear}
          className="text-sm underline underline-offset-2 hover:text-[hsl(var(--foreground))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))]"
        >
          Clear filters
        </button>
      )}
    </div>
  )
}

/**
 * Dimmed wireframes of what each well will hold.
 *
 * The console's own idiom, generalised from the first-run panel: a shape
 * teaches what is coming without claiming any of it exists. Hairlines and
 * dashes only — a solid block would read as content that failed to load,
 * which is the one thing an empty state must never look like.
 */
export function GhostRows({ rows = 4 }: { rows?: number }) {
  return (
    <div className="flex flex-col gap-2">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="flex items-center gap-3">
          <div className="h-6 w-6 shrink-0 rounded border border-dashed" />
          <div className="h-px flex-1 border-t border-dashed" />
          <div className="h-6 w-16 shrink-0 rounded border border-dashed" />
        </div>
      ))}
    </div>
  )
}

export function GhostChart() {
  return (
    <div className="flex h-20 items-end justify-center gap-2">
      {[40, 65, 30, 80, 55, 70, 45].map((h, i) => (
        <div
          key={i}
          className="w-8 rounded-t border border-b-0 border-dashed"
          style={{ height: `${h}%` }}
        />
      ))}
    </div>
  )
}

/** Alias, router, provider — the three columns the routing flow draws. */
export function GhostChain() {
  return (
    <div className="flex items-center gap-4">
      <div className="h-8 w-24 rounded border border-dashed" />
      <div className="h-px flex-1 border-t border-dashed" />
      <div className="h-8 w-8 rounded-full border border-dashed" />
      <div className="h-px flex-1 border-t border-dashed" />
      <div className="h-8 w-24 rounded border border-dashed" />
    </div>
  )
}
