import { useEffect, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
} from "darkraise-ui"
import type { Aliases, Model, Provider } from "../../lib/api-types"
import { useAliases, useModels, useProviders } from "../../lib/queries"
import { nav, settingsItem, type NavItem } from "./nav"

/** A request id is opaque and never listed, so it is recognised by shape
 *  rather than matched against a fetched set. Ids are hex, and short input is
 *  far more likely to be a word than an id. */
const REQUEST_ID = /^[0-9a-f]{8,}$/i

/** The most models one query lists. The filter runs over the whole catalog
 *  first, so a model past this many still turns up when it is named. */
const MAX_MODELS = 50

const DESTINATIONS: NavItem[] = [...nav.flatMap((g) => g.items), settingsItem]

export type PaletteMatches = {
  requestId: string | null
  destinations: NavItem[]
  providers: Provider[]
  aliases: string[]
  models: Model[]
}

function has(haystack: string, needle: string): boolean {
  return haystack.toLowerCase().includes(needle)
}

/**
 * What the palette lists for a query. An empty query lists the destinations
 * and every provider and alias; a typed one narrows each group to the
 * entries that contain it, by id and by name where both exist.
 */
export function paletteMatches(
  query: string,
  data: { providers?: Provider[]; aliases?: Aliases; models?: Model[] },
): PaletteMatches {
  const q = query.trim().toLowerCase()
  const all = q === ""
  const providers = (data.providers ?? []).filter(
    (p) => all || has(p.id, q) || has(p.name, q),
  )
  const aliases = Object.keys(data.aliases ?? {}).filter((a) => all || has(a, q))
  const models = (data.models ?? [])
    .filter((m) => !all && (has(m.model, q) || m.providers.some((p) => has(p, q))))
    .slice(0, MAX_MODELS)
  return {
    requestId: REQUEST_ID.test(query.trim()) ? query.trim() : null,
    destinations: DESTINATIONS.filter((d) => all || has(d.label, q)),
    providers,
    aliases,
    models,
  }
}

/**
 * ⌘K jumps to a provider, a model, a request id or an alias directly.
 *
 * §5: reachability is the palette's job, not the rail's, so nav depth only
 * matters to someone browsing rather than aiming. The library's SearchCommand
 * takes nav items alone, which cannot answer "where is groq" — these entries
 * are Darkrouter's own domain, so the palette is built here from the Command
 * primitives rather than pushed upstream.
 */
export function CommandPalette({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const [query, setQuery] = useState("")
  const navigate = useNavigate()

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        onOpenChange(!open)
      }
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [open, onOpenChange])

  // Only fetched while the palette is open: the rail is on every screen, and
  // three extra polls per screen to populate a dialog nobody opened is a cost
  // the gateway pays for nothing.
  const providers = useProviders({ enabled: open })
  const models = useModels({ enabled: open })
  const aliases = useAliases({ enabled: open })

  function close() {
    onOpenChange(false)
    setQuery("")
  }

  // A disabled query still reads the cache another mount already populated
  // under the same key, so none of these assume the response survived
  // whatever that other fetch actually returned.
  const found = paletteMatches(query, {
    providers: providers.data?.providers,
    aliases: aliases.data,
    models: models.data?.models,
  })

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent className="dr-command-dialog-content overflow-hidden p-0">
        <DialogTitle className="sr-only">Jump to</DialogTitle>
        <DialogDescription className="sr-only">
          Jump to a provider, model, alias or request id.
        </DialogDescription>
        {/* The library's fuzzy match would let "gro" surface "groq" from a
            list this component already narrowed by substring. Filtering is
            done once, above, so what is registered is what is shown. */}
        <Command className="dr-command-dialog" shouldFilter={false}>
          <CommandInput
            value={query}
            onValueChange={setQuery}
            placeholder="Jump to a provider, model, alias or request id…"
          />
          <CommandList>
            <CommandEmpty>Nothing matches.</CommandEmpty>

            {found.requestId && (
              <CommandGroup heading="Request">
                <CommandItem
                  value={`request ${found.requestId}`}
                  onSelect={() => {
                    close()
                    void navigate({ to: "/requests/$id", params: { id: found.requestId! } })
                  }}
                >
                  Open trace {found.requestId}
                </CommandItem>
              </CommandGroup>
            )}

            {found.destinations.length > 0 && (
              <CommandGroup heading="Go to">
                {found.destinations.map((item) => (
                  <CommandItem
                    key={item.href}
                    value={`go ${item.label}`}
                    onSelect={() => {
                      close()
                      void navigate({ to: item.href })
                    }}
                  >
                    {item.label}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {found.providers.length > 0 && (
              <CommandGroup heading="Providers">
                {found.providers.map((p) => (
                  <CommandItem
                    key={p.id}
                    value={`provider ${p.id}`}
                    onSelect={() => {
                      close()
                      void navigate({ to: "/providers/$id", params: { id: p.id } })
                    }}
                  >
                    {p.name}
                    {p.name !== p.id && (
                      <span className="ml-2 text-[hsl(var(--legend))]">{p.id}</span>
                    )}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {found.aliases.length > 0 && (
              <CommandGroup heading="Aliases">
                {found.aliases.map((name) => (
                  <CommandItem
                    key={name}
                    value={`alias ${name}`}
                    onSelect={() => {
                      close()
                      void navigate({ to: "/routing", search: { alias: name } })
                    }}
                  >
                    {name}
                  </CommandItem>
                ))}
              </CommandGroup>
            )}

            {found.models.length > 0 && (
              <CommandGroup heading="Models">
                {found.models.map((m) => (
                  <CommandItem
                    key={m.model}
                    value={`model ${m.model}`}
                    onSelect={() => {
                      close()
                      void navigate({ to: "/models", search: { model: m.model } })
                    }}
                  >
                    {m.model}
                    {/* The catalog merges by model id, so a row lists every
                        provider that serves it rather than naming just one. */}
                    <span className="ml-2 text-[hsl(var(--legend))]">
                      {m.providers.join(", ")}
                    </span>
                  </CommandItem>
                ))}
              </CommandGroup>
            )}
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  )
}
