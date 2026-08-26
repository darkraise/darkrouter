import { Component, type ReactNode } from "react"

/**
 * Keeps one screen's failure inside that screen.
 *
 * Without it a single unexpected shape — a field an older gateway does not
 * send, an endpoint that answered with an object where the screen expected a
 * list — unmounts the whole tree, sidebar included, and the console reads as
 * completely broken rather than as one panel that could not render. The rail
 * is how an operator gets to a screen that still works, so it has to survive.
 */
export class ScreenBoundary extends Component<
  { children: ReactNode; onReset?: () => void },
  { error: Error | null }
> {
  state: { error: Error | null } = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  render() {
    if (!this.state.error) return this.props.children
    // Deliberately plain: no Card, no PageHeader, nothing that reads a
    // context. An error screen that depends on a provider is an error screen
    // that can fail for the same reason the screen it is reporting on did --
    // which is exactly how this fallback was first written, and it threw.
    return (
      <div className="p-6">
        <h1 className="text-lg font-semibold">This screen could not render</h1>
        <p className="mt-2 text-sm text-[hsl(var(--muted-foreground))]">
          The gateway is still running — only this screen failed.
        </p>
        <pre className="mt-4 overflow-x-auto rounded border border-[hsl(var(--border))] p-3 font-mono text-xs">
          {this.state.error.message}
        </pre>
      </div>
    )
  }
}
