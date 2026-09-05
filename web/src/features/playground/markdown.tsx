import { useEffect, useMemo, useRef, useState } from "react"
import { Check, Copy } from "lucide-react"
import { parseBlocks, type Block, type Inline } from "./markdown-parse"

/** How often a streaming answer is re-parsed. A stream delivers many chunks
 *  a second, and parsing the whole answer on every one made a long reply
 *  stutter as it grew; fifty milliseconds is under a frame of reading. */
const STREAM_PARSE_MS = 50

/**
 * `text`, held back to one change per interval while `active`. Off, it is
 * the text itself, immediately: the finished answer must not lag its stream.
 */
function useThrottled(text: string, active: boolean): string {
  const [shown, setShown] = useState(text)
  const flushedAt = useRef(0)
  useEffect(() => {
    // Nothing to hold back when the answer is finished: the return below
    // hands back `text` itself, so `shown` is not read at all.
    if (!active) return
    // Always through the timer, even when the interval has already elapsed
    // and the wait is zero. Flushing synchronously here would land a second
    // render inside the first one's effects for every chunk that arrives
    // more than 50 ms after the last.
    const wait = Math.max(0, STREAM_PARSE_MS - (Date.now() - flushedAt.current))
    const timer = window.setTimeout(() => {
      flushedAt.current = Date.now()
      setShown(text)
    }, wait)
    return () => window.clearTimeout(timer)
  }, [text, active])
  return active ? shown : text
}

/**
 * A model's answer, rendered.
 *
 * Every node here is built by React from parsed data. There is no HTML string
 * anywhere in this file and no dangerouslySetInnerHTML, which is what makes a
 * provider's answer inert by construction rather than by sanitising: markup a
 * model writes arrives as the characters it wrote.
 *
 * The parse is memoised on the text, so a finished answer is parsed once
 * however often the turns around it re-render, and throttled while the answer
 * is still arriving.
 */
export function Markdown({ text, streaming = false }: { text: string; streaming?: boolean }) {
  const shown = useThrottled(text, streaming)
  const blocks = useMemo(() => parseBlocks(shown), [shown])
  return <div className="flex min-w-0 flex-col gap-3">{renderBlocks(blocks)}</div>
}

function renderBlocks(blocks: Block[]) {
  return blocks.map((b, i) => <Blk key={i} block={b} />)
}

