import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { useNow } from "./use-now"

describe("useNow", () => {
  beforeEach(() => vi.useFakeTimers({ now: 1_000_000 }))
  afterEach(() => vi.useRealTimers())

  it("ticks every thirty seconds so relative times do not freeze", () => {
    // "just now" under a run that finished ten minutes ago is a reading that
    // has silently become false.
    const { result } = renderHook(() => useNow())
    expect(result.current).toBe(1_000_000)
    act(() => vi.advanceTimersByTime(29_000))
    expect(result.current).toBe(1_000_000)
    act(() => vi.advanceTimersByTime(1_000))
    expect(result.current).toBe(1_030_000)
  })

  it("shares one interval across every subscriber", () => {
    const spy = vi.spyOn(globalThis, "setInterval")
    const a = renderHook(() => useNow())
    const b = renderHook(() => useNow())
    expect(spy).toHaveBeenCalledTimes(1)
    a.unmount()
    b.unmount()
    spy.mockRestore()
  })
})
