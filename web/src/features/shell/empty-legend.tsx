/**
 * What an empty well says.
 *
 * A blank panel with a faint grid is indistinguishable from broken equipment,
 * so every empty state names what will appear here and what produces it.
 */
export function EmptyLegend({ what, hint }: { what: string; hint: string }) {
  return (
    <div className="mt-4 rounded border border-dashed p-6 text-center">
      <p className="text-sm">{what}</p>
      <p className="mt-1 text-sm text-[hsl(var(--muted-foreground))]">{hint}</p>
    </div>
  )
}
