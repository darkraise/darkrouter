import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import {
  AddAccountsDialog,
  filterPresets,
  freeOnlyChange,
  phases,
  planFor,
} from "./add-accounts-dialog"
import { progressLabel } from "./accounts"
import { emptyAccounts } from "./account-fields"
import type { Credential, Preset, Provider } from "../../lib/api-types"

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>)
}

function stub(presets: Preset[], providers: Provider[] = [], probeOk = true) {
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    const json = (body: unknown, status = 200) =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      })
    if (url === "/api/presets") return json({ presets })
    if (url === "/api/providers" && (init as RequestInit)?.method !== "POST") {
      return json({ providers })
    }
    if (url === "/api/providers") return json({ id: "groq" }, 201)
    if (String(url).includes("/test")) {
      return json({ ok: probeOk, probe: "models", latency_ms: 12, error: probeOk ? "" : "401" })
    }
    if (String(url).endsWith("/keys")) return json({ id: "cred-1", label: "x" }, 201)
    return json({})
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

beforeEach(() => vi.unstubAllGlobals())

const preset = (over: Partial<Preset> & { id: string }): Preset => ({
  name: over.id, kind: "openaicompat", base_url: "https://x.example",
  surfaces: ["llm"], auth_kind: "bearer", website: "", free_tier: false,
  ...over,
})

const provider = (
  id: string,
  credentials: Credential[] = [],
  freeModelsOnly = false,
): Provider => ({
  id, name: id, preset: id, kind: "openaicompat", base_url: "https://x.example",
  priority: 10, enabled: true, auth_style: "bearer",
  free_models_only: freeModelsOnly, allow_unsanctioned_free: false, credentials,
})

const cred = (id: string): Credential => ({
  id, label: id, masked: "sk-…", enabled: true, cooling: false, kind: "static",
})

describe("filterPresets", () => {
  it("matches the id and the display name", () => {
    const all = [preset({ id: "groq", name: "Groq" }), preset({ id: "cerebras" })]
    expect(filterPresets(all, { q: "gro" }).map((p) => p.id)).toEqual(["groq"])
    expect(filterPresets(all, { q: "Groq" }).map((p) => p.id)).toEqual(["groq"])
  })

  it("narrows on free tier rather than toggling it", () => {
    // A two-way toggle would hide free-tier providers on its other setting
    // and leave the filter impossible to clear.
    const all = [preset({ id: "a", free_tier: true }), preset({ id: "b" })]
    expect(filterPresets(all, {})).toHaveLength(2)
    expect(filterPresets(all, { freeTier: true }).map((p) => p.id)).toEqual(["a"])
  })

  it("combines every filter", () => {
    const out = filterPresets(
      [
        preset({ id: "groq", surfaces: ["llm"], auth_kind: "bearer", free_tier: true }),
        preset({ id: "grok", surfaces: ["llm"], auth_kind: "bearer" }),
      ],
      { q: "gro", surface: "llm", authKind: "bearer", freeTier: true },
    )
    expect(out.map((p) => p.id)).toEqual(["groq"])
  })

  it("drops an excluded preset, and drops it before the search term", () => {
    // Excluding after the search would leave the preset one keystroke away
    // from being offered again, which is not what excluding it means.
    const all = [preset({ id: "ollama", name: "Ollama" }), preset({ id: "groq", name: "Groq" })]
    const exclude = new Set(["ollama"])
    expect(filterPresets(all, { exclude }).map((p) => p.id)).toEqual(["groq"])
    expect(filterPresets(all, { q: "ollama", exclude })).toEqual([])
  })
})

describe("planFor", () => {
  it("creates the provider row when nothing has ever used the preset", () => {
    // A preset only becomes a row the first time someone gives it a key.
    expect(planFor(preset({ id: "groq" }), [])).toEqual({ needsProvider: true })
  })

  it("adds to the existing provider rather than creating a second one", () => {
    const existing = provider("groq", [cred("k1")])
    expect(planFor(preset({ id: "groq" }), [existing])).toEqual({
      needsProvider: false,
      provider: existing,
    })
  })
})

