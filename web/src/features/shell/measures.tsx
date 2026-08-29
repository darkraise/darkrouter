/**
 * In-row marks: the numbers this console already prints, drawn as well as
 * written.
 *
 * Every mark here sits beside its own value rather than replacing it. A bar is
 * read in one sweep down a column — "which of these is the slow one" — and the
 * digits answer the question the bar cannot, which is "how slow". Dropping the
 * number would trade a precise reading for a rough one; dropping the bar
 * leaves two hundred rows nobody can scan.
 *
 * Colour follows the rule chart-scope.css already set for this app: magnitude
 * wears one accent and is separated by fill, never by hue, so no measurement
 * can be mistaken for a state. The only marks that carry state colour are the
 * ones that mean state, and each of those keeps a written label beside it.
 */

/** Where a value sits on a scale that spans orders of magnitude.
 *
 *  Log, because these quantities do: request latency runs from a hundred
 *  milliseconds to a hundred seconds, and a context window from four thousand
 *  tokens to two million. On a linear scale the whole population would sit in
 *  the first pixel and every outlier would look identical to every other.
 *
 *  The domain is fixed rather than taken from the rows on screen. A scale that
 *  rescaled itself as you filtered would redraw every surviving row without
 *  its value having changed, which is the fastest way to make a table lie. */
export function logFraction(value: number, min: number, max: number): number {
  if (!(value > 0) || !(max > min) || min <= 0) return 0
  const span = Math.log(max) - Math.log(min)
  const at = Math.log(Math.max(min, Math.min(max, value))) - Math.log(min)
  return Math.max(0, Math.min(1, at / span))
}

/** A value on a fixed log scale, with the reading beside it. */
export function ScaleBar({
  value,
  min,
  max,
  label,
  title,
  width = 44,
}: {
  value: number | null | undefined
  min: number
  max: number
  /** The written reading. The bar is the shape; this is the number. */
  label: string
  /** What the scale is, so it is stated rather than implied. */
  title?: string
  width?: number
}) {
  if (value === null || value === undefined) {
    return <span className="text-[hsl(var(--legend))]">—</span>
  }
  const fraction = logFraction(value, min, max)
  return (
    <span className="flex items-center gap-2 whitespace-nowrap" title={title}>
      <span
        className="h-1.5 shrink-0 overflow-hidden rounded-full bg-[hsl(var(--muted))]"
        style={{ width }}
        aria-hidden="true"
      >
        {/* A floor of two pixels: a real measurement that rounds to nothing
            reads as a missing one, and "fast" is not "absent". */}
        <span
          className="block h-full rounded-full bg-[hsl(var(--primary))]"
          style={{ width: `${Math.max(fraction * 100, 4)}%` }}
        />
      </span>
      <span className="tabular-nums">{label}</span>
    </span>
  )
}

/** A part of a whole, as a share of the fleet. */
export function ShareMeter({
  fraction,
  label,
  width = 56,
}: {
  fraction: number
  label: string
  width?: number
}) {
  return (
    <span className="flex items-center gap-2 whitespace-nowrap">
      <span
        className="h-1.5 shrink-0 overflow-hidden rounded-full bg-[hsl(var(--muted))]"
        style={{ width }}
        aria-hidden="true"
      >
        <span
          className="block h-full rounded-full bg-[hsl(var(--primary))]"
          style={{ width: `${Math.max(0, Math.min(1, fraction)) * 100}%` }}
        />
      </span>
      <span className="tabular-nums text-[hsl(var(--legend))]">{label}</span>
    </span>
  )
}

/**
 * A small count, drawn as one mark per unit.
 *
 * Counted rather than measured: at these sizes "three" is read by seeing three
 * things, and a bar three-fifths full is a slower way to learn the same fact.
 * Past the cap it becomes a number again, because nobody counts nine dots
 * faster than they read a nine.
 */
