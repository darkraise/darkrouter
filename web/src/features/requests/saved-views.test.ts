import { beforeEach, describe, expect, it } from "vitest"
import { deleteView, loadSavedViews, saveView } from "./saved-views"

beforeEach(() => localStorage.clear())

describe("saved views", () => {
  it("round-trips a view through localStorage", () => {
    saveView("errors today", { status: "error", since_ms: "1724630400000" })
    expect(loadSavedViews()).toEqual([
      { name: "errors today", filters: { status: "error", since_ms: "1724630400000" } },
    ])
  })

  it("replaces a view of the same name rather than duplicating it", () => {
    saveView("mine", { status: "error" })
    saveView("mine", { status: "success" })
    const views = loadSavedViews()
    expect(views).toHaveLength(1)
    expect(views[0]?.filters.status).toBe("success")
  })

  it("drops empty filters so a saved view carries only what it filters", () => {
    saveView("providers", { provider: "groq", model: "" })
    expect(loadSavedViews()[0]?.filters).toEqual({ provider: "groq" })
  })

  it("deletes by name", () => {
    saveView("a", { status: "error" })
    saveView("b", { status: "success" })
    expect(deleteView("a").map((v) => v.name)).toEqual(["b"])
  })

  it("survives corrupt storage rather than throwing on every render", () => {
    // Anything can write to localStorage, including an older build of this
    // app. A parse failure must lose the views, not the screen.
    localStorage.setItem("darkrouter_saved_views", "{not json")
    expect(loadSavedViews()).toEqual([])
  })
})
