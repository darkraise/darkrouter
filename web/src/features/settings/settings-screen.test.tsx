import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { Toaster } from "darkraise-ui"
import { RouterAdapterProvider } from "darkraise-ui/router"
import type { RouterAdapter } from "darkraise-ui/router"
import { beforeEach, describe, expect, it, vi } from "vitest"
import {
  orderSessions,
  readOnlyGroups,
  passwordProblem,
  reloadMessage,
  revokedText,
  SettingsScreen,
  syncMessage,
  toDraft,
  toWrite,
} from "./settings-screen"
import type { ConfigResponse } from "../../lib/api-types"
import { EDITABLE } from "./settings-catalog"

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

const cfg = (): ConfigResponse => ({
  valid: true,
  warnings: [],
  fields: {
    "log.retention": { source: "file", hot_reloadable: true },
    "catalog.discovery.interval": { source: "default", hot_reloadable: false },
    aliases: { source: "database", hot_reloadable: true },
  },
  blocks: {
    server: {
      proxy_listen: ":8080",
      admin_listen: ":8081",
      max_body_bytes: 1,
      shutdown_grace: "10s",
      sse: { max_line_bytes: 1, max_precommit_bytes: 1 },
    },
    log: { retention: "72h" },
    capture: { bodies: false, max_bytes: 0, retention: "24h" },
    catalog: {
      models_dev_url: "https://models.dev",
      sync_interval: "12h",
      sync_timeout: "30s",
      discovery: { enabled: true, interval: "6h" },
    },
    playground: { save_conversations: true },
    media: { inline: true },
    aliases: { fast: ["groq/a"] },
    policy: {
      cooldown: { max: "30m" },
      retry: { max_attempts: 3 },
      timeout: { connect: "10s", first_byte: "60s", total: "10m", idle: "30s" },
    },
  },
})

