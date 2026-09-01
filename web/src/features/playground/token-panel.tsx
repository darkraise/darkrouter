import { Card } from "darkraise-ui"
import { formatCost, type TurnRoute } from "./message"
import { duration, tokensPerSecond, type StreamMetrics } from "./metrics"

/** What a conversation has spent so far, summed from the turns that have a
 *  trace. A turn whose trace was swept by log retention contributes nothing
 *  rather than a zero, which is why the count of priced turns is shown: a
 *  total drawn from four of six turns should not read as the whole bill. */
export type Consumption = {
  tokensIn: number
  tokensOut: number
  /** Of `tokensOut`, how many were spent reasoning rather than answering.
   *  Inside that total rather than beside it — a reader who added the two
   *  would double-count the bill. */
  reasoningTokens: number
  costMicros: number
  /** Turns that contributed a token count. */
  counted: number
  /** Turns whose complete attempt chain was priced. */
  priced: number
  /** Turns with some, but not all, attempt prices. */
  partialPrices: number
  /** Assistant turns in the transcript, whether or not they contributed. */
  turns: number
}

export function consumptionOf(
  routes: Record<number, TurnRoute>,
  turns: number,
): Consumption {
  let tokensIn = 0
  let tokensOut = 0
  let reasoningTokens = 0
  let costMicros = 0
  let counted = 0
  let priced = 0
  let partialPrices = 0
  for (const route of Object.values(routes)) {
    if (route.tokensIn !== null || route.tokensOut !== null) {
      tokensIn += route.tokensIn ?? 0
      tokensOut += route.tokensOut ?? 0
      reasoningTokens += route.reasoningTokens
      counted += 1
    }
    if (route.costMicros !== null) costMicros += route.costMicros
    if (route.costCoverage === "complete") priced += 1
    if (route.costCoverage === "partial") partialPrices += 1
  }
  return {
    tokensIn, tokensOut, reasoningTokens, costMicros,
    counted, priced, partialPrices, turns,
  }
}

/** Grouped thousands, because a six-figure context is the number an operator
 *  is watching approach a limit and `128000` does not read as one. */
function count(n: number): string {
  return n.toLocaleString("en-US")
}

function Reading({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className="text-sm text-[hsl(var(--legend))]">{label}</span>
      <span className="text-sm tabular-nums">{value}</span>
    </div>
  )
}

/**
 * What the conversation has cost, and what the last turn cost.
 *
 * Two columns because they answer two different questions. The running total
 * is the one a conversation raises — a thread that has quietly grown to a
 * hundred thousand tokens of context is billing for all of it on every turn,
 * and nothing else on the screen says so. The last turn is the reading Lab's
 * metrics strip carried, kept because a slow answer is still the thing an
 * operator most wants explained.
 *
 * Every figure is the gateway's own. The token counts come from each turn's
 * trace rather than from tokenising in the browser, which would be a number
 * that looks authoritative and is not.
 */
export function TokenPanel({
  consumption,
  metrics,
}: {
  consumption: Consumption
  metrics: StreamMetrics
}) {
  const tps = tokensPerSecond(metrics)
  const partial = consumption.counted < consumption.turns
  const hasKnownPrice = consumption.priced + consumption.partialPrices > 0
  const partialCost = hasKnownPrice &&
    (consumption.priced < consumption.turns || consumption.partialPrices > 0)
  return (
    <Card className="flex shrink-0 flex-col gap-3 p-4">
      <h2 className="text-sm font-medium">Consumption</h2>

      <div className="flex flex-col gap-1">
        <span className="text-sm text-[hsl(var(--muted-foreground))]">
          This conversation
        </span>
        <Reading label="tokens in" value={count(consumption.tokensIn)} />
        <Reading label="tokens out" value={count(consumption.tokensOut)} />
        {/* Indented under the total it is part of, because it is a share of
            that total and not another one. Drawn only when there is some:
            a permanent "reasoning 0" on a conversation with a model that
            cannot reason is a row that never says anything. */}
        {consumption.reasoningTokens > 0 ? (
          <div className="pl-3">
            <Reading
              label="of which reasoning"
              value={count(consumption.reasoningTokens)}
            />
          </div>
        ) : null}
        <Reading
          label="cost"
          value={hasKnownPrice ? formatCost(consumption.costMicros) : "—"}
        />
        {partialCost ? (
          <p className="pt-1 text-sm text-[hsl(var(--legend))]">
            Cost includes only known prices; at least one answer or attempt is unpriced.
          </p>
        ) : null}
        {partial ? (
          // The honest caveat rather than a total that quietly understates.
          // A reopened conversation refetches each turn's trace, and one the
          // log has already swept can never contribute its counts again.
          <p className="pt-1 text-sm text-[hsl(var(--legend))]">
            {consumption.counted} of {consumption.turns} answers still have a trace to
            count.
          </p>
        ) : null}
      </div>

      <div className="flex flex-col gap-1 border-t pt-3">
        <span className="text-sm text-[hsl(var(--muted-foreground))]">Last turn</span>
        <Reading label="first token" value={duration(metrics.ttftMs)} />
        <Reading label="total" value={duration(metrics.totalMs)} />
        <Reading label="tokens/s" value={tps === null ? "—" : tps.toFixed(1)} />
      </div>
    </Card>
  )
}
