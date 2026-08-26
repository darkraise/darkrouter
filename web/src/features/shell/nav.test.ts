import { describe, it, expect } from "vitest"
import { nav, settingsItem } from "./nav"

describe("information architecture", () => {
  it("groups eight destinations into three, with Settings apart", () => {
    // §5 is explicit about the shape: eight items read as three because the
    // rail groups them, and Settings sits apart because it holds knobs rather
    // than decisions.
    expect(nav.map((g) => g.label)).toEqual(["Operate", "Configure", "Use"])
    expect(nav.flatMap((g) => g.items)).toHaveLength(8)
    expect(nav.flatMap((g) => g.items).map((i) => i.href)).not.toContain(
      settingsItem.href,
    )
  })

  it("asks one question per group", () => {
    expect(nav[0]?.items.map((i) => i.label)).toEqual([
      "Overview",
      "Requests",
      "Usage",
    ])
    expect(nav[1]?.items.map((i) => i.label)).toEqual([
      "Providers",
      "Models",
      "Routing",
    ])
    expect(nav[2]?.items.map((i) => i.label)).toEqual(["Playground", "Connect"])
  })

  it("gives no destination to what belongs beside its subject", () => {
    // Breaker state, discovery health, preset browsing, aliases and model
    // overrides are each one panel beside the thing they describe. A rail item
    // for any of them is how a console grows sections that are stubs.
    const hrefs = [...nav.flatMap((g) => g.items), settingsItem].map((i) => i.href)
    for (const forbidden of ["/breakers", "/health", "/presets", "/aliases", "/overrides"]) {
      expect(hrefs).not.toContain(forbidden)
    }
    expect(hrefs).toHaveLength(9)
  })
})
