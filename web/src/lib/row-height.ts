import { useCallback, useEffect, useLayoutEffect, useState, type RefObject } from "react"

/** A floor, not a guess. The real row height is read back from the rows,
 *  because the density and font-size axes both move it and a windowed table
 *  is placed from whatever number it is told. Low enough that no theme sits
 *  under it, so the measurement always converges from below. */
export const MIN_ROW_HEIGHT = 32

/** The axes that change how tall a row wants to be. Their pinned height has
 *  to be let go before a smaller one can be observed. */
const ROW_HEIGHT_AXES = ["data-density", "data-font-size"]

/**
 * The height a windowed DataTable's rows actually render at.
 *
 * DataTable spaces its window by the row height it is given, so a figure
 * shorter than the rendered rows makes the scroll height jump as the window
 * moves and shows the wrong rows at the edges.
 */
export function useRowHeight(ref: RefObject<HTMLElement | null>): number {
  const [rowHeight, setRowHeight] = useState(MIN_ROW_HEIGHT)

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    // The pinned height is a floor, so a row whose content does not fit
    // reports the taller figure it actually took. Reading the tallest one back
    // and pinning to that settles in a step or two and lands on the natural
    // row height for whatever density and font size are in force.
    const rows = el.querySelectorAll<HTMLElement>("tbody tr:not(.dr-data-table-virtual-pad)")
    let tallest = 0
    for (const row of rows) {
      // DataTable's "no results" row is several data rows tall. Measured while
      // a table waits for its data or a filter matches nothing, it would pin
      // every row that follows at its height, and a floor cannot come down.
      if (row.querySelector(".dr-data-table-empty")) continue
      tallest = Math.max(tallest, row.getBoundingClientRect().height)
    }
    if (tallest > 0) {
      const next = Math.max(Math.ceil(tallest), MIN_ROW_HEIGHT)
      setRowHeight((prev) => (prev !== next ? next : prev))
    }
  }, [ref])

  // After every render: rows arrive, change content and re-flow without
  // anything resizing, so no observer would fire for them.
  useLayoutEffect(measure)

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
