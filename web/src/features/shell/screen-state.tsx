import { Banner, Button, Skeleton } from "darkraise-ui"

/**
 * The two states every screen shares before it has data.
 *
 * `LoadingRows` is the shape of a list while its first response is
 * outstanding: a blank pane cannot be told apart from a gateway that is
 * down. `LoadError` is the same banner on every screen, with the message
 * the request actually failed with and one way to ask again.
 */
export function LoadingRows({ rows = 4, className }: { rows?: number; className?: string }) {
  return (
    <div className={className ?? "flex flex-col gap-2"} aria-hidden="true">
      {Array.from({ length: rows }, (_, i) => (
        <Skeleton key={i} className="h-9 w-full" />
      ))}
    </div>
  )
}

export function LoadError({
  what,
  error,
  onRetry,
  className,
}: {
  /** The thing that did not load, as the screen names it: "The usage". */
  what: string
  error: unknown
  onRetry: () => void
  className?: string
}) {
  const message = error instanceof Error ? error.message : String(error)
  return (
    <Banner
      variant="destructive"
      className={className}
      action={
        <Button size="sm" variant="secondary" onClick={onRetry}>
          Try again
        </Button>
      }
    >
      <p className="text-sm font-medium">{what} did not load</p>
      <p className="mt-1 text-sm">{message}</p>
    </Banner>
  )
}
