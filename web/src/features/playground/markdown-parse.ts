/**
 * The markdown a language model actually emits, parsed into a block tree.
 *
 * Not a CommonMark implementation and not trying to be. It covers what comes
 * back from a chat completion — fenced code, tables, task lists, headings,
 * quotes, lists and inline emphasis — and treats everything else as text.
 * That is the whole of the trade: a hundred lines we can read against a
 * dependency tree we would carry into a binary for the sake of footnotes and
 * reference links no model writes.
 *
 * Nothing here produces HTML. The parser yields data, the component in
 * markdown.tsx yields React nodes, and a provider's answer therefore cannot
 * put markup into the console however it is written.
 */

export type Inline =
  | { kind: "text"; text: string }
  | { kind: "code"; text: string }
  | { kind: "strong"; children: Inline[] }
  | { kind: "em"; children: Inline[] }
  | { kind: "del"; children: Inline[] }
  | { kind: "link"; href: string; children: Inline[] }

export type Align = "left" | "right" | "center"

export type Block =
  | { kind: "paragraph"; children: Inline[] }
  | { kind: "heading"; level: number; children: Inline[] }
  | { kind: "code"; lang: string; text: string }
  | { kind: "quote"; children: Block[] }
  | { kind: "list"; ordered: boolean; items: ListItem[] }
  | { kind: "table"; head: Inline[][]; align: Align[]; rows: Inline[][][] }
  | { kind: "rule" }

export type ListItem = {
  children: Inline[]
  /** Present only for a task item: `- [x]` or `- [ ]`. */
  checked?: boolean
}

/** The schemes a link may carry.
 *
 *  A model writes the href, so this is the whole of the defence: javascript:
 *  and data: are how a link becomes an exploit, and neither has any business
 *  in a chat answer. A refused link renders as its own text, which is both
 *  safe and honest — the reader sees exactly what was written. */
const SAFE_SCHEME = /^(https?:|mailto:)/i

function safeHref(href: string): string | null {
  const trimmed = href.trim()
  if (trimmed === "") return null
  // A relative link inside a transcript points at the console's own routes,
  // which is not what a model means by it, so only absolute forms pass.
  return SAFE_SCHEME.test(trimmed) ? trimmed : null
}

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([A-Za-z0-9_+-]*)\s*$/
const HEADING = /^\s{0,3}(#{1,6})\s+(.*)$/
const RULE = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/
const QUOTE = /^\s{0,3}>\s?(.*)$/
const BULLET = /^(\s*)[-*+]\s+(.*)$/
const NUMBER = /^(\s*)\d{1,9}[.)]\s+(.*)$/
const TASK = /^\[([ xX])\]\s+(.*)$/
/** A delimiter row is what makes the lines above and below it a table. */
const DELIMITER = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/

