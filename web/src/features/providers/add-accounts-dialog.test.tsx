import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { beforeEach, describe, expect, it, vi } from "vitest"
import { AddAccountsDialog, filterPresets, planFor } from "./add-accounts-dialog"
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

const provider = (id: string, credentials: Credential[] = []): Provider => ({
  id, name: id, preset: id, kind: "openaicompat", base_url: "https://x.example",
  priority: 10, enabled: true, auth_style: "bearer", free_models_only: false, credentials,
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

describe("the wizard", () => {
  it("walks provider, accounts, review before writing anything", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-aaaa")
    await userEvent.click(screen.getByRole("button", { name: /review/i }))

    // Still nothing sent: the review step is the last cheap place to notice a
    // wrong key.
    expect(
      fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "POST"),
    ).toHaveLength(0)

    await userEvent.click(screen.getByRole("button", { name: /add account/i }))
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

    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-bbbb")
    await userEvent.click(screen.getByRole("button", { name: /review/i }))
    await userEvent.click(screen.getByRole("button", { name: /add account/i }))

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

  it("will not leave the account step with nothing to add", async () => {
    stub([preset({ id: "groq", name: "Groq" })])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)
    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    expect(screen.getByRole("button", { name: /review/i })).toBeDisabled()
  })

  it("removes a key the provider refuses", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })], [provider("groq", [cred("k1")])], false)
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-bad")
    await userEvent.click(screen.getByRole("button", { name: /review/i }))
    await userEvent.click(screen.getByRole("button", { name: /add account/i }))

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

    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-unchecked")
    await userEvent.click(screen.getByRole("checkbox", { name: /check every key/i }))
    await userEvent.click(screen.getByRole("button", { name: /review/i }))
    await userEvent.click(screen.getByRole("button", { name: /add account/i }))

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

    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.type(screen.getByLabelText(/api key/i), "sk-aaaa")
    await userEvent.click(screen.getByRole("checkbox", { name: /free models only/i }))
    await userEvent.click(screen.getByRole("button", { name: /review/i }))
    await userEvent.click(screen.getByRole("button", { name: /add account/i }))

    await waitFor(() => {
      const create = fetchMock.mock.calls.find(
        ([url, init]) => url === "/api/providers" && (init as RequestInit)?.method === "POST",
      )
      expect(JSON.parse((create?.[1] as RequestInit).body as string)).toEqual({
        id: "groq", preset: "groq", free_models_only: true,
      })
    })
  })

  it("names each pasted account from its own line", async () => {
    const fetchMock = stub([preset({ id: "groq", name: "Groq" })])
    mount(<AddAccountsDialog open onOpenChange={() => {}} />)

    await userEvent.click(await screen.findByRole("button", { name: /groq/i }))
    await userEvent.click(screen.getByRole("radio", { name: /bulk import/i }))
    await userEvent.type(screen.getByLabelText(/one per line/i), "work|sk-aaa\nsk-bbb")
    await userEvent.click(screen.getByRole("button", { name: /review/i }))
    await userEvent.click(screen.getByRole("button", { name: /add 2 accounts/i }))

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
