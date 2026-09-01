import { render, screen } from "@testing-library/react"
import { describe, it, expect, vi } from "vitest"
import { RunCard } from "./results"
import type { AuxRun, AuxOutcome } from "./surfaces"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

function runOf(outcome: AuxOutcome): AuxRun {
  return { id: 1, at: Date.now(), summary: "what was asked", requestId: "01ABC", outcome }
}

describe("a run's heading", () => {
  it("says what was asked, so a scrolled-back result is still legible", () => {
    // Two embeddings in a column are indistinguishable without the text that
    // produced each.
    render(<RunCard run={runOf({ kind: "embedding", vector: [0.1] })} />)
    expect(screen.getByText("what was asked")).toBeInTheDocument()
    // The router Link is mocked to a bare anchor here, so this asserts on the
    // text rather than the link role.
    expect(screen.getByText(/view the trace/i)).toBeInTheDocument()
  })
})

describe("a rerank result", () => {
  it("numbers the documents in the order the model returned them", () => {
    render(
      <RunCard
        run={runOf({
          kind: "rerank",
          ranked: [
            { index: 2, score: 0.91, text: "the best one" },
            { index: 0, score: 0.12, text: "the worst one" },
          ],
        })}
      />,
    )
    expect(screen.getByText("the best one")).toBeInTheDocument()
    expect(screen.getByText("0.910")).toBeInTheDocument()
    expect(screen.getByText("0.120")).toBeInTheDocument()
    // The rank, not the wire index: what an operator reads is the ordering.
    expect(screen.getByText("1")).toBeInTheDocument()
    expect(screen.getByText("2")).toBeInTheDocument()
  })
})

describe("a moderation result", () => {
  it("leads with the verdict", () => {
    render(
      <RunCard
        run={runOf({
          kind: "moderation",
          flagged: true,
          scores: [{ name: "violence", score: 0.98, flagged: true }],
        })}
      />,
    )
    expect(screen.getByText("Flagged")).toBeInTheDocument()
    expect(screen.getByText("violence")).toBeInTheDocument()
  })

  it("hides the categories that scored nothing", () => {
    // A moderation response carries every category the model knows. Twenty
    // rows of zero bury the one row that says something.
    render(
      <RunCard
        run={runOf({
          kind: "moderation",
          flagged: false,
          scores: [
            { name: "violence", score: 0.4, flagged: false },
            { name: "hate", score: 0, flagged: false },
          ],
        })}
      />,
    )
    expect(screen.getByText("Clean")).toBeInTheDocument()
    expect(screen.getByText("violence")).toBeInTheDocument()
    expect(screen.queryByText("hate")).toBeNull()
  })

  it("says so when nothing scored at all, rather than drawing an empty panel", () => {
    render(<RunCard run={runOf({ kind: "moderation", flagged: false, scores: [] })} />)
    expect(screen.getByText(/no category scored/i)).toBeInTheDocument()
  })
})

describe("an embedding result", () => {
  it("states the length, which is the fact being checked", () => {
    const vector = Array.from({ length: 1536 }, (_, i) => (i % 2 ? 0.1 : -0.1))
    render(<RunCard run={runOf({ kind: "embedding", vector })} />)
    expect(screen.getByText(/1536 components/)).toBeInTheDocument()
    // The strip is a picture of the vector, named for anyone who cannot see it.
    expect(screen.getByRole("img", { name: /of 1536 components/ })).toBeInTheDocument()
  })
})

describe("a transcription result", () => {
  it("reads as prose rather than as a tree", () => {
    render(<RunCard run={runOf({ kind: "transcript", text: "what the caller said" })} />)
    expect(screen.getByText("what the caller said")).toBeInTheDocument()
  })
})

describe("a token-count result", () => {
  it("distinguishes a native count from a local estimate", () => {
    const { rerender } = render(
      <RunCard run={runOf({ kind: "count", tokens: 42, estimated: false })} />,
    )
    expect(screen.getByText("42 tokens")).toBeInTheDocument()
    expect(screen.getByText("Native count")).toBeInTheDocument()

    rerender(<RunCard run={runOf({ kind: "count", tokens: 39, estimated: true })} />)
    expect(screen.getByText("39 tokens")).toBeInTheDocument()
    expect(screen.getByText("Estimated locally")).toBeInTheDocument()
  })
})
