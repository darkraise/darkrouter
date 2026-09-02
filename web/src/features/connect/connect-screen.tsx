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
import type { Model, ProxyToken } from "../../lib/api-types"
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
 * The origin a client should be pointed at, which is not necessarily the
 * origin the console itself was loaded from: the console is served from
 * admin_listen, but a client needs proxy_listen. Equal ports mean the
 * operator put one listener in front of both, so the page's own origin is
 * trusted rather than rewritten.
 */
export function originFor(
  location: Pick<Location, "origin" | "hostname" | "protocol">,
  proxyListen: string,
  adminListen: string,
): string {
  const portOf = (listen: string) => listen.slice(listen.lastIndexOf(":") + 1)
  return portOf(proxyListen) === portOf(adminListen)
    ? location.origin
    : `${location.protocol}//${location.hostname}:${portOf(proxyListen)}`
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

  const server = config.data?.blocks.server
  const origin = server
    ? originFor(window.location, server.proxy_listen, server.admin_listen)
    : window.location.origin

  // A prefix, never a secret: the store holds a digest and cannot reproduce
  // one, so this is the same "…" the token table already shows.
  const firstToken = tokens.data?.[0]
  const tokenPrefix = firstToken ? `${firstToken.prefix}…` : ""

  const surfaces = liveSurfaces(models.data?.models ?? [])

  return (
    <>
      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Base URLs</h2>
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
      </Card>

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Client snippets</h2>
        <p className="mb-3 text-sm text-[hsl(var(--muted-foreground))]">
          {firstToken
            ? "Snippets show only the token's prefix, never the secret — the full value was shown once, at creation. Paste your own token in its place."
            : "No client token exists yet, so snippets show a placeholder. Create one under New client token, below, and paste it in."}
        </p>
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
              baseUrlFor(origin, DIALECT_OF[tool]),
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
