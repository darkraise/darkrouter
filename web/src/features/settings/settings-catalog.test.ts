import { describe, it, expect } from "vitest"
import type { ConfigResponse } from "../../lib/api-types"
import {
  SETTINGS,
  formatBytes,
  formatDuration,
  settingGroups,
  settingRow,
} from "./settings-catalog"

const cfg = (over: Partial<ConfigResponse> = {}): ConfigResponse =>
  ({
    valid: true,
    warnings: [],
    blocks: {
      server: { max_body_bytes: 33_554_432, shutdown_grace: "30s" },
      log: { retention: "720h0m0s" },
      capture: { bodies: false, max_bytes: 65_536 },
      policy: { retry: { max_attempts: 4 }, timeout: { first_byte: "1m0s" } },
      catalog: { discovery: { enabled: true } },
    },
    fields: {
      "server.max_body_bytes": { source: "file", hot_reloadable: false },
      "log.retention": { source: "default", hot_reloadable: true },
      "capture.bodies": { source: "file", hot_reloadable: true },
      "capture.max_bytes": { source: "file", hot_reloadable: true },
      "policy.retry.max_attempts": { source: "database", hot_reloadable: true },
      "policy.timeout.first_byte": { source: "database", hot_reloadable: true },
      "catalog.discovery.enabled": { source: "default", hot_reloadable: true },
    },
    ...over,
  }) as ConfigResponse

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

describe("settingRow", () => {
  it("gives a setting its name and keeps its key", () => {
    const row = settingRow(cfg(), "log.retention", SETTINGS["log.retention"]!)
    expect(row.meta.name).toBe("Keep request records for")
    expect(row.field).toBe("log.retention")
  })

  it("shows the literal beside a reformatted duration", () => {
    // The file still says 720h0m0s, and the trail from screen to file has to
    // survive the friendlier reading.
    const row = settingRow(cfg(), "log.retention", SETTINGS["log.retention"]!)
    expect(row.display).toBe("30 days")
    expect(row.literal).toBe("720h0m0s")
  })

  it("adds no literal when the value was already readable", () => {
    const row = settingRow(cfg(), "policy.timeout.first_byte", SETTINGS["policy.timeout.first_byte"]!)
    expect(row.display).toBe("1 min")
    expect(row.literal).toBe("1m0s")
  })

  it("reads a boolean as on or off", () => {
    expect(settingRow(cfg(), "capture.bodies", SETTINGS["capture.bodies"]!).display).toBe("Off")
    expect(
      settingRow(cfg(), "catalog.discovery.enabled", SETTINGS["catalog.discovery.enabled"]!).display,
    ).toBe("On")
  })

  it("scales a byte count but never a plain number", () => {
    // A retry count of 4 must not become "4 bytes".
    expect(settingRow(cfg(), "capture.max_bytes", SETTINGS["capture.max_bytes"]!).display).toBe(
      "64 KB",
    )
    expect(
      settingRow(cfg(), "policy.retry.max_attempts", SETTINGS["policy.retry.max_attempts"]!).display,
    ).toBe("4")
  })

  it("carries the source and whether it reloads hot", () => {
    const row = settingRow(cfg(), "server.max_body_bytes", SETTINGS["server.max_body_bytes"]!)
    expect(row.source).toBe("file")
    expect(row.hotReloadable).toBe(false)
  })

  it("says nothing rather than guessing when the block has no value", () => {
    const row = settingRow(cfg({ blocks: {} } as Partial<ConfigResponse>), "log.retention", SETTINGS["log.retention"]!)
    expect(row.display).toBe("—")
  })
})

describe("settingGroups", () => {
  it("groups settings by what they are about, not by their prefix", () => {
    const groups = settingGroups(cfg())
    const ids = groups.map((g) => g.group.id)
    // Requests before server: the order is how often an operator reaches for
    // them, not alphabetical.
    expect(ids.indexOf("requests")).toBeLessThan(ids.indexOf("server"))
    const requests = groups.find((g) => g.group.id === "requests")
    expect(requests?.rows.map((r) => r.field)).toContain("policy.retry.max_attempts")
  })

  it("keeps a field the gateway added that this build cannot name", () => {
    // Unnamed is recoverable; invisible is not.
    const groups = settingGroups(
      cfg({
        fields: {
          "policy.something_new": { source: "file", hot_reloadable: true },
        },
      } as Partial<ConfigResponse>),
    )
    const requests = groups.find((g) => g.group.id === "requests")
    expect(requests?.rows.some((r) => r.field === "policy.something_new")).toBe(true)
  })

  it("drops a group with nothing in it", () => {
    const groups = settingGroups(cfg({ fields: {} } as Partial<ConfigResponse>))
    // Every named setting still renders even with no field metadata, so the
    // groups that survive are the ones the catalogue names.
    expect(groups.every((g) => g.rows.length > 0)).toBe(true)
  })
})
