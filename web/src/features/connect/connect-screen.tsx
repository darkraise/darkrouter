import { useState } from "react"
import { PageHeader } from "darkraise-ui/layout"
import {
  Badge,
  Button,
  Card,
  Input,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui"
import { api } from "../../lib/api"
import { useApiMutation } from "../../lib/mutations"
import { keys, useProxyTokens } from "../../lib/queries"
import type { ProxyToken } from "../../lib/api-types"

export function ConnectScreen() {
  const [name, setName] = useState("")
  const [minted, setMinted] = useState<ProxyToken | null>(null)
  const tokens = useProxyTokens()

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

  return (
    <>
      <PageHeader
        title="Connect"
        description="How to point a client at this gateway"
      />

      <Card className="mb-6 p-4">
        <h2 className="mb-3 text-sm font-medium">New client token</h2>
        <div className="flex gap-2">
          <Input
            placeholder="what will use it — laptop, CI, a teammate"
            value={name}
            onChange={(e) => setName(e.target.value)}
            className="w-96"
          />
          <Button size="sm" disabled={!name} onClick={() => create.mutate(name)}>
            Create
          </Button>
        </div>

        {minted?.secret && (
          <div className="mt-4 rounded border border-[hsl(var(--warning))] p-3">
            <p className="text-sm font-medium">
              Copy this now — it is not stored and cannot be shown again.
            </p>
            {/* The column holds a digest, so the API genuinely cannot
                reproduce this. Saying "copy it now" is a statement of fact
                rather than a nudge. */}
            <pre className="mt-2 overflow-x-auto font-mono text-xs">
              {minted.secret}
            </pre>
          </div>
        )}
      </Card>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Token</TableHead>
            <TableHead>Created</TableHead>
            <TableHead>Last used</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {(tokens.data ?? []).map((t) => (
            <TableRow key={t.id}>
              <TableCell>{t.name}</TableCell>
              <TableCell className="font-mono text-xs">{t.prefix}…</TableCell>
              <TableCell>{new Date(t.created_at).toLocaleDateString()}</TableCell>
              <TableCell>
                {t.last_used_at ? (
                  new Date(t.last_used_at).toLocaleString()
                ) : (
                  // Never used is worth saying: it is the difference between a
                  // token to revoke and one that was never wired up.
                  <Badge variant="secondary">never</Badge>
                )}
              </TableCell>
              <TableCell>
                <Button size="sm" variant="ghost" onClick={() => revoke.mutate(t.id)}>
                  Revoke
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {(tokens.data ?? []).length === 0 && (
        <p className="mt-4 text-sm text-[hsl(var(--muted-foreground))]">
          No client tokens yet. The shared <code>server.proxy_token</code> still
          works if one is configured.
        </p>
      )}
    </>
  )
}
