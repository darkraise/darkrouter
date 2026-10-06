import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
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
import { keys, useConfig, useModels } from "../../lib/queries"
import { dateOnly, dateTime, zoneLabel } from "../../lib/format"
import type { ConfigResponse, Model, ProxyToken, ProxyTokensResponse } from "../../lib/api-types"
import { EmptyState } from "../shell/empty-state"
import { LoadError, LoadingRows } from "../shell/screen-state"
import { baseUrlFor, snippetFor, TOOLS, type Tool } from "./snippets"
import type { SaveResult } from "../settings/settings-screen"

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
  "Worked out from this page's address and the gateway's internal listen port; this estimate may not be reachable."

/**
 * Why `raw` cannot be the public base URL, or `undefined` when it can.
 *
 * The same rules the server applies to server.public_url, checked here so an
 * obvious mistake is named under the field before a round trip rather than
 * after it. The server stays the authority -- this only spares the operator
 * a save that was always going to be refused -- and every message quotes the
 * value as typed, not the normalised form the server would quote back.
 */
export function publicUrlProblem(raw: string): string | undefined {
  const typed = raw.trim()
  if (typed === "") return undefined
  // A bare domain is how the setting is written; the server prefixes https://
  // the same way, so "llm.example.com" is not a mistake.
  const candidate = typed.includes("://") || typed.startsWith("/") ? typed : `https://${typed}`
  const notAURL = `"${typed}" is not a URL. Enter a domain such as llm.example.com or an address such as http://gateway:18080.`
  // Whitespace first: Chromium's parser accepts "https://not a url" and
  // percent-encodes the spaces into the host, which the server then refuses.
  if (/\s/.test(typed)) return notAURL
  let url: URL
  try {
    url = new URL(candidate)
  } catch {
    return notAURL
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") {
    return `"${typed}" uses ${url.protocol.slice(0, -1)}://. Clients reach the gateway over http:// or https:// only.`
  }
  if (!url.hostname) return `"${typed}" names no host.`
  if (url.search || url.hash || typed.includes("?") || typed.includes("#")) {
    return `"${typed}" carries a query or fragment. Enter only the scheme, host, port and any path prefix.`
  }
  // Each dialect appends its own version segment, so a /v1 here becomes
  // /v1/v1 in every base URL and snippet below.
  if (/\/v1(beta)?\/*$/i.test(url.pathname)) {
    return `"${typed}" ends in ${url.pathname.replace(/\/+$/, "")}. Leave it off: each dialect adds its own /v1 or /v1beta.`
  }
  return undefined
}

function PublicUrlEditor({ value }: { value: string }) {
  const [draft, setDraft] = useState<string | null>(null)
  // The refusal stays beside the field until the value changes. A toast
  // alone vanished while the field still held the rejected value, and the
  // addresses below it still showed the previous one as though it were
  // current.
  const [refusal, setRefusal] = useState<string | null>(null)
  const queryClient = useQueryClient()
  const save = useApiMutation({
    mutationFn: (url: string) => api.put<SaveResult>("/api/config", {
      set: { "server.public_url": url.trim() },
    }),
    // Shown under the field instead; see `refusal`.
    quietError: true,
    onSuccess: async (result, url) => {
      await queryClient.invalidateQueries({ queryKey: keys.config })
      if (!result.valid) {
        setRefusal(
          `"${url.trim()}" was stored, but the gateway could not apply it: ${result.error ?? "reload configuration in Settings."}`,
        )
        return
      }
      setDraft(null)
      setRefusal(null)
      toast.success("Public base URL saved")
    },
  })
  const current = draft ?? value
  const problem = draft === null ? undefined : publicUrlProblem(draft)
  const error = problem ?? refusal ?? undefined
  return (
    <form
      className="mb-5 flex flex-col gap-2"
      onSubmit={(event) => {
        event.preventDefault()
        if (problem) return
        setRefusal(null)
        const typed = current
        save.mutate(typed, {
          onError: (err) => setRefusal(`"${typed.trim()}" was not saved: ${err.message}`),
        })
      }}
    >
      <Label htmlFor="public-base-url">Public base URL</Label>
      <div className="flex gap-2">
        <Input
          id="public-base-url"
          value={current}
          onChange={(event) => {
            setDraft(event.target.value)
            setRefusal(null)
          }}
          placeholder="http://gateway:18080 or https://llm.example.com"
          aria-invalid={error ? true : undefined}
          aria-describedby={error ? "public-base-url-error public-base-url-help" : "public-base-url-help"}
          disabled={save.isPending}
        />
        <Button type="submit" disabled={save.isPending || current === value || !!problem}>
          {save.isPending ? "Saving…" : "Save address"}
        </Button>
      </div>
      {error && (
        <p id="public-base-url-error" role="alert" className="text-sm text-[hsl(var(--destructive))]">
          {error}
        </p>
      )}
      <p id="public-base-url-help" className="text-sm text-[hsl(var(--muted-foreground))]">
        Enter the address clients use, including the host's published port or your
        domain. Use http:// for plain HTTP; a bare domain uses HTTPS. Include any
        path prefix, without /v1 or /v1beta. Leave empty to use the estimated
        address. Changes apply immediately and are stored in Settings as{" "}
        <code className="font-mono">server.public_url</code>.
      </p>
    </form>
  )
}

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

/**
 * Whether the gateway refuses a client that presents no token.
 *
 * Not readable off the token list. The proxy switches authentication on when
 * the first client token is issued and never back off -- revoking the last
 * token must refuse its clients, not open the gateway to everyone -- so an
 * empty list means "open" on a fresh gateway and "every client refused" on
 * one whose tokens were all revoked. The listing carries `issued` so this
 * screen can tell the two apart; a server too old to send it is read from
 * the list, which is right for every state but the all-revoked one.
 *
 * - `open`: no shared secret, no token ever issued. Anyone may call.
 * - `shared`: only the shared server.proxy_token is in force.
 * - `tokens`: client tokens are in force and at least one is live.
 * - `revoked`: tokens were issued and every one has been revoked, so only
 *   the shared secret, if set, still gets a client in.
 */
export type GatewayAuth = "open" | "shared" | "tokens" | "revoked"

export function gatewayAuth(listing: ProxyTokensResponse): GatewayAuth {
  if (listing.tokens.length > 0) return "tokens"
  if (listing.issued) return "revoked"
  return listing.shared_secret ? "shared" : "open"
}

function authStatus(auth: GatewayAuth, shared: boolean): string {
  switch (auth) {
    case "open":
      return "The gateway accepts requests without a token. Creating the first client token switches authentication on for every client, and it stays on."
    case "shared":
      return "Every request must carry the shared server.proxy_token. Client tokens work alongside it."
    case "tokens":
      return shared
        ? "Every request must carry a client token or the shared server.proxy_token. A request with neither is refused with 401."
        : "Every request must carry a client token. A request without one is refused with 401."
    case "revoked":
      return shared
        ? "Every client token has been revoked, and authentication is still on: a request without the shared server.proxy_token is refused with 401. Create a new token and give it to each client that should keep working."
        : "Every client token has been revoked, and authentication is still on: every client is refused with 401 until it presents a new token. Create one below and give it to each client that should keep working."
  }
}

/**
 * The full listing rather than the bare array `useProxyTokens` keeps: whether
 * authentication is on travels beside the list, not in it. Keyed under
 * keys.proxyTokens so every write that invalidates the list refreshes this.
 */
function useTokenListing() {
  return useQuery({
    queryKey: [...keys.proxyTokens, "listing"],
    queryFn: ({ signal }) => api.get<ProxyTokensResponse>("/api/proxy-tokens", { signal }),
  })
}

export function ConnectScreen() {
  const [name, setName] = useState("")
  const [minted, setMinted] = useState<ProxyToken | null>(null)
  // Which of the two addresses the snippets are written against. Only ever
  // offered when there are two; a copied snippet that names the wrong side of
  // the router is the failure this whole screen exists to prevent.
  const [scope, setScope] = useState<"public" | "lan">("public")
  const listing = useTokenListing()
  const tokenList = listing.data?.tokens
  const auth = listing.data ? gatewayAuth(listing.data) : undefined
  const shared = listing.data?.shared_secret === true
  // Asked before the first token, and whenever the listing cannot say
  // whether one was issued: creating it is a gateway-wide switch that no
  // later revoke undoes, so not knowing is a reason to ask, not to skip.
  const switchesAuthOn = auth !== "tokens" && auth !== "revoked"
  const [confirming, setConfirming] = useState(false)
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
  const firstToken = tokenList?.[0]
  const tokenPrefix = firstToken ? `${firstToken.prefix}…` : ""

  const surfaces = liveSurfaces(models.data?.models ?? [])

  return (
    <>
      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Base URLs</h2>
        {config.data && <PublicUrlEditor value={publicOrigin(config.data)} />}
        {config.isError && <LoadError what="configuration" error={config.error} onRetry={() => void config.refetch()} />}
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
                Estimated address
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
              path prefix sits in front of the gateway, set the public base URL
              above to the address clients actually use.
            </p>
          </>
        )}
      </Card>

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">Client snippets</h2>
        <p className="mb-3 text-sm text-[hsl(var(--muted-foreground))]">
          {firstToken
            ? "Snippets show only the token's prefix, never the secret — the full value was shown once, at creation. Paste your own token in its place."
            : auth === "revoked"
              ? "Every client token has been revoked, so snippets show a placeholder — and the gateway still requires a token. Create a new one under New client token, below, and paste it in."
              : auth === "shared"
                ? "No client token exists yet, so snippets show a placeholder. Paste the shared server.proxy_token in its place, or create a client token under New client token, below."
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
          {/* Wraps rather than overflows: five triggers are wider than a
              phone, and a strip that sticks out of its card scrolls the
              whole page sideways. */}
          <TabsList className="flex max-w-full flex-wrap justify-start">
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
        {auth && (
          <p
            className={
              auth === "revoked"
                ? "mb-3 rounded border border-[hsl(var(--warning))] p-3 text-sm"
                : "mb-3 text-sm text-[hsl(var(--muted-foreground))]"
            }
          >
            {authStatus(auth, shared)}
          </p>
        )}
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            if (!name) return
            if (switchesAuthOn) setConfirming(true)
            else create.mutate(name)
          }}
        >
          {/* The cap is on the wrapper, not the input: a capped input in a
              flex-1 wrapper left Create stranded at the card's far edge. */}
          <div className="flex min-w-0 max-w-96 flex-1 flex-col gap-1.5">
            <Label htmlFor="token-name">Name</Label>
            <Input
              id="token-name"
              placeholder="what will use it — laptop, CI, a teammate"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </div>
          <Button type="submit" size="sm" disabled={!name || create.isPending}>
            Create
          </Button>
        </form>
        {/* Controlled rather than a ConfirmButton: the form also submits on
            Enter, and the question has to stand in front of both. */}
        <AlertDialog open={confirming} onOpenChange={setConfirming}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Require a token from every client?</AlertDialogTitle>
              <AlertDialogDescription>
                {shared
                  ? "This is the first client token. Clients holding the shared server.proxy_token keep working, but from now on the gateway requires a token even if server.proxy_token is later removed. Revoking every client token does not switch authentication off."
                  : "This is the first client token. Once it exists, the gateway refuses every request that does not carry a valid token: each client that works today without one starts getting 401. Revoking the token later does not switch authentication back off. Give the new token to every client that should keep working."}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>Cancel</AlertDialogCancel>
              <AlertDialogAction onClick={() => create.mutate(name)}>
                Create and require tokens
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>

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

      {listing.isError && (
        <LoadError
          what="The client tokens"
          error={listing.error}
          onRetry={() => void listing.refetch()}
          className="mb-4"
        />
      )}

      {listing.isPending && <LoadingRows rows={3} />}

      {tokenList && tokenList.length > 0 && (
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
          {tokenList.map((t) => (
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
                  description={
                    tokenList.length === 1
                      ? "Every client still holding this token starts being refused, and the token cannot be reissued — the store keeps a digest, not the secret. It is the last client token, and authentication stays on without it: every client without a token is refused until you create a new one."
                      : "Every client still holding this token starts being refused, and the token cannot be reissued — the store keeps a digest, not the secret."
                  }
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

      {tokenList?.length === 0 &&
        // No action: the form that creates one is directly above, and a
        // second button for it would be the same offer twice.
        (auth === "revoked" ? (
          <EmptyState
            title="Every client token has been revoked"
            hint={`Authentication stayed on: revoking the last token does not reopen the gateway. ${
              shared
                ? "Only clients holding the shared server.proxy_token get in."
                : "Every client is refused with 401."
            } Create a new token in the form above and give it to each client that should keep working.`}
          />
        ) : (
          <EmptyState
            title="A client token names who is calling"
            hint={`Give each client its own, so revoking one stops that client without touching the others. Create one in the form above. ${
              shared
                ? "The shared server.proxy_token keeps working alongside them."
                : "The first one switches authentication on for the whole gateway, and revoking every token later does not switch it off."
            }`}
          />
        ))}
    </>
  )
}
