import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { render, screen, act } from "@testing-library/react"
import { useRef, type CSSProperties } from "react"
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

function EmptyTable() {
  const ref = useRef<HTMLDivElement>(null)
  const rowHeight = useRowHeight(ref)
  return (
    <div ref={ref}>
      <output>{rowHeight}</output>
      <table>
        <tbody>
          <tr data-testid="empty">
            <td>
              <div className="dr-data-table-empty">No results</div>
            </td>
          </tr>
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

/** A table pinned the way the screens pin theirs: every row held to at least
 *  the measured height through --row-h on the container. */
function PinnedTable({ rows, tick = 0 }: { rows: string[]; tick?: number }) {
  const ref = useRef<HTMLDivElement>(null)
  const rowHeight = useRowHeight(ref, [rows])
  return (
    <div ref={ref} data-tick={tick} style={{ "--row-h": `${rowHeight}px` } as CSSProperties}>
      <output>{rowHeight}</output>
      <table>
        <tbody>
          {rows.map((kind, i) => (
            <tr key={i} data-testid={kind}>
              <td>{kind}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

/** Natural heights by test id, held to the pin in force as CSS would hold
 *  them: a row is never shorter than --row-h, only taller. */
function stubPinnedHeights(natural: Record<string, number>) {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (
    this: HTMLElement,
  ) {
    const own = natural[this.dataset.testid ?? ""] ?? 0
    const pin = parseFloat(
      this.closest<HTMLElement>("[style]")?.style.getPropertyValue("--row-h") ?? "",
    )
    const h = Number.isNaN(pin) ? own : Math.max(own, pin)
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

  it("does not measure the empty-state row", () => {
    // It is far taller than a data row, and the pin is a floor: measured once
    // before the data arrived, it held every row after it at its height.
    stubHeights({ empty: 186 })
    render(
      <EmptyTable />,
    )
    expect(screen.getByRole("status")).toHaveTextContent(String(MIN_ROW_HEIGHT))
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

  it("comes back down when the tall row leaves the data", () => {
    // The pin is a floor, so measured under it every remaining row reports
    // the tall row's height and nothing short of an axis change let it go.
    stubPinnedHeights({ tall: 80, short: 40 })
    const { rerender } = render(<PinnedTable rows={["tall", "short"]} />)
    expect(screen.getByRole("status")).toHaveTextContent("80")

    rerender(<PinnedTable rows={["short", "short"]} />)
    expect(screen.getByRole("status")).toHaveTextContent("40")
  })

  it("holds the pin while the data is unchanged", () => {
    // A windowed table swaps rows as it scrolls without its data changing. A
    // pin that followed the rows on screen would re-space the window under
    // the reader's scroll.
    const natural = { tall: 80, short: 40 }
    stubPinnedHeights(natural)
    const rows = ["tall", "short"]
    const { rerender } = render(<PinnedTable rows={rows} />)
    expect(screen.getByRole("status")).toHaveTextContent("80")

    natural.tall = 40
    rerender(<PinnedTable rows={rows} tick={1} />)
    expect(screen.getByRole("status")).toHaveTextContent("80")
  })

  it("puts the pin back after measuring without it", () => {
    // New data that measures the same height re-renders nothing, so React
    // never rewrites the style and a released pin would stay released.
    stubPinnedHeights({ tall: 80, short: 40 })
    const { container, rerender } = render(<PinnedTable rows={["tall", "short"]} />)
    rerender(<PinnedTable rows={["tall"]} />)
    expect(screen.getByRole("status")).toHaveTextContent("80")
    const pinned = container.querySelector<HTMLElement>("[style]")
    expect(pinned?.style.getPropertyValue("--row-h")).toBe("80px")
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
