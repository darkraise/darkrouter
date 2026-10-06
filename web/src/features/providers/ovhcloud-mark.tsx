/**
 * OVHcloud's logomark: the two shapes at the head of its wordmark, alone.
 *
 * `@lobehub/icons` has no OVHcloud mark, and the logo file the preset
 * generator copied in was the whole horizontal wordmark in fixed navy ink. In
 * a square tile that is a line of text about four pixels tall, and on the dark
 * canvas it is navy on near-black. The glyph is square enough to fill a tile,
 * and drawn in currentColor it takes whatever ink its brand tile gives it.
 *
 * The path is the glyph's own, lifted unchanged from the wordmark; only the
 * viewBox is new, squared around it. Listed in brand-marks.ts, so the preset
 * generator stops copying the wordmark back in.
 */
export function OvhcloudMark({ size = 24 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="-3 -23 109 109"
      fill="currentColor"
      aria-hidden="true"
      focusable="false"
    >
      <path
        fillRule="evenodd"
        d="M41.06404 62.90648H11.91689C-1.87443 46.03088-3.88064 22.4168 6.86649 3.45635l18.90473 32.83878L46.61337.00177h30.68416L41.08391 62.88721l-.01987.01927zM97.52103 3.59542c10.5236 18.95112 8.55232 42.3844-4.99161 59.31106H63.9416l8.80445-15.55176H61.10727l13.7154-24.1946h11.71837l10.97999-19.5647"
      />
    </svg>
  )
}