export function Pips({
  count,
  cap = 5,
  title,
}: {
  count: number
  cap?: number
  title?: string
}) {
  if (count > cap) {
    return (
      <span className="tabular-nums" title={title}>
        {count}
      </span>
    )
  }
  return (
    <span className="flex items-center gap-1" title={title ?? `${count}`}>
      {Array.from({ length: Math.max(count, 1) }, (_, i) => (
        <span
          key={i}
          // The first mark is the attempt every request makes; the rest are
          // the ones it needed. Two weights rather than two hues, so a busy
          // row never reads as a failing one.
          className={
            i === 0
              ? "h-1.5 w-1.5 rounded-full bg-[hsl(var(--muted-foreground))]"
              : "h-1.5 w-1.5 rounded-full bg-[hsl(var(--warning))]"
          }
          aria-hidden="true"
        />
      ))}
      <span className="sr-only">{count}</span>
    </span>
  )
}

export type AccountMix = { usable: number; cooling: number; disabled: number }

/**
 * A provider's accounts by what the router can do with them right now.
 *
 * Status colour, which this app reserves — so the written count stays beside
 * it and the strip is never the only thing saying a provider is in trouble.
 * Segments are separated by a surface gap rather than a border, so three
 * adjacent states do not merge into one bar.
 */
export function AccountStrip({ mix, label }: { mix: AccountMix; label: string }) {
  const total = mix.usable + mix.cooling + mix.disabled
  const segments = [
    { n: mix.usable, className: "bg-[hsl(var(--primary))]", name: "usable" },
    { n: mix.cooling, className: "bg-[hsl(var(--warning))]", name: "cooling" },
    { n: mix.disabled, className: "bg-[hsl(var(--muted-foreground))]", name: "disabled" },
  ].filter((s) => s.n > 0)

  return (
    <span
      className="flex items-center gap-2 whitespace-nowrap"
      title={segments.map((s) => `${s.n} ${s.name}`).join(" · ") || "no accounts"}
    >
      <span className="flex h-1.5 w-14 shrink-0 gap-px overflow-hidden rounded-full" aria-hidden="true">
        {total === 0 ? (
          <span className="block h-full w-full rounded-full bg-[hsl(var(--muted))]" />
        ) : (
          segments.map((s) => (
            <span
              key={s.name}
              className={`block h-full rounded-full ${s.className}`}
              style={{ width: `${(s.n / total) * 100}%` }}
            />
          ))
        )}
      </span>
      <span className="tabular-nums text-[hsl(var(--legend))]">{label}</span>
    </span>
  )
}

/**
 * The three capabilities, always in the same three places.
 *
 * Fixed slots rather than a row of badges that appears and disappears: a
 * column of loose badges cannot be scanned down, because "tools" sits in a
 * different place on every line. Present is filled, absent is a hairline, and
 * the letter stays in both — a filled square alone would be identity carried
 * by colour, which is exactly what a reader with a colour deficiency cannot
 * use.
 */
export function CapabilityTriad({
  tools,
  vision,
  reasoning,
}: {
  tools: boolean
  vision: boolean
  reasoning: boolean
}) {
  const slots = [
    { on: tools, letter: "T", name: "tools" },
    { on: vision, letter: "V", name: "vision" },
    { on: reasoning, letter: "R", name: "reasoning" },
  ]
  return (
    <span className="flex items-center gap-1">
      {slots.map((s) => (
        <span
          key={s.name}
          title={`${s.name}: ${s.on ? "yes" : "no"}`}
          className={
            s.on
              ? "flex h-5 w-5 items-center justify-center rounded border border-[hsl(var(--primary))] bg-[hsl(var(--primary))] font-mono text-sm leading-none text-[hsl(var(--primary-foreground))]"
              : "flex h-5 w-5 items-center justify-center rounded border border-dashed font-mono text-sm leading-none text-[hsl(var(--legend))]"
          }
        >
          {/* The letter is for the eye and the sentence is for the reader:
              without the hidden mark on the first, a screen reader announces
              "T tools yes" and the initial is noise it has to skip. */}
          <span aria-hidden="true">{s.letter}</span>
          <span className="sr-only">
            {s.name}: {s.on ? "yes" : "no"}
          </span>
        </span>
      ))}
    </span>
  )
}
