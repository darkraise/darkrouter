import {
  ArrowRight,
  CircleCheck,
  CircleMinus,
  CircleSlash,
  CircleX,
  Clock,
  Shuffle,
  TriangleAlert,
  type LucideIcon,
} from "lucide-react"

/**
 * A state, as a shape.
 *
 * Words like "success", "live" and "passthrough" are true once and repeated on
 * every row after that. A column of two hundred identical words is text the
 * eye has to read to discover it says nothing new, where a column of identical
 * marks is scanned in one pass and the odd one out is found without reading
 * anything.
 *
 * The shape carries the meaning, not the colour: `CircleCheck` and `CircleX`
 * differ before either is coloured, so a reader who cannot separate green from
 * red still sees which row failed. The word survives as the accessible name
 * and as the tooltip, so nothing is lost — it is moved out of the way.
 */
type Tone = "good" | "warning" | "serious" | "neutral"

const TONE_CLASS: Record<Tone, string> = {
  good: "text-[hsl(var(--success))]",
  warning: "text-[hsl(var(--warning))]",
  serious: "text-[hsl(var(--destructive))]",
  neutral: "text-[hsl(var(--muted-foreground))]",
}

export function StatusMark({
  icon: Icon,
  tone,
  label,
}: {
  icon: LucideIcon
  tone: Tone
  /** What the mark means, kept for the tooltip and the screen reader. */
  label: string
}) {
  return (
    <span className={`inline-flex items-center ${TONE_CLASS[tone]}`} title={label}>
      <Icon className="size-[var(--icon-size)]" aria-hidden="true" />
      <span className="sr-only">{label}</span>
    </span>
  )
}

/** Whether a request was served. */
export function RequestStatus({ status }: { status: string }) {
  return status === "success" ? (
    <StatusMark icon={CircleCheck} tone="good" label="served" />
  ) : (
    <StatusMark icon={CircleX} tone="serious" label={status} />
  )
}

/** Which renderer served it. Not an outcome, so it never wears a state tone. */
export function PathMark({ path }: { path?: string }) {
  if (!path) return <span className="text-[hsl(var(--legend))]">—</span>
  return path === "passthrough" ? (
    <StatusMark icon={ArrowRight} tone="neutral" label="passthrough — sent as it arrived" />
  ) : (
    <StatusMark icon={Shuffle} tone="neutral" label="translated between dialects" />
  )
}

/** Where a catalogue row stands with its provider. */
export function ModelState({ state }: { state: string }) {
  switch (state) {
    case "live":
      return <StatusMark icon={CircleCheck} tone="good" label="live" />
    case "stale":
      return (
        <StatusMark
          icon={Clock}
          tone="warning"
          label="stale — the provider's last probes failed, still routable"
        />
      )
    case "removed_upstream":
      return (
        <StatusMark
          icon={CircleSlash}
          tone="neutral"
          label="removed upstream — no longer listed, not routable"
        />
      )
    default:
      return <StatusMark icon={CircleMinus} tone="neutral" label={state} />
  }
}

/** A provider, by what the router can do with it. */
export function ProviderStateMark({ state }: { state: string }) {
  switch (state) {
    case "healthy":
      return <StatusMark icon={CircleCheck} tone="good" label="healthy" />
    case "degraded":
      return <StatusMark icon={TriangleAlert} tone="warning" label="degraded — a credential is cooling" />
    case "disabled":
      return <StatusMark icon={CircleSlash} tone="neutral" label="disabled — the router will not choose it" />
    default:
      return <StatusMark icon={CircleMinus} tone="neutral" label="unconfigured — no accounts yet" />
  }
}
