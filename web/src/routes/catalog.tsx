import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Input } from "darkraise-ui/components/input"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "darkraise-ui/components/table"
import { api, POLL } from "../lib/api"

type Model = {
  model: string
  providers: string[]
  surfaces: string[]
  context_window: number
  max_output_tokens: number
  tools: boolean
  vision: boolean
  reasoning: boolean
  inferred: boolean
  state: string
}

type Catalog = {
  models: Model[]
  aliases: { name: string; targets: string[] }[]
}

export function CatalogScreen() {
  const [q, setQ] = useState("")
  const [surface, setSurface] = useState("")

  const cat = useQuery({
    queryKey: ["models", q, surface],
    queryFn: () => {
      const p = new URLSearchParams()
      if (q) p.set("q", q)
      if (surface) p.set("surface", surface)
      return api.get<Catalog>(`/api/models?${p.toString()}`)
    },
    // Spec §5: 30s. The catalog changes on a discovery sweep, not per request.
    refetchInterval: POLL.slow,
  })

  const data = cat.data
  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-wrap gap-2">
        <Input
          placeholder="Search models"
          value={q}
          onChange={(e) => setQ(e.target.value)}
        />
        <Input
          placeholder="surface"
          value={surface}
          onChange={(e) => setSurface(e.target.value)}
        />
      </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Model</TableHead>
            <TableHead>Providers</TableHead>
            <TableHead>Surfaces</TableHead>
            <TableHead>Context</TableHead>
            <TableHead>Capabilities</TableHead>
            <TableHead>Metadata</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data?.models ?? []).map((m) => (
            <TableRow key={m.model}>
              <TableCell className="font-mono text-xs">{m.model}</TableCell>
              <TableCell>{m.providers.join(", ")}</TableCell>
              <TableCell>{m.surfaces.join(", ")}</TableCell>
              <TableCell>{m.context_window || "—"}</TableCell>
              <TableCell>
                <span className="flex gap-1">
                  {m.tools ? <Badge variant="secondary">tools</Badge> : null}
                  {m.vision ? <Badge variant="secondary">vision</Badge> : null}
                  {m.reasoning ? <Badge variant="secondary">reasoning</Badge> : null}
                </span>
              </TableCell>
              <TableCell>
                {/* Master design §6.4 routes a guessed model with a warning.
                    An operator who cannot see which rows are guesses reads a
                    refused tool call as a Darkrouter bug. */}
                {m.inferred ? (
                  <Badge variant="amber">inferred</Badge>
                ) : (
                  <Badge variant="green">known</Badge>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {(data?.models ?? []).length === 0 ? (
        <p className="text-muted-foreground text-sm">No models match this search.</p>
      ) : null}

      <section>
        <h2 className="text-muted-foreground mb-2 text-sm font-medium">Aliases</h2>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Alias</TableHead>
              <TableHead>Resolves to</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {(data?.aliases ?? []).map((a) => (
              <TableRow key={a.name}>
                <TableCell className="font-mono text-xs">{a.name}</TableCell>
                <TableCell className="font-mono text-xs">{a.targets.join(" → ")}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <p className="text-muted-foreground mt-2 text-xs">
          Aliases are edited in darkrouter.yaml, not here.
        </p>
      </section>
    </div>
  )
}
