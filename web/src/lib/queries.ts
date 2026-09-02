import { useQuery, type UseQueryOptions } from "@tanstack/react-query"
import { api, POLL } from "./api"
import type {
  Aliases,
  BreakerEntry,
  ConfigResponse,
  CatalogResponse,
  DiscoveryHealthResponse,
  Overview,
  PlaygroundConversation,
  PlaygroundConversationDetail,
  PlaygroundConversationsResponse,
  PlaygroundPreset,
  PlaygroundPresetsResponse,
  PolicyBlock,
  PresetsResponse,
  ProviderHealthResponse,
  ProvidersResponse,
  ProxyToken,
  ProxyTokensResponse,
  RequestPage,
  RequestTrace,
  Session,
  SessionsResponse,
  UsageDimension,
  UsageResponse,
} from "./api-types"

/**
 * One key factory, so a cache invalidation names the same thing the fetch did.
 *
 * Raw path strings inline in `useQuery` meant a mutation had to guess which
 * key a screen used, and a typo produced a stale table rather than an error.
 */
export const keys = {
  authStatus: ["auth-status"] as const,
  overview: ["overview"] as const,
  requests: (filters: Record<string, string>) => ["requests", filters] as const,
  trace: (id: string) => ["requests", "trace", id] as const,
  usage: (dimension?: UsageDimension, days?: number) =>
    ["usage", dimension ?? "day", days ?? 0] as const,
  providers: ["providers"] as const,
  presets: ["presets"] as const,
  playgroundPresets: ["playground-presets"] as const,
  // "list" rather than the bare prefix: TanStack matches by prefix, so a bare
  // ["playground-conversations"] would make every write to the rail invalidate
  // whichever conversation is open as well.
  playgroundConversations: ["playground-conversations", "list"] as const,
  playgroundConversation: (id: string) => ["playground-conversations", id] as const,
  models: ["models"] as const,
  aliases: ["aliases"] as const,
  config: ["config"] as const,
  health: ["health", "providers"] as const,
  proxyTokens: ["proxy-tokens"] as const,
  sessions: ["sessions"] as const,
  discovery: ["health", "discovery"] as const,
  policy: ["policy"] as const,
  override: (provider: string, model: string) =>
    ["models", "override", provider, model] as const,
} as const

/**
 * Poll cadence lives here rather than at each call site, so §5's intervals
 * cannot drift per screen: 3s for the overview and the requests first page,
 * 30s for the catalog and usage, and nothing while the tab is hidden — the
 * query client sets `refetchIntervalInBackground: false` for that.
 */
type Extra<T> = Omit<UseQueryOptions<T, Error, T>, "queryKey" | "queryFn">

export function useOverview(extra?: Extra<Overview>) {
  return useQuery({
    queryKey: keys.overview,
    queryFn: ({ signal }) => api.get<Overview>("/api/overview", { signal }),
    refetchInterval: POLL.fast,
    ...extra,
  })
}

export function useRequests(
  filters: Record<string, string>,
  extra?: Extra<RequestPage>,
) {
  const query = new URLSearchParams(
    Object.entries(filters).filter(([, v]) => v !== ""),
  ).toString()
  return useQuery({
    queryKey: keys.requests(filters),
    queryFn: ({ signal }) => api.get<RequestPage>(`/api/requests${query ? `?${query}` : ""}`, { signal }),
    // The first page only. A cursor-paged view that repolled every three
    // seconds would reshuffle under the reader mid-scroll.
    refetchInterval: POLL.fast,
    ...extra,
  })
}

export function useTrace(id: string, extra?: Extra<RequestTrace>) {
  return useQuery({
    queryKey: keys.trace(id),
    queryFn: ({ signal }) => api.get<RequestTrace>(`/api/requests/${id}`, { signal }),
    // A finished request does not change, so this one never polls.
    ...extra,
  })
}

type UsageOpts = { dimension?: UsageDimension; days?: number }

