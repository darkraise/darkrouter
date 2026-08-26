/**
 * §3.5's mark: a graticule with one branch taken.
 *
 * Geometry transcribed from `docs/ux/mockups/fragments/15-login.html`, which
 * §10 makes the contract. Rects rather than lines and `shapeRendering` crisp,
 * so a 1px hairline lands on a pixel boundary at every size instead of
 * softening into two grey rows.
 */
export function IdentityMark({ size = 96 }: { size?: number }) {
  const line = "hsl(var(--legend))"
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      role="img"
      aria-label="darkrouter"
      shapeRendering="crispEdges"
    >
      <rect x="2.5" y="2.5" width="19" height="19" fill="none" stroke={line} strokeWidth="1" />
      {/* Three segments, not one rule: a continuous spine would fill the
          hollow squares and render two skip marks as served. */}
      <rect className="spine-seg" x="11.5" y="2" width="1" height="3.5" fill={line} />
      <rect className="spine-seg" x="11.5" y="8.5" width="1" height="7" fill={line} />
      <rect className="spine-seg" x="11.5" y="18.5" width="1" height="3.5" fill={line} />
      <rect x="2" y="6.5" width="10" height="1" fill={line} />
      {/* The branch that was taken: it crosses the spine and reaches the pip. */}
      <rect x="2" y="11.5" width="17" height="1" fill={line} />
      <rect x="2" y="16.5" width="10" height="1" fill={line} />
      <rect x="11" y="6" width="2" height="2" fill="none" stroke={line} strokeWidth="1" />
      <rect x="11" y="16" width="2" height="2" fill="none" stroke={line} strokeWidth="1" />
      <rect className="pip" x="18" y="10" width="4" height="4" fill="hsl(var(--primary))" />
    </svg>
  )
}
