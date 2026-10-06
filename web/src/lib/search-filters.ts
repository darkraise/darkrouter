import { useCallback, useMemo } from "react"
import { useRouter, useRouterState } from "@tanstack/react-router"

/**
 * Filters that live in the URL rather than in component state.
 *
 * §5 states this as a rule: every filtered view is a URL, so a filtered
 * Requests view survives a reload and can be pasted to yourself. Component
 * state gives neither, and the difference only shows up when someone tries to
 * share what they are looking at.
 *
 * An empty value is dropped rather than written as `?provider=`, so the URL
 * carries what is filtered and nothing else, and two ways of expressing "no
 * filter" cannot produce two cache keys for one view.
 */
export function useSearchFilters<K extends string>(
  fields: readonly K[],
): [Record<K, string>, (key: K, value: string) => void, () => void] {
  const router = useRouter()
  const search = useRouterState({
    select: (s) => s.location.searchStr,
  })

  const filters = useMemo(() => {
    const params = new URLSearchParams(search)
    const out = {} as Record<K, string>
    for (const field of fields) out[field] = params.get(field) ?? ""
    return out
  }, [search, fields])

  const pathname = useRouterState({ select: (s) => s.location.pathname })

  const write = useCallback(
    (next: Record<string, string>) => {
      const params = new URLSearchParams(search)
      for (const [k, v] of Object.entries(next)) {
        // Deleted rather than set to "": a URL carrying ?provider= reads as a
        // filter that is set to nothing, and would key the cache differently
        // from the same view with no filter at all.
        if (v === "") params.delete(k)
        else params.set(k, v)
      }
      const str = params.toString()
      // The history API rather than navigate(): navigate's search parameter is
      // typed against one route's schema, and this hook is deliberately
      // generic because each screen filters different fields. The router
      // re-parses the URL through validateSearch either way.
      //
      // Replaced rather than pushed: typing into a filter box would otherwise
      // put one history entry per keystroke between the reader and the page
      // they came from.
      router.history.replace(`${pathname}${str ? `?${str}` : ""}`)
    },
    [router, pathname, search],
  )

  const setFilter = useCallback(
    (key: K, value: string) => write({ [key]: value }),
    [write],
  )

  const clear = useCallback(() => {
    const empty: Record<string, string> = {}
    for (const field of fields) empty[field] = ""
    write(empty)
  }, [write, fields])

  return [filters, setFilter, clear]
}

/**
 * The router's reading of a query string: every value a plain string.
 *
 * TanStack's default reads each value as JSON, and so writes a string that
 * looks like a number with quotes around it to keep it a string on the way
 * back — `since_ms="1788739200000"`. This console treats every search param as
 * a string (the root route's validateSearch keeps nothing else), and several
 * screens read the query string directly with URLSearchParams, as does the
 * API those values are forwarded to. A quoted value was a 400 there. Plain
 * query strings in both directions make the URL the router writes the same
 * URL every other reader expects.
 */
export function parseSearch(searchStr: string): Record<string, string> {
  return Object.fromEntries(new URLSearchParams(searchStr))
}

/** The inverse of parseSearch: `?a=1&b=x`, or "" when there is nothing to
 *  write. An undefined value is a key being removed, not the word
 *  "undefined"; a non-string one is written in its plain text form. */
export function stringifySearch(search: Record<string, unknown>): string {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(search)) {
    if (v === undefined || v === null) continue
    params.set(k, typeof v === "object" ? JSON.stringify(v) : String(v))
  }
  const str = params.toString()
  return str ? `?${str}` : ""
}

/** The query string for a filter set, with empty values omitted. */
export function filterQuery(filters: Record<string, string>): string {
  const params = new URLSearchParams(
    Object.entries(filters).filter(([, v]) => v !== ""),
  )
  const str = params.toString()
  return str ? `?${str}` : ""
}
