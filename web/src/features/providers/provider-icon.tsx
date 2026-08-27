import type { CSSProperties } from "react"
import "./provider-icon.css"
import { BRAND_MARKS } from "./brand-marks"

/** A hue from the identifier itself, so a provider keeps the same colour on
 *  every screen and across reloads. Random would be prettier and useless:
 *  the point of the tile is recognition. */
export function monogramHue(id: string): number {
  let hash = 0
  for (let i = 0; i < id.length; i++) hash = (hash * 31 + id.charCodeAt(i)) % 360
  return hash
}

/** One or two letters, skipping the parts that carry no identity. A gateway
 *  called `free-ai-api` is `FA`, not `FR`. */
export function monogramText(id: string, name?: string): string {
  const source = (name && name.trim()) || id
  const words = source
    .split(/[\s\-_./]+/)
    // Only the words every other gateway on the list also has. "free" is not
    // among them: half a dozen presets are called free-something and it is
    // the half of the name that distinguishes them.
    .filter((w) => w.length > 0 && !/^(ai|api|cloud|io|dev|app|gateway)$/i.test(w))
  const picked = words.length > 0 ? words : source.split(/[\s\-_./]+/).filter(Boolean)
  if (picked.length === 0) return "?"
  if (picked.length === 1) return picked[0]!.slice(0, 2).toUpperCase()
  return (picked[0]![0]! + picked[1]![0]!).toUpperCase()
}

/**
 * The mark for a provider or preset.
 *
 * `preset` is what carries the brand: a provider's own id is whatever the
 * operator typed when they added it, so `my-groq` and `groq-backup` both
 * resolve through the preset they were created from and both show the groq
 * mark. Without a preset — a raw provider pointing at some base URL — there
 * is no brand to claim, and the monogram says so honestly.
 */
export function ProviderIcon({
  preset,
  id,
  name,
  size = 28,
}: {
  preset?: string
  id: string
  name?: string
  size?: number
}) {
  const brand = preset ? BRAND_MARKS[preset] : undefined
  if (brand) {
    const { Mark } = brand
    return (
      <span
        aria-hidden="true"
        className={
          brand.background === null
            ? "inline-flex shrink-0 items-center justify-center rounded-[6px] border bg-[hsl(var(--muted))]"
            : "inline-flex shrink-0 items-center justify-center rounded-[6px]"
        }
        style={{
          width: size,
          height: size,
          background: brand.background ?? undefined,
          color: brand.color ?? "hsl(var(--foreground))",
        }}
      >
        <Mark size={Math.round(size * 0.62)} />
      </span>
    )
  }

  const hue = monogramHue(preset || id)
  return (
    <span
      aria-hidden="true"
      className="provider-monogram inline-flex shrink-0 items-center justify-center rounded-[6px] font-semibold"
      style={
        {
          width: size,
          height: size,
          fontSize: Math.round(size * 0.4),
          "--pi-hue": hue,
        } as CSSProperties
      }
    >
      {monogramText(id, name)}
    </span>
  )
}
