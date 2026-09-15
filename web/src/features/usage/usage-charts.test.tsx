import { render } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { CostLineChart, StackedAreaChart } from "./usage-charts"

// Series names are model and provider ids from upstream. `gpt-4.1` and
// `meta-llama/llama-3` are not valid custom-property names, and a hostile id
// could close the rule and write its own.
const keys = ["gpt-4.1", "meta-llama/llama-3:free", "x: red; } body { display: none; } .y {"]
const data = [{ day: "2026-09-01", ...Object.fromEntries(keys.map((k, i) => [k, i + 1])) }]

function chartStyles(container: HTMLElement): string {
  return [...container.querySelectorAll("style")].map((s) => s.innerHTML).join("\n")
}

describe.each([
  ["the stacked area chart", () => <StackedAreaChart data={data} keys={keys} legend />],
  [
    "the cost line chart",
    () => <CostLineChart data={data} keys={keys} formatValue={String} legend />,
  ],
])("%s", (_, chart) => {
  it("declares one valid colour variable per series and nothing else", () => {
    const { container } = render(chart())
    const css = chartStyles(container)
    const declarations = css.match(/--color-[^:]*:/g) ?? []
    // Two themes, one declaration per series in each.
    expect(declarations).toHaveLength(keys.length * 2)
    for (const d of declarations) expect(d).toMatch(/^--color-[A-Za-z0-9_-]+:$/)
    expect(css).not.toContain("display: none")
  })
})
