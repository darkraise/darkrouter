import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { toast } from "darkraise-ui"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { afterEach, describe, it, expect, vi } from "vitest"
import {
  ProviderSettingsDialog,
  draftOf,
  priorityError,
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

  it("sends region and project only once they differ from what the provider holds", () => {
    // Both are pointer fields on the backend: a key present with value ""
    // means "set this to empty", not "leave alone", so re-sending an untouched
    // value is a write nobody asked for.
    const p = provider()
    expect(settingsPatch(draft(p, { region: "us-east1" }), p)).toEqual({ region: "us-east1" })
    expect(settingsPatch(draft(p, { project: "my-gcp-project" }), p)).toEqual({
      project: "my-gcp-project",
    })
    const bedrock = provider({ kind: "bedrock", region: "us-east-1" })
    expect(settingsPatch(draft(bedrock), bedrock)).toEqual({})
  })

  it("opens on the region the provider holds rather than calling it unset", () => {
    // Bedrock with us-east-1 stored read "unset" in the one field it needs.
    const bedrock = provider({ kind: "bedrock", region: "us-east-1" })
    expect(draftOf(bedrock).region).toBe("us-east-1")
    mount(<ProviderSettingsDialog provider={bedrock} open onOpenChange={() => {}} />)
    expect(screen.getByLabelText("Region")).toHaveValue("us-east-1")
    expect(screen.queryByDisplayValue("unset")).toBeNull()
    expect(screen.queryByPlaceholderText("unset")).toBeNull()
  })

  it("sends location only once it has been touched", () => {
    // A Vertex row created before location was required has none, and this
    // is the only way to give it one.
    const p = provider({ kind: "vertex" })
    expect(settingsPatch(draft(p), p)).not.toHaveProperty("location")
    expect(settingsPatch(draft(p, { location: "us-central1" }), p)).toEqual({
      location: "us-central1",
    })
  })

  it("trims a location, and sends none that is only whitespace", () => {
    // It can be set once, so a stray space would be stored for good.
    const p = provider({ kind: "vertex" })
    expect(settingsPatch(draft(p, { location: "  us-central1 " }), p)).toEqual({
      location: "us-central1",
    })
    expect(settingsPatch(draft(p, { location: "   " }), p)).toEqual({})
  })

  it("sends no location for a provider that is not Vertex", () => {
    const p = provider()
    expect(settingsPatch(draft(p, { location: "us-central1" }), p)).toEqual({})
  })

  it("carries an intentional clear", () => {
    // Emptying a box that held a value is a deliberate clear rather than an
    // unset value, and has to travel as one.
    const p = provider({ region: "us-east-1" })
    expect(settingsPatch(draft(p, { region: "", project: "keep" }), p)).toEqual({
      region: "", project: "keep",
    })
  })

  it("explains a priority it will not save, and holds Save", async () => {
    // Save used to go grey on "abc" with no reason given -- or, with another
    // field edited, stay live and drop the typed priority from the write.
    expect(priorityError("abc")).toMatch(/whole number/)
    expect(priorityError("10.5")).toMatch(/whole number/)
    expect(priorityError("0x10")).toMatch(/whole number/)
    expect(priorityError("")).toMatch(/whole number/)
    expect(priorityError(" -3 ")).toBeNull()

    mount(<ProviderSettingsDialog provider={provider()} open onOpenChange={() => {}} />)
    await userEvent.clear(screen.getByLabelText("Priority"))
    await userEvent.type(screen.getByLabelText("Priority"), "abc")
    await userEvent.click(screen.getByLabelText("Import free models only"))

    expect(screen.getByRole("alert")).toHaveTextContent("Priority must be a whole number")
    expect(screen.getByLabelText("Priority")).toHaveAttribute("aria-invalid", "true")
    expect(screen.getByRole("button", { name: "Save changes" })).toBeDisabled()
  })

  it("offers only the endpoint fields the provider's kind reads", () => {
    const { unmount } = mount(
      <ProviderSettingsDialog provider={provider()} open onOpenChange={() => {}} />,
    )
    expect(screen.queryByLabelText("Region")).toBeNull()
    expect(screen.queryByLabelText("Project")).toBeNull()
    unmount()

    mount(<ProviderSettingsDialog provider={provider({ kind: "bedrock" })} open onOpenChange={() => {}} />)
    expect(screen.getByLabelText("Region")).toBeInTheDocument()
    expect(screen.queryByLabelText("Project")).toBeNull()
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

describe("the base URL of a provider whose endpoint is derived", () => {
  it("previews the endpoint an empty box means, following the region", async () => {
    // Bedrock stores no base URL, so the box opened empty under help text
    // that said nothing about where the requests then go.
    const bedrock = provider({ kind: "bedrock", base_url: "", region: "us-east-1" })
    mount(<ProviderSettingsDialog provider={bedrock} open onOpenChange={() => {}} />)

    const box = screen.getByLabelText("Base URL")
    expect(box).toHaveValue("")
    expect(box).toHaveAttribute("placeholder", "https://bedrock-runtime.us-east-1.amazonaws.com")
    expect(screen.getByText(/derives the endpoint from the region/)).toBeInTheDocument()

    const region = screen.getByLabelText("Region")
    await userEvent.clear(region)
    await userEvent.type(region, "eu-west-1")
    expect(box).toHaveAttribute("placeholder", "https://bedrock-runtime.eu-west-1.amazonaws.com")
  })

  it("keeps the ordinary help for a provider that names its own", () => {
    mount(<ProviderSettingsDialog provider={provider()} open onOpenChange={() => {}} />)
    expect(screen.getByLabelText("Base URL")).not.toHaveAttribute("placeholder")
    expect(screen.getByText(/Emptying the box leaves it unchanged/)).toBeInTheDocument()
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

describe("the location field", () => {
  it("is offered only for a Vertex provider, and says it is set once", () => {
    const { unmount } = mount(
      <ProviderSettingsDialog provider={provider()} open onOpenChange={() => {}} />,
    )
    expect(screen.queryByLabelText("Location")).toBeNull()
    unmount()

    mount(<ProviderSettingsDialog provider={provider({ kind: "vertex" })} open onOpenChange={() => {}} />)
    expect(screen.getByLabelText("Location")).toBeInTheDocument()
    expect(screen.getByText(/location can be set once/i)).toBeInTheDocument()
  })
})

describe("saving settings the gateway did not load", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it("closes on a saved note rather than reporting a failure", async () => {
    // A 500 carrying routing_updated:false committed. Left open on an error,
    // the dialog invites a second save of a change that already happened.
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(
          JSON.stringify({ error: "the change was saved, but the gateway could not load it", routing_updated: false }),
          { status: 500, headers: { "Content-Type": "application/json" } },
        ),
      ),
    )
    const warning = vi.spyOn(toast, "warning")
    const error = vi.spyOn(toast, "error")
    const onOpenChange = vi.fn()
    mount(<ProviderSettingsDialog provider={provider()} open onOpenChange={onOpenChange} />)

    await userEvent.clear(screen.getByLabelText("Priority"))
    await userEvent.type(screen.getByLabelText("Priority"), "20")
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }))

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(warning).toHaveBeenCalledWith(expect.stringMatching(/saved, but the gateway could not load it/))
    expect(error).not.toHaveBeenCalled()
  })
})
