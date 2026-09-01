import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { TokenPanel, type Consumption } from "./token-panel"
import { NO_METRICS } from "./metrics"

const consumption = (over: Partial<Consumption> = {}): Consumption => ({
  tokensIn: 1,
  tokensOut: 2,
  reasoningTokens: 0,
  costMicros: 0,
  counted: 1,
  priced: 1,
  partialPrices: 0,
  turns: 1,
  ...over,
})

describe("conversation price coverage", () => {
  it("renders a known zero cost as zero", () => {
    render(<TokenPanel consumption={consumption()} metrics={NO_METRICS} />)
    expect(screen.getByText("$0.0000")).toBeInTheDocument()
  })

  it("states when the displayed cost contains only known portions", () => {
    render(
      <TokenPanel
        consumption={consumption({ costMicros: 125, priced: 0, partialPrices: 1 })}
        metrics={NO_METRICS}
      />,
    )
    expect(screen.getByText(/cost includes only known prices/i)).toBeInTheDocument()
  })

  it("shows an unknown cost when no turn has usable pricing", () => {
    render(
      <TokenPanel
        consumption={consumption({ costMicros: 0, priced: 0 })}
        metrics={NO_METRICS}
      />,
    )
    expect(screen.getByText("cost").parentElement).toHaveTextContent("—")
  })
})
