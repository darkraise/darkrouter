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

export function portOf(p: Preset): string {
  const u = new URL(p.base_url)
  if (u.port) return u.port
  return u.protocol === "https:" ? "443" : "80"
}

/**
 * Reduces what was typed in the Host box to a bare host.
 *
 * Pasting the whole endpoint into a box labelled Host is the likeliest wrong
 * input there is, and what was meant is unambiguous, so it is read rather than
 * rejected.
 */
export function normalizeHost(raw: string): string {
  let h = raw.trim()
  h = h.replace(/^[a-z][a-z0-9+.-]*:\/\//i, "")
  h = h.replace(/\/.*$/, "")
  if (h.startsWith("[")) {
    const end = h.indexOf("]")
    return end === -1 ? h : h.slice(0, end + 1)
  }
  // One colon is a host:port that the port box owns. Two or more is an IPv6
  // literal, where every colon belongs to the address itself.
  if ((h.match(/:/g) ?? []).length === 1) h = h.replace(/:\d+$/, "")
  return h
}

/**
 * The endpoint the gateway will call, or null when the form cannot yet make
 * one. Host and port replace the preset's authority; its path is kept, which
 * is what lets lemonade's /api/v1 and docker-model-runner's /engines/v1 work
 * without either being named here.
 */
export function composeBaseUrl(p: Preset, host: string, port: string): string | null {
  const h = normalizeHost(host)
  if (h === "") return null
  if (!/^\d+$/.test(port)) return null
  const n = Number(port)
  if (n < 1 || n > 65535) return null
  const u = new URL(p.base_url)
  const authority = h.includes(":") && !h.startsWith("[") ? `[${h}]` : h
  return `${u.protocol}//${authority}:${n}${u.pathname}`
}
