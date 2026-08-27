import { Card } from "darkraise-ui"

/**
 * What a fresh install sees instead of a login it cannot pass.
 *
 * §12: an install with no password hash must explain itself. Showing the login
 * form would be worse than useless — every password is refused, and nothing on
 * screen says why or what to do about it.
 *
 * Deliberately context-free markup, like the screen boundary: this renders
 * before the router and the theme provider have anything to say.
 */
export function FirstRun() {
  return (
    <div className="mx-auto mt-24 max-w-xl px-6">
      <h1 className="text-lg font-semibold">Darkrouter is running</h1>
      <p className="mt-2 text-sm text-[hsl(var(--muted-foreground))]">
        The gateway is up and serving requests on its proxy port. The console
        has no admin password set, so there is nothing to log in with yet.
      </p>
      <Card className="mt-6 p-4">
        <p className="text-sm">Set one, then restart:</p>
        <pre className="mt-3 overflow-x-auto rounded bg-[hsl(var(--muted))] p-3 font-mono text-sm">
          {`darkrouter hash-password
export DARKROUTER_ADMIN_PASSWORD_HASH='<the output>'`}
        </pre>
        <p className="mt-3 text-sm text-[hsl(var(--muted-foreground))]">
          The proxy port is unaffected either way — this gates the console, not
          the gateway.
        </p>
      </Card>
    </div>
  )
}
