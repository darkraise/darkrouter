import { describe, it, expect } from "vitest"
import type { ConfigFieldMeta, ConfigResponse } from "../../lib/api-types"
import {
  SETTINGS,
  displayOf,
  formatBytes,
  formatDuration,
  parseBytes,
  sameSetting,
  settingGroups,
  settingRow,
} from "./settings-catalog"

/** Fills in the ConfigResponse boilerplate every test here ignores. */
const cfgWith = (
  values: Record<string, string>,
  fields: Record<string, ConfigFieldMeta>,
): ConfigResponse => ({
  valid: true,
  warnings: [],
  values,
  fields,
  pending_restart: [],
})

describe("formatBytes", () => {
  it("reads a byte count at the scale it was written in", () => {
    // 33554432 is 32 MB, and nobody reads it as that.
    expect(formatBytes(33_554_432)).toBe("32 MB")
    expect(formatBytes(65_536)).toBe("64 KB")
    expect(formatBytes(1024 ** 3)).toBe("1 GB")
  })

  it("keeps one decimal where the number is not round", () => {
    expect(formatBytes(1_572_864)).toBe("1.5 MB")
  })

  it("leaves a small count alone", () => {
    expect(formatBytes(512)).toBe("512 bytes")
  })
})

describe("sameSetting", () => {
  it("reads a duration the way the gateway re-spells it", () => {
    expect(sameSetting("96h0m0s", "96h", "duration")).toBe(true)
    expect(sameSetting("1m30s", "90s", "duration")).toBe(true)
    expect(sameSetting("500ms", "0.5s", "duration")).toBe(true)
    expect(sameSetting("0s", "0", "duration")).toBe(true)
    expect(sameSetting("96h0m0s", "95h", "duration")).toBe(false)
    expect(sameSetting("soon", "soon ", "duration")).toBe(true)
    expect(sameSetting("soon", "later", "duration")).toBe(false)
  })

  it("reads numbers and switches the way the gateway parses them", () => {
    expect(sameSetting("10", "010", "int")).toBe(true)
    expect(sameSetting("1048576", "+1048576", "bytes")).toBe(true)
    expect(sameSetting("10", "11", "int")).toBe(false)
    expect(sameSetting("true", "1", "bool")).toBe(true)
    expect(sameSetting("false", "F", "bool")).toBe(true)
    expect(sameSetting("true", "yes", "bool")).toBe(false)
  })

  it("compares a string as text", () => {
    expect(sameSetting("10", "010", "string")).toBe(false)
  })
})

describe("formatDuration", () => {
  it("says days when the duration is whole days", () => {
    // 720h0m0s is the retention default, and it is thirty days.
    expect(formatDuration("720h0m0s")).toBe("30 days")
    expect(formatDuration("24h0m0s")).toBe("1 day")
  })

  it("reads hours, minutes and seconds", () => {
    expect(formatDuration("1m0s")).toBe("1 min")
    expect(formatDuration("10s")).toBe("10s")
    expect(formatDuration("15m0s")).toBe("15 min")
    expect(formatDuration("1h30m0s")).toBe("1h 30m")
  })

  it("leaves anything it cannot parse exactly as it came", () => {
    // Better an unfamiliar string than a confidently wrong reading.
    expect(formatDuration("forever")).toBe("forever")
    expect(formatDuration(":8080")).toBe(":8080")
  })
})

describe("formatDuration rounding", () => {
  it("carries a rounded remainder into the unit above it", () => {
    // Rounding the remainder alone lets it reach a full unit and print there:
    // 1h59m30s used to render as "1h 60m".
    expect(formatDuration("1h59m30s")).toBe("2h")
    expect(formatDuration("1m59.5s")).toBe("2 min")
  })

  it("still splits a duration that does not round up", () => {
    expect(formatDuration("1h30m")).toBe("1h 30m")
    expect(formatDuration("90s")).toBe("1 min 30s")
  })

  it("leaves whole units alone", () => {
    expect(formatDuration("2h")).toBe("2h")
    expect(formatDuration("24h")).toBe("1 day")
  })
})

