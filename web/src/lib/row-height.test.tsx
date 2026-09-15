import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
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

describe("the row pin", () => {
  // The measured figure is only true if every row is held to it. jsdom applies
  // no stylesheet, so the rule itself is what is checked.
  const css = readFileSync(
    path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../styles/globals.css"),
    "utf8",
  ).replace(/\/\*[\s\S]*?\*\//g, "")

  it("holds every non-spacer row of a pinned table to --row-h", () => {
    const rule = css.match(
      /\.row-height-pinned \.dr-data-table-frame tbody tr:not\(\.dr-data-table-virtual-pad\)\s*\{([^}]*)\}/,
    )
    expect(rule?.[1]).toMatch(/(^|[\s;])height:\s*var\(--row-h\)/)
  })

  it("defaults the height to auto until one is measured", () => {
    const rule = css.match(/\n\.row-height-pinned\s*\{([^}]*)\}/)
    expect(rule?.[1]).toMatch(/--row-h:\s*auto/)
  })
})
