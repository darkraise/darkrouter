import { describe, expect, it } from "vitest"
import { priorityOrder } from "./strategy-card"
import type { Credential, Provider } from "../../lib/api-types"

const cred = (): Credential => ({
  id: "k1", label: "k1", masked: "sk-…", enabled: true, cooling: false, kind: "static",
})

const provider = (id: string, over: Partial<Provider> = {}): Provider => ({
  id, name: id, preset: id, kind: "openaicompat", base_url: "https://x.example",
  priority: 10, enabled: true, auth_style: "bearer", free_models_only: false,
  allow_unsanctioned_free: false,
  credentials: [cred()],
  ...over,
})

describe("priorityOrder", () => {
  it("puts the highest priority first, as the router does", () => {
    // sqlsource loads the set `ORDER BY priority DESC, id` and byPriority
    // sorts `Priority >`. Ascending here would print the failover order
    // backwards.
    const out = priorityOrder([provider("b", { priority: 20 }), provider("a", { priority: 5 })])
    expect(out.map((p) => p.id)).toEqual(["b", "a"])
  })

  it("agrees with the order the providers screen shows", () => {
    // mergeProviderRows sorts configured providers descending too. Two screens
    // that disagree about the same order are worse than one that is silent.
    const out = priorityOrder([
      provider("low", { priority: 10 }),
      provider("high", { priority: 90 }),
    ])
    expect(out.map((p) => p.id)).toEqual(["high", "low"])
  })

  it("breaks a tie by id, so the order does not depend on what the API returned", () => {
    const out = priorityOrder([provider("z"), provider("a")])
    expect(out.map((p) => p.id)).toEqual(["a", "z"])
  })

  it("leaves out providers the router would never reach for", () => {
    // A disabled provider, one with no accounts, and one whose every account
    // is disabled are not steps in the order: sqlsource skips a provider whose
    // enabledOnly(creds) is empty, so it is not in the provider set at all.
    const out = priorityOrder([
      provider("live"),
      provider("off", { enabled: false }),
      provider("empty", { credentials: [] }),
      provider("keys-off", { credentials: [{ ...cred(), enabled: false }] }),
    ])
    expect(out.map((p) => p.id)).toEqual(["live"])
  })

  it("does not reorder the array it was given", () => {
    const input = [provider("b", { priority: 20 }), provider("a", { priority: 5 })]
    priorityOrder(input)
    expect(input.map((p) => p.id)).toEqual(["b", "a"])
  })
})
