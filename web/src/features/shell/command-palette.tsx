import { useEffect, useState } from "react"
import { useNavigate } from "@tanstack/react-router"
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "darkraise-ui"
import { useAliases, useModels, useProviders } from "../../lib/queries"
import { nav, settingsItem } from "./nav"

/** A request id is opaque and never listed, so it is recognised by shape
 *  rather than matched against a fetched set. Ids are hex, and short input is
 *  far more likely to be a word than an id. */
const REQUEST_ID = /^[0-9a-f]{8,}$/i

/**
 * ⌘K jumps to a provider, a model, a request id or an alias directly.
 *
 * §5: reachability is the palette's job, not the rail's, so nav depth only
 * matters to someone browsing rather than aiming. The library's SearchCommand
 * takes nav items alone, which cannot answer "where is groq" — these entries
 * are Darkrouter's own domain, so the palette is built here from the Command
 * primitives rather than pushed upstream.
 */
export function CommandPalette() {
  const [open, setOpen] = useState(false)
  const [query, setQuery] = useState("")
  const navigate = useNavigate()

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        setOpen((v) => !v)
      }
    }
    document.addEventListener("keydown", onKey)
    return () => document.removeEventListener("keydown", onKey)
  }, [])

  // Only fetched while the palette is open: the rail is on every screen, and
  // three extra polls per screen to populate a dialog nobody opened is a cost
  // the gateway pays for nothing.
  const providers = useProviders({ enabled: open })
  const models = useModels({ enabled: open })
  const aliases = useAliases({ enabled: open })

  function go(to: string) {
    setOpen(false)
    setQuery("")
    void navigate({ to })
  }

  const looksLikeRequestId = REQUEST_ID.test(query.trim())

  return (
    <CommandDialog open={open} onOpenChange={setOpen}>
      <CommandInput
        value={query}
        onValueChange={setQuery}
        placeholder="Jump to a provider, model, alias or request id…"
      />
      <CommandList>
        <CommandEmpty>Nothing matches.</CommandEmpty>

        {looksLikeRequestId && (
          <CommandGroup heading="Request">
            <CommandItem
              value={query}
              onSelect={() => go(`/requests/${query.trim()}`)}
            >
              Open trace {query.trim()}
            </CommandItem>
          </CommandGroup>
        )}

        <CommandGroup heading="Go to">
          {[...nav.flatMap((g) => g.items), settingsItem].map((item) => (
            <CommandItem
              key={item.href}
              value={item.label}
              onSelect={() => go(item.href)}
            >
              {item.label}
            </CommandItem>
          ))}
        </CommandGroup>

        {/* A disabled query still reads the cache another mount already
            populated under the same key, so this cannot assume `providers`
            survived whatever that other fetch actually returned. */}
        {providers.data?.providers && providers.data.providers.length > 0 && (
          <CommandGroup heading="Providers">
            {providers.data.providers.map((p) => (
              <CommandItem
                key={p.id}
                value={`provider ${p.id} ${p.name}`}
                onSelect={() => go(`/providers/${p.id}`)}
              >
                {p.name}
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        {aliases.data && Object.keys(aliases.data).length > 0 && (
          <CommandGroup heading="Aliases">
            {Object.keys(aliases.data).map((name) => (
              <CommandItem
                key={name}
                value={`alias ${name}`}
                onSelect={() => go(`/routing?alias=${encodeURIComponent(name)}`)}
              >
                {name}
              </CommandItem>
            ))}
          </CommandGroup>
        )}

        {models.data && models.data.models.length > 0 && (
          <CommandGroup heading="Models">
            {models.data.models.slice(0, 50).map((m) => (
              <CommandItem
                key={m.model}
                value={`model ${m.model} ${m.providers.join(" ")}`}
                onSelect={() => go(`/models?model=${encodeURIComponent(m.model)}`)}
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
    </CommandDialog>
  )
}
