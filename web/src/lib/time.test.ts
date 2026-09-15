import { describe, expect, it } from "vitest"
import { relativeTime, utcDayStartMs, utcDays } from "./time"

describe("a usage window's days", () => {
  it("lists every UTC day from first to last, across a month end", () => {
    expect(utcDays("2026-08-30", "2026-09-02")).toEqual([
      "2026-08-30",
      "2026-08-31",
      "2026-09-01",
      "2026-09-02",
    ])
  })

  it("is empty for a window that ends before it starts or cannot be read", () => {
    expect(utcDays("2026-09-02", "2026-09-01")).toEqual([])
    expect(utcDays("", "2026-09-01")).toEqual([])
  })

  it("starts a day at UTC midnight", () => {
    expect(utcDayStartMs("2026-08-20")).toBe(Date.UTC(2026, 7, 20))
  })
})

describe("how long ago something was", () => {
  const now = new Date("2026-08-28T12:00:00Z").getTime()

  it("reads as relative while that is the useful reading", () => {
    expect(relativeTime(now - 2_000, now)).toBe("just now")
    expect(relativeTime(now - 30_000, now)).toBe("30s ago")
    expect(relativeTime(now - 5 * 60_000, now)).toBe("5m ago")
    expect(relativeTime(now - 3 * 3_600_000, now)).toBe("3h ago")
  })

  it("falls back to a date once relative stops carrying", () => {
    // "48h ago" is a number nobody converts; the date is what they wanted.
    expect(relativeTime(now - 2 * 86_400_000, now)).toMatch(/\d/)
    expect(relativeTime(now - 2 * 86_400_000, now)).not.toMatch(/ago/)
  })
})
