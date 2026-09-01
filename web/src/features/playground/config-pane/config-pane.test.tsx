import { render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { Sampling } from "./sampling"
import { Reasoning } from "./reasoning"
import { StructuredOutput } from "./structured-output"
import { emptyConfig, DIALECTS } from "../config"
import { reasonFor } from "../dialect-support"

vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, ...rest }: { children: React.ReactNode }) => <a {...rest}>{children}</a>,
}))

// reasonFor is stubbed (never faked for the tests above, which exercise the
// real matrix) so one test below can force a reason onto a control the
// matrix never actually refuses, proving the disabled prop tracks the
// reason rather than happening to agree with it under today's data.
const realReasonFor = vi.hoisted(() => ({ fn: undefined as unknown as typeof reasonFor }))

vi.mock("../dialect-support", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../dialect-support")>()
  realReasonFor.fn = actual.reasonFor
  return { ...actual, reasonFor: vi.fn(actual.reasonFor) }
})

afterEach(() => {
  vi.mocked(reasonFor).mockImplementation(realReasonFor.fn)
})

const noop = () => {}

describe("the sampling controls", () => {
  it("names the stream switch, which a wrapping label could not", () => {
    // Switch renders a role="switch" button, and a button takes its name
    // from aria-label or its own subtree -- never from an enclosing <label>.
    // Wrapped, it announced as "switch, off" with nothing saying what it
    // switched.
    render(<Sampling config={emptyConfig()} onChange={noop} />)
    expect(screen.getByRole("switch", { name: /stream the reply/i })).toBeInTheDocument()
  })

  it("enables top K on anthropic", () => {
    render(<Sampling config={{ ...emptyConfig(), dialect: "anthropic" }} onChange={noop} />)
    expect(screen.getByLabelText(/top k/i)).toBeEnabled()
  })

  it("disables top K on openai and says why", () => {
    // Disabled rather than hidden: a control that appears and disappears as
    // the dialect changes reads as a bug, while a disabled one with a reason
    // teaches something true about the three wires.
    render(<Sampling config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    const topK = screen.getByLabelText(/top k/i)
    expect(topK).toBeDisabled()
    expect(screen.getByText(/no top_k field/i)).toBeInTheDocument()
  })

  it("keeps a disabled control's stored value visible", () => {
    // A preset written under another dialect keeps what it stored. Blanking
    // the box would make the setting quietly lossy on every round trip.
    render(
      <Sampling config={{ ...emptyConfig(), dialect: "openai", topK: "40" }} onChange={noop} />,
    )
    expect(screen.getByLabelText(/top k/i)).toHaveValue("40")
  })

  it("enables top P on every dialect", () => {
    for (const dialect of ["openai", "anthropic", "gemini"] as const) {
      const { unmount } = render(<Sampling config={{ ...emptyConfig(), dialect }} onChange={noop} />)
      expect(screen.getByLabelText(/top p/i)).toBeEnabled()
      unmount()
    }
  })

  it("enables temperature and max tokens on every dialect", () => {
    for (const dialect of DIALECTS) {
      const { unmount } = render(<Sampling config={{ ...emptyConfig(), dialect }} onChange={noop} />)
      expect(screen.getByLabelText(/temperature/i)).toBeEnabled()
      expect(screen.getByLabelText(/max tokens/i)).toBeEnabled()
      unmount()
    }
  })

  it("disables temperature and max tokens when their reason is set", () => {
    // No live dialect refuses either control today, so this drives Sampling
    // with a stubbed reasonFor to prove the wiring itself -- not today's
    // matrix -- is what keeps a control's disabled state honest. Without
    // disabled tied to the reason, this would stay green even though
    // GatedField prints a reason underneath the still-enabled input.
    vi.mocked(reasonFor).mockImplementation((dialect, control) =>
      control === "temperature" || control === "maxTokens"
        ? "stubbed for this test: not actually refused by any dialect"
        : realReasonFor.fn(dialect, control),
    )
    render(<Sampling config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    expect(screen.getByLabelText(/temperature/i)).toBeDisabled()
    expect(screen.getByLabelText(/max tokens/i)).toBeDisabled()
  })
})

describe("reasoning, in whichever spelling the dialect uses", () => {
  it("offers an effort tier on openai", () => {
    render(<Reasoning config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    expect(screen.getByLabelText(/effort/i)).toBeEnabled()
    expect(screen.getByLabelText(/budget/i)).toBeDisabled()
  })

  it("offers a token budget on anthropic and gemini", () => {
    for (const dialect of ["anthropic", "gemini"] as const) {
      const { unmount } = render(<Reasoning config={{ ...emptyConfig(), dialect }} onChange={noop} />)
      expect(screen.getByLabelText(/budget/i)).toBeEnabled()
      expect(screen.getByLabelText(/effort/i)).toBeDisabled()
      unmount()
    }
  })
})

describe("structured output", () => {
  it("is a schema editor, not a switch", () => {
    // The OpenAI edge honours response_format only with a schema present, so
    // a JSON-mode toggle would be a control that did nothing on two of the
    // three dialects.
    render(<StructuredOutput config={{ ...emptyConfig(), dialect: "openai" }} onChange={noop} />)
    expect(screen.getByLabelText(/schema/i)).toBeEnabled()
  })

  it("reports malformed JSON rather than sending nothing", () => {
    render(
      <StructuredOutput
        config={{ ...emptyConfig(), dialect: "openai", schemaRaw: "{not json" }}
        onChange={noop}
      />,
    )
    expect(screen.getByText(/schema must be JSON/i)).toBeInTheDocument()
  })

  it("is disabled on anthropic, whose edge never reads it", () => {
    render(<StructuredOutput config={{ ...emptyConfig(), dialect: "anthropic" }} onChange={noop} />)
    expect(screen.getByLabelText(/schema/i)).toBeDisabled()
    expect(screen.getByText(/does not read response_format/i)).toBeInTheDocument()
  })
})