// The picker rows are listbox options, not toggle buttons: single choice
// among two hundred, which is what "option" means and what aria-pressed did
// not. Selecting one still advances the wizard.
describe("the wizard", () => {
  it("picks a provider, takes the key, and writes on the one button", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-aaaa")

    // Nothing is sent while the form is being filled in.
    expect(
      fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "POST"),
    ).toHaveLength(0)

    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))
    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([, init]) => (init as RequestInit)?.method === "POST",
      )
      // The provider row, the key, then the probe that proves the key works.
      expect(posts.map(([url]) => String(url))).toEqual([
        "/api/providers",
        "/api/providers/groq/keys",
        "/api/providers/groq/test?key=cred-1",
      ])
    })
  })

  it("skips creating the provider when it already has accounts", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })], [provider("groq", [cred("k1")])])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-bbbb")
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([, init]) => (init as RequestInit)?.method === "POST",
      )
      // No second POST /api/providers: it would 409 against the row that is
      // already there.
      expect(posts.map(([url]) => String(url))).toEqual([
        "/api/providers/groq/keys",
        "/api/providers/groq/test?key=cred-1",
      ])
    })
  })

  it("will not submit with nothing to add", async () => {
    stub([preset({ id: "groq", name: "Groq" })])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)
    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    expect(screen.getByRole("button", { name: /add credential/i })).toBeDisabled()
  })

  it("removes a key the provider refuses", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })], [provider("groq", [cred("k1")])], false)
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-bad")
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      // Only what works survives: the key is written, probed, and taken back.
      const deletes = fetchMock.mock.calls.filter(
        ([, init]) => (init as RequestInit)?.method === "DELETE",
      )
      expect(deletes.map(([url]) => String(url))).toEqual([
        "/api/providers/groq/keys/cred-1",
      ])
    })
  })

  it("keeps every key when the check is turned off", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })], [provider("groq", [cred("k1")])], false)
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-unchecked")
    await userEvent.click(screen.getByRole("checkbox", { name: /check every key/i }))
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([, init]) => (init as RequestInit)?.method === "POST",
      )
      expect(posts.map(([url]) => String(url))).toEqual(["/api/providers/groq/keys"])
    })
  })

  it("carries the free-models choice into the provider it creates", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-aaaa")
    await userEvent.click(screen.getByRole("checkbox", { name: /free models only/i }))
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      const create = fetchMock.mock.calls.find(
        ([url, init]) => url === "/api/providers" && (init as RequestInit)?.method === "POST",
      )
      expect(JSON.parse((create?.[1] as RequestInit).body as string)).toEqual({
        id: "groq", preset: "groq", free_models_only: true,
      })
    })
  })

  it("reports which account it is checking while the run is in flight", async () => {
    // A probe is a round trip to the provider per key, so a paste of several
    // is a wait long enough that silence reads as a hang.
    let release: (() => void) | undefined
    const held = new Promise<void>((resolve) => {
      release = resolve
    })
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })])
    const inner = fetchMock.getMockImplementation()!
    fetchMock.mockImplementation(async (url, init) => {
      if (String(url).includes("/test")) await held
      return inner(url, init)
    })

    mount(<AddAccountsDialog open onOpenChange={() => {}} />)
    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    await userEvent.click(screen.getByRole("radio", { name: /bulk import/i }))
    await userEvent.type(screen.getByLabelText(/one per line/i), "work|sk-aaa\nspare|sk-bbb")
    await userEvent.click(screen.getByRole("button", { name: /add 2 credentials/i }))

    expect(await screen.findByText("Checking work · 1 of 2")).toBeInTheDocument()
    expect(screen.getByRole("progressbar")).toBeInTheDocument()
    release?.()
  })

  it("names each pasted account from its own line", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("option", { name: /groq/i }))
    await userEvent.click(screen.getByRole("radio", { name: /bulk import/i }))
    await userEvent.type(screen.getByLabelText(/one per line/i), "work|sk-aaa\nsk-bbb")
    await userEvent.click(screen.getByRole("button", { name: /add 2 credentials/i }))

    await waitFor(() => {
      const keyPosts = fetchMock.mock.calls.filter(
        ([url, init]) =>
          String(url).endsWith("/keys") && (init as RequestInit)?.method === "POST",
      )
      expect(keyPosts).toHaveLength(2)
      expect(JSON.parse((keyPosts[0]?.[1] as RequestInit).body as string)).toEqual({
        label: "work", secret: "sk-aaa",
      })
      expect(JSON.parse((keyPosts[1]?.[1] as RequestInit).body as string)).toEqual({
        label: "key-2", secret: "sk-bbb",
      })
    })
  })
})

