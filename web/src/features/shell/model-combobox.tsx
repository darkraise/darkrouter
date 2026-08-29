import { useMemo } from "react"
import { ChevronDown } from "lucide-react"
import {
  Combobox,
  ComboboxContent,
  ComboboxControl,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxItemText,
  ComboboxList,
  ComboboxTrigger,
  Spinner,
  type ComboboxItemData,
} from "darkraise-ui"
import { useAliases, useModels } from "../../lib/queries"
import type { Model } from "../../lib/api-types"

/**
 * Every string that names something routable, in the order the router would
 * try to resolve them.
 *
 * Aliases first because they resolve first: an exact alias match wins before
 * `provider/model` or a bare name is considered. Then both model forms,
 * because both route — the bare name fans out across providers in priority
 * order, the qualified one pins to a single provider.
 *
 * `aliases` is left out where an alias would not resolve. Inside a chain the
 * router expands targets through rules 2 and 3 only, so suggesting an alias
 * there would suggest something that cannot work.
 *
 * `surface` narrows to models that serve it. An embeddings field offering a
 * chat model is offering a request that will be refused.
 */
export function modelCandidates({
  models,
  aliases = [],
  surface,
}: {
  models: Model[]
  aliases?: string[]
  surface?: string
}): string[] {
  const usable = surface ? models.filter((m) => m.surfaces.includes(surface)) : models
  const targets = new Set<string>()
  for (const m of usable) {
    targets.add(m.model)
    for (const p of m.providers) targets.add(`${p}/${m.model}`)
  }
  // An alias is free to share a model's name, and the router would still
  // resolve the alias first. Listing it twice offers the same string as two
  // choices and hands the combobox duplicate keys.
  const named = [...new Set(aliases)].sort()
  for (const a of named) targets.delete(a)
  return [...named, ...[...targets].sort()]
}

/** Matches for what has been typed, prefix matches first. Capped because a
 *  list longer than the popover is a list nobody reads to the end of. */
export function filterCandidates(candidates: string[], query: string, limit = 40): string[] {
  const q = query.trim().toLowerCase()
  if (q === "") return candidates.slice(0, limit)
  const starts: string[] = []
  const contains: string[] = []
  for (const c of candidates) {
    const lower = c.toLowerCase()
    if (lower.startsWith(q)) starts.push(c)
    else if (lower.includes(q)) contains.push(c)
    if (starts.length >= limit) break
  }
  return [...starts, ...contains].slice(0, limit)
}

/** The catalogue and alias list a field should suggest from, for the call
 *  sites that have no reason to hold either themselves.
 *
 *  `loading` is part of the answer rather than something a caller re-derives:
 *  an empty candidate list means two different things — the catalogue is empty
 *  or it has not arrived — and only the query knows which. */
export function useModelCandidates({
  aliases = true,
  surface,
}: { aliases?: boolean; surface?: string } = {}): { candidates: string[]; loading: boolean } {
  const models = useModels()
  const aliasMap = useAliases()
  const modelRows = models.data?.models ?? []
  const aliasNames = aliasMap.data ? Object.keys(aliasMap.data) : []
  const candidates = useMemo(
    () => modelCandidates({ models: modelRows, aliases: aliases ? aliasNames : [], surface }),
    // The two arrays are rebuilt on every render by the lines above; the query
    // results they come from are the values that actually change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [models.data, aliasMap.data, aliases, surface],
  )
  // Only the first load, not a background refetch: this catalogue polls, and a
  // spinner every poll would be a field that flickers while nothing is
  // happening. An alias query that is still out counts only when aliases are
  // actually being suggested.
  return { candidates, loading: models.isPending || (aliases && aliasMap.isPending) }
}

/**
 * A field for naming something to route to: a text box that suggests, not a
 * menu that constrains.
 *
 * Free text has to survive everywhere this is used. Discovery imports on a
 * sweep, so an operator naming a model the catalogue has not seen yet is doing
 * something reasonable — and a picker that refused it would make the field
 * unusable until the next sweep.
 */