describe("the read-only configuration", () => {
  it("leaves out the settings shown as editable fields above it", () => {
    // Listing a value as a live input and again as a read-only row is what
    // made the previous version of this screen show the same five settings
    // twice under two different names.
    const fields = readOnlyGroups(cfg()).flatMap((g) => g.rows.map((r) => r.field))
    for (const editable of EDITABLE) expect(fields).not.toContain(editable.field)
  })

  it("keeps the policy settings that exist but cannot be edited", () => {
    // connect and first_byte configure the one shared transport built at
    // startup, so PUT /api/policy refuses them — which is exactly why they
    // belong in the read-only view rather than nowhere.
    const fields = readOnlyGroups(cfg()).flatMap((g) => g.rows.map((r) => r.field))
    expect(fields).toContain("policy.timeout.connect")
    expect(fields).toContain("policy.timeout.first_byte")
  })

  it("drops a group left with nothing in it", () => {
    // A heading over an empty card reads as a section that failed to load.
    for (const section of readOnlyGroups(cfg())) {
      expect(section.rows.length).toBeGreaterThan(0)
    }
  })

  it("carries each field's source and reloadability", () => {
    // §8.1: after the first run, editing these in the file has no effect, and
    // the view has to say so at the point of display.
    const rows = readOnlyGroups(cfg()).flatMap((g) => g.rows)
    expect(rows.find((r) => r.field === "log.retention")?.source).toBe("file")
    expect(rows.find((r) => r.field === "catalog.discovery.interval")?.source).toBe("default")
    expect(rows.find((r) => r.field === "catalog.discovery.interval")?.hotReloadable).toBe(false)
    expect(rows.find((r) => r.field === "log.retention")?.hotReloadable).toBe(true)
  })

  it("renders the value a person reads and keeps the file's own spelling", () => {
    const rows = readOnlyGroups(cfg()).flatMap((g) => g.rows)
    const retention = rows.find((r) => r.field === "log.retention")
    expect(retention?.display).toBe("3 days")
    expect(retention?.literal).toBe("72h")
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
  it("reports an invalid file without claiming the gateway stopped", () => {
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

function stubSettingsFetch(overrides: {
  reload?: { valid: boolean; error?: string; serving?: string }
  sync?: { triggered: boolean }
  sessions?: unknown[]
}) {
  let configFetches = 0
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    const method = (init as RequestInit | undefined)?.method ?? "GET"
    if (url === "/api/config" && method === "GET") {
      configFetches += 1
      return new Response(JSON.stringify(cfg()), {
        status: 200,
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
  return { fetchMock, configFetches: () => configFetches }
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
    expect(screen.queryByText(/^the configuration file is invalid\.$/i)).not.toBeInTheDocument()
    // Only the initial load fetched it; the failed reload did not trigger a
    // second GET for the same answer.
    await waitFor(() => expect(configFetches()).toBe(1))
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

describe("the policy draft", () => {
  const policy = {
    cooldown: { trip_after: 3, max: "15m" },
    retry: { max_attempts: 4 },
    timeout: { connect: "10s", first_byte: "1m", total: "10m", idle: "2m" },
  }

  it("fills the form from what the gateway is running", () => {
    expect(toDraft(policy as never)["policy.retry.max_attempts"]).toBe("4")
    expect(toDraft(policy as never)["policy.cooldown.max"]).toBe("15m")
  })

  it("survives a response missing a block rather than blanking the screen", () => {
    // Reading through an absent block unguarded took the whole screen down
    // with it, including the banners that would have said what was wrong.
    expect(() => toDraft({} as never)).not.toThrow()
    expect(toDraft({} as never)["policy.cooldown.max"]).toBe("")
  })

  it("omits the two restart-only timeouts from the write", () => {
    const write = toWrite(toDraft(policy as never)) as Record<string, unknown>
    expect(JSON.stringify(write)).not.toContain("connect")
    expect(JSON.stringify(write)).not.toContain("first_byte")
  })

  it("omits an empty trip_after rather than sending zero", () => {
    // Zero is a real value meaning "cool on the first failure", and sending
    // it for an empty box would change behaviour nobody asked to change.
    const write = toWrite({ ...toDraft(policy as never), "policy.cooldown.trip_after": "" })
    expect("trip_after" in write.cooldown).toBe(false)
  })
})

describe("the policy write", () => {
  const draft = {
    "policy.cooldown.max": "5m",
    "policy.cooldown.trip_after": "3",
    "policy.retry.max_attempts": "4",
    "policy.timeout.total": "10m",
    "policy.timeout.idle": "30s",
  }

  it("sends the attempts a number was typed into", () => {
    expect(toWrite(draft).retry).toEqual({ max_attempts: 4 })
  })

  it("leaves attempts out when the box was emptied", () => {
    // Number("") is 0, and the store reads 0 as "no override" and deletes the
    // setting — reverting to the file default under a success toast.
    expect(toWrite({ ...draft, "policy.retry.max_attempts": "" }).retry).toBeUndefined()
  })

  it("leaves trip_after out when it will not parse as a whole number", () => {
    // The same rule as attempts: a count that is not a whole number is not
    // a setting, and sending NaN or 2.5 would either be ignored silently or
    // refused after the operator was told it saved.
    expect("trip_after" in toWrite({ ...draft, "policy.cooldown.trip_after": "abc" }).cooldown).toBe(false)
    expect("trip_after" in toWrite({ ...draft, "policy.cooldown.trip_after": "2.5" }).cooldown).toBe(false)
    expect(toWrite(draft).cooldown.trip_after).toBe(3)
  })

  it("leaves attempts out when it will not parse as a whole number", () => {
    // NaN serialises to null and the Go pointer stays nil, so the field is
    // ignored with no complaint. A fraction cannot reach a Go *int either.
    expect(toWrite({ ...draft, "policy.retry.max_attempts": "abc" }).retry).toBeUndefined()
    expect(toWrite({ ...draft, "policy.retry.max_attempts": "2.5" }).retry).toBeUndefined()
  })
})

describe("the read-only section on the page", () => {
  it("shows a setting with its value, key, source and restart badge", async () => {
    stubSettingsFetch({})
    mount(<SettingsScreen />)

    // The humanised value once, with the file's own spelling on its title
    // rather than printed as a second value.
    const value = await screen.findByText("3 days")
    expect(value).toHaveAttribute("title", "72h")
    expect(screen.queryByText("72h")).not.toBeInTheDocument()
    // The dotted key, which is what the YAML file and every error message use.
    expect(screen.getByText("log.retention")).toBeInTheDocument()
    // §8.1: where the value came from, said at the point of display.
    expect(screen.getAllByText("file").length).toBeGreaterThan(0)
    expect(screen.getAllByText("default").length).toBeGreaterThan(0)
    // Whether changing it takes a restart, stated rather than discovered.
    expect(screen.getAllByText("restart").length).toBeGreaterThan(0)
  })

  it("does not repeat a setting that is editable above it", async () => {
    stubSettingsFetch({})
    mount(<SettingsScreen />)
    await screen.findByText("3 days")

    // policy.retry.max_attempts is an input up the page; its dotted key must
    // appear once, under that field, not again as a read-only row.
    expect(screen.getAllByText("policy.retry.max_attempts")).toHaveLength(1)
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
