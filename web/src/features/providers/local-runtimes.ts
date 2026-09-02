import type { Preset } from "../../lib/api-types"

/** Mirrors the backend's rule at internal/catalog/preset.go:160. Localness is
 *  read off the address rather than a flag, so a runtime added to presets.yaml
 *  appears here with no change to the console. */
const LOOPBACK = /^https?:\/\/(localhost|127\.0\.0\.1|\[::1\]|0\.0\.0\.0)(:|\/|$)/

export function isLocalPreset(p: Preset): boolean {
  return LOOPBACK.test(p.base_url)
}

export function localRuntimes(presets: Preset[]): Preset[] {
  return presets.filter(isLocalPreset).sort((a, b) => a.name.localeCompare(b.name))
}

/** Mirrors the backend's check at internal/admin/providers.go:33, so a URL the
 *  server would reject is caught before a provider row is created for it. */
export function validBaseUrl(raw: string): boolean {
  let u: URL
  try {
    u = new URL(raw.trim())
  } catch {
    return false
  }
  return (u.protocol === "http:" || u.protocol === "https:") && u.hostname !== ""
}

const LOOPBACK_HOSTS = new Set(["localhost", "127.0.0.1", "::1", "0.0.0.0"])

/** Whether an address names this machine from the caller's own point of view,
 *  which inside a container is the container and not the operator's desktop. */
export function isLoopbackUrl(raw: string): boolean {
  if (!validBaseUrl(raw)) return false
  return LOOPBACK_HOSTS.has(new URL(raw.trim()).hostname.replace(/^\[|\]$/g, ""))
}

/**
 * The same address reached through a different host.
 *
 * Scheme, port and path are the runtime's, and only the host is the operator's
 * problem — which is the whole of the container case, where every preset's
 * localhost has to become the gateway's name and nothing else changes.
 */
export function withHost(raw: string, host: string): string {
  const u = new URL(raw.trim())
  const authority = host.includes(":") && !host.startsWith("[") ? `[${host}]` : host
  return `${u.protocol}//${authority}${u.port ? `:${u.port}` : ""}${u.pathname}${u.search}`
}