describe("the wizard opened from an unconfigured preset", () => {
  it("starts on the accounts step with no provider to pick", async () => {
    // The operator named the provider by navigating to it; asking again is a
    // step that can only be answered one way.
    stub([preset({ id: "groq", name: "Groq" })])
    mount(
      <AddAccountsDialog
        open
        onOpenChange={() => {}}
        preset={preset({ id: "groq", name: "Groq" })}
      />,
    )
    expect(await screen.findByLabelText(/api key/i)).toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/search providers/i)).not.toBeInTheDocument()
  })

  it("creates the provider row with the first account", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })])
    mount(
      <AddAccountsDialog
        open
        onOpenChange={() => {}}
        preset={preset({ id: "groq", name: "Groq" })}
      />,
    )

    await userEvent.type(await screen.findByLabelText(/api key/i), "sk-aaaa")
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([, init]) => (init as RequestInit)?.method === "POST",
      )
      expect(posts.map(([url]) => String(url))).toEqual([
        "/api/providers",
        "/api/providers/groq/keys",
        "/api/providers/groq/test?key=cred-1",
      ])
    })
  })

  it("does not recreate a row that appeared while the page was open", async () => {
    // A second POST would 409 against the row that is already there.
    const fetchMock = stub(
      [preset({ id: "groq", name: "Groq" })],
      [provider("groq", [cred("k1")])],
    )
    mount(
      <AddAccountsDialog
        open
        onOpenChange={() => {}}
        preset={preset({ id: "groq", name: "Groq" })}
      />,
    )

    await userEvent.type(await screen.findByLabelText(/api key/i), "sk-bbbb")
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([, init]) => (init as RequestInit)?.method === "POST",
      )
      expect(posts.map(([url]) => String(url))).toEqual([
        "/api/providers/groq/keys",
        "/api/providers/groq/test?key=cred-1",
      ])
    })
  })
})

describe("freeOnlyChange", () => {
  it("is nothing to write while the provider row is being created", () => {
    // The flag rides along in the POST that creates it.
    expect(
      freeOnlyChange({ ...emptyAccounts, freeModelsOnly: true }, { needsProvider: true }),
    ).toBe(false)
  })

  it("is a write only when it differs from what the provider holds", () => {
    const existing = provider("groq", [cred("k1")])
    const plan = { needsProvider: false, provider: existing }
    expect(freeOnlyChange({ ...emptyAccounts, freeModelsOnly: true }, plan)).toBe(true)
    expect(freeOnlyChange({ ...emptyAccounts, freeModelsOnly: false }, plan)).toBe(false)
  })
})

describe("progressLabel", () => {
  it("names the account and where the run has got to", () => {
    expect(progressLabel({ done: 0, total: 3, label: "work", step: "adding" })).toBe(
      "Adding work · 1 of 3",
    )
    expect(progressLabel({ done: 2, total: 3, label: "spare", step: "checking" })).toBe(
      "Checking spare · 3 of 3",
    )
  })

  it("does not count past the total on the last account", () => {
    // `done` reaches the total when the run finishes, and "4 of 3" is the
    // kind of number that makes an operator distrust the rest of the screen.
    expect(progressLabel({ done: 3, total: 3, label: "last", step: "checking" })).toBe(
      "Checking last · 3 of 3",
    )
  })
})

describe("phases", () => {
  it("drops the picker when the provider is already settled, and never reviews", () => {
    expect(phases(false)).toEqual(["provider", "accounts"])
    expect(phases(true)).toEqual(["accounts"])
  })
})

