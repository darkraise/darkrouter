import { Link } from "@tanstack/react-router"
import { Card } from "darkraise-ui"
import type { PolicyBlock, Provider } from "../../lib/api-types"

/**
 * Providers in the order rule 3 walks them: highest priority first.
 *
 * Descending, because that is what the router does — `sqlsource` loads the set
 * `ORDER BY priority DESC, id` and `provider.byPriority` sorts `Priority >`. A
 * card that listed them ascending would state the failover order backwards,
 * which is worse than not stating it at all.
 *
 * The tie-break matters too: without it two providers at the same priority
 * would appear in whatever order the API happened to return.
 *
 * A provider with no enabled credential is left out because the provider set
 * drops it: `sqlsource` skips any whose `enabledOnly(creds)` is empty, so it
 * is not a step in the order at all.
 */
export function priorityOrder(providers: Provider[]): Provider[] {
  return [...providers]
    .filter((p) => p.enabled && p.credentials.some((c) => c.enabled))
    .sort((a, b) => b.priority - a.priority || a.id.localeCompare(b.id))
}

function Rule({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span
        className="mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-[hsl(var(--muted))] font-mono text-sm"
        aria-hidden="true"
      >
        {n}
      </span>
      <span className="min-w-0">
        <span className="block text-sm font-medium">{title}</span>
        <span className="block text-sm text-[hsl(var(--legend))]">{children}</span>
      </span>
    </li>
  )
}

/**
 * How the router picks, before any chain is read.
 *
 * Read-only on purpose. None of this is a setting: the resolution order is
 * fixed in the router, and the two numbers that are configurable live in
 * Settings, where changing them is one job rather than a second place that
 * can disagree with the first.
 */
export function StrategyCard({
  policy,
  providers,
}: {
  policy?: PolicyBlock
  providers: Provider[]
}) {
  const order = priorityOrder(providers)
  return (
    <Card className="mb-6 p-4">
      <h2 className="mb-1 text-sm font-medium">How it chooses</h2>
      <p className="mb-4 text-sm text-[hsl(var(--muted-foreground))]">
        Three rules, tried in order. The first one that resolves wins, and the rest are
        never consulted.
      </p>

      <ol className="flex flex-col gap-3">
        <Rule n={1} title="An alias, matched exactly">
          The chains below. Each target is expanded by rules 2 and 3 in turn — an alias
          never points at another alias.
        </Rule>
        <Rule n={2} title="provider/model">
          Pinned to one provider. Split on the first slash, and only when the prefix
          names a configured provider — a model id that merely contains a slash stays
          whole and falls through to rule 3.
        </Rule>
        <Rule n={3} title="A bare model name">
          Every enabled provider offering it, in priority order
          {order.length > 0 ? ":" : "."}
          {order.length > 0 && (
            <span className="mt-1.5 flex flex-wrap items-center gap-1">
              {order.map((p, i) => (
                <span
                  key={p.id}
                  className="rounded-full border px-2 py-0.5 font-mono text-sm text-[hsl(var(--foreground))]"
                >
                  {i + 1}. {p.id}
                </span>
              ))}
            </span>
          )}
        </Rule>
      </ol>

      <div className="mt-4 flex flex-wrap gap-x-6 gap-y-1 border-t pt-3 text-sm text-[hsl(var(--legend))]">
        <span>
          Within a provider, the least recently used credential goes first.
        </span>
        {policy && (
          <>
            <span>
              Up to <span className="font-mono">{policy.retry.max_attempts}</span>{" "}
              {policy.retry.max_attempts === 1 ? "attempt" : "attempts"} per request.
            </span>
            <span>
              Cooldown after{" "}
              <span className="font-mono">{policy.cooldown.trip_after ?? "—"}</span>{" "}
              consecutive failures, up to{" "}
              <span className="font-mono">{policy.cooldown.max}</span>.
            </span>
          </>
        )}
        <Link to="/settings" className="ml-auto hover:underline">
          Change in Settings →
        </Link>
      </div>
    </Card>
  )
}