describe("settingRow", () => {
  it("gives a setting its name and keeps its key", () => {
    const cfg = cfgWith(
      { "log.retention": "720h0m0s" },
      { "log.retention": { source: "database", hot_reloadable: true, kind: "duration" } },
    )
    const row = settingRow(cfg, "log.retention", SETTINGS["log.retention"]!)
    expect(row.meta.name).toBe("Keep request records for")
    expect(row.field).toBe("log.retention")
  })

  it("carries the source and whether it reloads hot", () => {
    const cfg = cfgWith(
      { "server.max_body_bytes": "33554432" },
      { "server.max_body_bytes": { source: "database", hot_reloadable: false, kind: "bytes" } },
    )
    const row = settingRow(cfg, "server.max_body_bytes", SETTINGS["server.max_body_bytes"]!)
    expect(row.source).toBe("database")
    expect(row.hotReloadable).toBe(false)
  })

  it("says nothing rather than guessing when there is no value", () => {
    const row = settingRow(cfgWith({}, {}), "log.retention", SETTINGS["log.retention"]!)
    expect(row.display).toBe("—")
  })

  it("carries the stored spelling and a readable one", () => {
    const cfg = cfgWith({ "policy.timeout.total": "720h0m0s" }, {
      "policy.timeout.total": { source: "database", hot_reloadable: true, kind: "duration" },
    })
    const row = settingGroups(cfg).flatMap((s) => s.rows).find((r) => r.field === "policy.timeout.total")
    // Both, because the save submits one and the operator reads the other.
    expect(row?.value).toBe("720h0m0s")
    expect(row?.display).toBe("30 days")
  })

  it("marks an environment row as not editable and names its variable", () => {
    const cfg = cfgWith({ "server.proxy_listen": ":18080" }, {
      "server.proxy_listen": {
        source: "env", hot_reloadable: false, kind: "string",
        env: "DARKROUTER_PROXY_LISTEN",
      },
    })
    const row = settingGroups(cfg).flatMap((s) => s.rows).find((r) => r.field === "server.proxy_listen")
    expect(row?.editable).toBe(false)
    expect(row?.env).toBe("DARKROUTER_PROXY_LISTEN")
  })

  it("marks a stored row as editable", () => {
    const cfg = cfgWith({ "log.retention": "720h0m0s" }, {
      "log.retention": { source: "database", hot_reloadable: true, kind: "duration" },
    })
    const row = settingGroups(cfg).flatMap((s) => s.rows).find((r) => r.field === "log.retention")
    expect(row?.editable).toBe(true)
  })

  it("names the nine catalogue keys that used to arrive unnamed", () => {
    for (const field of [
      "catalog.free_catalog_url", "catalog.free_catalog_interval",
      "catalog.free_catalog_sync", "catalog.litellm_url",
      "catalog.litellm_interval", "catalog.litellm_sync",
      "catalog.seed_free_providers", "catalog.discovery.timeout",
      "catalog.discovery.concurrency",
    ]) {
      // A key with no entry falls back to printing itself, which is the state
      // this fixes: the console served them as bare dotted keys or not at all.
      expect(SETTINGS[field]?.name, field).toBeTruthy()
      expect(SETTINGS[field]?.name, field).not.toBe(field)
    }
  })

  it("says the shutdown grace is cut short by the container's 30s stop", () => {
    // The grace reloads from this screen, the container's stop period does
    // not, and raising one without the other gets the drain killed.
    const description = SETTINGS["server.shutdown_grace"]?.description
    expect(description).toMatch(/30s/)
    expect(description).toMatch(/25s/)
  })
})

describe("displayOf", () => {
  it("reads a size at the scale it was written in", () => {
    expect(displayOf("33554432", "bytes")).toBe("32 MB")
  })
  it("reads a bool as on or off", () => {
    expect(displayOf("true", "bool")).toBe("On")
    expect(displayOf("false", "bool")).toBe("Off")
  })
  it("leaves a string alone", () => {
    expect(displayOf("https://models.dev/api.json", "url")).toBe("https://models.dev/api.json")
  })
  it("never scales a plain count", () => {
    // The old heuristic keyed on the key's _bytes suffix; the kind carries it
    // now, and a retry count of 4 must not read as a size.
    expect(displayOf("4", "int")).toBe("4")
  })
})