export function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n")
  // Indexed reads are `string | undefined` under this tsconfig, and every one
  // of them here is bounded by the loop above it.
  const at = (n: number) => lines[n] ?? ""
  const out: Block[] = []
  let i = 0

  while (i < lines.length) {
    const line = at(i)

    if (line.trim() === "") {
      i++
      continue
    }

    const fence = FENCE.exec(line)
    if (fence) {
      const marker = (fence[1] ?? "`")[0] ?? "`"
      const body: string[] = []
      i++
      // An unterminated fence runs to the end of what has arrived. Every
      // streamed answer is in that state while the code is still coming, and
      // holding it back until the closing fence would make code blocks
      // materialise all at once instead of filling in.
      while (i < lines.length && !new RegExp(`^\\s{0,3}${marker}{3,}\\s*$`).test(at(i))) {
        body.push(at(i))
        i++
      }
      // The trailing newline belongs to the closing fence, so an answer still
      // arriving does not get one: it would put the caret on a line the model
      // has not written yet.
      const closed = i < lines.length
      i++
      out.push({
        kind: "code",
        lang: fence[2] ?? "",
        text: body.join("\n") + (closed && body.length ? "\n" : ""),
      })
      continue
    }

    const heading = HEADING.exec(line)
    if (heading) {
      out.push({
        kind: "heading",
        level: (heading[1] ?? "#").length,
        children: parseInline((heading[2] ?? "").replace(/\s+#+\s*$/, "")),
      })
      i++
      continue
    }

    if (RULE.test(line)) {
      out.push({ kind: "rule" })
      i++
      continue
    }

    if (QUOTE.test(line)) {
      const body: string[] = []
      while (i < lines.length && QUOTE.test(at(i))) {
        body.push(QUOTE.exec(at(i))?.[1] ?? "")
        i++
      }
      out.push({ kind: "quote", children: parseBlocks(body.join("\n")) })
      continue
    }

    // A table needs the row under its header to be the delimiter, so both
    // lines are read before either is claimed.
    if (line.includes("|") && i + 1 < lines.length && DELIMITER.test(at(i + 1))) {
      const head = splitRow(line)
      const align = splitRow(at(i + 1)).map(alignOf)
      i += 2
      const rows: Inline[][][] = []
      while (i < lines.length && at(i).includes("|") && at(i).trim() !== "") {
        rows.push(splitRow(at(i)).map(parseInline))
        i++
      }
      out.push({
        kind: "table",
        head: head.map(parseInline),
        align,
        rows,
      })
      continue
    }

    if (BULLET.test(line) || NUMBER.test(line)) {
      const ordered = !BULLET.test(line)
      const items: ListItem[] = []
      while (i < lines.length) {
        const m = ordered ? NUMBER.exec(at(i)) : BULLET.exec(at(i))
        if (!m) break
        const body = m[2] ?? ""
        const task = TASK.exec(body)
        items.push(
          task
            ? { checked: (task[1] ?? "").toLowerCase() === "x", children: parseInline(task[2] ?? "") }
            : { children: parseInline(body) },
        )
        i++
      }
      out.push({ kind: "list", ordered, items })
      continue
    }

    const para: string[] = []
    while (
      i < lines.length &&
      at(i).trim() !== "" &&
      !FENCE.test(at(i)) &&
      !HEADING.test(at(i)) &&
      !RULE.test(at(i)) &&
      !QUOTE.test(at(i)) &&
      !BULLET.test(at(i)) &&
      !NUMBER.test(at(i))
    ) {
      para.push(at(i))
      i++
    }
    out.push({ kind: "paragraph", children: parseInline(para.join("\n")) })
  }

  return out
}

function splitRow(row: string): string[] {
  const trimmed = row.trim().replace(/^\|/, "").replace(/\|$/, "")
  return trimmed.split("|").map((c) => c.trim())
}

function alignOf(cell: string): Align {
  const left = cell.startsWith(":")
  const right = cell.endsWith(":")
  if (left && right) return "center"
  if (right) return "right"
  return "left"
}

/**
 * Inline spans, innermost first.
 *
 * Code is taken before everything else because a backtick span is literal:
 * `**not bold**` is four asterisks and a phrase, and a model writing about
 * markdown depends on that being true.
 */
export function parseInline(src: string): Inline[] {
  const out: Inline[] = []
  let rest = src

  // Each pattern captures its delimiter so an unclosed one — which every
  // stream produces on its way through — is left as the text it currently is
  // rather than swallowing the remainder of the answer.
  const patterns: { re: RegExp; make: (m: RegExpExecArray) => Inline }[] = [
    { re: /`([^`]+)`/, make: (m) => ({ kind: "code", text: m[1] ?? "" }) },
    {
      re: /\[([^\]]*)\]\(([^)\s]+)\)/,
      make: (m) => {
        const href = safeHref(m[2] ?? "")
        return href === null
          ? { kind: "text", text: m[0] }
          : { kind: "link", href, children: parseInline(m[1] ?? "") }
      },
    },
    { re: /\*\*([^*]+)\*\*/, make: (m) => ({ kind: "strong", children: parseInline(m[1] ?? "") }) },
    { re: /__([^_]+)__/, make: (m) => ({ kind: "strong", children: parseInline(m[1] ?? "") }) },
    { re: /~~([^~]+)~~/, make: (m) => ({ kind: "del", children: parseInline(m[1] ?? "") }) },
    { re: /(?<![*\w])\*([^*\n]+)\*(?!\*)/, make: (m) => ({ kind: "em", children: parseInline(m[1] ?? "") }) },
    { re: /(?<![_\w])_([^_\n]+)_(?![_\w])/, make: (m) => ({ kind: "em", children: parseInline(m[1] ?? "") }) },
  ]

  while (rest !== "") {
    let best: { at: number; len: number; node: Inline } | null = null
    for (const p of patterns) {
      const m = p.re.exec(rest)
      if (m && (best === null || m.index < best.at)) {
        best = { at: m.index, len: m[0].length, node: p.make(m) }
      }
    }
    if (best === null) {
      out.push({ kind: "text", text: rest })
      break
    }
    if (best.at > 0) out.push({ kind: "text", text: rest.slice(0, best.at) })
    out.push(best.node)
    rest = rest.slice(best.at + best.len)
  }

  return out.filter((n) => n.kind !== "text" || n.text !== "")
}
