import { useEffect, useState } from "react"

/** How often a relative time is allowed to be wrong. A reading that says
 *  "just now" under a run that finished ten minutes ago has silently become
 *  false; thirty seconds keeps every "Ns ago" honest to the half-minute. */
const TICK_MS = 30_000

const listeners = new Set<() => void>()
let timer: number | null = null

/** One interval for every subscriber on the page, rather than one per row
 *  of a rail. Started with the first and cleared with the last. */
function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  if (timer === null) {
    timer = window.setInterval(() => {
      for (const l of listeners) l()
    }, TICK_MS)
  }
  return () => {
    listeners.delete(listener)
    if (listeners.size === 0 && timer !== null) {
      window.clearInterval(timer)
      timer = null
    }
  }
}

/** The current time, re-read on a shared thirty-second tick. */
export function useNow(): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => subscribe(() => setNow(Date.now())), [])
  return now
}
