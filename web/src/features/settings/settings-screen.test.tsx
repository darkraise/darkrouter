import { act, render, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "darkraise-ui"
import { RouterAdapterProvider } from "darkraise-ui/router"
import type { RouterAdapter } from "darkraise-ui/router"
import { beforeEach, describe, expect, it, vi } from "vitest"
import {
  fieldErrors,
  orderSessions,
  passwordProblem,
  pendingRestartMessage,
  reloadMessage,
  revokedText,
  SettingsScreen,
  settingsPatch,
  syncMessage,
} from "./settings-screen"
import type { ConfigFieldMeta, ConfigResponse } from "../../lib/api-types"

// PageHeader calls useRouterAdapter unconditionally even without breadcrumbs
// or tabs, so anything rendering it needs a provider — Settings never uses
// Link, so a stub satisfying the interface is enough.
const stubRouterAdapter: RouterAdapter = {
  Link: ({ children }) => <>{children}</>,
  useNavigate: () => () => {},
  usePathname: () => "/settings",
  useBack: () => () => {},
  useInvalidate: () => () => {},
}

function mount(ui: React.ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={client}>
      <RouterAdapterProvider value={stubRouterAdapter}>{ui}</RouterAdapterProvider>
      <Toaster />
    </QueryClientProvider>,
  )
}

beforeEach(() => vi.unstubAllGlobals())

/** Fills in the ConfigResponse boilerplate every test here ignores. */
const cfgWith = (
  values: Record<string, string>,
  fields: Record<string, ConfigFieldMeta>,
): ConfigResponse => ({
  valid: true,
  warnings: [],
  values,
  fields,
  pending_restart: [],
})

const cfg = (): ConfigResponse =>
  cfgWith(
    {
      "log.retention": "72h",
      "catalog.discovery.interval": "6h",
      "catalog.sync_timeout": "30s",
      "policy.retry.max_attempts": "3",
      "capture.bodies": "true",
      "server.proxy_listen": ":8080",
    },
    {
      "log.retention": { source: "database", hot_reloadable: true, kind: "duration" },
      "capture.bodies": { source: "database", hot_reloadable: true, kind: "bool" },
      "catalog.discovery.interval": { source: "default", hot_reloadable: false, kind: "duration" },
      "catalog.sync_timeout": { source: "database", hot_reloadable: false, kind: "duration" },
      "policy.retry.max_attempts": { source: "database", hot_reloadable: true, kind: "int" },
      // The API reports a listen address as hot-reloadable because nothing
      // captures it at construction; it still cannot be changed from here.
      "server.proxy_listen": {
        source: "env",
        hot_reloadable: true,
        kind: "string",
        env: "DARKROUTER_PROXY_LISTEN",
      },
    },
  )

