import { Card } from "darkraise-ui"
import { AUX_SURFACES } from "./surfaces"
import type { AuxSurface } from "../../../lib/api-types"

/**
 * The six tools, as a rail rather than a tab strip.
 *
 * A tab strip stretched edge to edge across the page put six equal-weight
 * words above a form and said nothing about what any of them did. The rail is
 * the same shape Chat's conversations take, so the two surfaces read as one
 * screen — and it has room for the line that says what each tool is for,
 * which "Rerank" on its own does not.
 *
 * The selected treatment is the sidebar's: tinted, bolder, and a primary bar
 * down the left edge, so "where I am" looks the same in all three places the
 * console draws a list.
 */
export function ToolRail({
  active,
  onSelect,
  runCounts,
}: {
  active: AuxSurface
  onSelect: (surface: AuxSurface) => void
  /** How many runs each tool has this session, so the rail says where the
   *  work has been without opening anything. */
  runCounts: Partial<Record<AuxSurface, number>>
}) {
  return (
    <Card className="flex min-h-0 w-full flex-1 flex-col overflow-hidden p-0">
      <div className="shrink-0 border-b px-4 py-2">
        <h2 className="text-sm font-medium">Tools</h2>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        <ul className="flex flex-col gap-0.5">
          {AUX_SURFACES.map(({ surface, label, blurb }) => {
            const count = runCounts[surface] ?? 0
            return (
              <li key={surface}>
                <button
                  type="button"
                  onClick={() => onSelect(surface)}
                  aria-current={surface === active ? "true" : undefined}
                  className={`flex w-full flex-col gap-0.5 rounded-[var(--radius)] px-2 py-2 text-left text-sm transition-colors hover:bg-[hsl(var(--muted))] ${
                    surface === active
                      ? "bg-[hsl(var(--muted))] font-semibold shadow-[inset_3px_0_0_0_hsl(var(--primary))]"
                      : ""
                  }`}
                >
                  <span className="flex items-baseline gap-2">
                    <span className="min-w-0 flex-1 truncate">{label}</span>
                    {/* Drawn only once there is one. A column of zeroes is a
                        count nobody asked for. */}
                    {count > 0 && (
                      <span className="shrink-0 font-normal tabular-nums text-[hsl(var(--legend))]">
                        {count}
                      </span>
                    )}
                  </span>
                  <span className="truncate font-normal text-[hsl(var(--muted-foreground))]">
                    {blurb}
                  </span>
                </button>
              </li>
            )
          })}
        </ul>
      </div>
    </Card>
  )
}
