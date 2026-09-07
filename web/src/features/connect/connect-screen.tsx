import { useState } from "react"
import { Link } from "@tanstack/react-router"
import {
  Badge,
  Button,
  Card,
  Input,
  Label,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  toast,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { ConfirmButton } from "../shell/confirm-button"
import { useApiMutation } from "../../lib/mutations"
import { keys, useConfig, useModels, useProxyTokens } from "../../lib/queries"
import { dateOnly, dateTime, zoneLabel } from "../../lib/format"
import type { ConfigResponse, Model, ProxyToken } from "../../lib/api-types"
import { EmptyState } from "../shell/empty-state"
import { LoadError, LoadingRows } from "../shell/screen-state"
import { baseUrlFor, snippetFor, TOOLS, type Tool } from "./snippets"

const DIALECTS = ["anthropic", "openai", "gemini"] as const
type Dialect = (typeof DIALECTS)[number]

const DIALECT_LABEL: Record<Dialect, string> = {
  anthropic: "Anthropic",
  openai: "OpenAI",
  gemini: "Gemini",
}

// Which base URL each tab's snippet points at. Cursor and the OpenAI SDK
// speak the OpenAI dialect even though neither is OpenAI itself.
const DIALECT_OF: Record<Tool, Dialect> = {
  "claude-code": "anthropic",
  codex: "openai",
  cursor: "openai",
  "openai-sdk": "openai",
  "anthropic-sdk": "anthropic",
}

const TOOL_LABEL: Record<Tool, string> = {
  "claude-code": "Claude Code",
  codex: "Codex",
  cursor: "Cursor",
  "openai-sdk": "OpenAI SDK",
  "anthropic-sdk": "Anthropic SDK",
}

/**
 * The two addresses this gateway answers on, which are not the same question.
 *
 * `lan` is where the console itself was reached, with proxy_listen's port
 * swapped in. It is a guess, and the port is the part that is guessed: what a
 * container published or a proxy rewrote is invisible from inside the process.
 * It is still worth showing, because the plain LAN deployment is the common
 * one and there the guess is right.
 *
 * `public` is server.public_url, and it is present only when an operator set
 * one. It is not derived from anything -- it is the operator stating what the
 * outside world dials, which is the only way that can be known.
 *
 * Both are returned rather than one chosen, because a gateway with a domain
 * still answers on the LAN, and an operator on the LAN wants the address that
 * does not leave the building.
 */
export type ConnectOrigins = { lan: string; public?: string }

/** The operator-stated public origin, or `""` when none is stored. */
export function publicOrigin(cfg: Pick<ConfigResponse, "values">): string {
  return cfg.values["server.public_url"] ?? ""
}

export function originsFor(
  location: Pick<Location, "origin" | "hostname" | "protocol">,
  proxyListen: string,
  adminListen: string,
  publicURL?: string,
): ConnectOrigins {
  const portOf = (listen: string) => listen.slice(listen.lastIndexOf(":") + 1)
  // Matching ports cannot be one listener in front of both -- the process
  // binds each separately and the second bind would fail -- so this is two
  // bind addresses sharing a port. The page origin is then the same host and
  // port the swap below would build, minus an explicit :80 or :443 that
  // location.origin already omits.
  const lan =
    portOf(proxyListen) === portOf(adminListen)
      ? location.origin
      : `${location.protocol}//${location.hostname}:${portOf(proxyListen)}`
  const configured = publicURL?.trim().replace(/\/+$/, "")
  return configured ? { lan, public: configured } : { lan }
}

/**
 * navigator.clipboard is absent from jsdom by default, and from any
 * real browser on an insecure origin — the plain-HTTP LAN deployment this
 * console typically runs behind. Resolving false rather than letting the
 * rejection propagate lets a copy button fall back to "select it yourself"
 * instead of the click silently doing nothing.
 */
export async function copyToClipboard(text: string): Promise<boolean> {
  if (!navigator.clipboard?.writeText) return false
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

export function liveSurfaces(models: Model[]): string[] {
  const seen = new Set<string>()
  for (const m of models) for (const s of m.surfaces) seen.add(s)
  return [...seen].sort()
}

// The LAN row is derived, never configured, so the caveat belongs beside it
// permanently rather than only while nothing else is set.
const LAN_NOTE =
  "Worked out from this page's address and the gateway's listen port."

function DialectRows({ origin }: { origin: string }) {
  return (
    <div className="flex flex-col gap-2">
      {DIALECTS.map((dialect) => {
        const url = baseUrlFor(origin, dialect)
        return (
          <div key={dialect} className="flex items-center gap-3">
            <span className="w-24 shrink-0 text-sm text-[hsl(var(--muted-foreground))]">
              {DIALECT_LABEL[dialect]}
            </span>
            <code className="flex-1 overflow-x-auto rounded bg-[hsl(var(--muted))] px-2 py-1 font-mono text-sm">
              {url}
            </code>
            <CopyButton text={url} />
          </div>
        )
      })}
    </div>
  )
}

function CopyButton({ text }: { text: string }) {
  return (
    <Button
      size="sm"
      variant="outline"
      onClick={async () => {
        const ok = await copyToClipboard(text)
        if (ok) toast.success("Copied")
        // No clipboard on this origin — the text is still selectable in
        // place, so this is a hint rather than a dead end.
        else toast.error("Couldn't copy — select the text and copy it manually")
      }}
    >
      Copy
    </Button>
  )
}

export function ConnectScreen() {
  const [name, setName] = useState("")
  const [minted, setMinted] = useState<ProxyToken | null>(null)
  // Which of the two addresses the snippets are written against. Only ever
  // offered when there are two; a copied snippet that names the wrong side of
  // the router is the failure this whole screen exists to prevent.
  const [scope, setScope] = useState<"public" | "lan">("public")
  const tokens = useProxyTokens()
  const config = useConfig()
  const models = useModels()

  const create = useApiMutation({
    mutationFn: (n: string) => api.post<ProxyToken>("/api/proxy-tokens", { name: n }),
    invalidates: [keys.proxyTokens],
    onSuccess: (token) => {
      setMinted(token)
      setName("")
    },
  })
  const revoke = useApiMutation({
    mutationFn: (id: string) => api.del(`/api/proxy-tokens/${id}`),
    success: "Token revoked",
    invalidates: [keys.proxyTokens],
  })

  const values = config.data?.values
  const origins = values
    ? originsFor(
        window.location,
        values["server.proxy_listen"] ?? "",
        values["server.admin_listen"] ?? "",
        publicOrigin({ values }),
      )
    : { lan: window.location.origin }
  // Defaults to the public address whenever there is one: it is the address
  // an operator went out of their way to configure, and the one that works
  // from both sides of the router.
  const origin = origins.public ?? origins.lan
  const snippetOrigin = origins.public && scope === "lan" ? origins.lan : origin

  // A prefix, never a secret: the store holds a digest and cannot reproduce
  // one, so this is the same "…" the token table already shows.
  const firstToken = tokens.data?.[0]
  const tokenPrefix = firstToken ? `${firstToken.prefix}…` : ""

  const surfaces = liveSurfaces(models.data?.models ?? [])

  return (
    <>
      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Base URLs</h2>
        {origins.public ? (
          <div className="flex flex-col gap-5">
            <div>
              <h3 className="mb-2 text-sm font-medium">
                Public
              </h3>
              <DialectRows origin={origins.public} />
            </div>
            <div>
              <h3 className="mb-2 text-sm font-medium">
                On this network
              </h3>
              <DialectRows origin={origins.lan} />
              <p className="mt-2 text-sm text-[hsl(var(--muted-foreground))]">
                {LAN_NOTE} If the gateway's port is published under a different
                number, use the public address above instead.
              </p>
            </div>
          </div>
        ) : (
          <>
            <DialectRows origin={origins.lan} />
            <p className="mt-3 text-sm text-[hsl(var(--muted-foreground))]">
              {LAN_NOTE} If a published container port, a reverse proxy or a
              path prefix sits in front of the gateway, these are wrong. The
              gateway cannot see what sits in front of it, so{" "}
              <code className="font-mono">server.public_url</code> has to be set
              to the domain clients actually use. This screen does not offer an
              editor for it yet, so it is written through the API for now.
            </p>
          </>
        )}
      </Card>

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Client snippets</h2>
        <p className="mb-3 text-sm text-[hsl(var(--muted-foreground))]">
          {firstToken
            ? "Snippets show only the token's prefix, never the secret — the full value was shown once, at creation. Paste your own token in its place."
            : "No client token exists yet, so snippets show a placeholder. Create one under New client token, below, and paste it in."}
        </p>
        {origins.public && (
          <div className="mb-3 flex flex-wrap items-center gap-2">
            <span className="text-sm text-[hsl(var(--muted-foreground))]">
              Written for
            </span>
            {(
              [
                ["public", "Public"],
                ["lan", "This network"],
              ] as const
            ).map(([value, label]) => (
              <Button
                key={value}
                size="sm"
                variant={scope === value ? "secondary" : "ghost"}
                aria-pressed={scope === value}
                onClick={() => setScope(value)}
              >
                {label}
              </Button>
            ))}
          </div>
        )}
        <Tabs defaultValue={TOOLS[0]}>
          <TabsList>
            {TOOLS.map((tool) => (
              <TabsTrigger key={tool} value={tool}>
                {TOOL_LABEL[tool]}
              </TabsTrigger>
            ))}
          </TabsList>
          {TOOLS.map((tool) => {
            const snippet = snippetFor(
              tool,
              baseUrlFor(snippetOrigin, DIALECT_OF[tool]),
              tokenPrefix,
            )
            return (
              <TabsContent key={tool} value={tool} className="flex flex-col gap-2">
                <pre className="overflow-x-auto rounded bg-[hsl(var(--muted))] p-3 font-mono text-sm">
                  {snippet}
                </pre>
                <div>
                  <CopyButton text={snippet} />
                </div>
              </TabsContent>
            )
          })}
        </Tabs>
      </Card>

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Live surfaces</h2>
        {surfaces.length > 0 ? (
          <div className="flex flex-wrap gap-2">
            {surfaces.map((s) => (
              <Badge key={s} variant="secondary">
                {s}
              </Badge>
            ))}
          </div>
        ) : (
          <EmptyState
            title="A surface goes live when a model that serves it is found"
            hint="Chat, embeddings and the rest appear as discovery works out what your providers can do."
            action={
              <Button asChild size="sm" variant="secondary">
                <Link to="/providers">Add a provider account</Link>
              </Button>
            }
          />
        )}
      </Card>

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">New client token</h2>
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (name) create.mutate(name)
          }}
        >
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <Label htmlFor="token-name">Name</Label>
            <Input
              id="token-name"
              placeholder="what will use it — laptop, CI, a teammate"
              value={name}
              onChange={(e) => setName(e.target.value)}
              className="max-w-96"
            />
          </div>
          <Button type="submit" size="sm" disabled={!name || create.isPending}>
            Create
          </Button>
        </form>

        {minted?.secret && (
          // A region rather than an alert: role="alert" would read the
          // secret itself aloud the moment it appeared. This names the block
          // so it can be found, and leaves reading it to the operator.
          <div
            role="region"
            aria-label="New key"
            className="mt-4 rounded border border-[hsl(var(--warning))] p-3"
          >
            <p className="text-sm font-medium">
              Copy this now — it is not stored and cannot be shown again.
            </p>
            {/* The column holds a digest, so the API genuinely cannot
                reproduce this. Saying "copy it now" is a statement of fact
                rather than a nudge. */}
            <pre className="mt-2 overflow-x-auto font-mono text-sm">
              {minted.secret}
            </pre>
          </div>
        )}
      </Card>

      {tokens.isError && (
        <LoadError
          what="The client tokens"
          error={tokens.error}
          onRetry={() => void tokens.refetch()}
          className="mb-4"
        />
      )}

      {tokens.isPending && <LoadingRows rows={3} />}

      {tokens.data && tokens.data.length > 0 && (
      <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Token</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Last used ({zoneLabel()})</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {tokens.data.map((t) => (
            <TableRow key={t.id}>
              <TableCell>{t.name}</TableCell>
              <TableCell className="font-mono text-sm">{t.prefix}…</TableCell>
              <TableCell className="tabular-nums">{dateOnly(t.created_at)}</TableCell>
              <TableCell className="tabular-nums">
                {t.last_used_at ? (
                  dateTime(t.last_used_at)
                ) : (
                  // Never used is worth saying: it is the difference between a
                  // token to revoke and one that was never wired up.
                  <Badge variant="secondary">never</Badge>
                )}
              </TableCell>
              <TableCell>
                <ConfirmButton
                  size="sm"
                  variant="ghost"
                  className="text-[hsl(var(--destructive))]"
                  title={`Revoke ${t.name}?`}
                  description="Every client still holding this token starts being refused, and the token cannot be reissued — the store keeps a digest, not the secret."
                  confirmLabel="Revoke"
                  destructive
                  onConfirm={() => revoke.mutate(t.id)}
                >
                  Revoke
                </ConfirmButton>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      </div>
      )}

      {tokens.data?.length === 0 && (
        // No action: the form that creates one is directly above, and a
        // second button for it would be the same offer twice.
        <EmptyState
          title="A client token names who is calling"
          hint="Give each client its own, and a token you revoke stops that client alone. Create one in the form above. The shared server.proxy_token keeps working if one is configured."
        />
      )}
    </>
  )
}
