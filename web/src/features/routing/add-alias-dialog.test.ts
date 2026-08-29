import { describe, expect, it } from "vitest"
import { aliasNameProblem, plannedTargets } from "./add-alias-dialog"

describe("aliasNameProblem", () => {
  it("says nothing about a name nobody has typed yet", () => {
    // An empty form is not a mistake to report. The Create button is disabled
    // on emptiness instead.
    expect(aliasNameProblem("", ["sonnet"])).toBeNull()
    expect(aliasNameProblem("   ", ["sonnet"])).toBeNull()
  })

  it("accepts a name nothing else is using", () => {
    expect(aliasNameProblem("haiku", ["sonnet"])).toBeNull()
  })

  it("refuses a name already taken", () => {
    // The editor holds one entry per name, so creating it would silently
    // replace the chain that is there.
    expect(aliasNameProblem("sonnet", ["sonnet"])).toMatch(/already an alias called sonnet/)
  })

  it("compares the trimmed name, not the typed one", () => {
    expect(aliasNameProblem("  sonnet  ", ["sonnet"])).toMatch(/already/)
  })

  it("refuses a name with a space in it", () => {
    // The router resolves an alias by exact match against the requested model
    // name, so a name with a space could be written and never matched.
    expect(aliasNameProblem("my alias", [])).toMatch(/cannot contain spaces/)
  })
})

describe("plannedTargets", () => {
  it("keeps the order they were typed in, which is the fallback order", () => {
    expect(
      plannedTargets([
        { id: "1", value: "groq/a" },
        { id: "2", value: "nebius/b" },
      ]),
    ).toEqual(["groq/a", "nebius/b"])
  })

  it("drops a row that was added and never used", () => {
    // A blank row is not an empty target; it is a row waiting to be typed
    // into, and an empty target would be saved as one.
    expect(
      plannedTargets([
        { id: "1", value: "groq/a" },
        { id: "2", value: "  " },
      ]),
    ).toEqual(["groq/a"])
  })

  it("trims what was typed", () => {
    expect(plannedTargets([{ id: "1", value: " groq/a " }])).toEqual(["groq/a"])
  })
})
