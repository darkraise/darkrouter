import "../../styles/ladder.css"

/**
 * The routing ladder, defined once.
 *
 * Fragment 01 of the approved mockups is the contract, and screens 04, 09, 10
 * and 17 embed it identically. This component is that markup; if ladder markup
 * is being written a second time somewhere else, that is the bug.
 *
 * Three modes, separated by one thing only — fill versus outline:
 *
 * - `retrospective` draws attempts that happened, so its marks are filled.
 * - `predictive` and `compressed` draw what the router *would* do, so their
 *   marks are hollow. Nothing has been sent.
 *
 * A filled mark outside a trace is a bug, which is why the mark union is
 * narrowed by mode rather than left open and policed by review.
 */
export type LadderMode = "retrospective" | "predictive" | "compressed"

/** Filled. Only a trace may use these: each reports something that happened. */
export type RetrospectiveMark = "served" | "failed" | "terminated"

/** Hollow. A candidate the router considered, and did not send to. */
export type PredictiveMark = "skipped" | "cooling"

export type LadderRow<M extends string> = {
  /** 1-based position in the chain. */
  rank: number
  mark: M
  target: string
  /** Short machine-ish token: an HTTP code, `timeout`, a skip reason. */
  reasonCode?: string
  reasonProse?: string
  /** Attempt duration in pixels, already scaled by the caller. */
  latencyPx?: number
  /** A major tick marks a chain boundary rather than a step within one. */
  major?: boolean
  /** Dimmed: the chain stopped before reaching this candidate. */
  terminated?: boolean
}

type Props<M extends string> = {
  mode: LadderMode
  rows: LadderRow<M>[]
  /** Smaller marks, for the compressed ladder embedded in a catalog row. */
  catalog?: boolean
}

export function Ladder({
  mode,
  rows,
  catalog,
}: Props<RetrospectiveMark> | Props<PredictiveMark>) {
  return (
    <div className="ladder">
      {rows.map((row) => {
        const served = mode === "retrospective" && row.mark === "served"
        return (
          <div
            key={row.rank}
            className={served ? "ladder-row ladder-row-served" : "ladder-row"}
          >
            <span
              className={[
                "rank",
                "rank-tick",
                row.major ? "rank-tick-major" : "",
                row.terminated ? "text-terminated" : "",
              ]
                .filter(Boolean)
                .join(" ")}
            >
              {String(row.rank).padStart(2, "0")}
            </span>
            <span
              className={row.terminated ? "spine spine-terminated" : "spine"}
            />
            <span
              className={["mark", `mark-${row.mark}`, catalog ? "mark-catalog" : ""]
                .filter(Boolean)
                .join(" ")}
            />
            <span className={served ? "stub" : "stub stub-dashed"} />
            <span className={row.terminated ? "target target-muted" : "target"}>
              {row.target}
            </span>
            {(row.reasonCode || row.reasonProse) && (
              <span className="reason">
                {row.reasonCode && (
                  <span className="reason-code">{row.reasonCode}</span>
                )}
                {row.reasonProse && (
                  <span className="reason-prose">{row.reasonProse}</span>
                )}
              </span>
            )}
            {row.latencyPx !== undefined && (
              <span
                className={
                  row.mark === "failed"
                    ? "latency-bar latency-bar-failed"
                    : "latency-bar"
                }
                style={{ width: `${row.latencyPx}px` }}
              />
            )}
          </div>
        )
      })}
    </div>
  )
}
