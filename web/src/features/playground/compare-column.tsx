import { Link } from "@tanstack/react-router"
import { Button, Card } from "darkraise-ui"
import { X } from "lucide-react"
import { ModelCombobox } from "../shell/model-combobox"

/** Where one column is in its run. Idle is before the first Run, not an error. */
export type ColumnStatus = "idle" | "streaming" | "done" | "stopped" | "error"

export type Column = {
  id: string
  model: string
  text: string
  requestId: string
  error: string
  status: ColumnStatus
  latencyMs: number | undefined
}

export function emptyColumn(id: string): Column {
  return { id, model: "", text: "", requestId: "", error: "", status: "idle", latencyMs: undefined }
}

const DOT: Record<ColumnStatus, string> = {
  idle: "bg-[hsl(var(--legend))]",
  streaming: "bg-[hsl(var(--primary))] motion-safe:animate-pulse",
  done: "bg-[hsl(var(--primary))]",
  stopped: "bg-[hsl(var(--legend))]",
  error: "bg-[hsl(var(--destructive))]",
}

/** Named rather than drawn only as a colour: a dot alone tells a screen
 *  reader nothing, and colour alone tells a colourblind reader nothing. */
function StatusDot({ status }: { status: ColumnStatus }) {
  // role="img" rather than role="status": status is a live region, so a dot
  // per column re-announced on every idle -> streaming -> done transition and
  // four columns running at once talked over each other. The label is read
  // when the dot is reached instead. target-pill.tsx names its state the same
  // way.
  return <span role="img" aria-label={status} className={`size-2 shrink-0 rounded-full ${DOT[status]}`} />
}

export function CompareColumn({
  column,
  index,
  onModel,
  onRemove,
  candidates,
  loading,
  removable,
  disabled = false,
}: {
  column: Column
  index: number
  onModel: (model: string) => void
  onRemove: () => void
  candidates: string[]
  loading?: boolean
  removable: boolean
  disabled?: boolean
}) {
  return (
    <Card className="flex min-w-0 flex-col gap-3 p-4">
      <div className="flex items-center gap-2">
        <StatusDot status={column.status} />
        <div className="min-w-0 flex-1">
          <ModelCombobox
            label={`Column ${index + 1} model or alias`}
            value={column.model}
            onChange={onModel}
            candidates={candidates}
            loading={loading}
            disabled={disabled}
            className="w-full"
          />
        </div>
        <Button
          variant="ghost"
          aria-label={`Remove column ${index + 1}`}
          disabled={disabled || !removable}
          onClick={onRemove}
        >
          <X className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
        </Button>
      </div>

      <div className="min-h-24 rounded border p-3 font-mono text-sm whitespace-pre-wrap">
        {column.text}
      </div>
      {column.error ? <p className="text-destructive text-sm">{column.error}</p> : null}
      <div className="flex items-center gap-3 text-sm text-[hsl(var(--muted-foreground))]">
        {column.latencyMs !== undefined ? <span>{Math.round(column.latencyMs)} ms</span> : null}
        {column.requestId ? (
          <Link to="/requests/$id" params={{ id: column.requestId }} className="underline">
            View the trace for this request
          </Link>
        ) : null}
      </div>
    </Card>
  )
}
