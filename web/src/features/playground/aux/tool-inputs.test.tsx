import { fireEvent, render, screen } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"
import { ToolInputs } from "./tool-inputs"

function inputs(form: Record<string, string>) {
  return render(
    <ToolInputs
      surface="count"
      needsFile={false}
      form={form}
      busy={false}
      onField={vi.fn()}
      onFile={vi.fn()}
      onRun={vi.fn()}
    />,
  )
}

describe("the tool inputs", () => {
  it("says why Run is disabled", () => {
    // A disabled button that gives no reason is a control the operator
    // clicks and then reads the whole form to work out why.
    inputs({})
    expect(screen.getByRole("button", { name: /run/i })).toBeDisabled()
    expect(screen.getByText(/name a model/i)).toBeInTheDocument()
  })

  it("drops the explanation once the run can go", () => {
    inputs({ model: "claude-3", dialect: "anthropic", prompt: "count me" })
    expect(screen.getByRole("button", { name: /run/i })).toBeEnabled()
    expect(screen.queryByText(/name a model/i)).toBeNull()
  })

  it("ignores Enter on the prompt while an input method is composing", () => {
    const onRun = vi.fn()
    render(
      <ToolInputs
        surface="count"
        needsFile={false}
        form={{ model: "claude-3", dialect: "anthropic", prompt: "数える" }}
        busy={false}
        onField={vi.fn()}
        onFile={vi.fn()}
        onRun={onRun}
      />,
    )
    const field = screen.getByLabelText("Prompt")
    fireEvent.keyDown(field, { key: "Enter", isComposing: true })
    expect(onRun).not.toHaveBeenCalled()
    fireEvent.keyDown(field, { key: "Enter" })
    expect(onRun).toHaveBeenCalledTimes(1)
  })
})
