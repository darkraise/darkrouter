import type { ConfigKind, ConfigResponse, ConfigSource } from "../../lib/api-types"

/**
 * What each setting is called, and what it does.
 *
 * The wire key is how the gateway addresses a setting; it is not how an
 * operator thinks about one. `policy.timeout.first_byte` is "how long to wait
 * for a model to start answering", and a screen that only prints the key makes
 * every reader translate it themselves, every time.
 *
 * The key is still shown, in mono, beneath the name — it is what the settings
 * table and every error message use, so hiding it would break the trail from
 * this screen to the stored value.
 */
export type SettingMeta = {
  name: string
  description: string
  group: GroupId
}

export type GroupId = "requests" | "failure" | "catalogue" | "logging" | "server"

export type Group = {
  id: GroupId
  title: string
  blurb: string
}

/** Ordered by how often an operator reaches for them, not alphabetically. */
export const GROUPS: Group[] = [
  {
    id: "requests",
    title: "Requests",
    blurb: "How long the router waits, and how many providers it will try.",
  },
  {
    id: "failure",
    title: "Failure handling",
    blurb: "When a credential is taken out of rotation, and for how long.",
  },
  {
    id: "catalogue",
    title: "Model catalogue",
    blurb: "Where model metadata comes from and how often it is refreshed.",
  },
  {
    id: "logging",
    title: "Logging and capture",
    blurb:
      "What is recorded about each request, and how long it is kept. Two of these keep prompt text on disk and they are not the same thing: body capture records other people's traffic passing through the gateway, and saved conversations record your own typing in the playground.",
  },
  {
    id: "server",
    title: "Server",
    blurb:
      "Addresses and limits. The listen addresses come from the environment and take a restart to change; every other key here applies on reload.",
  },
]

export const SETTINGS: Record<string, SettingMeta> = {
  "policy.timeout.connect": {
    name: "Connection timeout",
    description: "Give up if a provider has not accepted the connection by then.",
    group: "requests",
  },
  "policy.timeout.first_byte": {
    name: "Wait for the first token",
    description:
      "How long a model may think before it starts answering. The one to raise for slow reasoning models.",
    group: "requests",
  },
  "policy.timeout.total": {
    name: "Total request time",
    description: "The ceiling on one request, including every retry it makes.",
    group: "requests",
  },
  "policy.timeout.idle": {
    name: "Idle stream timeout",
    description: "Abandon a stream that has gone this long without sending anything.",
    group: "requests",
  },
  "policy.retry.max_attempts": {
    name: "Attempts per request",
    description:
      "How many providers one request may try before it fails. Each attempt is a different provider or key.",
    group: "requests",
  },
  "policy.cooldown.trip_after": {
    name: "Failures before cooling",
    description: "Consecutive failures on one credential before the router stops choosing it.",
    group: "failure",
  },
  "policy.cooldown.max": {
    name: "Longest cooldown",
    description: "Backoff doubles with each failure and stops growing here.",
    group: "failure",
  },
  "catalog.models_dev_url": {
    name: "Metadata source",
    description: "Where pricing, context windows and capabilities are fetched from.",
    group: "catalogue",
  },
  "catalog.sync_interval": {
    name: "Refresh metadata every",
    description: "How often that document is re-fetched.",
    group: "catalogue",
  },
  "catalog.sync_timeout": {
    name: "Metadata fetch timeout",
    description: "Give up on the fetch and keep serving the previous document.",
    group: "catalogue",
  },
  "catalog.discovery.enabled": {
    name: "Ask providers what they serve",
    description:
      "Sweeps each provider's model list and adds what it finds. Off means the catalogue is whatever the metadata source says.",
    group: "catalogue",
  },
  "catalog.discovery.interval": {
    name: "Sweep providers every",
    description: "How often each provider's model list is re-read.",
    group: "catalogue",
  },
  "catalog.discovery.timeout": {
    name: "Provider sweep timeout",
    description:
      "Give up on a provider whose model list has not arrived by then; the sweep counts it as a failure.",
    group: "catalogue",
  },
  "catalog.discovery.concurrency": {
    name: "Providers swept at once",
    description: "How many providers the sweep queries at once.",
    group: "catalogue",
  },
  "catalog.free_catalog_url": {
    name: "Free-tier catalogue source",
    description: "Where the curated list of which models are free on each provider is fetched from.",
    group: "catalogue",
  },
  "catalog.free_catalog_interval": {
    name: "Refresh the free-tier list every",
    description: "How often that curated list is re-fetched.",
    group: "catalogue",
  },
  "catalog.free_catalog_sync": {
    name: "Keep the free-tier list current",
    description:
      "Re-fetches the curated free-tier list on the schedule above. Off keeps the list this build shipped with.",
    group: "catalogue",
  },
  "catalog.litellm_url": {
    name: "Community price index",
    description: "Where prices for models the metadata source does not cover are fetched from.",
    group: "catalogue",
  },
  "catalog.litellm_interval": {
    name: "Refresh prices every",
    description: "How often that price index is re-fetched.",
    group: "catalogue",
  },
  "catalog.litellm_sync": {
    name: "Keep prices current",
    description:
      "Re-fetches the community price index on the schedule above. Off leaves a model unpriced when neither the metadata source nor its own provider gives a price.",
    group: "catalogue",
  },
  "catalog.seed_free_providers": {
    name: "Add free providers on first start",
    description:
      "At startup, adds a provider for every hosted preset that needs no credential and imports only its free models. Takes effect on the next restart; delete a seeded provider to decline it, since it is not offered twice.",
    group: "catalogue",
  },
  "media.inline": {
    name: "Fetch remote images",
    description:
      "Download an image a request refers to by URL so a provider that cannot fetch it still receives it.",
    group: "catalogue",
  },
  "log.retention": {
    name: "Keep request records for",
    description: "Rows older than this are deleted. Everything on Usage and Requests comes from them.",
    group: "logging",
  },
  "capture.bodies": {
    name: "Record request bodies",
    description:
      "Stores prompts and responses so a trace can show them. Off keeps the timing and the cost without the content.",
    group: "logging",
  },
  "capture.max_bytes": {
    name: "Largest body recorded",
    description: "Anything past this is truncated rather than dropped.",
    group: "logging",
  },
  "capture.retention": {
    name: "Keep bodies for",
    description: "Bodies are deleted on their own schedule, usually sooner than the records.",
    group: "logging",
  },
  "playground.save_conversations": {
    name: "Save playground conversations",
    description:
      "Keeps Chat mode's conversations so you can return to one tomorrow. Off stops new ones being written; it does not delete what is already there — the button at the top of the page does that.",
    group: "logging",
  },
  "server.proxy_listen": {
    name: "Gateway address",
    description: "Where clients send their requests.",
    group: "server",
  },
  "server.admin_listen": {
    name: "Console address",
    description: "Where this console is served.",
    group: "server",
  },
  "server.public_url": {
    name: "Public domain",
    description:
      "The domain clients outside this machine reach the gateway at, which it cannot work out for itself once a published port, a reverse proxy or a path prefix sits in front of it. A bare domain is assumed https. Set it and the Connect page lists this address alongside the LAN one; leave it empty and only the LAN address is shown.",
    group: "server",
  },
  "server.max_body_bytes": {
    name: "Largest request accepted",
    description: "A request bigger than this is refused before it reaches a provider.",
    group: "server",
  },
  "server.shutdown_grace": {
    name: "Shutdown grace period",
    description: "How long in-flight requests have to finish when the gateway is stopping.",
    group: "server",
  },
  "server.sse.max_line_bytes": {
    name: "Largest stream line",
    description: "A single streamed event larger than this ends the stream.",
    group: "server",
  },
  "server.sse.max_precommit_bytes": {
    name: "Stream buffer before commit",
    description:
      "How much of a stream is held before the response is committed, so an early failure can still fail over.",
    group: "server",
  },
}

