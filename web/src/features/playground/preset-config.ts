import { emptyConfig, type PlaygroundConfig } from "./config"
import type { PlaygroundDialect } from "../../lib/api-types"

/**
 * A stored preset, reconstituted into pane state.
 *
 * Merged over the defaults rather than spread onto them, and only where the
 * stored value's type matches the default's. The missing-field half of blob
 * drift is what a plain spread fixes; the wrong-typed half is the one that
 * bites, because chatBody calls .split and .trim on these without checking, so
 * a value of the wrong type is a crash rather than a degraded setting. Keys the
 * config does not have are dropped here, which is what lets the writer below
 * spread safely.
 */
export function mergeStoredConfig(
  stored: unknown,
  model: string,
  dialect: PlaygroundDialect,
): PlaygroundConfig {
  const base = emptyConfig()
  const out: PlaygroundConfig = { ...base, model, dialect }
  if (typeof stored !== "object" || stored === null || Array.isArray(stored)) return out

  const record = stored as Record<string, unknown>
  const writable = out as unknown as Record<string, unknown>
  for (const key of Object.keys(base)) {
    if (key === "model" || key === "dialect") continue
    const value = record[key]
    if (typeof value === typeof (base as unknown as Record<string, unknown>)[key]) {
      writable[key] = value
    }
  }
  return out
}

/** What goes in the blob: everything but the two columns. */
export function toStoredConfig(config: PlaygroundConfig): Record<string, unknown> {
  const { model: _model, dialect: _dialect, ...rest } = config
  return rest
}
