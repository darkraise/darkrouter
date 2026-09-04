/**
 * The storage key darkraise-ui reads for each switcher axis.
 *
 * The library stores every axis under its own key and falls back to the
 * config defaults only when the key is absent, so a value stored before an
 * axis was hidden keeps winning with no control left to change it back.
 *
 * `presetAxes` has no fixed key: it stores `theme-<preset>-<axis>`, which is
 * why the sweep below works by exclusion rather than by listing them.
 */
const AXIS_KEYS: Record<string, string> = {
  mode: "mode",
  accentColor: "theme-accent",
  surfaceColor: "theme-surface-color",
  preset: "theme-preset",
  backgroundStyle: "theme-bg-style",
  backgroundIntensity: "theme-bg-intensity",
  gradientPattern: "theme-gradient-pattern",
  density: "theme-density",
  elevation: "theme-elevation",
  buttonElevation: "theme-button-elevation",
  surfaceIntensity: "theme-surface-intensity",
  radius: "theme-radius",
  fontSize: "theme-font-size",
  accentIntensity: "theme-accent-intensity",
  controlDepth: "theme-control-depth",
  outerGlow: "theme-outer-glow",
  innerGlow: "theme-inner-glow",
}

const KNOWN_KEYS = new Set(Object.values(AXIS_KEYS))

/**
 * Removes the stored value for every axis the switcher does not offer, so the
 * shipped defaults are what an operator actually sees.
 *
 * Runs before the provider mounts: it reads storage in its state initialisers,
 * so clearing afterwards would need a second render to take effect.
 */
export function clearHiddenAxisStorage(axes: Record<string, boolean | undefined>): void {
  const keep = new Set<string>()
  for (const [axis, shown] of Object.entries(axes)) {
    if (shown && AXIS_KEYS[axis]) keep.add(AXIS_KEYS[axis])
  }
  const keepPresetAxes = axes.presetAxes === true

  try {
    for (const key of Object.keys(localStorage)) {
      if (key !== "mode" && !key.startsWith("theme-")) continue
      if (keep.has(key)) continue
      // Anything theme-prefixed that is not one of the fixed axis keys is a
      // per-preset axis value.
      if (keepPresetAxes && !KNOWN_KEYS.has(key)) continue
      localStorage.removeItem(key)
    }
  } catch {
    // A browser refusing storage has nothing stored to clear either.
  }
}
