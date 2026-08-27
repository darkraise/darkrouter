import { useHealthz } from "../../lib/queries"
import { droppedText } from "./overview-screen"

/**
 * Version, uptime and the dropped-record counter.
 *
 * The counter is the honest signal that usage figures are a lower bound, and
 * nothing else in the console surfaces it.
 */
export function OpsFooter() {
  const health = useHealthz()
  if (!health.data) return null
  const h = health.data
  const dropped = droppedText(h.log_records_dropped, h.log_records_written)
  return (
    <footer className="mt-8 border-t pt-3 font-mono text-xs text-[hsl(var(--legend))]">
      {h.version} · up {h.uptime} ·{" "}
      <span
        className={h.log_records_dropped > 0 ? "text-[hsl(var(--warning))]" : undefined}
      >
        {dropped}
      </span>
    </footer>
  )
}
