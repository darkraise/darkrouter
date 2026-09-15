import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react"

/** A floor, not a guess. The real row height is read back from the rows,
 *  because the density and font-size axes both move it and a windowed table
 *  is placed from whatever number it is told. Low enough that no theme sits
 *  under it, so the measurement always converges from below. */
export const MIN_ROW_HEIGHT = 32

/** The axes that change how tall a row wants to be. Their pinned height has
 *  to be let go before a smaller one can be observed. */
const ROW_HEIGHT_AXES = ["data-density", "data-font-size"]

function dataRows(el: HTMLElement): HTMLElement[] {
  // DataTable's "no results" row is several data rows tall. Measured while a
  // table waits for its data or a filter matches nothing, it would pin every
  // row that follows at its height.
  return [...el.querySelectorAll<HTMLElement>("tbody tr:not(.dr-data-table-virtual-pad)")].filter(
    (row) => !row.querySelector(".dr-data-table-empty"),
  )
}

function tallestOf(rows: HTMLElement[]): number {
  let tallest = 0
  for (const row of rows) tallest = Math.max(tallest, row.getBoundingClientRect().height)
  return tallest
}

/** The tallest row as it renders with no pin holding it up. The pin is
 *  released on the element that sets it and put back before anything paints.
 *  With it gone the window is briefly shorter, and a scroller near its end
 *  would be clamped to the shorter height, so every offset on the way up is
 *  put back too. */
function naturalTallest(el: HTMLElement, rows: HTMLElement[]): number {
  const scrolled: [Element, number][] = []
  for (let node = rows[0]?.parentElement ?? null; node; node = node.parentElement) {
    if (node.scrollTop > 0) scrolled.push([node, node.scrollTop])
  }
  const pinned = el.style.getPropertyValue("--row-h")
  el.style.setProperty("--row-h", "auto")
  const tallest = tallestOf(rows)
  if (pinned) el.style.setProperty("--row-h", pinned)
  else el.style.removeProperty("--row-h")
  for (const [node, top] of scrolled) node.scrollTop = top
  return tallest
}

/**
 * The height a windowed DataTable's rows actually render at.
 *
 * DataTable spaces its window by the row height it is given, so a figure
 * shorter than the rendered rows makes the scroll height jump as the window
 * moves and shows the wrong rows at the edges.
 *
 * `data` is what the table renders from, compared item by item. The pin comes
 * down only when it changes: the rows on screen also change as a windowed
 * table scrolls, and a pin that followed them would re-space the window under
 * the reader.
 */
export function useRowHeight(ref: RefObject<HTMLElement | null>, data: readonly unknown[] = []): number {
  const [rowHeight, setRowHeight] = useState(MIN_ROW_HEIGHT)
  const measuredFor = useRef<readonly unknown[] | null>(null)

  const measure = useCallback(
    (current?: readonly unknown[]) => {
      const el = ref.current
      if (!el) return
      const rows = dataRows(el)
      if (rows.length === 0) return
      const seen = measuredFor.current
      const changed =
        current !== undefined &&
        (seen === null || seen.length !== current.length || current.some((d, i) => !Object.is(d, seen[i])))
      // Under the pin a row reports the taller of its own height and the pin,
      // so reading the tallest back can raise the pin but never lower it.
      // That settles in a step or two for rows that grow; new data is measured
      // without the pin, so a tall row that has left lets it come back down.
      const tallest = changed ? naturalTallest(el, rows) : tallestOf(rows)
      if (changed) measuredFor.current = current
      if (tallest > 0) {
        const next = Math.max(Math.ceil(tallest), MIN_ROW_HEIGHT)
        setRowHeight((prev) => (prev !== next ? next : prev))
      }
    },
    [ref],
  )

  // After every render: rows arrive, change content and re-flow without
  // anything resizing, so no observer would fire for them.
  useLayoutEffect(() => measure(data))

  useEffect(() => {
    // Changing density or font size has to let the pin go before it can be
    // re-measured. The pinned height is a floor, so while it stands no row
    // can report wanting less than it, and a list that had been at spacious
    // would keep those rows for ever after a switch to compact. Dropping back
    // to the floor lets the measurement climb to the new height from below.
    const axes = new MutationObserver(() => setRowHeight(MIN_ROW_HEIGHT))
    axes.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ROW_HEIGHT_AXES,
    })
    // Inter arrives after first paint and re-flows the rows. Nothing
    // re-renders for it, so the measurement has to be asked for again.
    let cancelled = false
    void document.fonts?.ready.then(() => {
      if (!cancelled) measure()
    })
    return () => {
      cancelled = true
      axes.disconnect()
    }
  }, [measure])

  return rowHeight
}