export function ModelCombobox({
  value,
  onChange,
  candidates,
  label,
  placeholder = "alias or provider/model",
  emptyText = "Nothing in the catalogue matches. It will still be sent.",
  loading = false,
  className,
}: {
  value: string
  onChange: (next: string) => void
  candidates: string[]
  label: string
  placeholder?: string
  /** What the popover says when nothing matches. The default speaks for a
   *  field whose value is about to be sent; a filter is not sending anything
   *  and needs to say so differently. */
  emptyText?: string
  /** The catalogue behind `candidates` is still on its way. Only the first
   *  load: a refetch that already has rows to show is not a wait. */
  loading?: boolean
  className?: string
}) {
  const items: ComboboxItemData[] = useMemo(
    () => filterCandidates(candidates, value).map((c) => ({ value: c, label: c })),
    [candidates, value],
  )
  // Loading is only worth saying while there is nothing to show. Once the
  // first rows are in, a list that quietly grows is better than a spinner over
  // suggestions the operator can already use.
  const waiting = loading && items.length === 0

  return (
    <Combobox
      className={className ?? "min-w-0 flex-1"}
      items={items}
      inputValue={value}
      onInputValueChange={(d) => onChange(d.value)}
      onValueChange={(d) => {
        const picked = d.value[0]
        if (picked !== undefined) onChange(picked)
      }}
      // Opens on click, because a field that only reveals its list once you
      // have guessed the first character is a text box wearing a combobox's
      // markup — and every one of these sits in front of a catalogue the
      // operator is not expected to have memorised. Focus is left alone so
      // tabbing through a form does not pop a list at every stop.
      openOnClick
      openOnFocus={false}
    >
      <ComboboxControl className="relative">
        <ComboboxInput
          aria-label={label}
          className="pr-8 font-mono text-sm"
          placeholder={placeholder}
        />
        {/* The affordance, not just the behaviour: a chevron is what tells a
            reader this opens before they click it. */}
        {/* Only the colour is ours. The recipe already positions this —
            `absolute top-1/2 -translate-y-1/2` — and the `inset-y-0` this used
            to add set `top: 0` while leaving the translate in place, which
            pulled the chevron twelve pixels above the field and left it
            sitting on the top border. */}
        <ComboboxTrigger
          className="text-[hsl(var(--legend))]"
          aria-label={`Show ${label.toLowerCase()} suggestions`}
        >
          {/* In the chevron's place rather than beside it: the two mean the
              same thing at different times — "there is a list here" and "the
              list is coming" — and showing both would widen the field for a
              second and shift the text under the cursor. */}
          {waiting ? (
            <Spinner size="sm" aria-label={`Loading ${label.toLowerCase()} suggestions`} />
          ) : (
            /* With a fallback: --icon-size is set by the button and field
               recipes, and a bare trigger is neither — without one the chevron
               collapses to eight pixels and reads as a speck. */
            <ChevronDown className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
          )}
        </ComboboxTrigger>
      </ComboboxControl>
      <ComboboxContent>
        <ComboboxList>
          {items.map((item) => (
            <ComboboxItem key={item.value} item={item}>
              <ComboboxItemText>{item.value}</ComboboxItemText>
            </ComboboxItem>
          ))}
        </ComboboxList>
        {/* An empty popover during the first load would say "nothing matches",
            which is a claim about the catalogue rather than about the wait —
            and the operator would go and change what they typed. */}
        <ComboboxEmpty>
          {waiting ? (
            <span className="flex items-center gap-2">
              <Spinner size="sm" aria-hidden="true" />
              Loading the catalogue…
            </span>
          ) : (
            emptyText
          )}
        </ComboboxEmpty>
      </ComboboxContent>
    </Combobox>
  )
}
