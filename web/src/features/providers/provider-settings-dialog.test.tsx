import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, it, expect } from "vitest"
import {
  ProviderSettingsDialog,
  draftOf,
  settingsPatch,
  type SettingsDraft,
} from "./provider-settings-dialog"
import type { Provider } from "../../lib/api-types"

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

const provider = (over: Partial<Provider> = {}): Provider => ({
  id: "groq", name: "Groq", preset: "groq", kind: "openaicompat",
  base_url: "https://api.groq.com", priority: 10, enabled: true,
  auth_style: "bearer", free_models_only: false, allow_unsanctioned_free: false, credentials: [],
  ...over,
})

const draft = (p: Provider, over: Partial<SettingsDraft> = {}): SettingsDraft => ({
  ...draftOf(p),
  ...over,
})

describe("settingsPatch", () => {
  it("sends nothing when the dialog is opened and saved untouched", () => {
    // The draft starts from the provider, so an unedited save must not rewrite
    // values that are already what they are.
    const p = provider()
    expect(settingsPatch(draft(p), p)).toEqual({})
  })

  it("sends only the fields that changed", () => {
    const p = provider()
    expect(settingsPatch(draft(p, { priority: "20" }), p)).toEqual({ priority: 20 })
    expect(settingsPatch(draft(p, { freeModelsOnly: true }), p)).toEqual({
      free_models_only: true,
    })
  })

  it("sends region and project only once they have been touched", () => {
    // Both are pointer fields on the backend: a key present with value ""
    // means "set this to empty", not "leave alone". GET /api/providers never
    // returns either, so an untouched field has no current value to re-send
    // and sending one anyway would wipe it.
    const p = provider()
    expect(settingsPatch(draft(p, { region: "us-east1" }), p)).toEqual({ region: "us-east1" })
    expect(settingsPatch(draft(p, { project: "my-gcp-project" }), p)).toEqual({
      project: "my-gcp-project",
    })
  })

  it("carries an intentional clear", () => {
    // Once an operator has focused a field, an empty string is a deliberate
    // clear rather than an unset value, and has to travel as one.
    const p = provider()
    expect(settingsPatch(draft(p, { region: "", project: "keep" }), p)).toEqual({
      region: "", project: "keep",
    })
  })

  it("leaves out a priority that will not parse", () => {
    // NaN serialises to null, which the backend would read as "clear the
    // priority" rather than as the typo it is.
    const p = provider()
    expect(settingsPatch(draft(p, { priority: "" }), p)).toEqual({})
    expect(settingsPatch(draft(p, { priority: "abc" }), p)).toEqual({})
  })

  it("sends every touched field together", () => {
    // One visit is one write: three separate saves is a way to leave two
    // applied and the third not.
    const p = provider()
    expect(
      settingsPatch(
        draft(p, { priority: "5", freeModelsOnly: true, region: "eu", project: "proj" }),
        p,
      ),
    ).toEqual({ priority: 5, free_models_only: true, region: "eu", project: "proj" })
  })
})

describe("reopening the settings dialog", () => {
  it("shows what the provider says now, not what it said at mount", () => {
    // The dialog outlives any one visit. A draft seeded once would keep
    // showing — and on save write back — a priority the provider stopped
    // having while the dialog sat closed.
    const { rerender } = mount(
      <ProviderSettingsDialog provider={provider()} open={false} onOpenChange={() => {}} />,
    )

    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    rerender(
      <QueryClientProvider client={client}>
        <ProviderSettingsDialog
          provider={provider({ priority: 42 })}
          open
          onOpenChange={() => {}}
        />
      </QueryClientProvider>,
    )

    expect(screen.getByLabelText("Priority")).toHaveValue("42")
  })
})

describe("settingsPatch base URL", () => {
  it("sends the base URL once it differs from the provider's", () => {
    const p = provider()
    expect(settingsPatch(draft(p, { baseUrl: "http://gw:11434/v1" }), p)).toEqual({
      base_url: "http://gw:11434/v1",
    })
  })

  it("ignores surrounding whitespace rather than writing it", () => {
    const p = provider({ base_url: "http://gw:11434/v1" })
    expect(settingsPatch(draft(p, { baseUrl: "  http://gw:11434/v1  " }), p)).toEqual({})
  })

  it("leaves an emptied box alone instead of clearing the endpoint", () => {
    // A provider with no base URL is unreachable, and the backend rejects the
    // write anyway. Refusing to send it keeps a slip from becoming a 400.
    const p = provider()
    expect(settingsPatch(draft(p, { baseUrl: "   " }), p)).toEqual({})
  })
})
