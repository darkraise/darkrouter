import { Link } from "@tanstack/react-router"
import { Badge } from "darkraise-ui"
import { JsonTreeView } from "darkraise-ui/components/json-tree-view"
import { relativeTime } from "../../../lib/time"
import { vectorPreview, type AuxOutcome, type AuxRun } from "./surfaces"

/** How many components of a vector the strip draws. Enough to show that a
 *  vector has structure, few enough that each bar is still a bar. */
const STRIP_COMPONENTS = 48

/**
 * A vector, as a shape rather than as a list of numbers.
 *
 * The number an operator is checking is the length — whether the model
 * returned 1536 components or the 256 they asked for — and that is stated in
 * words. The strip beside it exists so two embeddings of different text look
 * different, which a row of `0.031, -0.014, …` does not.
 */
function VectorStrip({ vector }: { vector: number[] }) {
  const head = vector.slice(0, STRIP_COMPONENTS)
  // Scaled to the largest component in view rather than to a fixed range:
  // embedding scales differ by model, and a fixed range renders most of them
  // as a flat line.
  const peak = Math.max(...head.map((v) => Math.abs(v)), Number.EPSILON)
  return (
    <div className="flex flex-col gap-2">
      <p className="font-mono text-sm text-[hsl(var(--legend))]">
        {vectorPreview(vector, 4)}
      </p>
      {/* Signed: the centre line is zero, and a component below it points
          down. An unsigned strip would draw two very different vectors the
          same way. */}
      <div
        className="flex h-12 items-center gap-px"
        role="img"
        aria-label={`The first ${head.length} of ${vector.length} components`}
      >
        {head.map((v, i) => (
          <div key={i} className="flex h-full flex-1 flex-col justify-center">
            <div className="flex h-1/2 items-end">
              <div
                className="w-full bg-[hsl(var(--primary))]"
                style={{ height: v > 0 ? `${(v / peak) * 100}%` : 0 }}
              />
            </div>
            <div className="flex h-1/2 items-start">
              <div
                className="w-full bg-[hsl(var(--legend))]"
                style={{ height: v < 0 ? `${(-v / peak) * 100}%` : 0 }}
              />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

/** A score, drawn beside its own value. The bar is the comparison between
 *  rows; the number is the fact. */
function ScoreRow({
  rank,
  label,
  score,
  emphasis = false,
}: {
  rank?: number
  label: string
  score: number
  emphasis?: boolean
}) {
  const width = `${Math.max(0, Math.min(1, score)) * 100}%`
  return (
    <div className="flex items-center gap-3">
      {rank !== undefined && (
        <span className="w-5 shrink-0 text-right font-mono text-sm text-[hsl(var(--legend))]">
          {rank}
        </span>
      )}
      <span className="min-w-0 flex-1 truncate text-sm" title={label}>
        {label}
      </span>
      <span className="h-1.5 w-24 shrink-0 rounded-full bg-[hsl(var(--muted))]" aria-hidden="true">
        <span
          className={
            emphasis
              ? "block h-1.5 rounded-full bg-[hsl(var(--destructive))]"
              : "block h-1.5 rounded-full bg-[hsl(var(--primary))]"
          }
          style={{ width }}
        />
      </span>
      <span className="w-14 shrink-0 text-right font-mono text-sm tabular-nums">
        {score.toFixed(3)}
      </span>
    </div>
  )
}

function Outcome({ outcome }: { outcome: AuxOutcome }) {
  if (outcome.kind === "embedding") return <VectorStrip vector={outcome.vector} />

  if (outcome.kind === "rerank") {
    return (
      <div className="flex flex-col gap-1.5">
        {outcome.ranked.map((r, i) => (
          <ScoreRow key={`${r.index}-${i}`} rank={i + 1} label={r.text} score={r.score} />
        ))}
      </div>
    )
  }

  if (outcome.kind === "moderation") {
    // Only the categories that scored. A moderation response carries every
    // category the model knows, and a list of twenty zeroes buries the one
    // row that says something.
    const scored = outcome.scores.filter((s) => s.flagged || s.score >= 0.001)
    return (
      <div className="flex flex-col gap-3">
        <Badge
          variant={outcome.flagged ? "destructive" : "green"}
          size="lg"
          className="w-fit"
        >
          {outcome.flagged ? "Flagged" : "Clean"}
        </Badge>
        {scored.length === 0 ? (
          <p className="text-sm text-[hsl(var(--legend))]">
            No category scored above a thousandth.
          </p>
        ) : (
          <div className="flex flex-col gap-1.5">
            {scored.map((s) => (
              <ScoreRow key={s.name} label={s.name} score={s.score} emphasis={s.flagged} />
            ))}
          </div>
        )}
      </div>
    )
  }

  if (outcome.kind === "image") {
    return (
      <div className="flex flex-wrap gap-2">
        {outcome.urls.map((url, i) => (
          <img key={i} src={url} alt="" className="max-h-64 rounded-[var(--radius)] border" />
        ))}
      </div>
    )
  }

  if (outcome.kind === "audio") {
    return (
      <div className="flex flex-col gap-2">
        <audio controls src={outcome.url} className="w-full" />
        <p className="text-sm text-[hsl(var(--legend))]">
          {(outcome.bytes / 1024).toFixed(1)} KB
        </p>
      </div>
    )
  }

  if (outcome.kind === "transcript") {
    // Prose, because that is what it is. It arrived as a JSON object with one
    // string in it, and reading a paragraph out of a tree is work nobody
    // should be doing.
    return <p className="max-w-prose text-sm whitespace-pre-wrap">{outcome.text}</p>
  }

  return <JsonTreeView data={outcome.json} toolbar copyable defaultExpandLevel={2} />
}

/**
 * One run, headed by what was asked and when.
 *
 * The heading is what makes a scrolled-back result legible: two embeddings in
 * a column are indistinguishable without the text that produced each.
 */
export function RunCard({ run }: { run: AuxRun }) {
  return (
    <article className="flex flex-col gap-3 border-b pb-6 last:border-b-0 last:pb-0">
      <header className="flex items-baseline gap-3">
        <h3 className="min-w-0 flex-1 truncate text-sm font-medium" title={run.summary}>
          {run.summary}
        </h3>
        <span className="shrink-0 text-sm text-[hsl(var(--legend))]">
          {relativeTime(run.at)}
        </span>
      </header>

      <Outcome outcome={run.outcome} />

      {run.requestId !== "" && (
        <Link
          to="/requests/$id"
          params={{ id: run.requestId }}
          className="w-fit text-sm text-[hsl(var(--legend))] underline-offset-2 hover:underline"
        >
          View the trace for this run
        </Link>
      )}
    </article>
  )
}
