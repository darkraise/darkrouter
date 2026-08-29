import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { Markdown } from "./markdown"

/** The renderer builds React nodes, so a test reads the DOM it produced. */
function md(text: string) {
  const { container } = render(<Markdown text={text} />)
  return container
}

describe("what a model actually emits", () => {
  it("keeps a code fence whole, with its language", () => {
    // The reason this renderer exists: a fenced block arrived as one run of
    // unstyled monospace, indistinguishable from prose around it.
    const c = md("Try this:\n\n```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n")
    const code = c.querySelector("pre code")
    expect(code?.textContent).toBe('func main() {\n\tprintln("hi")\n}\n')
    expect(screen.getByText("go")).toBeInTheDocument()
  })

  it("leaves markdown inside a fence alone", () => {
    // Code is not prose. A model explaining markdown must not have its
    // example eaten by the renderer.
    const c = md("```\n**not bold** and | not | a | table\n```")
    expect(c.querySelector("pre code")?.textContent).toBe(
      "**not bold** and | not | a | table\n",
    )
    expect(c.querySelector("strong")).toBeNull()
  })

  it("renders a GFM table", () => {
    const c = md(
      "| model | tokens |\n|---|---:|\n| gpt-5 | 120 |\n| haiku | 8 |\n",
    )
    expect(c.querySelectorAll("thead th")).toHaveLength(2)
    expect(c.querySelectorAll("tbody tr")).toHaveLength(2)
    expect(screen.getByText("gpt-5")).toBeInTheDocument()
    // The delimiter row carries alignment, which is the only reason it is
    // three characters of punctuation rather than noise.
    expect(c.querySelectorAll("thead th")[1]?.getAttribute("style")).toContain("right")
  })

  it("renders a task list with its boxes", () => {
    const c = md("- [x] shipped\n- [ ] still open\n")
    const boxes = c.querySelectorAll('input[type="checkbox"]')
    expect(boxes).toHaveLength(2)
    expect((boxes[0] as HTMLInputElement).checked).toBe(true)
    expect((boxes[1] as HTMLInputElement).checked).toBe(false)
    // Nobody can tick a model's answer.
    expect((boxes[0] as HTMLInputElement).disabled).toBe(true)
  })

  it("renders ordered and unordered lists", () => {
    expect(md("- one\n- two\n").querySelectorAll("ul li")).toHaveLength(2)
    expect(md("1. one\n2. two\n").querySelectorAll("ol li")).toHaveLength(2)
  })

  it("renders headings, quotes and rules", () => {
    expect(md("## Heading\n").querySelector("h2")?.textContent).toBe("Heading")
    expect(md("> quoted\n").querySelector("blockquote")?.textContent).toContain("quoted")
    expect(md("---\n").querySelector("hr")).not.toBeNull()
  })

  it("renders inline emphasis, code and links", () => {
    const c = md("**bold** _italic_ ~~gone~~ `code` [docs](https://example.com/x)")
    expect(c.querySelector("strong")?.textContent).toBe("bold")
    expect(c.querySelector("em")?.textContent).toBe("italic")
    expect(c.querySelector("del")?.textContent).toBe("gone")
    expect(c.querySelector("code")?.textContent).toBe("code")
    const link = c.querySelector("a")
    expect(link?.getAttribute("href")).toBe("https://example.com/x")
    // Someone else's model, someone else's link: it opens in a new tab and
    // must not hand the opener over.
    expect(link?.getAttribute("rel")).toContain("noopener")
  })
})

describe("model output is not trusted markup", () => {
  it("shows HTML as text rather than rendering it", () => {
    // The transcript prints whatever a provider streamed back. Rendering it
    // as markup would let any provider script the console.
    const c = md("<img src=x onerror=alert(1)> <b>not bold</b>")
    expect(c.querySelector("img")).toBeNull()
    expect(c.querySelector("b")).toBeNull()
    expect(c.textContent).toContain("<img src=x onerror=alert(1)>")
  })

  it("refuses a javascript: link", () => {
    // Rendered as text, so the reader sees exactly what the model wrote and
    // no click can run it.
    const c = md("[click me](javascript:alert(1))")
    expect(c.querySelector("a")).toBeNull()
    expect(c.textContent).toContain("click me")
  })

  it("allows only the schemes a link can safely carry", () => {
    expect(md("[a](https://x.test)").querySelector("a")).not.toBeNull()
    expect(md("[a](http://x.test)").querySelector("a")).not.toBeNull()
    expect(md("[a](mailto:x@x.test)").querySelector("a")).not.toBeNull()
    expect(md("[a](data:text/html;base64,PHNjcmlwdD4=)").querySelector("a")).toBeNull()
  })
})

describe("a stream half-arrived", () => {
  it("renders an unclosed fence as code rather than losing it", () => {
    // Every streamed answer passes through this state on its way to being
    // complete. Waiting for the closing fence would make code appear to
    // arrive all at once at the end.
    const c = md("here:\n\n```py\nprint(1)")
    expect(c.querySelector("pre code")?.textContent).toBe("print(1)")
  })

  it("renders an unclosed bold as the text so far", () => {
    expect(md("this is **half").textContent).toContain("this is **half")
  })
})
