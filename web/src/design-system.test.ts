import { describe, it, expect } from "vitest"
import { readdirSync, readFileSync } from "node:fs"
import { join } from "node:path"

/** Every source file under src/, tests excluded — a test asserting on its own
 *  fixtures is not a violation. */
function sources(dir: string): string[] {
  const out: string[] = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      if (entry.name === "node_modules") continue
      out.push(...sources(path))
      continue
    }
    if (!/\.tsx?$/.test(entry.name)) continue
    if (/\.test\.tsx?$/.test(entry.name)) continue
    out.push(path)
  }
  return out
}

const FILES = sources(__dirname).map((path) => ({
  path: path.slice(__dirname.length + 1),
  text: readFileSync(path, "utf8"),
}))

/**
 * Two rules the console kept breaking one call site at a time.
 *
 * Both are the kind a reviewer catches on a good day and misses on a busy one,
 * and both were found by an audit rather than by anything that runs. Stated
 * here so the next occurrence fails before it ships rather than being found by
 * the audit after.
 */
describe("the design system's own rules", () => {
  it("reaches for PasswordInput rather than a masked Input", () => {
    // A masked field with no way to check what is in it makes a mistyped
    // secret indistinguishable from a wrong one. Every one of these six sites
    // had that problem; PasswordInput carries the reveal toggle that fixes it.
    const offenders = FILES.filter((f) => /<Input[^>]*type="password"/s.test(f.text))
    expect(offenders.map((f) => f.path)).toEqual([])
  })

  it("keeps the 14px floor CLAUDE.md sets", () => {
    // The rule's own words: "Never text-xs. 14px (text-sm) is the floor."
    // Three darkraise-ui components default below it, which globals.css
    // repairs centrally -- this catches the case where a call site writes a
    // small size itself, which no override can reach.
    const offenders = FILES.filter((f) =>
      /\btext-xs\b|text-\[\d+px\]|text-\[length:/.test(f.text),
    )
    expect(offenders.map((f) => f.path)).toEqual([])
  })

  it("never hands a chart a numeric font size", () => {
    // recharts takes `fontSize={11}` and writes it as an attribute the
    // font-size axis cannot reach. Ticks take their size from a token in the
    // stylesheet instead, which the library then measures.
    const offenders = FILES.filter((f) =>
      /fontSize=\{\s*\d|fontSize:\s*\d/.test(f.text),
    )
    expect(offenders.map((f) => f.path)).toEqual([])
  })
})
