import { Badge, Card } from "darkraise-ui"
import type { Preset } from "../../lib/api-types"

/**
 * One shipped preset in the browse grid.
 *
 * The website link sits outside the selectable button rather than nested
 * inside it: a `<button>` cannot legally contain an `<a>`, and stacking two
 * separately-clickable regions instead keeps "pick this preset" and "visit
 * the site" from fighting over the same click.
 */
export function PresetCard({
  preset,
  onSelect,
}: {
  preset: Preset
  onSelect: () => void
}) {
  return (
    <Card className="flex flex-col gap-2 p-3">
      <button
        type="button"
        onClick={onSelect}
        className="flex flex-col gap-2 text-left"
      >
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium">{preset.name}</span>
          {preset.free_tier && <Badge variant="secondary">Free tier</Badge>}
        </div>
        <span className="font-mono text-xs text-[hsl(var(--legend))]">{preset.kind}</span>
        <div className="flex flex-wrap gap-1">
          {preset.surfaces.map((s) => (
            <Badge key={s} variant="outline">
              {s}
            </Badge>
          ))}
        </div>
      </button>
      {preset.website && (
        <a
          href={preset.website}
          target="_blank"
          rel="noreferrer"
          className="text-xs text-[hsl(var(--legend))] underline"
        >
          Website
        </a>
      )}
    </Card>
  )
}