describe("parseBytes", () => {
  it("accepts what displayOf produces, so a round trip holds", () => {
    expect(parseBytes("32 MB")).toBe(33554432)
    expect(parseBytes("32MB")).toBe(33554432)
    expect(parseBytes("33554432")).toBe(33554432)
  })
  it("refuses what is not a size", () => {
    expect(parseBytes("")).toBeUndefined()
    expect(parseBytes("many")).toBeUndefined()
    expect(parseBytes("-1")).toBeUndefined()
  })
})

describe("settingGroups", () => {
  it("groups settings by what they are about, not by their prefix", () => {
    const cfg = cfgWith(
      { "policy.retry.max_attempts": "4", "server.max_body_bytes": "33554432" },
      {
        "policy.retry.max_attempts": { source: "database", hot_reloadable: true, kind: "int" },
        "server.max_body_bytes": { source: "database", hot_reloadable: false, kind: "bytes" },
      },
    )
    const groups = settingGroups(cfg)
    const ids = groups.map((g) => g.group.id)
    // Requests before server: the order is how often an operator reaches for
    // them, not alphabetical.
    expect(ids.indexOf("requests")).toBeLessThan(ids.indexOf("server"))
    const requests = groups.find((g) => g.group.id === "requests")
    expect(requests?.rows.map((r) => r.field)).toContain("policy.retry.max_attempts")
  })

  it("keeps a field the gateway added that this build cannot name", () => {
    // Unnamed is recoverable; invisible is not.
    const cfg = cfgWith(
      { "policy.something_new": "1" },
      { "policy.something_new": { source: "database", hot_reloadable: true, kind: "int" } },
    )
    const groups = settingGroups(cfg)
    const requests = groups.find((g) => g.group.id === "requests")
    expect(requests?.rows.some((r) => r.field === "policy.something_new")).toBe(true)
  })

  it("drops a group with nothing in it", () => {
    const groups = settingGroups(cfgWith({}, {}))
    // Every named setting still renders even with no field metadata, so the
    // groups that survive are the ones the catalogue names.
    expect(groups.every((g) => g.rows.length > 0)).toBe(true)
  })
})

describe("the two settings that govern prompt text at rest", () => {
  it("shows them under one heading, so the distinction is offered rather than inferred", () => {
    const cfg = cfgWith(
      { "capture.bodies": "false", "playground.save_conversations": "true" },
      {
        "capture.bodies": { source: "database", hot_reloadable: true, kind: "bool" },
        "playground.save_conversations": { source: "default", hot_reloadable: true, kind: "bool" },
      },
    )

    const logging = settingGroups(cfg).find((g) => g.group.id === "logging")
    const fields = logging?.rows.map((r) => r.field) ?? []
    expect(fields).toContain("capture.bodies")
    expect(fields).toContain("playground.save_conversations")
    // An operator who turned body capture off for privacy would reasonably
    // expect that stance to cover the playground. It does not, and the
    // heading has to say so rather than leave it to be discovered.
    expect(logging?.group.blurb).toMatch(/your own/i)
  })

  it("reads the switch as On and Off rather than as a raw boolean", () => {
    // settingRow derives display from the value alone, so passing the
    // catalogue entry through a non-null assertion let this pass with the
    // entry deleted -- and a setting missing from the catalogue is exactly
    // what it should catch. Assert the entry before using it.
    const meta = SETTINGS["playground.save_conversations"]
    expect(meta).toBeDefined()
    expect(meta?.group).toBe("logging")

    const off = cfgWith(
      { "playground.save_conversations": "false" },
      { "playground.save_conversations": { source: "default", hot_reloadable: true, kind: "bool" } },
    )
    expect(settingRow(off, "playground.save_conversations", meta!).display).toBe("Off")

    const on = cfgWith(
      { "playground.save_conversations": "true" },
      { "playground.save_conversations": { source: "default", hot_reloadable: true, kind: "bool" } },
    )
    expect(settingRow(on, "playground.save_conversations", meta!).display).toBe("On")
  })
})
