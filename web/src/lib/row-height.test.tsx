import { render, screen, act } from "@testing-library/react"
import { useRef } from "react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { MIN_ROW_HEIGHT, useRowHeight } from "./row-height"

function Table({ rows }: { rows: number }) {
  const ref = useRef<HTMLDivElement>(null)
  const rowHeight = useRowHeight(ref)
  return (
    <div ref={ref}>
      <output>{rowHeight}</output>
      <table>
        <tbody>
          {Array.from({ length: rows }, (_, i) => (
            <tr key={i} data-testid="row">
              <td>row</td>
            </tr>
          ))}
          <tr className="dr-data-table-virtual-pad" data-testid="pad" />
        </tbody>
      </table>
    </div>
  )
}

function stubHeights(byTestId: Record<string, number>) {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
    this: HTMLElement,
  ) {
    const h = byTestId[this.dataset.testid ?? ""] ?? 0
    return { height: h, width: 0, top: 0, left: 0, bottom: h, right: 0, x: 0, y: 0, toJSON() {} }
  })
}

afterEach(() => vi.restoreAllMocks())

describe("useRowHeight", () => {
  it("reads the rendered row height back instead of trusting a constant", () => {
    stubHeights({ row: 48.6, pad: 900 })
    render(<Table rows={3} />)
    expect(screen.getByRole("status")).toHaveTextContent("49")
  })

  it("holds the floor when nothing can be measured", () => {
    stubHeights({})
    render(<Table rows={3} />)
    expect(screen.getByRole("status")).toHaveTextContent(String(MIN_ROW_HEIGHT))
  })

  it("lets the pin go when the density axis changes", async () => {
    const heights = { row: 60 }
    stubHeights(heights)
    render(<Table rows={2} />)
    expect(screen.getByRole("status")).toHaveTextContent("60")

    heights.row = 40
    await act(async () => {
      document.documentElement.setAttribute("data-density", "compact")
    })
    expect(screen.getByRole("status")).toHaveTextContent("40")
    document.documentElement.removeAttribute("data-density")
  })
})