export function useUsage(
  opts?: UsageDimension | UsageOpts,
  extra?: Extra<UsageResponse>,
) {
  const { dimension, days } =
    typeof opts === "string" ? { dimension: opts, days: undefined } : (opts ?? {})
  const params = new URLSearchParams()
  if (dimension) params.set("group_by", dimension)
  if (days) params.set("days", String(days))
  const query = params.toString()
  return useQuery({
    queryKey: keys.usage(dimension, days),
    queryFn: ({ signal }) => api.get<UsageResponse>(`/api/usage${query ? `?${query}` : ""}`, { signal }),
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function useProviders(extra?: Extra<ProvidersResponse>) {
  return useQuery({
    queryKey: keys.providers,
    queryFn: ({ signal }) => api.get<ProvidersResponse>("/api/providers", { signal }),
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function usePresets(extra?: Extra<PresetsResponse>) {
  return useQuery({
    queryKey: keys.presets,
    queryFn: ({ signal }) => api.get<PresetsResponse>("/api/presets", { signal }),
    // Shipped with the binary: it cannot change while the tab is open.
    staleTime: Infinity,
    ...extra,
  })
}

/**
 * The list endpoints answer `{presets: [...]}`, `{sessions: [...]}` and so on
 * rather than a bare array, and the wrapper is unpacked here so a screen
 * reads the list it asked for and the wire shape stays one edit.
 */
export function usePlaygroundPresets(extra?: Extra<PlaygroundPreset[]>) {
  return useQuery({
    queryKey: keys.playgroundPresets,
    queryFn: async ({ signal }) =>
      (await api.get<PlaygroundPresetsResponse>("/api/playground/presets", { signal })).presets,
    ...extra,
  })
}

export function usePlaygroundConversations(extra?: Extra<PlaygroundConversation[]>) {
  return useQuery({
    queryKey: keys.playgroundConversations,
    queryFn: async ({ signal }) =>
      (
        await api.get<PlaygroundConversationsResponse>("/api/playground/conversations", {
          signal,
        })
      ).conversations,
    ...extra,
  })
}

export function usePlaygroundConversation(
  id: string,
  extra?: Extra<PlaygroundConversationDetail>,
) {
  return useQuery({
    queryKey: keys.playgroundConversation(id),
    queryFn: ({ signal }) =>
      api.get<PlaygroundConversationDetail>(`/api/playground/conversations/${id}`, { signal }),
    ...extra,
  })
}

export function useModels(extra?: Extra<CatalogResponse>) {
  return useQuery({
    queryKey: keys.models,
    queryFn: ({ signal }) => api.get<CatalogResponse>("/api/models", { signal }),
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function useAliases(extra?: Extra<Aliases>) {
  return useQuery({
    queryKey: keys.aliases,
    queryFn: ({ signal }) => api.get<Aliases>("/api/aliases", { signal }),
    ...extra,
  })
}

export function useConfig(extra?: Extra<ConfigResponse>) {
  return useQuery({
    queryKey: keys.config,
    queryFn: ({ signal }) => api.get<ConfigResponse>("/api/config", { signal }),
    ...extra,
  })
}

export function useProviderHealth(extra?: Extra<BreakerEntry[]>) {
  return useQuery({
    queryKey: keys.health,
    queryFn: async ({ signal }) =>
      (await api.get<ProviderHealthResponse>("/api/health/providers", { signal })).providers,
    // A cooldown expires on its own, so this follows the overview's cadence
    // rather than the catalog's.
    refetchInterval: POLL.fast,
    ...extra,
  })
}

export function useProxyTokens(extra?: Extra<ProxyToken[]>) {
  return useQuery({
    queryKey: keys.proxyTokens,
    queryFn: async ({ signal }) =>
      (await api.get<ProxyTokensResponse>("/api/proxy-tokens", { signal })).tokens,
    ...extra,
  })
}

export function useSessions(extra?: Extra<Session[]>) {
  return useQuery({
    queryKey: keys.sessions,
    queryFn: async ({ signal }) =>
      (await api.get<SessionsResponse>("/api/sessions", { signal })).sessions,
    ...extra,
  })
}

export function useDiscoveryHealth(extra?: Extra<DiscoveryHealthResponse>) {
  return useQuery({
    queryKey: keys.discovery,
    queryFn: ({ signal }) => api.get<DiscoveryHealthResponse>("/api/health/discovery", { signal }),
    // A sweep interval, not a request interval: this changes when discovery
    // runs, which is minutes apart.
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function usePolicy(extra?: Extra<PolicyBlock>) {
  return useQuery({
    queryKey: keys.policy,
    queryFn: ({ signal }) => api.get<PolicyBlock>("/api/policy", { signal }),
    ...extra,
  })
}