const UNITS: [number, string][] = [
  [1024 ** 3, "GB"],
  [1024 ** 2, "MB"],
  [1024, "KB"],
]

/** Bytes at the scale the number was written in. 33554432 is 32 MB, and
 *  nobody reads it as that. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return String(bytes)
  for (const [size, unit] of UNITS) {
    if (bytes >= size) {
      const n = bytes / size
      return `${Number.isInteger(n) ? n : n.toFixed(1)} ${unit}`
    }
  }
  return `${bytes} bytes`
}

/**
 * A Go duration string as a person would say it.
 *
 * `720h0m0s` is thirty days, and reading that off the screen is arithmetic an
 * operator should not have to do. The original is kept beside it wherever the
 * rounding could matter.
 */
export function formatDuration(raw: string): string {
  const m = /^(?:(\d+(?:\.\d+)?)h)?(?:(\d+(?:\.\d+)?)m)?(?:(\d+(?:\.\d+)?)s)?$/.exec(raw.trim())
  if (!m || (!m[1] && !m[2] && !m[3])) return raw
  const hours = Number(m[1] ?? 0)
  const minutes = Number(m[2] ?? 0)
  const seconds = Number(m[3] ?? 0)
  const total = hours * 3600 + minutes * 60 + seconds
  if (total === 0) return "0s"

  if (total % 86_400 === 0) {
    const days = total / 86_400
    return days === 1 ? "1 day" : `${days} days`
  }
  // Rounded to the unit below before splitting, not after. Rounding the
  // remainder on its own lets it reach a full unit and print it there:
  // 1h59m30s rounded per-part gives "1h 60m", and 119.5s gives "1 min 60s".
  if (total >= 3600) {
    const minutes = Math.round(total / 60)
    const h = Math.floor(minutes / 60)
    const rest = minutes % 60
    return rest === 0 ? `${h}h` : `${h}h ${rest}m`
  }
  if (total >= 60) {
    const seconds = Math.round(total)
    const mins = Math.floor(seconds / 60)
    const rest = seconds % 60
    return rest === 0 ? `${mins} min` : `${mins} min ${rest}s`
  }
  return `${total}s`
}