describe("settingsPatch", () => {
  const cfg = cfgWith(
    { "log.retention": "720h0m0s", "capture.bodies": "false", "policy.timeout.total": "10m0s" },
    {
      "log.retention": { source: "default", hot_reloadable: true, kind: "duration" },
      "capture.bodies": { source: "default", hot_reloadable: true, kind: "bool" },
      "policy.timeout.total": { source: "database", hot_reloadable: true, kind: "duration" },
    },
  )

  it("sends nothing when nothing changed", () => {
    expect(settingsPatch({ "log.retention": "720h0m0s" }, new Set(), cfg)).toEqual({})
  })

  it("sends only the keys the draft changed", () => {
    // The screen used to write three policy keys on every save whatever the
    // operator touched, which reported them all as stored until the next
    // restart reconciled them away.
    const draft = { "log.retention": "96h", "capture.bodies": "false", "policy.timeout.total": "10m0s" }
    expect(settingsPatch(draft, new Set(), cfg)).toEqual({ set: { "log.retention": "96h" } })
  })

  it("sends a padded value trimmed rather than as padding nobody typed", () => {
    expect(settingsPatch({ "log.retention": "  96h  " }, new Set(), cfg)).toEqual({
      set: { "log.retention": "96h" },
    })
  })

  it("reads a padded value equal to the stored one as no change at all", () => {
    // The empty case already trims; comparing the untrimmed text against it
    // made " 10m0s " a change and sent the padding to the server.
    expect(settingsPatch({ "policy.timeout.total": " 10m0s " }, new Set(), cfg)).toEqual({})
  })

  it("sends an emptied box as a reset for that key alone", () => {
    const draft = { "policy.timeout.total": "" }
    expect(settingsPatch(draft, new Set(), cfg)).toEqual({ reset: ["policy.timeout.total"] })
  })

  it("resets only the emptied box, leaving the other stored keys alone", () => {
    // Emptying one duration box used to send "" for three policy keys, so two
    // the operator never touched were reset with it.
    const stored = cfgWith(
      { "log.retention": "720h0m0s", "policy.timeout.total": "10m0s" },
      {
        "log.retention": { source: "database", hot_reloadable: true, kind: "duration" },
        "policy.timeout.total": { source: "database", hot_reloadable: true, kind: "duration" },
      },
    )
    const draft = { "log.retention": "720h0m0s", "policy.timeout.total": "" }
    expect(settingsPatch(draft, new Set(), stored)).toEqual({ reset: ["policy.timeout.total"] })
  })

  it("sends nothing for an emptied box that has no stored row", () => {
    // log.retention is on its default: there is no row to delete, and naming
    // it would come back as a restart-pending write of nothing.
    expect(settingsPatch({ "log.retention": "" }, new Set(), cfg)).toEqual({})
  })

  it("sends an explicit reset even when the box still holds the value", () => {
    expect(settingsPatch({}, new Set(["policy.timeout.total"]), cfg)).toEqual({
      reset: ["policy.timeout.total"],
    })
  })

  it("never resets a key that has no stored row", () => {
    // log.retention is on its default; resetting it would be a no-op the
    // answer would still report as a restart-pending write.
    expect(settingsPatch({}, new Set(["log.retention"]), cfg)).toEqual({})
  })
})

describe("fieldErrors", () => {
  it("attaches a refusal to the key it names", () => {
    expect(fieldErrors("log.retention must be at least 48h, got 1h", ["log.retention", "capture.bodies"]))
      .toEqual({ "log.retention": "log.retention must be at least 48h, got 1h" })
  })

  it("attaches a cross-key refusal to every key it names", () => {
    const msg =
      "[policy.timeout.total policy.timeout.connect policy.timeout.first_byte] broke the timeout budget rule (policy.timeout.total (5s) must be at least connect + first_byte (1m10s))"
    expect(fieldErrors(msg, ["policy.timeout.total", "policy.timeout.connect", "log.retention"]))
      .toEqual({ "policy.timeout.total": msg, "policy.timeout.connect": msg })
  })

  it("attaches nothing when the message names no key", () => {
    expect(fieldErrors("something went wrong", ["log.retention"])).toEqual({})
  })
})

describe("the password form", () => {
  it("refuses a short password before spending a round trip", () => {
    // The server's floor is twelve. Checking it here is a courtesy; the
    // server stays the authority.
    expect(passwordProblem("short", "short")).toMatch(/12 characters/)
  })

  it("refuses a mismatched confirmation", () => {
    expect(passwordProblem("long-enough-passphrase", "long-enough-passphras")).toMatch(
      /do not match/i,
    )
  })

  it("accepts a long matching pair", () => {
    expect(passwordProblem("long-enough-passphrase", "long-enough-passphrase")).toBeNull()
  })
})

