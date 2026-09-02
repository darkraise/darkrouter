import { describe, expect, it } from "vitest"
import { compact, count, dateTime, duration, money, percent, pricePerMillion, utcDay, zoneLabel } from "./format"

describe("money", () => {
  it("treats null as unpriced, not free", () => {
    expect(money(null)).toBe("—")
    expect(money(undefined)).toBe("—")
  })
  it("says free for a priced zero", () => {
    expect(money(0)).toBe("free")
  })
  it("keeps the fraction below a cent", () => {
    expect(money(4_000)).toBe("$0.0040")
    expect(money(50)).toBe("<$0.0001")
  })
  it("rounds to a cent at and above one", () => {
    expect(money(10_000)).toBe("$0.01")
    expect(money(1_234_567)).toBe("$1.23")
  })
})

describe("pricePerMillion", () => {
  it("keeps four places for catalog prices", () => {
    expect(pricePerMillion(4_000)).toBe("$0.0040")
    expect(pricePerMillion(0)).toBe("free")
    expect(pricePerMillion(null)).toBe("—")
  })
})

describe("duration", () => {
  it("switches to seconds past a thousand", () => {
    expect(duration(412)).toBe("412 ms")
    expect(duration(8_100)).toBe("8.1 s")
    expect(duration(null)).toBe("—")
  })
})

describe("counts", () => {
  it("groups and compacts", () => {
    expect(count(131072)).toBe("131,072")
    expect(compact(131072)).toBe("131.1k")
    expect(compact(2_000_000)).toBe("2M")
    expect(compact(999)).toBe("999")
  })
  it("formats percentages from fractions", () => {
    expect(percent(0.0123)).toBe("1.2%")
    expect(percent(null)).toBe("—")
  })
})

describe("dates", () => {
  it("always carries the date with the time", () => {
    const s = dateTime(Date.UTC(2026, 8, 2, 12, 3, 4))
    expect(s).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
  })
  it("labels the zone as an offset", () => {
    expect(zoneLabel(new Date())).toMatch(/^UTC([+−]\d{1,2}(:\d{2})?)?$/)
  })
  it("passes UTC days through unchanged", () => {
    expect(utcDay("2026-09-02")).toBe("2026-09-02")
  })
})
