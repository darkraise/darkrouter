import type { ThemeConfig } from "darkraise-ui/theme"

/**
 * Warm Console, expressed as axis settings rather than an override block.
 *
 * Everything below is a value darkraise-ui already emits: 6.5.0 exposed the
 * warm neutral ramps and added the coral accent precisely so this file could
 * be a config block. A rule that has to be written in CSS instead belongs in
 * globals.css with a comment saying what the library cannot express.
 */
export const themeConfig: ThemeConfig = {
  defaults: {
    // Brand. Coral is position and primary action only, never state, so it
    // never joins amber and red in the routing ladder's gutter.
    accentColor: "coral",
    // Hue 36: the warm ground the language is built on. Slate is the cool
    // baseline this console deliberately is not.
    surfaceColor: "sepia",
    mode: "system",
    density: "comfortable",
    radius: "rounded",
    elevation: "low",
    // Pinned. Every other step leaves button labels under AA against their own
    // fill -- 6.5.0's changelog records the measurements under Known
    // limitations -- and calm is the one that clears it.
    accentIntensity: "calm",
    preset: "default",
    backgroundStyle: "solid",
    backgroundIntensity: "neutral",
    buttonElevation: "flat",
    controlDepth: "flush",
    surfaceIntensity: "balanced",
    fontSize: "medium",
    outerGlow: "none",
    innerGlow: "none",
    gradientPattern: "blobs",
  },
  switcher: {
    enabled: true,
    // Only the two axes an operator has a reason to change: mode, because a
    // room's light changes, and accentColor, because it is a preference.
    //
    // The rest are the Warm Console identity rather than preferences -- the
    // warm ground the language is built on, the pinned accent intensity that
    // is the one step keeping button labels above AA, the density and radius
    // the screens were drawn against. Exposing them offered an operator a
    // dozen ways to make the console worse and one to make it theirs.
    //
    // Worth knowing before turning accentColor: coral is position and primary
    // action only, and the routing ladder and provider pips sit beside it in
    // amber, green and red. An accent moved into that range makes "brand" and
    // "state" the same colour, and the ladder is then legible only by shape.
    //
    // A hidden axis keeps whatever the defaults above say, and any value a
    // browser stored while it was still offered is cleared on load -- see
    // theme-storage.ts, which reads this same block so the two cannot drift.
    axes: {
      mode: true,
      accentColor: true,
      density: false,
      surfaceColor: false,
      preset: false,
      backgroundStyle: false,
      backgroundIntensity: false,
      gradientPattern: false,
      elevation: false,
      buttonElevation: false,
      controlDepth: false,
      surfaceIntensity: false,
      radius: false,
      fontSize: false,
      accentIntensity: false,
      outerGlow: false,
      innerGlow: false,
      presetAxes: false,
    },
  },
}
