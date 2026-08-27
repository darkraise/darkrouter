import type { ConfigFieldMeta, ConfigResponse } from "../../lib/api-types"

/**
 * What each setting is called, and what it does.
 *
 * The wire key is how the gateway addresses a setting; it is not how an
 * operator thinks about one. `policy.timeout.first_byte` is "how long to wait
 * for a model to start answering", and a screen that only prints the key makes
 * every reader translate it themselves, every time.
 *
 * The key is still shown, in mono, beneath the name — it is what the YAML file
 * and every error message use, so hiding it would break the trail from this
 * screen to the file being edited.
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
    blurb: "What is recorded about each request, and how long it is kept.",
  },
  {
    id: "server",
    title: "Server",
    blurb: "Addresses and limits. Every one of these needs a restart.",
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
  if (total >= 3600) {
    const h = Math.floor(total / 3600)
    const rest = Math.round((total % 3600) / 60)
    return rest === 0 ? `${h}h` : `${h}h ${rest}m`
  }
  if (total >= 60) {
    const mins = Math.floor(total / 60)
    const rest = Math.round(total % 60)
    return rest === 0 ? `${mins} min` : `${mins} min ${rest}s`
  }
  return `${total}s`
}

export type SettingRow = {
  field: string
  meta: SettingMeta
  /** What the value is, as a person reads it. */
  display: string
  /** The literal the configuration file carries, when it differs from the
   *  display — a duration of 720h0m0s reads as 30 days, and the file still
   *  says the first. Empty when the two are the same. */
  literal: string
  source: ConfigFieldMeta["source"]
  hotReloadable: boolean
}

function raw(cfg: ConfigResponse, field: string): unknown {
  let node: unknown = cfg.blocks
  for (const part of field.split(".")) {
    if (typeof node !== "object" || node === null) return undefined
    node = (node as Record<string, unknown>)[part]
  }
  return node
}

/** One setting, formatted for reading. */
export function settingRow(
  cfg: ConfigResponse,
  field: string,
  meta: SettingMeta,
): SettingRow {
  const value = raw(cfg, field)
  const fieldMeta = cfg.fields[field]
  const row = {
    field,
    meta,
    source: fieldMeta?.source ?? "default",
    hotReloadable: fieldMeta?.hot_reloadable ?? false,
  }

  if (value === undefined || value === null) {
    return { ...row, display: "—", literal: "" }
  }
  if (typeof value === "boolean") {
    return { ...row, display: value ? "On" : "Off", literal: "" }
  }
  if (typeof value === "number") {
    // Only the byte fields are big enough to need scaling; a retry count of 4
    // must not become "4 bytes".
    const isBytes = field.endsWith("_bytes")
    return { ...row, display: isBytes ? formatBytes(value) : String(value), literal: "" }
  }
  if (typeof value === "string") {
    const human = formatDuration(value)
    return { ...row, display: human, literal: human === value ? "" : value }
  }
  return { ...row, display: JSON.stringify(value), literal: "" }
}

/** Every setting this build knows how to explain, in reading order.
 *
 *  A field the API sends that has no entry here is not dropped: it lands in
 *  the group its prefix names, under its own key, so a setting added to the
 *  gateway shows up unnamed rather than invisible. */
export function settingGroups(cfg: ConfigResponse): { group: Group; rows: SettingRow[] }[] {
  const known = new Set(Object.keys(SETTINGS))
  const extra = Object.keys(cfg.fields)
    .filter((f) => !known.has(f) && f !== "aliases")
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
  if (field.startsWith("log") || field.startsWith("capture")) return "logging"
  return "server"
}

export const SOURCE_NOTE = {
  file: "Read from darkrouter.yaml",
  // §8.1 requires the config view to say this at the point of display: after
  // the first run, editing the file has no effect on these.
  database: "Stored in the database — the file is no longer read for this",
  default: "Not set anywhere; this is the built-in default",
} as const

export const SOURCE_LABEL = {
  file: "file",
  database: "database",
  default: "default",
} as const

/**
 * The settings this console can actually change.
 *
 * Only `policy` and `aliases` are writable at all -- both moved into the
 * database after the first run -- and aliases are a routing concept with
 * their own editor. Everything else on the gateway comes from
 * darkrouter.yaml and has no write endpoint, so listing it here would be a
 * page of controls that refuse to move.
 *
 * `policy.timeout.connect` and `policy.timeout.first_byte` are absent for the
 * same reason: both configure the one shared transport built at startup, and
 * `PUT /api/policy` refuses a write that touches either.
 */
export type EditableSetting = {
  field: string
  name: string
  description: string
  group: "requests" | "failure"
  kind: "duration" | "count"
  placeholder: string
}

export const EDITABLE: EditableSetting[] = [
  {
    field: "policy.retry.max_attempts",
    name: SETTINGS["policy.retry.max_attempts"]!.name,
    description: SETTINGS["policy.retry.max_attempts"]!.description,
    group: "requests",
    kind: "count",
    placeholder: "4",
  },
  {
    field: "policy.timeout.total",
    name: SETTINGS["policy.timeout.total"]!.name,
    description: SETTINGS["policy.timeout.total"]!.description,
    group: "requests",
    kind: "duration",
    placeholder: "10m",
  },
  {
    field: "policy.timeout.idle",
    name: SETTINGS["policy.timeout.idle"]!.name,
    description: SETTINGS["policy.timeout.idle"]!.description,
    group: "requests",
    kind: "duration",
    placeholder: "2m",
  },
  {
    field: "policy.cooldown.trip_after",
    name: SETTINGS["policy.cooldown.trip_after"]!.name,
    description: SETTINGS["policy.cooldown.trip_after"]!.description,
    group: "failure",
    kind: "count",
    placeholder: "3",
  },
  {
    field: "policy.cooldown.max",
    name: SETTINGS["policy.cooldown.max"]!.name,
    description: SETTINGS["policy.cooldown.max"]!.description,
    group: "failure",
    kind: "duration",
    placeholder: "15m",
  },
]