describe("the revocation notice", () => {
  it("says how many other sessions were ended", () => {
    // The operator has just logged every other browser out. Not saying so
    // makes the next login failure elsewhere look like a fault.
    expect(revokedText(3)).toMatch(/3 other sessions/)
  })

  it("says none rather than zero", () => {
    expect(revokedText(0)).toMatch(/no other sessions/i)
  })

  it("says one session in the singular", () => {
    expect(revokedText(1)).toMatch(/1 other session\b/)
  })
})

describe("the reload result", () => {
  it("reports an invalid reload without claiming the gateway stopped", () => {
    expect(
      reloadMessage({ valid: false, error: "yaml: bad", serving: "the previous configuration is still serving" }),
    ).toMatch(/previous configuration is still serving/)
  })

  it("carries the parse error so the operator knows what to fix", () => {
    expect(reloadMessage({ valid: false, error: "yaml: line 4" })).toContain("yaml: line 4")
  })

  it("confirms a clean reload", () => {
    expect(reloadMessage({ valid: true })).toMatch(/reloaded/i)
  })
})

describe("the sync result", () => {
  it("says started rather than synced, since the gateway answers 202", () => {
    expect(syncMessage({ triggered: true })).toMatch(/started/i)
  })
})

describe("pendingRestartMessage", () => {
  it("names one key", () => {
    expect(pendingRestartMessage(["catalog.sync_interval"])).toContain("catalog.sync_interval")
  })
  it("names several", () => {
    const msg = pendingRestartMessage(["catalog.sync_interval", "media.inline"])
    expect(msg).toContain("catalog.sync_interval")
    expect(msg).toContain("media.inline")
  })
})

/** A promise the test decides when to settle, so a request can be held open
 *  while the screen is inspected mid-flight. */
function gate() {
  let open: () => void = () => {}
  const held = new Promise<void>((resolve) => {
    open = resolve
  })
  return { held, open: () => open() }
}

