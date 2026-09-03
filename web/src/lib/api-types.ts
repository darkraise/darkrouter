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

/** Micro-dollars. `micros` is null and `priced` false when no model in scope
 *  had a catalog price: the spend is unknown, not zero. */
/** `estimated` marks a total that counted at least one price nobody sold at:
 *  a third-party index or a guess. The figure still counts it — an estimate is
 *  nearer the truth than the zero an omission would leave. */
export type Spend = { micros: number | null; priced: boolean; estimated: boolean }

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
  /** Where the request came from: `proxy` for a client through the gateway,
   *  `console` for one an operator sent by hand from the playground or a
   *  provider's test drawer. */
  source: string
  tokens_in: number
  tokens_out: number
  cache_read_tokens: number
  /** Output tokens the model spent reasoning rather than answering. Counted
   *  inside `tokens_out` rather than beside it, so it explains that total
   *  instead of adding to it. Served on the trace only. */
  reasoning_tokens?: number
  cost_micros: number | null
  ttft_ms: number | null
  total_ms: number | null
  error_code?: string
  attempts: number
  /** Which rendering served: the fast path untouched, or translated through
   *  the IR. Absent when nothing served. */
  path?: "passthrough" | "ir"
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

export type RequestTrace = Omit<RequestRow, "attempts" | "source"> & {
  /** The trace handler does not emit source; the list does. */
  source?: string
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

/** A target the router considered and rejected, with the reason. */
export type RouteSkip = {
  provider_id: string
  key_id?: string
  model: string
  reason: SkipReason | string
}

export type RoutePreview = {
  candidates: RouteCandidate[]
  skips: RouteSkip[]
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
  kind: string
  /** OAuth only. Seconds since the epoch. */
  expires_at?: number
  scope?: string
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
  /** Narrows what the next discovery sweep imports to models it can show are
   *  free. A filter on the catalogue, not on routing. */
  free_models_only: boolean
  /** Off by default: the router refuses a free tier OmniRoute grades "avoid"
   *  until the operator opts in here. A free-models-only import applies the
   *  same veto, but only while that filter is on and only to a still-live
   *  grading. */
  allow_unsanctioned_free: boolean
}

export type Preset = {
  id: string
  name: string
  kind: string
  base_url: string
  surfaces: string[]
  auth_kind: string
  website: string
  free_tier: boolean
}

/** Both of these are envelopes. Typing them as bare arrays is what made every
 *  screen throw on first contact with a real gateway. */
export type ProvidersResponse = { providers: Provider[] }
export type PresetsResponse = { presets: Preset[] }

export type Pricing = {
  input_micros: number
  output_micros: number
  price_source: MergeSource
  price_grade: PriceGrade
}

/** Where a merged model row's data came from. Mirrors catalog.MergeSource. */
export type MergeSource =
  | "models_dev"
  | "discovered"
  | "inferred"
  | "override"
  | "litellm"
  | "registry"

/**
 * What a provider gives away, when it gives anything away.
 *
 * A zero in either token field means uncapped or unquantified, never "no
 * allowance" — the wire format omits `omitempty` precisely so a real zero
 * stays distinguishable from an absent field.
 */
export type FreeTier = {
  free_type: string
  monthly_tokens: number
  credit_tokens: number
  pool_key: string
  tos: string
  /** The router is refusing at least one provider serving this model over
   *  this tier, and allowing unsanctioned tiers on that provider lifts it.
   *  Folded by the server: the opt-in is provider state, and a model row has
   *  no provider to read it from. */
  opt_in_required: boolean
}

/** How confident the catalogue is in a price. Mirrors catalog.Grade. */
export type PriceGrade = "measured" | "declared" | "indexed" | "guessed"

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
  /** Null when the catalog has no price. Zero would claim the model is free. */
  pricing: Pricing | null
  /** One free-tier record folded from every provider serving the model, the
   *  unsanctioned one winning where they disagree — that is the tier the
   *  router acts on. Null when the curated catalogue documents no free tier
   *  here at all, which a withdrawn one is not: that record still travels. */
  free_tier: FreeTier | null
  publisher?: string
  merge_source: MergeSource
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

export type Healthz = {
  config_valid: boolean
  warnings: string[]
  uptime: string
  version: string
  log_records_dropped: number
  log_records_written: number
  config_error?: string
}

export type DiscoveryHealthRow = {
  provider_id: string
  total: number
  live: number
  stale: number
  removed_upstream: number
  max_missing_streak: number
  /** Models the last sweep dropped before recording it. Non-zero only under
   *  the free-models filter, and the only thing that tells a provider serving
   *  nothing apart from one serving nothing free. */
  filtered_out: number
}

export type DiscoveryHealthResponse = { providers: DiscoveryHealthRow[] }

export type ProviderHealthResponse = { providers: BreakerEntry[] }

/** POST /api/providers/{id}/test. A rejected credential is a 200 with
 *  ok:false, not an error response — the button exists to discover exactly
 *  that outcome. */
export type ProbeResult = {
  ok: boolean
  probe: string
  latency_ms: number
  model_count?: number
  error?: string
}

// --- playground ---

export type AuxSurface =
  | "count" | "embeddings" | "rerank" | "moderations"
  | "images" | "speech" | "transcriptions"

export type PlaygroundMessage = { role: string; content: string }

export type PlaygroundDialect = "openai" | "anthropic" | "gemini"

export type PlaygroundChatBody = {
  model: string
  prompt?: string
  system?: string
  messages?: PlaygroundMessage[]
  temperature?: number
  max_tokens?: number
  top_p?: number
  top_k?: number
  stop?: string[]
  response_schema?: unknown
  reasoning_effort?: string
  reasoning_budget?: number
  tools?: Record<string, unknown>[]
  stream?: boolean
  dialect?: PlaygroundDialect
}

/** A saved request configuration. Distinct from `Preset`, which is a provider
 *  preset shipped with the binary and never written by an operator. */
export type PlaygroundPreset = {
  id: string
  name: string
  dialect: PlaygroundDialect
  model: string
  /** The console's own settings, stored and returned untouched. */
  config: unknown
  created_at: string
  updated_at: string
}

/** A saved Chat-mode conversation, as the history rail lists it. */
export type PlaygroundConversation = {
  id: string
  title: string
  dialect: PlaygroundDialect
  model: string
  /** The console's own settings, stored and returned untouched. */
  config: unknown
  /** The most recent user turn, truncated by the server. The rail draws one
   *  line of it beneath the title. */
  preview: string
  created_at: string
  updated_at: string
}

export type PlaygroundPresetsResponse = { presets: PlaygroundPreset[] }

export type PlaygroundConversationsResponse = { conversations: PlaygroundConversation[] }

export type PlaygroundStoredTurn = {
  seq: number
  role: string
  content: string
  /** Empty when the turn has no trace, which is ordinary: a turn can be stored
   *  before the log writer's batch lands, and the log's retention sweep
   *  outlives plenty of conversations. */
  request_id: string
  created_at: string
}

export type PlaygroundConversationDetail = PlaygroundConversation & {
  messages: PlaygroundStoredTurn[]
}

export type AuxBody = {
  surface: AuxSurface
  model?: string
  body?: Record<string, unknown>
  file_b64?: string
  filename?: string
}

// --- derived, not mirrored ---

/** No handler emits this shape. POST /api/playground/count answers with a
 *  dialect-specific body — `{"input_tokens": n}` for the OpenAI-style count,
 *  `{"totalTokens": n}` for Gemini's — and reports whether the count is
 *  exact or approximated in the `X-Darkrouter-Estimated` response header,
 *  never in the body. `CountResult` is what `readCount(res)` builds
 *  after reading both: the normalised result a client assembles, not a
 *  server claim. */
export type CountResult = { tokens: number; estimated: boolean }

/** A named filter set for the requests screen. Lives in localStorage, not on
 *  the server, so it is declared here rather than mirrored from Go — shared
 *  between the screen and its persistence module, and a second definition of
 *  the same shape is exactly how the two would drift. */
export type SavedView = { name: string; filters: Record<string, string> }

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
  playground: { save_conversations: boolean }
  media: { inline: boolean }
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

export type ProxyTokensResponse = { tokens: ProxyToken[] }

export type Session = {
  id: string
  prefix: string
  created_at: string
  expires_at: string
  current: boolean
}

export type SessionsResponse = { sessions: Session[] }
