import { describe, it, expect } from "vitest"
import { readFileSync, existsSync, readdirSync } from "node:fs"
import { join } from "node:path"

const publicDir = join(__dirname, "../../../public")
const indexHtml = readFileSync(join(__dirname, "../../../index.html"), "utf8")
const manifest = JSON.parse(readFileSync(join(publicDir, "site.webmanifest"), "utf8"))
const brandDir = join(__dirname, "../../../../docs/brand")
const svgFiles = [
  join(publicDir, "favicon.svg"),
  ...readdirSync(brandDir).filter((f) => f.endsWith(".svg")).map((f) => join(brandDir, f)),
]

describe("the brand assets", () => {
  it("ships every icon the manifest names", () => {
    // A manifest entry with no file is an install prompt with a broken image,
    // and nothing in the build fails when one goes missing.
    for (const icon of manifest.icons) {
      expect(existsSync(join(publicDir, icon.src)), icon.src).toBe(true)
    }
  })

  it("ships every icon index.html links", () => {
    const hrefs = [...indexHtml.matchAll(/<link[^>]+href="(\/[^"]+)"/g)].map((m) => m[1] ?? "")
    const local = hrefs.filter((h) => h && h !== "/theme-restore.js")
    expect(local.length).toBeGreaterThan(0)
    for (const href of local) {
      expect(existsSync(join(publicDir, href)), href).toBe(true)
    }
  })

  it("offers a maskable icon, which Android crops and 'any' does not cover", () => {
    const purposes = manifest.icons.map((i: { purpose?: string }) => i.purpose)
    expect(purposes).toContain("maskable")
  })

  it("ships SVGs a browser will actually parse", () => {
    // XML forbids a double hyphen inside a comment, and these files carry long
    // ones. A malformed SVG still serves with a 200 and still passes an
    // existence check; what it does not do is render, and the first place that
    // shows up is an empty tab icon that no test was watching.
    for (const file of svgFiles) {
      const text = readFileSync(file, "utf8")
      for (const m of text.matchAll(/<!--([\s\S]*?)-->/g)) {
        expect((m[1] ?? "").includes("--"), `${file}: '--' inside a comment`).toBe(false)
      }
      const doc = new DOMParser().parseFromString(text, "image/svg+xml")
      expect(doc.querySelector("parsererror"), file).toBeNull()
      expect(doc.documentElement.tagName).toBe("svg")
    }
  })

  it("keeps the favicon on whole units of its own 16 grid", () => {
    // This is the whole reason the favicon is a separate drawing from the
    // console's 24-grid mark. A fractional coordinate puts a hairline across
    // two pixel columns at 16px, which no amount of crispEdges can undo, so
    // it is worth failing a build over rather than discovering in a tab.
    const svg = readFileSync(join(publicDir, "favicon.svg"), "utf8")
    expect(svg).toContain('viewBox="0 0 16 16"')
    expect(svg).toContain('shape-rendering="crispEdges"')

    const coords = [...svg.matchAll(/\s(?:x|y|width|height)="([\d.]+)"/g)].map((m) => m[1])
    const pathNumbers = [...svg.matchAll(/\sd="([^"]+)"/g)]
      .flatMap((m) => (m[1] ?? "").match(/[\d.]+/g) ?? [])
    expect(coords.length).toBeGreaterThan(0)
    for (const n of [...coords, ...pathNumbers]) {
      expect(Number.isInteger(Number(n)), n).toBe(true)
    }
  })
})
