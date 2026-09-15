import { readFileSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"
import { render } from "@testing-library/react"
import { describe, it, expect } from "vitest"
import { Ladder } from "./ladder"

describe("the routing ladder", () => {
  it("emits fragment 01's markup", () => {
    // The mockups are the contract. A row is a rank, a spine, a mark, a stub,
    // a target, a reason and a latency bar, in that order.
    const { container } = render(
      <Ladder
        mode="retrospective"
        rows={[
          {
            rank: 3,
            mark: "served",
            target: "together/gpt-oss-120b",
            reasonCode: "200",
            reasonProse: "1,847ms attempt",
            latencyPx: 4,
          },
        ]}
      />,
    )
    const row = container.querySelector(".ladder-row")
    expect(row).toHaveClass("ladder-row-served")
    expect(row?.querySelector(".rank")?.textContent).toBe("03")
    expect(row?.querySelector(".mark")).toHaveClass("mark-served")
    expect(row?.querySelector(".target")?.textContent).toBe(
      "together/gpt-oss-120b",
    )
    expect(row?.querySelector(".reason-code")?.textContent).toBe("200")
    expect(
      [...(row?.children ?? [])].map((c) => c.className.split(" ")[0]),
    ).toEqual(["rank", "spine", "mark", "stub", "target", "reason", "latency-bar"])
  })

  it("fills a served mark only in a trace", () => {
    // Fill versus outline is the only thing separating the three modes, so a
    // predictive ladder never carries a filled mark. The type narrows it; this
    // pins the rendered result.
    const { container } = render(
      <Ladder
        mode="predictive"
        rows={[
          { rank: 1, mark: "skipped", target: "groq/a" },
          { rank: 2, mark: "cooling", target: "cerebras/a" },
        ]}
      />,
    )
    const marks = [...container.querySelectorAll(".mark")].map((m) => m.className)
    expect(marks.join(" ")).not.toContain("mark-served")
    expect(marks.join(" ")).not.toContain("mark-failed")
    expect(marks.join(" ")).not.toContain("mark-terminated")
  })

  it("marks a terminated row without colouring it", () => {
    const { container } = render(
      <Ladder
        mode="retrospective"
        rows={[{ rank: 4, mark: "failed", target: "x/y", terminated: true }]}
      />,
    )
    expect(container.querySelector(".rank")).toHaveClass("text-terminated")
    expect(container.querySelector(".spine")).toHaveClass("spine-terminated")
    expect(container.querySelector(".target")).toHaveClass("target-muted")
  })

  it("shrinks its marks for a catalog row", () => {
    const { container } = render(
      <Ladder
        mode="compressed"
        catalog
        rows={[{ rank: 1, mark: "skipped", target: "groq/a" }]}
      />,
    )
    expect(container.querySelector(".mark")).toHaveClass("mark-catalog")
  })
})

/** One mark rule's declarations, as written. */
function markRule(css: string, mark: string): string {
  const match = css.match(new RegExp(`\\.mark-${mark}\\s*\\{([^}]*)\\}`))
  return match?.[1] ?? ""
}

describe("the marks read without colour", () => {
  // Fill, stroke weight, a centre dot and silhouette size are the channels
  // that survive greyscale. Colour is a fifth, and must never be the only one.
  const testFile = fileURLToPath(import.meta.url)
  const css = readFileSync(path.resolve(path.dirname(testFile), "../../styles/ladder.css"), "utf8")

  it("separates a hollow skip from a filled attempt by fill", () => {
    expect(markRule(css, "skipped")).toMatch(/background:\s*transparent/)
    expect(markRule(css, "failed")).toMatch(/background:\s*var\(/)
    expect(markRule(css, "served")).toMatch(/background:\s*var\(/)
  })

  it("gives cooling a second channel beyond its stroke weight", () => {
    // 1px against 2px is the weakest pairing in the greyscale proof, so
    // cooling also carries a centre dot.
    expect(markRule(css, "cooling")).toMatch(/border:\s*2px/)
    expect(css).toMatch(/\.mark-cooling::after\s*\{/)
  })

  it("makes a terminated mark a smaller silhouette, not a paler one", () => {
    const rule = markRule(css, "terminated")
    expect(rule).toMatch(/width:\s*6px/)
    expect(rule).toMatch(/background:\s*transparent/)
  })

  it("gives all five states a rule of their own", () => {
    for (const mark of ["skipped", "cooling", "failed", "served", "terminated"]) {
      expect(markRule(css, mark)).not.toBe("")
    }
  })
})

/** A class rule's declarations, without its comments. */
function classRule(css: string, name: string): string {
  const match = css.match(new RegExp(`(?:^|\\n)\\.${name}\\s*\\{([^}]*)\\}`))
  return (match?.[1] ?? "").replace(/\/\*[\s\S]*?\*\//g, "")
}

describe("the ladder in a narrow panel or under a long reason", () => {
  // A trace row carries the upstream error verbatim, and the trace drawer is
  // full width on a phone. Rows sized to one line spill wrapped text over the
  // rows below, and a target that cannot shrink pushes the row past its panel.
  const testFile = fileURLToPath(import.meta.url)
  const css = readFileSync(path.resolve(path.dirname(testFile), "../../styles/ladder.css"), "utf8")

  it("lets a row grow past one line rather than fixing its height", () => {
    const row = classRule(css, "ladder-row")
    expect(row).not.toMatch(/(^|[\s;])height:/)
    expect(row).toMatch(/min-height:\s*var\(--lad-row-h\)/)
  })

  it("scrolls inside its own border when a row is wider than the panel", () => {
    // The target column is fixed, so a narrow panel cannot hold a row; the
    // rows share one column as wide as the widest needs, and the ladder
    // scrolls rather than letting rows spill past it.
    const ladder = css.match(/\n\.ladder\s*\{([^}]*display:[^}]*)\}/)?.[1] ?? ""
    expect(ladder).toMatch(/display:\s*grid/)
    expect(ladder).toMatch(/grid-template-columns:\s*minmax\(min-content,\s*1fr\)/)
    expect(ladder).toMatch(/overflow-x:\s*auto/)
  })

  it("keeps a reason wide enough to read, and breaks one with no spaces", () => {
    expect(classRule(css, "reason")).toMatch(/min-width:\s*12rem/)
    expect(classRule(css, "reason-prose")).toMatch(/overflow-wrap:\s*anywhere/)
  })
})
