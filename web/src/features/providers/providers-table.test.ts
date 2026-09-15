import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { describe, expect, it } from "vitest"

describe("where the actions column stops being pinned", () => {
  const css = readFileSync(
    path.resolve(path.dirname(fileURLToPath(import.meta.url)), "providers-table.css"),
    "utf8",
  ).replace(/\/\*[\s\S]*?\*\//g, "")

  it("unpins below the width the name and actions need at extra-large text", () => {
    // Measured in the running console on the shipped catalogue: the name and
    // actions columns take 587px at medium, 653px at large and 706px at
    // extra-large. The font-size axis rebinds --text-* but not the root size,
    // so a rem threshold does not grow with it and has to cover the largest
    // step. jsdom has no layout, so the threshold is read from the rule.
    const match = css.match(/@container\s*\(max-width:\s*([\d.]+)rem\)/)
    expect(match).not.toBeNull()
    expect(Number(match![1]) * 16).toBeGreaterThanOrEqual(706)
  })
})
