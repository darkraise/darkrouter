/**
 * Every wire type the console reads, in one place.
 *
 * These mirror the json tags on the Go structs in `internal/admin` and
 * `internal/store`. **Nothing enforces the mirror.** There is no codegen and no
 * shared schema, so a Go field rename lands here by hand — the value of this
 * module is that it lands in one place rather than in five, not that a compiler
 * catches it. Treat a shape that looks wrong as evidence the Go side moved.
 *
 * Optional fields carry `?` only where the Go side emits `omitempty` or a
 * pointer, so a missing value and a zero value stay distinguishable.
 */

// --- shared ---

/** Micro-dollars. `priced` is false when no model in scope had a catalog price. */
export type Spend = { micros: number; priced: boolean }

export type UsageDay = {
  day: string
  requests: number
  attempts: number
  tokens_in: number
  tokens_out: number
  /** Null rather than zero for an unpriced model: zero would read as free. */
  cost_micros: number | null
}

/** A usage row split by one dimension. `key` is empty for the day-only view. */
export type UsageRow = UsageDay & { key?: string }

export type UsageDimension = "provider" | "model" | "alias"

/** GET /api/usage. An envelope, not a bare array: `priced` reports whether any
 *  row in scope had a catalog price, which a caller cannot derive from rows
 *  whose cost is null for two different reasons. */
export type UsageResponse = {
  days: UsageRow[]
  priced: boolean
  group_by?: UsageDimension
}

// --- overview ---

/** The four states GET /api/overview emits. `degraded` is not `cooling`: a
 *  credential cools, a provider degrades. */
export type ProviderState = "healthy" | "degraded" | "disabled" | "unconfigured"

export type ProviderTile = {
  id: string
  name: string
  state: ProviderState
  cooling: number
  credentials: number
  enabled: boolean
  needs_reauth: boolean
}

export type FailoverRow = {
  id: string
  ts: number
  alias: string
  final_provider_id: string
  final_model: string
  attempts: number
  total_ms: number
}

/** Who handed work to whom. The routing graph draws a dashed return per pair;
 *  `failovers` names only where a request ended, which cannot place an arc. */
export type FailoverEdge = {
  from_provider_id: string
  to_provider_id: string
  requests: number
}

export type Overview = {
  providers: ProviderTile[]
  requests_per_min: number
  error_rate: number
  window_sec: number
  today_spend: Spend
  latency: { p50_ms: number; p95_ms: number }
  series: UsageRow[]
  failovers: FailoverRow[]
  failover_edges: FailoverEdge[]
}

// --- requests and the trace ---

export type RequestRow = {
  id: string
  ts_ms: number
  dialect: string
  surface: string
  /** What the client asked for: an alias or a bare model name. */
  model: string
  /** Present when `model` resolved through an alias. */
  alias?: string
  /** The provider that served. Absent when nothing did. */
  provider?: string
  final_model?: string
  /** A word, not an HTTP code: "success", "error", and so on. */
  status: string
  tokens_in: number
  tokens_out: number
  cache_read_tokens: number
  cost_micros: number | null
  ttft_ms: number | null
  total_ms: number | null
  error_code?: string
  attempts: number
}

export type RequestPage = { requests: RequestRow[]; next_cursor?: string }

export type TraceAttempt = {
  seq: number
  provider: string
  key_label: string
  model: string
  outcome: string
  status_code: number
  latency_ms: number
  error: string
  path: string
  tokens_in: number
  tokens_out: number
  cost_micros: number | null
}

export type TraceBody = { kind: string; content: string }

export type RequestTrace = Omit<RequestRow, "attempts"> & {
  /** Three separate lists, deliberately: attempts alone explains a failover,
   *  while candidates and skips explain the routing decision that led to it.
   *  Both are stored as formatted strings rather than structured rows. */
  candidates: string[]
  skips: string[]
  /** The handler reuses the key `attempts` for the list, where a row uses
   *  it for a count. Omit above is what lets both be typed truthfully. */
  attempts: TraceAttempt[]
  warnings?: string[]
  surface_meta?: Record<string, unknown>
  bodies?: TraceBody[]
}