describe("the wizard opened from a provider", () => {
  it("starts on the accounts step with no provider to pick", async () => {
    stub([preset({ id: "groq", name: "Groq" })], [provider("groq", [cred("k1")])])
    mount(
      <AddAccountsDialog open onOpenChange={() => {}} provider={provider("groq", [cred("k1")])} />,
    )

    // Straight to the key field: an operator who navigated to this provider
    // has already answered the question the picker asks.
    expect(await screen.findByLabelText(/api key/i)).toBeInTheDocument()
    expect(screen.queryByPlaceholderText(/search providers/i)).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /back/i })).not.toBeInTheDocument()
  })

  it("offers the free-models setting, showing what the provider holds", async () => {
    // An unticked box over a free-only provider would misreport it, and saving
    // would then turn the setting off without anyone asking for that.
    stub([preset({ id: "groq", name: "Groq" })], [provider("groq", [cred("k1")], true)])
    mount(
      <AddAccountsDialog
        open
        onOpenChange={() => {}}
        provider={provider("groq", [cred("k1")], true)}
      />,
    )
    await screen.findByLabelText(/api key/i)
    expect(screen.getByRole("checkbox", { name: /free models only/i })).toBeChecked()
  })

  it("writes the free-models setting when it is changed", async () => {
    const fetchMock = stub(
      [preset({ id: "groq", name: "Groq" })],
      [provider("groq", [cred("k1")])],
    )
    mount(
      <AddAccountsDialog open onOpenChange={() => {}} provider={provider("groq", [cred("k1")])} />,
    )

    await userEvent.type(await screen.findByLabelText(/api key/i), "sk-cccc")
    await userEvent.click(screen.getByRole("checkbox", { name: /free models only/i }))
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      const patch = fetchMock.mock.calls.find(
        ([url, init]) =>
          url === "/api/providers/groq" && (init as RequestInit)?.method === "PATCH",
      )
      expect(JSON.parse((patch?.[1] as RequestInit).body as string)).toEqual({
        free_models_only: true,
      })
    })
  })

  it("leaves the setting alone when the box was not touched", async () => {
    // Adding a key is not an occasion to rewrite a setting nobody edited.
    const fetchMock = stub(
      [preset({ id: "groq", name: "Groq" })],
      [provider("groq", [cred("k1")], true)],
    )
    mount(
      <AddAccountsDialog
        open
        onOpenChange={() => {}}
        provider={provider("groq", [cred("k1")], true)}
      />,
    )

    await userEvent.type(await screen.findByLabelText(/api key/i), "sk-cccc")
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "POST"),
      ).not.toHaveLength(0),
    )
    expect(
      fetchMock.mock.calls.some(([, init]) => (init as RequestInit)?.method === "PATCH"),
    ).toBe(false)
  })

  it("adds the key without recreating the provider row", async () => {
    const fetchMock = stub(
      [preset({ id: "groq", name: "Groq" })],
      [provider("groq", [cred("k1")])],
    )
    const onDone = vi.fn()
    mount(
      <AddAccountsDialog
        open
        onOpenChange={() => {}}
        onDone={onDone}
        provider={provider("groq", [cred("k1")])}
      />,
    )

    await userEvent.type(await screen.findByLabelText(/api key/i), "sk-cccc")
    await userEvent.click(screen.getByRole("button", { name: /add credential/i }))

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([, init]) => (init as RequestInit)?.method === "POST",
      )
      expect(posts.map(([url]) => String(url))).toEqual([
        "/api/providers/groq/keys",
        "/api/providers/groq/test?key=cred-1",
      ])
    })
    await waitFor(() => expect(onDone).toHaveBeenCalledWith("groq"))
  })
})

describe("the provider picker and local runtimes", () => {
  const ollama = preset({ id: "ollama", name: "Ollama", base_url: "http://localhost:11434/v1" })
  const added = (): Provider => ({
    ...provider("ollama"),
    base_url: "http://localhost:11434/v1",
  })

  it("stops offering a local runtime once it has a row", async () => {
    // Its row is the address it listens on, and that is the whole of its
    // setup. Left in the picker it is a provider you can select and then find
    // nothing to type, because there is no key a local server wants.
    stub([ollama, preset({ id: "groq", name: "Groq" })], [added()])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    expect(await screen.findByRole("option", { name: /groq/i })).toBeInTheDocument()
    expect(screen.queryByRole("option", { name: /ollama/i })).toBeNull()
  })

  it("cannot be searched back into the picker", async () => {
    stub([ollama, preset({ id: "groq", name: "Groq" })], [added()])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await screen.findByRole("option", { name: /groq/i })
    await userEvent.type(screen.getByPlaceholderText(/search/i), "ollama")
    await waitFor(() => expect(screen.queryByRole("option", { name: /groq/i })).toBeNull())
    expect(screen.queryByRole("option", { name: /ollama/i })).toBeNull()
  })

  it("still offers one nobody has added yet", async () => {
    // The exclusion is about the row existing, not about being local.
    stub([ollama], [])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    expect(await screen.findByRole("option", { name: /ollama/i })).toBeInTheDocument()
  })
})
