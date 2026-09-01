import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { AssistantTurn, UserTurn, formatCost, routeFromTrace, type TurnRoute } from "./message"
import type { RequestTrace, TraceAttempt } from "../../lib/api-types"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

const attempt = (provider: string, extra: Partial<TraceAttempt> = {}): TraceAttempt => ({
  seq: 0, provider, key_label: "", model: "m", outcome: "success", status_code: 200,
  latency_ms: 10, error: "", path: "passthrough", tokens_in: 0, tokens_out: 0,
  cost_micros: null, ...extra,
})

const trace = (over: Partial<RequestTrace> = {}): RequestTrace =>
  ({
    id: "01ABC", dialect: "openai", model: "fast", surface: "llm", status: "success",
    ts_ms: 0, tokens_in: 12, tokens_out: 30, cost_micros: null, ttft_ms: 100,
    total_ms: 900, attempts: [attempt("groq")], candidates: [], skips: [],
    ...over,
  }) as RequestTrace

const route = (over: Partial<TurnRoute> = {}): TurnRoute => ({
  requestId: "01ABC", provider: "groq", model: "llama-3.3", totalMs: 900,
  tokensIn: 12, tokensOut: 30, reasoningTokens: 0, costMicros: null,
  failedOver: [], warnings: [], ...over,
})

describe("reading the route off a trace", () => {
  it("names the provider that answered, not the one asked for", () => {
    // The client asks for an alias or a bare model. Which provider served is
    // the routing decision, and it is the reason to look at all.
    const r = routeFromTrace(trace({ model: "fast", final_model: "llama-3.3" }))
    expect(r.provider).toBe("groq")
    expect(r.model).toBe("llama-3.3")
  })

  it("records what was tried before the one that answered", () => {
    const r = routeFromTrace(
      trace({ attempts: [attempt("hackclub", { outcome: "retryable_credential", status_code: 401 }), attempt("groq")] }),
    )
    expect(r.failedOver).toEqual(["hackclub"])
    expect(r.provider).toBe("groq")
  })

  it("survives a trace with no attempts at all", () => {
    // A request refused before it reached a provider still writes a row.
    const r = routeFromTrace(trace({ attempts: [], provider: undefined }))
    expect(r.provider).toBe("")
    expect(r.failedOver).toEqual([])
  })
})

describe("an answered turn", () => {
  it("shows who answered and what it took", () => {
    render(<AssistantTurn text="hello" route={route()} />)
    expect(screen.getByText(/groq/)).toBeInTheDocument()
    expect(screen.getByText(/llama-3.3/)).toBeInTheDocument()
    expect(screen.getByText(/900ms/)).toBeInTheDocument()
    expect(screen.getByText(/12 in · 30 out/)).toBeInTheDocument()
  })

  it("calls out a failover rather than filing it as a number", () => {
    // The most interesting thing that can happen to a request.
    render(<AssistantTurn text="hi" route={route({ failedOver: ["hackclub", "naga-ac"] })} />)
    expect(screen.getByText(/failed over from hackclub, naga-ac/)).toBeInTheDocument()
  })

  it("renders the answer as markdown", () => {
    const { container } = render(<AssistantTurn text={"# Title\n\n`code`"} route={route()} />)
    expect(container.querySelector("h1")?.textContent).toBe("Title")
    expect(container.querySelector("code")?.textContent).toBe("code")
  })

  it("waits visibly before the first token, with no empty bubble", () => {
    render(<AssistantTurn text="" streaming />)
    expect(screen.getByLabelText("Waiting for the first token")).toBeInTheDocument()
  })

  it("offers no copy button until there is something to copy", () => {
    const { rerender } = render(<AssistantTurn text="" streaming />)
    expect(screen.queryByLabelText("Copy this answer")).toBeNull()
    rerender(<AssistantTurn text="done" route={route()} />)
    expect(screen.getByLabelText("Copy this answer")).toBeInTheDocument()
  })

  it("keeps the gutter before the trace lands", () => {
    // The route arrives a beat after the answer. The column must not resize
    // under the reader when it does.
    const { container } = render(<AssistantTurn text="hi" />)
    expect(container.querySelector(".size-7")).not.toBeNull()
    // Dashed is the loading mark, and this turn genuinely is still loading.
    expect(container.querySelector(".border-dashed")).not.toBeNull()
  })

  it("marks a settled turn whose trace is gone as settled, not as loading", () => {
    // A restored turn whose trace the log has swept has a route but no
    // provider. Drawing the dashed mark there says "still on its way" about
    // an answer that arrived days ago and never will again.
    const { container } = render(
      <AssistantTurn text="hi" route={route({ provider: "", totalMs: null })} />,
    )
    expect(container.querySelector(".size-7")).not.toBeNull()
    expect(container.querySelector(".border-dashed")).toBeNull()
  })
})

