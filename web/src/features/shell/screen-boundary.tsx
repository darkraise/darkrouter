import { Component, type ReactNode } from "react"

/**
 * What a screen shows in place of itself when it could not render.
 *
 * Deliberately plain: no Card, no PageHeader, nothing that reads a context.
 * An error screen that depends on a provider is an error screen that can
 * fail for the same reason the screen it is reporting on did -- which is
 * exactly how this fallback was first written, and it threw.
 */
export function ScreenError({ error, reset }: { error: unknown; reset?: () => void }) {
  const message = error instanceof Error ? error.message : String(error)
  return (
    <div className="p-6">
      <h1 className="text-lg font-semibold">This screen could not render</h1>
      <p className="mt-2 text-sm text-[hsl(var(--muted-foreground))]">
        The gateway is still running — only this screen failed.
      </p>
      <pre className="mt-4 overflow-x-auto rounded border border-[hsl(var(--border))] p-3 font-mono text-sm">
        {message}
      </pre>
      {reset && (
        <button
          type="button"
          onClick={reset}
          className="mt-4 rounded border border-[hsl(var(--border))] px-3 py-1.5 text-sm hover:bg-[hsl(var(--muted))] focus-visible:outline focus-visible:outline-2 focus-visible:outline-[hsl(var(--focus-ring))]"
        >
          Try again
        </button>
      )}
    </div>
  )
}

/**
 * Keeps one screen's failure inside that screen.
 *
 * Without it a single unexpected shape — a field an older gateway does not
 * send, an endpoint that answered with an object where the screen expected a
 * list — unmounts the whole tree, sidebar included, and the console reads as
 * completely broken rather than as one panel that could not render. The rail
 * is how an operator gets to a screen that still works, so it has to survive.
 *
 * The caller keys it by pathname, so navigating away from a failed screen
 * mounts a fresh boundary rather than carrying the error to the next one.
 */
export class ScreenBoundary extends Component<
  { children: ReactNode; onReset?: () => void },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  reset = () => {
    this.setState({ error: null })
    this.props.onReset?.()
  }

  render() {
    if (!this.state.error) return this.props.children
    return <ScreenError error={this.state.error} reset={this.reset} />
  }
}
