import { Button, Card } from "darkraise-ui"

// Step one is deliberately not the literal button caption below it: the two
// would otherwise read as the same string twice, which is confusing on the
// page and ambiguous to anything that queries by text.
const STEPS = [
  "Add your first provider",
  "Let discovery find its models",
  "Point a client at Connect",
]

/**
 * What the Overview shows instead of four empty tiles and a blank graph.
 *
 * An operator with no providers is logged in and can reach every screen —
 * this teaches the three steps rather than taking the console away at the
 * moment it would help most.
 */
export function FirstRunProviders({ onAdd }: { onAdd: () => void }) {
  return (
    <Card className="p-6">
      <h2 className="text-sm font-medium">Nothing is configured yet</h2>
      <p className="mt-1 max-w-prose text-sm text-[hsl(var(--muted-foreground))]">
        The router has nothing to route to. Three steps get it serving.
      </p>

      <ol className="mt-4 flex flex-col gap-2 text-sm">
        {STEPS.map((step, i) => (
          <li key={step} className="flex items-center gap-2">
            <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-[hsl(var(--muted))] font-mono text-xs">
              {i + 1}
            </span>
            {step}
          </li>
        ))}
      </ol>

      {/* Dimmed rather than absent: the shape teaches what the routing graph
          will draw once traffic exists, without claiming any traffic does. */}
      <div className="mt-6 opacity-30" aria-hidden="true">
        <div className="flex items-center gap-4">
          <div className="h-8 w-24 rounded border border-dashed" />
          <div className="h-px flex-1 border-t border-dashed" />
          <div className="h-8 w-8 rounded-full border border-dashed" />
          <div className="h-px flex-1 border-t border-dashed" />
          <div className="h-8 w-24 rounded border border-dashed" />
        </div>
      </div>

      <Button className="mt-6" onClick={onAdd}>
        Add a provider
      </Button>
    </Card>
  )
}