export type SettingRow = {
  field: string
  meta: SettingMeta
  /** The stored spelling, which is what a save submits. */
  value: string
  /** The same value as a person reads it: 720h0m0s as "30 days". */
  display: string
  source: ConfigSource
  hotReloadable: boolean
  kind: ConfigKind
  /** The variable that owns a bootstrap key, or "". */
  env: string
  editable: boolean
}

/** One setting, formatted for reading and for writing back. */
export function settingRow(
  cfg: ConfigResponse,
  field: string,
  meta: SettingMeta,
): SettingRow {
  const fieldMeta = cfg.fields[field]
  const value = cfg.values[field] ?? ""
  const source = fieldMeta?.source ?? "default"
  const kind = fieldMeta?.kind ?? "string"
  return {
    field,
    meta,
    value,
    display: displayOf(value, kind),
    source,
    hotReloadable: fieldMeta?.hot_reloadable ?? false,
    kind,
    env: fieldMeta?.env ?? "",
    // The environment owns its keys and the console cannot write them. Every
    // other key is a registry row, and the write path takes all of them.
    editable: source !== "env",
  }
}

/** The stored spelling as a person reads it. The row keeps both: one is what
 *  the operator reads, the other is what a save submits. */
export function displayOf(value: string, kind: ConfigKind): string {
  if (value === "") return "—"
  switch (kind) {
    case "duration":
      return formatDuration(value)
    case "bytes": {
      const n = Number(value)
      return Number.isFinite(n) ? formatBytes(n) : value
    }
    case "bool":
      return value === "true" ? "On" : "Off"
    default:
      return value
  }
}

const BYTE_UNITS: Record<string, number> = {
  b: 1, kb: 1024, mb: 1024 ** 2, gb: 1024 ** 3,
}

/**
 * A size an operator typed, as bytes.
 *
 * It accepts the shapes `formatBytes` produces: a bare number, which is what
 * the store holds, or a number followed by a unit.
 *
 * The round trip is exact only for sizes formatBytes did not round: 33554433
 * shows as "32.0 MB" and parses back one byte short. An editor therefore
 * seeds from the stored value, not from the display.
 */
export function parseBytes(text: string): number | undefined {
  const m = /^\s*(\d+(?:\.\d+)?)\s*([a-zA-Z]*)\s*$/.exec(text)
  if (!m) return undefined
  const n = Number(m[1])
  const unit = (m[2] ?? "").toLowerCase()
  if (!Number.isFinite(n) || n < 0) return undefined
  if (unit === "" || unit === "bytes") return Math.round(n)
  const scale = BYTE_UNITS[unit]
  if (scale === undefined) return undefined
  return Math.round(n * scale)
}

/** Every setting this build knows how to explain, in reading order.
 *
 *  A field the API sends that has no entry here is not dropped: it lands in
 *  the group its prefix names, under its own key, so a setting added to the
 *  gateway shows up unnamed rather than invisible. */
export function settingGroups(cfg: ConfigResponse): { group: Group; rows: SettingRow[] }[] {
  const known = new Set(Object.keys(SETTINGS))
  const extra = Object.keys(cfg.fields)
    .filter((f) => !known.has(f))
    .map((f): [string, SettingMeta] => [
      f,
      { name: f, description: "", group: groupForPrefix(f) },
    ])

  const all: [string, SettingMeta][] = [...Object.entries(SETTINGS), ...extra]
  return GROUPS.map((group) => ({
    group,
    rows: all
      .filter(([, meta]) => meta.group === group.id)
      .map(([field, meta]) => settingRow(cfg, field, meta))
      // Reported by the API, or holding a value. A name this build knows for
      // a field the gateway no longer reports is stale and drops out; a field
      // the gateway reports that this build cannot name still shows.
      .filter((row) => cfg.fields[row.field] !== undefined || row.display !== "—"),
  })).filter((section) => section.rows.length > 0)
}

function groupForPrefix(field: string): GroupId {
  if (field.startsWith("policy.cooldown")) return "failure"
  if (field.startsWith("policy")) return "requests"
  if (field.startsWith("catalog") || field.startsWith("media")) return "catalogue"
  if (field.startsWith("log") || field.startsWith("capture") || field.startsWith("playground")) {
    return "logging"
  }
  return "server"
}

export const SOURCE_NOTE = {
  env: "Read from the environment at startup; a restart applies a change",
  database: "Stored in the database, where the console reads and writes it",
  default: "Not set anywhere; this is the built-in default",
} as const

export const SOURCE_LABEL = {
  env: "environment",
  database: "database",
  default: "default",
} as const