function Blk({ block }: { block: Block }) {
  switch (block.kind) {
    case "paragraph":
      return (
        <p className="text-sm leading-relaxed whitespace-pre-wrap">
          <Spans nodes={block.children} />
        </p>
      )

    case "heading": {
      // The scale, not a size: the console rebinds --text-* on the font-size
      // axis, so a heading has to move with it. h1 and h2 in an answer are
      // the model's structure, not the page's, so they sit a step below what
      // the page uses for its own headings.
      const size = block.level <= 2 ? "text-lg" : "text-base"
      const Tag = `h${Math.min(block.level, 6)}` as "h1"
      return (
        <Tag className={`${size} mt-1 font-semibold tracking-tight`}>
          <Spans nodes={block.children} />
        </Tag>
      )
    }

    case "code":
      return <CodeBlock lang={block.lang} text={block.text} />

    case "quote":
      return (
        <blockquote className="border-l-2 pl-3 text-[hsl(var(--muted-foreground))]">
          {renderBlocks(block.children)}
        </blockquote>
      )

    case "list": {
      const Tag = block.ordered ? "ol" : "ul"
      return (
        <Tag
          className={`flex flex-col gap-1 pl-5 text-sm ${
            block.ordered ? "list-decimal" : "list-disc"
          } marker:text-[hsl(var(--legend))]`}
        >
          {block.items.map((item, i) => (
            <li key={i} className={item.checked === undefined ? "" : "list-none -ml-5 flex gap-2"}>
              {item.checked === undefined ? null : (
                // Disabled, because this is a record of what a model said and
                // not a checklist anybody owns.
                <input
                  type="checkbox"
                  checked={item.checked}
                  disabled
                  readOnly
                  className="mt-1 size-[var(--icon-size,1rem)] shrink-0 accent-[hsl(var(--primary))]"
                />
              )}
              <span className={item.checked ? "text-[hsl(var(--muted-foreground))] line-through" : ""}>
                <Spans nodes={item.children} />
              </span>
            </li>
          ))}
        </Tag>
      )
    }

    case "table":
      return (
        // Scrolls on its own rather than widening the transcript: a model
        // asked for a comparison writes as many columns as it likes.
        <div className="overflow-x-auto rounded-[var(--radius)] border">
          <table className="w-full border-collapse text-sm">
            <thead>
              <tr className="border-b bg-[hsl(var(--muted))]">
                {block.head.map((cell, i) => (
                  <th
                    key={i}
                    style={{ textAlign: block.align[i] ?? "left" }}
                    className="px-3 py-2 font-medium"
                  >
                    <Spans nodes={cell} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {block.rows.map((row, r) => (
                <tr key={r} className="border-b last:border-b-0">
                  {row.map((cell, c) => (
                    <td key={c} style={{ textAlign: block.align[c] ?? "left" }} className="px-3 py-2">
                      <Spans nodes={cell} />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )

    case "rule":
      return <hr className="border-t" />
  }
}

function Spans({ nodes }: { nodes: Inline[] }) {
  return (
    <>
      {nodes.map((n, i) => {
        switch (n.kind) {
          case "text":
            return <span key={i}>{n.text}</span>
          case "code":
            return (
              <code
                key={i}
                className="rounded bg-[hsl(var(--muted))] px-1 py-0.5 font-mono text-sm"
              >
                {n.text}
              </code>
            )
          case "strong":
            return (
              <strong key={i} className="font-semibold">
                <Spans nodes={n.children} />
              </strong>
            )
          case "em":
            return (
              <em key={i}>
                <Spans nodes={n.children} />
              </em>
            )
          case "del":
            return (
              <del key={i} className="text-[hsl(var(--muted-foreground))]">
                <Spans nodes={n.children} />
              </del>
            )
          case "link":
            return (
              // Someone else's model wrote this href. New tab, and no opener
              // handed over with it.
              <a
                key={i}
                href={n.href}
                target="_blank"
                rel="noopener noreferrer"
                className="underline underline-offset-2"
              >
                <Spans nodes={n.children} />
              </a>
            )
        }
      })}
    </>
  )
}

/**
 * A fenced block: the language it declared, and a copy button.
 *
 * Copying is the thing an operator does with code in a playground, and
 * selecting it out of a streamed transcript by hand is the fiddliest gesture
 * on the page.
 */
function CodeBlock({ lang, text }: { lang: string; text: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <figure className="group relative overflow-hidden rounded-[var(--radius)] border bg-[hsl(var(--muted))]">
      <figcaption className="flex items-center justify-between border-b px-3 py-1.5">
        <span className="font-mono text-sm text-[hsl(var(--legend))]">{lang || "text"}</span>
        <button
          type="button"
          aria-label={`Copy the ${lang || "code"} block`}
          className="flex items-center gap-1.5 rounded-sm px-1.5 py-0.5 text-sm text-[hsl(var(--legend))] transition-colors hover:text-[hsl(var(--foreground))]"
          onClick={() => {
            void navigator.clipboard?.writeText(text)
            setCopied(true)
            window.setTimeout(() => setCopied(false), 1200)
          }}
        >
          {copied ? (
            <Check className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
          ) : (
            <Copy className="size-[var(--icon-size,1rem)]" aria-hidden="true" />
          )}
          {copied ? "Copied" : "Copy"}
        </button>
      </figcaption>
      <pre className="overflow-x-auto px-3 py-2">
        <code className="font-mono text-sm">{text}</code>
      </pre>
    </figure>
  )
}
