import { describe, expect, it } from "vitest"
import { consumptionOf } from "./token-panel"
import type { TurnRoute } from "./message"

function route(over: Partial<TurnRoute> = {}): TurnRoute {
  return {
    requestId: "01A",
    provider: "groq",
    model: "m",
    totalMs: 1000,
    tokensIn: 100,
    tokensOut: 50,
    reasoningTokens: 0,
    costMicros: 200,
    failedOver: [],
    warnings: [],
    ...over,
  }
}

describe("what a conversation has spent", () => {
  it("sums every turn rather than reporting only the last", () => {
    // A thread bills for its whole context on every turn, and the per-turn
    // route line cannot say what that has added up to.
    const total = consumptionOf({ 1: route(), 3: route() }, 2)
    expect(total.tokensIn).toBe(200)
    expect(total.tokensOut).toBe(100)
    expect(total.costMicros).toBe(400)
    expect(total.counted).toBe(2)
  })

  it("counts a turn whose trace carried no price as spent but unpriced", () => {
    // A model with no pricing row still consumed tokens. Dropping the turn
    // would understate the context; inventing a cost would be worse.
    const total = consumptionOf({ 1: route({ costMicros: null }) }, 1)
    expect(total.tokensIn).toBe(100)
    expect(total.costMicros).toBe(0)
    expect(total.counted).toBe(1)
  })

  it("skips a turn with no counts at all, and says how many it skipped", () => {
    // A trace swept by log retention can never contribute its counts again.
    // A total drawn from one of two turns must not read as the whole bill.
    const total = consumptionOf(
      { 1: route(), 3: route({ tokensIn: null, tokensOut: null }) },
      2,
    )
    expect(total.tokensIn).toBe(100)
    expect(total.counted).toBe(1)
    expect(total.turns).toBe(2)
  })

  it("totals reasoning tokens as a share of the output, not beside it", () => {
    // They are billed inside tokens_out. A panel that added them would
    // double-count, and a panel that omitted them would hide the line item
    // that is usually the largest.
    const total = consumptionOf(
      { 1: route({ tokensOut: 600, reasoningTokens: 500 }) },
      1,
    )
    expect(total.tokensOut).toBe(600)
    expect(total.reasoningTokens).toBe(500)
  })

  it("is zero for a conversation that has sent nothing", () => {
    expect(consumptionOf({}, 0)).toEqual({
      tokensIn: 0,
      tokensOut: 0,
      reasoningTokens: 0,
      costMicros: 0,
      counted: 0,
      turns: 0,
    })
  })
})