function stubSettingsFetch(overrides: {
  reload?: { valid: boolean; error?: string; serving?: string }
  sync?: { triggered: boolean }
  sessions?: unknown[]
  save?: { status?: number; body?: unknown }
  /** What GET /api/config answers before any save. */
  config?: () => ConfigResponse
  /** What GET /api/config answers once a save has landed. */
  configAfterSave?: () => ConfigResponse
  /** Holds every GET after the first, so the refetch a save triggers can be
   *  inspected while it is still in flight. */
  holdRefetch?: { held: Promise<void> }
  /** Holds the PUT, so the screen can be inspected while the save is pending. */
  holdSave?: { held: Promise<void> }
}) {
  let configFetches = 0
  let saved = false
  const saves: unknown[] = []
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    const method = (init as RequestInit | undefined)?.method ?? "GET"
    if (url === "/api/config" && method === "GET") {
      configFetches += 1
      if (configFetches > 1 && overrides.holdRefetch) await overrides.holdRefetch.held
      const body =
        saved && overrides.configAfterSave
          ? overrides.configAfterSave()
          : (overrides.config?.() ?? cfg())
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    if (url === "/api/config" && method === "PUT") {
      saves.push(JSON.parse(String((init as RequestInit).body)))
      saved = true
      if (overrides.holdSave) await overrides.holdSave.held
      return new Response(JSON.stringify(overrides.save?.body ?? { valid: true, restart_required: [] }), {
        status: overrides.save?.status ?? 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    if (url === "/api/sessions" && method === "GET") {
      return new Response(JSON.stringify({ sessions: overrides.sessions ?? [] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    if (url === "/api/config/reload" && method === "POST") {
      return new Response(JSON.stringify(overrides.reload ?? { valid: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    if (url === "/api/catalog/sync" && method === "POST") {
      return new Response(JSON.stringify(overrides.sync ?? { triggered: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    if (url === "/api/playground/conversations" && method === "DELETE") {
      return new Response(JSON.stringify({ deleted: 2 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    return new Response("{}", { status: 200, headers: { "Content-Type": "application/json" } })
  })
  vi.stubGlobal("fetch", fetchMock)
  return { fetchMock, configFetches: () => configFetches, saves }
}

describe("a failed reload", () => {
  it("shows one banner instead of stacking a second from a needless refetch", async () => {
    // GET /api/config reports valid: false too — reload just set that error,
    // so a refetch would faithfully repeat it, not correct it.
    const { configFetches } = stubSettingsFetch({
      reload: { valid: false, error: "yaml: bad", serving: "the previous configuration is still serving" },
    })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    await user.click(await screen.findByRole("button", { name: /reload config/i }))
    // Replacing what the gateway serves asks first.
    await user.click(await screen.findByRole("button", { name: /^reload$/i }))

    expect(await screen.findByText(/the reloaded configuration is invalid/i)).toBeInTheDocument()
    expect(screen.queryByText(/^the configuration is invalid$/i)).not.toBeInTheDocument()
    // Only the initial load fetched it; the failed reload did not trigger a
    // second GET for the same answer.
    await waitFor(() => expect(configFetches()).toBe(1))
  })
})

describe("the pending-restart notice", () => {
  it("names what is stored but not yet running", async () => {
    stubSettingsFetch({ config: () => ({ ...cfg(), pending_restart: ["catalog.sync_interval"] }) })
    mount(<SettingsScreen />)

    expect(await screen.findByText(/waiting for a restart/i)).toBeInTheDocument()
    expect(screen.getByText(/catalog\.sync_interval/)).toBeInTheDocument()
  })

  it("says nothing when nothing is pending", async () => {
    stubSettingsFetch({ config: () => ({ ...cfg(), pending_restart: [] }) })
    mount(<SettingsScreen />)

    // Waits on a stable element from the same load before asserting the
    // negative, so the query does not just run before the fetch resolves.
    await screen.findByText("log.retention")
    expect(screen.queryByText(/waiting for a restart/i)).not.toBeInTheDocument()
  })
})

describe("a sync request", () => {
  it("refreshes the models list once the gateway has accepted the run", async () => {
    const { fetchMock } = stubSettingsFetch({ sync: { triggered: true } })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    await user.click(await screen.findByRole("button", { name: /sync catalog now/i }))
    await user.click(await screen.findByRole("button", { name: /^sync$/i }))

    await waitFor(() =>
      expect(fetchMock.mock.calls.some(([u, i]) => u === "/api/catalog/sync" && (i as RequestInit)?.method === "POST")).toBe(
        true,
      ),
    )
  })
})

describe("the settings form", () => {
  it("shows a setting with its value, key, source and restart badge", async () => {
    stubSettingsFetch({})
    mount(<SettingsScreen />)

    // The dotted key, which is what the settings table and every error
    // message use.
    expect(await screen.findByText("log.retention")).toBeInTheDocument()
    // §8.1: where the value came from, said at the point of display.
    expect(screen.getAllByText("database").length).toBeGreaterThan(0)
    expect(screen.getAllByText("default").length).toBeGreaterThan(0)
    // Whether changing it takes a restart, stated rather than discovered.
    expect(screen.getAllByText("restart").length).toBeGreaterThan(0)
  })

  it("shows an environment value as a reading rather than an editor", async () => {
    // hot_reloadable is true for a listen address, and saying "hot" would
    // promise a live edit an environment variable cannot take. The
    // environment chip is the whole story for those fields.
    stubSettingsFetch({})
    mount(<SettingsScreen />)

    const envRow = (await screen.findByText("server.proxy_listen")).closest(".border-t")
    expect(envRow).not.toBeNull()
    expect(within(envRow as HTMLElement).getByText("environment")).toBeInTheDocument()
    expect(within(envRow as HTMLElement).queryByText("hot")).not.toBeInTheDocument()
    expect(within(envRow as HTMLElement).queryByText("restart")).not.toBeInTheDocument()
    expect(within(envRow as HTMLElement).queryByRole("textbox")).not.toBeInTheDocument()
    expect(within(envRow as HTMLElement).queryByRole("button", { name: /reset/i })).not.toBeInTheDocument()
  })

  it("saves only the key the operator edited", async () => {
    // The old form wrote three policy keys on every save whatever was
    // touched, so those three reported as stored until the next restart.
    const { saves } = stubSettingsFetch({})
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Keep request records for")
    await user.clear(box)
    await user.type(box, "96h")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    await waitFor(() => expect(saves).toEqual([{ set: { "log.retention": "96h" } }]))
  })

  it("sends a reset for the row whose Reset was pressed, and nothing else", async () => {
    const { saves } = stubSettingsFetch({})
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const row = (await screen.findByText("catalog.sync_timeout")).closest(".border-t")
    await user.click(within(row as HTMLElement).getByRole("button", { name: /reset/i }))
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    await waitFor(() => expect(saves).toEqual([{ reset: ["catalog.sync_timeout"] }]))
  })

  it("clears the Save bar after a save the answer reads back unchanged", async () => {
    // /api/config reports the typed config, so a saved "10m" reads back as
    // "10m0s" and the refetch is byte-identical. Query shares that response
    // structurally, so the reference never changes and a draft waiting on a
    // changed reference would sit dirty over a value that is stored.
    stubSettingsFetch({})
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Keep request records for")
    await user.clear(box)
    await user.type(box, "72h0m0s")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    await waitFor(() =>
      expect(screen.queryByRole("button", { name: /^save$/i })).not.toBeInTheDocument(),
    )
  })

  it("names a restart-only key the save accepted but cannot apply", async () => {
    // Accepted rather than refused: the value belongs in the database either
    // way. Silence about it reads as applied.
    stubSettingsFetch({
      save: { body: { valid: true, restart_required: ["catalog.sync_timeout"] } },
    })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Metadata fetch timeout")
    await user.clear(box)
    await user.type(box, "45s")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    const statuses = await screen.findAllByRole("status")
    expect(
      statuses.some((s) => /catalog\.sync_timeout.*after a restart/.test(s.textContent ?? "")),
    ).toBe(true)
  })

  it("puts a refused save on the row the server named", async () => {
    stubSettingsFetch({
      save: { status: 400, body: { error: "log.retention must be at least 48h, got 1h" } },
    })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Keep request records for")
    await user.clear(box)
    await user.type(box, "1h")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    const row = (await screen.findByText("log.retention")).closest(".border-t")
    expect(
      await within(row as HTMLElement).findByText(/must be at least 48h/),
    ).toBeInTheDocument()
    // Once, not twice: the row already says it, and a toast saying the same
    // 180 characters in the corner reads as a second, separate refusal.
    expect(
      screen.queryAllByRole("status").some((s) => /must be at least 48h/.test(s.textContent ?? "")),
    ).toBe(false)
  })

  it("toasts a refusal that names no field", async () => {
    // fieldErrors only attaches a message to keys the message names, so a
    // refusal about none of them -- a database failure, an alias problem --
    // has nowhere to land on the form and would otherwise be silent.
    stubSettingsFetch({
      save: { status: 500, body: { error: "writing the settings failed: database is locked" } },
    })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Keep request records for")
    await user.clear(box)
    await user.type(box, "96h")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    await waitFor(() =>
      expect(
        screen.queryAllByRole("status").some((s) => /database is locked/.test(s.textContent ?? "")),
      ).toBe(true),
    )
  })

  it("shows one banner for a committed write whose republish failed, not two", async () => {
    // The rows are durable, so the refetch reports valid:false too. A banner
    // from the save beside the banner from the refetched config is the same
    // fact told twice, in the same colour, about the same failure.
    const bad = "policy.timeout.total (5s) must be at least connect + first_byte"
    stubSettingsFetch({
      save: {
        body: { valid: false, error: bad, serving: "the previous configuration is still serving" },
      },
      configAfterSave: () => ({
        ...cfg(),
        valid: false,
        error: bad,
        serving: "the previous configuration is still serving",
      }),
    })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Keep request records for")
    await user.clear(box)
    await user.type(box, "96h")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    // GET /api/config carries both `error` and `serving` whenever the config
    // failed to validate, so the query-derived banner says the whole thing.
    expect(await screen.findByText(/the configuration is invalid/i)).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByText(bad)).toHaveLength(1))
    expect(screen.getByText(/previous configuration is still serving/)).toBeInTheDocument()
    expect(screen.queryByText(/the saved configuration is invalid/i)).not.toBeInTheDocument()
  })

  it("says a row is going to be reset instead of showing it as switched off", async () => {
    // Emptying the draft made a stored `true` render as an unchecked switch,
    // which is what "set this to false" looks like.
    stubSettingsFetch({})
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const row = (await screen.findByText("capture.bodies")).closest(".border-t") as HTMLElement
    await user.click(within(row).getByRole("button", { name: /^reset$/i }))

    const toggle = within(row).getByRole("switch", { name: "Record request bodies" })
    expect(toggle).toBeChecked()
    expect(toggle).toBeDisabled()
    expect(within(row).getByText(/resets to default on save/i)).toBeInTheDocument()
  })

  it("offers Keep to take a row back out of the reset", async () => {
    stubSettingsFetch({})
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const row = (await screen.findByText("catalog.sync_timeout")).closest(".border-t") as HTMLElement
    await user.click(within(row).getByRole("button", { name: /^reset$/i }))
    expect(within(row).getByLabelText("Metadata fetch timeout")).toBeDisabled()

    await user.click(within(row).getByRole("button", { name: /^keep$/i }))
    expect(within(row).getByLabelText("Metadata fetch timeout")).toBeEnabled()
    expect(within(row).queryByText(/resets to default on save/i)).not.toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /^save$/i })).not.toBeInTheDocument()
  })

  it("says an emptied box will reset the row it belongs to", async () => {
    // An emptied box is a reset -- that is the store's rule, since it cannot
    // hold "" as a value distinct from absent -- so the row has to say so.
    stubSettingsFetch({})
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const row = (await screen.findByText("log.retention")).closest(".border-t") as HTMLElement
    await user.clear(within(row).getByLabelText("Keep request records for"))
    expect(within(row).getByText(/resets to default on save/i)).toBeInTheDocument()
  })

  it("gates the editors until the reseed that follows the save has run", async () => {
    // A keystroke landing during the save is wiped by the reseed that follows
    // it, so the box would swallow an edit the operator watched themselves
    // make. The window runs to the end of the reseed, not to the end of the
    // PUT: the refetch the reseed reads is still in flight after the PUT has
    // answered, and an editor re-enabled there is an editor whose next
    // keystroke is discarded.
    const put = gate()
    const refetch = gate()
    stubSettingsFetch({ holdSave: put, holdRefetch: refetch })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Keep request records for")
    await user.clear(box)
    await user.type(box, "96h")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    await waitFor(() => expect(screen.getByLabelText("Keep request records for")).toBeDisabled())

    put.open()
    // The toast fires inside the success handler, so it marks the moment the
    // PUT has answered while the reseed is still awaiting the refetch.
    const statuses = await screen.findAllByRole("status")
    expect(statuses.some((s) => s.textContent?.includes("Settings saved"))).toBe(true)
    // Long enough for the mutation to have dispatched success had it been
    // going to: the refetch is still gated, so nothing else can move here.
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20))
    })
    expect(screen.getByLabelText("Keep request records for")).toBeDisabled()

    refetch.open()
    await waitFor(() => expect(screen.getByLabelText("Keep request records for")).toBeEnabled())
  })

  it("holds the typed value until the refetch that replaces it has landed", async () => {
    // Reseeding from the render-time config puts the pre-save value back, so
    // the box visibly flips to the old value and only returns to the new one
    // when the refetch lands -- or stays wrong, if the refetch fails.
    const held = gate()
    stubSettingsFetch({
      holdRefetch: held,
      configAfterSave: () => ({ ...cfg(), values: { ...cfg().values, "log.retention": "96h0m0s" } }),
    })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    const box = await screen.findByLabelText("Keep request records for")
    await user.clear(box)
    await user.type(box, "96h")
    await user.click(await screen.findByRole("button", { name: /^save$/i }))

    const statuses = await screen.findAllByRole("status")
    expect(statuses.some((s) => s.textContent?.includes("Settings saved"))).toBe(true)
    // The refetch is still in flight: what the operator typed is still what
    // the box shows, rather than the value the save replaced.
    expect(screen.getByLabelText("Keep request records for")).toHaveValue("96h")

    held.open()
    await waitFor(() =>
      expect(screen.getByLabelText("Keep request records for")).toHaveValue("96h0m0s"),
    )
  })
})

describe("the sessions list", () => {
  const session = (id: string, current: boolean) => ({
    id,
    prefix: id.slice(0, 4),
    created_at: "2026-08-01T10:00:00Z",
    expires_at: "2026-09-01T10:00:00Z",
    current,
  })

  it("puts the caller's own session first", () => {
    expect(orderSessions([session("bbbb1", false), session("aaaa1", true)]).map((s) => s.id)).toEqual([
      "aaaa1",
      "bbbb1",
    ])
  })

  it("marks the current browser on the page, at the top of the list", async () => {
    stubSettingsFetch({ sessions: [session("bbbb1", false), session("aaaa1", true)] })
    mount(<SettingsScreen />)
    const items = await screen.findAllByRole("listitem")
    const rows = items.filter((li) => /since/.test(li.textContent ?? ""))
    expect(rows[0]).toHaveTextContent("this browser")
    expect(rows[1]).toHaveTextContent("Revoke")
  })
})

describe("the password on the page", () => {
  it("opens the change dialog from Settings, not only from the account menu", async () => {
    stubSettingsFetch({})
    mount(<SettingsScreen />)
    await userEvent.click(await screen.findByRole("button", { name: /change password/i }))
    expect(await screen.findByRole("dialog", { name: /change password/i })).toBeInTheDocument()
  })
})

describe("a clean reload", () => {
  it("announces the result in a status region", async () => {
    stubSettingsFetch({ reload: { valid: true } })
    const user = userEvent.setup()
    mount(<SettingsScreen />)
    await user.click(await screen.findByRole("button", { name: /reload config/i }))
    await user.click(await screen.findByRole("button", { name: /^reload$/i }))
    const statuses = await screen.findAllByRole("status")
    expect(statuses.some((s) => s.textContent?.includes("Configuration reloaded."))).toBe(true)
  })
})

describe("the saved-conversation purge", () => {
  it("asks before it destroys, and says what it destroys", async () => {
    // A separate action from the key on purpose: config is file-backed and
    // reloadable, and a setting whose reload deleted data would mean an edit
    // to a file on disk silently destroying the operator's history.
    const { fetchMock } = stubSettingsFetch({})
    mount(<SettingsScreen />)

    await userEvent.click(
      await screen.findByRole("button", { name: /delete saved conversations/i }),
    )
    expect(screen.getByText(/cannot be undone/i)).toBeInTheDocument()

    // Nothing has been destroyed by opening the dialog.
    expect(
      fetchMock.mock.calls.some(([u, i]) =>
        u === "/api/playground/conversations" && (i as RequestInit)?.method === "DELETE"),
    ).toBe(false)

    await userEvent.click(screen.getByRole("button", { name: "Delete" }))
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(([u, i]) =>
          u === "/api/playground/conversations" && (i as RequestInit)?.method === "DELETE"),
      ).toBe(true),
    )
  })
})
