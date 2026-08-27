import { describe, it, expect } from "vitest"
import { configRows, passwordProblem, readValue, reloadMessage, revokedText } from "./settings-screen"
import type { ConfigResponse } from "../../lib/api-types"

const cfg = (): ConfigResponse => ({
  valid: true,
  warnings: [],
  fields: {
    "log.retention": { source: "file", hot_reloadable: true },
    "catalog.discovery.interval": { source: "default", hot_reloadable: false },
    aliases: { source: "database", hot_reloadable: true },
  },
  blocks: {
    server: {
      proxy_listen: ":8080",
      admin_listen: ":8081",
      max_body_bytes: 1,
      shutdown_grace: "10s",
      sse: { max_line_bytes: 1, max_precommit_bytes: 1 },
    },
    log: { retention: "72h" },
    capture: { bodies: false, max_bytes: 0, retention: "24h" },
    catalog: {
      models_dev_url: "https://models.dev",
      sync_interval: "12h",
      sync_timeout: "30s",
      discovery: { enabled: true, interval: "6h" },
    },
    aliases: { fast: ["groq/a"] },
    policy: {
      cooldown: { max: "30m" },
      retry: { max_attempts: 3 },
      timeout: { connect: "10s", first_byte: "60s", total: "10m", idle: "30s" },
    },
  },
})

describe("readValue", () => {
  it("walks a dotted field into the blocks payload", () => {
    expect(readValue(cfg(), "log.retention")).toBe("72h")
    expect(readValue(cfg(), "catalog.discovery.interval")).toBe("6h")
  })

  it("renders a block value rather than [object Object]", () => {
    expect(readValue(cfg(), "aliases")).toBe('{"fast":["groq/a"]}')
  })

  it("shows a missing field as unknown rather than crashing", () => {
    // The field list and the blocks are two payloads; one naming something the
    // other omits must not take the screen down.
    expect(readValue(cfg(), "server.nothing.here")).toBe("—")
  })
})

describe("configRows", () => {
  it("carries each field's source and reloadability", () => {
    const rows = configRows(cfg())
    const aliases = rows.find((r) => r.field === "aliases")
    // §8.1: after the first run, editing aliases in the file has no effect,
    // and the config view has to say so at the point of display.
    expect(aliases?.meta.source).toBe("database")
    const interval = rows.find((r) => r.field === "catalog.discovery.interval")
    expect(interval?.meta.hot_reloadable).toBe(false)
  })

  it("orders fields so the table does not reshuffle between polls", () => {
    const rows = configRows(cfg()).map((r) => r.field)
    expect(rows).toEqual([...rows].sort((a, b) => a.localeCompare(b)))
  })
})

describe("the password form", () => {
  it("refuses a short password before spending a round trip", () => {
    // The server's floor is twelve. Checking it here is a courtesy; the
    // server stays the authority.
    expect(passwordProblem("short", "short")).toMatch(/12 characters/)
  })

  it("refuses a mismatched confirmation", () => {
    expect(passwordProblem("long-enough-passphrase", "long-enough-passphras")).toMatch(
      /do not match/i,
    )
  })

  it("accepts a long matching pair", () => {
    expect(passwordProblem("long-enough-passphrase", "long-enough-passphrase")).toBeNull()
  })
})

describe("the revocation notice", () => {
  it("says how many other sessions were ended", () => {
    // The operator has just logged every other browser out. Not saying so
    // makes the next login failure elsewhere look like a fault.
    expect(revokedText(3)).toMatch(/3 other sessions/)
  })

  it("says none rather than zero", () => {
    expect(revokedText(0)).toMatch(/no other sessions/i)
  })

  it("says one session in the singular", () => {
    expect(revokedText(1)).toMatch(/1 other session\b/)
  })
})

describe("the reload result", () => {
  it("reports an invalid file without claiming the gateway stopped", () => {
    expect(
      reloadMessage({ valid: false, error: "yaml: bad", serving: "the previous configuration is still serving" }),
    ).toMatch(/previous configuration is still serving/)
  })

  it("carries the parse error so the operator knows what to fix", () => {
    expect(reloadMessage({ valid: false, error: "yaml: line 4" })).toContain("yaml: line 4")
  })

  it("confirms a clean reload", () => {
    expect(reloadMessage({ valid: true })).toMatch(/reloaded/i)
  })
})
