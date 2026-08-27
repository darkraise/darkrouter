import { Card } from "darkraise-ui"
import type { CapabilityCount } from "./provider-stats"

/** A sparkline over a daily series. Decorative: the number beside it is the
 *  reading, this is only its shape. Neutral rather than branded — a traffic
 *  trend is neither a provider state nor a router verdict. */
export function Sparkline({ points, className }: { points: number[]; className?: string }) {
  if (points.length < 2) return null
  const max = Math.max(...points, 1)
  const step = 100 / (points.length - 1)
  const coords = points.map((p, i) => `${i * step},${28 - (p / max) * 24}`)
  return (
    <svg
      className={className ?? "mt-2 h-7 w-full"}
      viewBox="0 0 100 28"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <polygon
        points={`${coords.join(" ")} 100,28 0,28`}
        fill="hsl(var(--muted-foreground))"
        opacity="0.08"
        stroke="none"
      />
      <polyline
        points={coords.join(" ")}
        fill="none"
        stroke="hsl(var(--muted-foreground))"
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  )
}

/** One reading in the strip: what it is, the number, and either its shape
 *  over time or a word qualifying it. */
export function Stat({
  caption,
  value,
  note,
  tone,
  children,
}: {
  caption: string
  value: string
  note?: string
  tone?: "warning" | "muted"
  children?: React.ReactNode
}) {
  return (
    <Card className="flex flex-col p-3">
      <p className="text-sm text-[hsl(var(--legend))]">{caption}</p>
      <p className="mt-0.5 text-2xl font-semibold leading-none tracking-tight tabular-nums">
        {value}
      </p>
      {note && (
        <p
          className={
            tone === "warning"
              ? "mt-1 text-sm text-[hsl(var(--warning))]"
              : "mt-1 text-sm text-[hsl(var(--legend))]"
          }
        >
          {note}
        </p>
      )}
      {children}
    </Card>
  )
}

/**
 * What share of this provider's models can do each thing.
 *
 * A bar rather than three counts: the question is "mostly, or barely", and
 * "12" answers that only once you have also found the 40 it is out of.
 */
export function CapabilityBars({ caps }: { caps: CapabilityCount }) {
  const rows: [string, number][] = [
    ["tools", caps.tools],
    ["vision", caps.vision],
    ["reasoning", caps.reasoning],
  ]
  return (
    <dl className="flex flex-col gap-2">
      {rows.map(([label, n]) => {
        const share = caps.total === 0 ? 0 : n / caps.total
        return (
          <div key={label} className="flex items-center gap-2">
            <dt className="w-20 shrink-0 text-sm text-[hsl(var(--legend))]">{label}</dt>
            <div
              className="h-1.5 flex-1 overflow-hidden rounded-full bg-[hsl(var(--muted))]"
              role="img"
              aria-label={`${n} of ${caps.total} models support ${label}`}
            >
              <div
                className="h-full rounded-full bg-[hsl(var(--info))]"
                style={{ width: `${Math.round(share * 100)}%` }}
              />
            </div>
            <dd className="w-14 shrink-0 text-right font-mono text-sm tabular-nums text-[hsl(var(--legend))]">
              {n}/{caps.total}
            </dd>
          </div>
        )
      })}
    </dl>
  )
}