describe("the quiet route line", () => {
  it("says routed when the trace carried no duration", async () => {
    // A restored turn whose trace the log has swept has a route and no
    // numbers. The quiet line still has to say something, and "routed" is
    // the one thing that is true without inventing a figure.
    render(<AssistantTurn text="hi" route={route({ totalMs: null })} quiet />)
    expect(screen.getByRole("button", { name: "Show routing detail" })).toHaveTextContent(
      "routed",
    )
  })

  it("expands and collapses again", async () => {
    // The disclosure was one-way: once opened there was no control to shut
    // it, so a transcript read end to end filled up with detail nobody asked
    // to keep on screen.
    render(<AssistantTurn text="hi" route={route()} quiet />)
    await userEvent.click(screen.getByRole("button", { name: "Show routing detail" }))
    expect(screen.getByText("trace")).toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: "Hide routing detail" }))
    expect(screen.queryByText("trace")).toBeNull()
    expect(screen.getByRole("button", { name: "Show routing detail" })).toBeInTheDocument()
  })
})

describe("a typed turn", () => {
  it("keeps the operator's line breaks and does not render their markdown", () => {
    // What they typed is what was sent. Rendering it would show something
    // other than the prompt the provider received.
    const { container } = render(<UserTurn text={"one\ntwo **kept**"} />)
    expect(container.querySelector("strong")).toBeNull()
    expect(container.textContent).toContain("two **kept**")
    expect(container.querySelector("p")?.className).toContain("whitespace-pre-wrap")
  })
})

describe("cost", () => {
  it("keeps a fraction of a cent legible instead of rounding it to free", () => {
    expect(formatCost(2_500_000)).toBe("$2.50")
    expect(formatCost(3_400)).toBe("$0.0034")
    expect(formatCost(12)).toBe("<$0.0001")
  })
})

describe("what the provider dropped", () => {
  it("carries the trace's warnings onto the turn", () => {
    const r = routeFromTrace(trace({ warnings: ["top_k -> openai: not expressible"] }))
    expect(r.warnings).toEqual(["top_k -> openai: not expressible"])
  })

  it("treats a trace without warnings as none, not as unknown", () => {
    // The field is optional on the wire; a run that dropped nothing simply
    // omits it.
    expect(routeFromTrace(trace()).warnings).toEqual([])
  })

  it("shows each warning under the answer it belongs to", () => {
    // A control the dialect accepted can still be dropped by the provider --
    // temperature alongside thinking, say. Silence there is the same lie the
    // dialect gating exists to prevent.
    render(
      <AssistantTurn
        text="an answer"
        route={route({ warnings: ["temperature -> anthropic: rejected alongside thinking"] })}
      />,
    )
    expect(screen.getByText(/rejected alongside thinking/i)).toBeInTheDocument()
  })

  it("renders the warning as sent, without re-splitting it", () => {
    // The string is the Go side's format. Parsing it back into field, target
    // and reason would mis-split any reason containing the separator.
    const odd = "stop -> gemini: not expressible -> see the adapter notes"
    render(<AssistantTurn text="a" route={route({ warnings: [odd] })} />)
    expect(screen.getByText(odd)).toBeInTheDocument()
  })

  it("says nothing when there are no warnings", () => {
    const { container } = render(<AssistantTurn text="a" route={route({ warnings: [] })} />)
    expect(container.textContent).not.toMatch(/dropped/i)
    expect(container.textContent).not.toMatch(/not expressible/i)
  })
})

