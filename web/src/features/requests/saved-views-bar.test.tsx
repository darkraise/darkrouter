import { render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it } from "vitest"
import { SavedViewsBar } from "./saved-views-bar"
import { loadSavedViews } from "./saved-views"

beforeEach(() => localStorage.clear())

function renderBar() {
  render(<SavedViewsBar fields={["status"]} filters={{ status: "error" }} onApply={() => {}} />)
}

describe("saving a view", () => {
  it("saves on Enter, trimmed", async () => {
    renderBar()
    await userEvent.click(screen.getByRole("button", { name: "Save this view" }))
    await userEvent.type(screen.getByRole("textbox", { name: "View name" }), "  errors {Enter}")
    expect(loadSavedViews()).toEqual([{ name: "errors", filters: { status: "error" } }])
    expect(screen.getByRole("button", { name: "errors" })).toBeInTheDocument()
  })

  it("cancels on Escape", async () => {
    renderBar()
    await userEvent.click(screen.getByRole("button", { name: "Save this view" }))
    await userEvent.type(screen.getByRole("textbox", { name: "View name" }), "half{Escape}")
    expect(screen.queryByRole("textbox", { name: "View name" })).toBeNull()
    expect(loadSavedViews()).toEqual([])
  })

  it("will not save without a name, and says so by disabling Save", async () => {
    renderBar()
    await userEvent.click(screen.getByRole("button", { name: "Save this view" }))
    const save = screen.getByRole("button", { name: "Save" })
    expect(save).toBeDisabled()
    await userEvent.type(screen.getByRole("textbox", { name: "View name" }), "   ")
    expect(save).toBeDisabled()
  })
})
