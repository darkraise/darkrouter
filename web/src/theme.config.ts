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
    // Every axis is exposed. The identity still ships as the defaults above,
    // so a console that has never been touched looks the way it was designed;
    // what changed is that an operator is now trusted to change it.
    //
    // The one axis worth knowing about before turning it: accentColor. Coral
    // is position and primary action only, and the routing ladder and the
    // provider pips sit beside it in amber, green and red. An accent moved
    // into that range makes "brand" and "state" the same colour, and the
    // ladder is then legible only by shape.
    axes: {
      mode: true,
      density: true,
      accentColor: true,
      surfaceColor: true,
      preset: true,
      backgroundStyle: true,
      backgroundIntensity: true,
      gradientPattern: true,
      elevation: true,
      buttonElevation: true,
      controlDepth: true,
      surfaceIntensity: true,
      radius: true,
      fontSize: true,
      accentIntensity: true,
      outerGlow: true,
      innerGlow: true,
      presetAxes: true,
    },
  },
}