// --- routing ---

/** Why a target was considered and rejected. Mirrors router.SkipReason. */
export type SkipReason =
  | "disabled"
  | "cooling"
  | "surface"
  | "capability"
  | "no_credential"
  | "removed_upstream"
  | "adapter_surface"

export type RouteCandidate = {
  provider_id: string
  key_id: string
  model: string
  kind: string
  publisher?: string
  /** Admitted on guessed capability metadata, per master design §6.4. */
  inferred: boolean
}

export type RoutePreview = {
  candidates: RouteCandidate[]
  skips: string[]
  /** Present when nothing routed; the skips say why. */
  error?: string
}

export type Aliases = Record<string, string[]>

// --- providers, credentials, catalog ---

export type Credential = {
  id: string
  label: string
  /** Enough of the secret to recognise it, never enough to use it. */
  masked: string
  enabled: boolean
  cooling: boolean
}

export type Provider = {
  id: string
  name: string
  preset: string
  kind: string
  base_url: string
  priority: number
  enabled: boolean
  /** Which credential form to show. A static key form is useless for an oauth
   *  provider: there is no key to type. */
  auth_style: string
  credentials: Credential[]
}

export type Preset = { id: string; name: string; kind: string; base_url: string }

/** Both of these are envelopes. Typing them as bare arrays is what made every
 *  screen throw on first contact with a real gateway. */
export type ProvidersResponse = { providers: Provider[] }
export type PresetsResponse = { presets: Preset[] }

/** A model as the catalog merges it: one row per model id, listing every
 *  provider that serves it, rather than one row per (provider, model). */
export type Model = {
  model: string
  providers: string[]
  surfaces: string[]
  context_window: number
  max_output_tokens: number
  tools: boolean
  vision: boolean
  reasoning: boolean
  /** Capabilities were guessed rather than read. Master design §6.4 routes
   *  these with a warning, and an operator needs to know which they are. */
  inferred: boolean
  state: string
}

export type AliasView = { name: string; targets: string[] }

export type CatalogResponse = { models: Model[]; aliases: AliasView[] }

export type ModelCapabilities = {
  tools?: boolean
  vision?: boolean
  reasoning?: boolean
}

export type ModelOverride = {
  surfaces?: string[]
  capabilities?: ModelCapabilities
  context_window?: number
}

// --- health ---

export type BreakerEntry = {
  provider_id: string
  key_id: string
  model: string
  /** Absent when the entry is not cooling. */
  cooling_until?: string
  backoff_level: number
  consecutive_failures: number
}

// --- config ---

/** Where a value came from. `database` means editing the YAML has no effect,
 *  which §8.1 requires the config view to say at the point of display. */
export type ConfigSource = "file" | "database" | "default"

export type ConfigFieldMeta = {
  source: ConfigSource
  hot_reloadable: boolean
}

export type PolicyBlock = {
  cooldown: { trip_after?: number; max: string }
  retry: { max_attempts: number }
  timeout: { connect: string; first_byte: string; total: string; idle: string }
}

export type ConfigBlocks = {
  server: {
    proxy_listen: string
    admin_listen: string
    max_body_bytes: number
    shutdown_grace: string
    sse: { max_line_bytes: number; max_precommit_bytes: number }
  }
  log: { retention: string }
  capture: { bodies: boolean; max_bytes: number; retention: string }
  catalog: {
    models_dev_url: string
    sync_interval: string
    sync_timeout: string
    discovery: { enabled: boolean; interval: string }
  }
  aliases: Aliases
  policy: PolicyBlock
}

export type ConfigResponse = {
  valid: boolean
  warnings: string[]
  blocks: ConfigBlocks
  fields: Record<string, ConfigFieldMeta>
  error?: string
  serving?: string
}

// --- credentials for clients, and sessions ---

export type ProxyToken = {
  id: string
  name: string
  prefix: string
  created_at: string
  last_used_at: string | null
  /** Returned by creation only. The column holds a hash, so this is the one
   *  chance to show it. */
  secret?: string
}

export type Session = {
  id: string
  prefix: string
  created_at: string
  expires_at: string
  current: boolean
}