describe("the route line in Chat mode", () => {
  const route = {
    requestId: "01TRACE",
    provider: "groq",
    model: "llama",
    totalMs: 1240,
    tokensIn: 12,
    tokensOut: 40,
    reasoningTokens: 0,
    costMicros: 1500,
    failedOver: [],
    warnings: [],
  }

  it("quiets to the duration, and expands to the whole line on click", async () => {
    // A twenty-minute conversation does not want cost and token counts under
    // every turn. It does want them under the one turn being questioned,
    // which is what makes this a disclosure rather than a removal.
    render(<AssistantTurn text="an answer" route={route} quiet />)
    expect(screen.getByText("1.2s")).toBeInTheDocument()
    expect(screen.queryByText(/12 in/)).not.toBeInTheDocument()
    expect(screen.queryByRole("link", { name: "trace" })).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole("button", { name: /routing detail/i }))
    expect(screen.getByText(/12 in · 40 out/)).toBeInTheDocument()
    expect(screen.getByText("groq")).toBeInTheDocument()
  })

  it("stays expanded in Lab, where measurement is the point", () => {
    render(<AssistantTurn text="an answer" route={route} />)
    expect(screen.getByText(/12 in · 40 out/)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /routing detail/i })).not.toBeInTheDocument()
  })
})

describe("a turn the model reasoned before answering", () => {
  it("folds the working away behind its duration", async () => {
    // Reasoning is not the answer. A transcript that printed it inline is a
    // model talking to itself in front of the thing that was asked for -- and
    // the duration is the reading that explains a slow turn without opening
    // a trace.
    render(
      <AssistantTurn
        text="42"
        thinking={{ text: "first I weighed it", ms: 4200 }}
      />,
    )
    const trigger = screen.getByRole("button", { name: /thinking in 4\.2s/i })
    expect(screen.queryByText("first I weighed it")).toBeNull()

    await userEvent.click(trigger)
    expect(screen.getByText("first I weighed it")).toBeInTheDocument()
  })

  it("says it is still thinking while the working is arriving", () => {
    // ms is null until the model starts answering, so a live turn must not
    // print a duration it does not have yet.
    render(<AssistantTurn text="" streaming thinking={{ text: "weighing", ms: null }} />)
    expect(screen.getByRole("button", { name: /thinking…/i })).toBeInTheDocument()
    // And the waiting dots stand down: two things saying "working" at once.
    expect(screen.queryByLabelText("Waiting for the first token")).toBeNull()
  })

  it("shows nothing at all for a model that did not reason", () => {
    render(<AssistantTurn text="42" route={route()} />)
    expect(screen.queryByRole("button", { name: /thinking/i })).toBeNull()
    expect(screen.queryByText(/reasoned for/i)).toBeNull()
  })

  it("says a turn reasoned even when the working never arrived", () => {
    // The token count is off the trace, so it survives a reply whose wire
    // shape the extractor does not recognise -- and a provider that bills
    // reasoning while deliberately withholding the text. Before this, such a
    // turn spent most of its output budget thinking and showed nothing to
    // say so.
    render(<AssistantTurn text="42" route={route({ reasoningTokens: 512 })} />)
    expect(screen.getByText(/reasoned for 512 tokens/i)).toBeInTheDocument()
  })

  it("prefers the working itself when both are available", () => {
    render(
      <AssistantTurn
        text="42"
        route={route({ reasoningTokens: 512 })}
        thinking={{ text: "weighing it", ms: 900 }}
      />,
    )
    expect(screen.getByRole("button", { name: /thinking in 900ms/i })).toBeInTheDocument()
    expect(screen.queryByText(/working not returned/i)).toBeNull()
  })
})
