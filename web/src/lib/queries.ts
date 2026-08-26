import { useQuery, type UseQueryOptions } from "@tanstack/react-query"
import { api, POLL } from "./api"
import type {
  Aliases,
  BreakerEntry,
  ConfigResponse,
  CatalogResponse,
  Overview,
  PresetsResponse,
  ProvidersResponse,
  ProxyToken,
  RequestPage,
  RequestTrace,
  Session,
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
  usage: (dimension?: UsageDimension) => ["usage", dimension ?? "day"] as const,
  providers: ["providers"] as const,
  presets: ["presets"] as const,
  models: ["models"] as const,
  aliases: ["aliases"] as const,
  config: ["config"] as const,
  health: ["health", "providers"] as const,
  proxyTokens: ["proxy-tokens"] as const,
  sessions: ["sessions"] as const,
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
    queryFn: () => api.get<Overview>("/api/overview"),
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
    queryFn: () => api.get<RequestPage>(`/api/requests${query ? `?${query}` : ""}`),
    // The first page only. A cursor-paged view that repolled every three
    // seconds would reshuffle under the reader mid-scroll.
    refetchInterval: POLL.fast,
    ...extra,
  })
}

export function useTrace(id: string, extra?: Extra<RequestTrace>) {
  return useQuery({
    queryKey: keys.trace(id),
    queryFn: () => api.get<RequestTrace>(`/api/requests/${id}`),
    // A finished request does not change, so this one never polls.
    ...extra,
  })
}

export function useUsage(dimension?: UsageDimension, extra?: Extra<UsageResponse>) {
  const query = dimension ? `?group_by=${dimension}` : ""
  return useQuery({
    queryKey: keys.usage(dimension),
    queryFn: () => api.get<UsageResponse>(`/api/usage${query}`),
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function useProviders(extra?: Extra<ProvidersResponse>) {
  return useQuery({
    queryKey: keys.providers,
    queryFn: () => api.get<ProvidersResponse>("/api/providers"),
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function usePresets(extra?: Extra<PresetsResponse>) {
  return useQuery({
    queryKey: keys.presets,
    queryFn: () => api.get<PresetsResponse>("/api/presets"),
    // Shipped with the binary: it cannot change while the tab is open.
    staleTime: Infinity,
    ...extra,
  })
}

export function useModels(extra?: Extra<CatalogResponse>) {
  return useQuery({
    queryKey: keys.models,
    queryFn: () => api.get<CatalogResponse>("/api/models"),
    refetchInterval: POLL.slow,
    ...extra,
  })
}

export function useAliases(extra?: Extra<Aliases>) {
  return useQuery({
    queryKey: keys.aliases,
    queryFn: () => api.get<Aliases>("/api/aliases"),
    ...extra,
  })
}

export function useConfig(extra?: Extra<ConfigResponse>) {
  return useQuery({
    queryKey: keys.config,
    queryFn: () => api.get<ConfigResponse>("/api/config"),
    ...extra,
  })
}

export function useProviderHealth(extra?: Extra<BreakerEntry[]>) {
  return useQuery({
    queryKey: keys.health,
    queryFn: () => api.get<BreakerEntry[]>("/api/health/providers"),
    // A cooldown expires on its own, so this follows the overview's cadence
    // rather than the catalog's.
    refetchInterval: POLL.fast,
    ...extra,
  })
}

export function useProxyTokens(extra?: Extra<ProxyToken[]>) {
  return useQuery({
    queryKey: keys.proxyTokens,
    queryFn: () => api.get<ProxyToken[]>("/api/proxy-tokens"),
    ...extra,
  })
}

export function useSessions(extra?: Extra<Session[]>) {
  return useQuery({
    queryKey: keys.sessions,
    queryFn: () => api.get<Session[]>("/api/sessions"),
    ...extra,
  })
}
