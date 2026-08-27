import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { RouterAdapterProvider } from "darkraise-ui/router"
import type { RouterAdapter } from "darkraise-ui/router"
import { beforeEach, describe, expect, it, vi } from "vitest"
import {
  configRows,
  passwordProblem,
  readValue,
  reloadMessage,
  revokedText,
  SettingsScreen,
  syncMessage,
  toDraft,
  toWrite,
} from "./settings-screen"
import type { ConfigResponse } from "../../lib/api-types"

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
    aliases: { fast: ["groq/a"] },
    policy: {
      cooldown: { max: "30m" },
      retry: { max_attempts: 3 },
      timeout: { connect: "10s", first_byte: "60s", total: "10m", idle: "30s" },
    },
  },
})

describe("readValue", () => {
  it("walks a dotted field into the blocks payload", () => {
    expect(readValue(cfg(), "log.retention")).toBe("72h")
    expect(readValue(cfg(), "catalog.discovery.interval")).toBe("6h")
  })

  it("renders a block value rather than [object Object]", () => {
    expect(readValue(cfg(), "aliases")).toBe('{"fast":["groq/a"]}')
  })

  it("shows a missing field as unknown rather than crashing", () => {
    // The field list and the blocks are two payloads; one naming something the
    // other omits must not take the screen down.
    expect(readValue(cfg(), "server.nothing.here")).toBe("—")
  })
})

describe("configRows", () => {
  it("carries each field's source and reloadability", () => {
    const rows = configRows(cfg())
    const aliases = rows.find((r) => r.field === "aliases")
    // §8.1: after the first run, editing aliases in the file has no effect,
    // and the config view has to say so at the point of display.
    expect(aliases?.meta.source).toBe("database")
    const interval = rows.find((r) => r.field === "catalog.discovery.interval")
    expect(interval?.meta.hot_reloadable).toBe(false)
  })

  it("orders fields so the table does not reshuffle between polls", () => {
    const rows = configRows(cfg()).map((r) => r.field)
    expect(rows).toEqual([...rows].sort((a, b) => a.localeCompare(b)))
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
  it("reports a failed sync without claiming the catalog is empty", () => {
    expect(
      syncMessage({ synced: false, error: "models.dev unreachable", serving: "the previous metadata is still serving" }),
    ).toMatch(/previous metadata is still serving/)
  })

  it("carries the sync error so the operator knows what failed", () => {
    expect(syncMessage({ synced: false, error: "timeout after 30s" })).toContain("timeout after 30s")
  })

  it("says synced rather than started, since SyncOnce already finished", () => {
    expect(syncMessage({ synced: true })).toMatch(/synced/i)
  })
})

function stubSettingsFetch(overrides: {
  reload?: { valid: boolean; error?: string; serving?: string }
  sync?: { synced: boolean; error?: string; serving?: string }
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
      return new Response("[]", { status: 200, headers: { "Content-Type": "application/json" } })
    }
    if (url === "/api/config/reload" && method === "POST") {
      return new Response(JSON.stringify(overrides.reload ?? { valid: true }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    }
    if (url === "/api/catalog/sync" && method === "POST") {
      return new Response(JSON.stringify(overrides.sync ?? { synced: true }), {
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

    expect(await screen.findByText(/the reloaded configuration is invalid/i)).toBeInTheDocument()
    expect(screen.queryByText(/^the configuration file is invalid\.$/i)).not.toBeInTheDocument()
    // Only the initial load fetched it; the failed reload did not trigger a
    // second GET for the same answer.
    await waitFor(() => expect(configFetches()).toBe(1))
  })
})

describe("a failed sync", () => {
  it("gets a durable banner rather than a toast that can vanish unread", async () => {
    const { fetchMock } = stubSettingsFetch({
      sync: { synced: false, error: "models.dev unreachable", serving: "the previous metadata is still serving" },
    })
    const user = userEvent.setup()
    mount(<SettingsScreen />)

    await user.click(await screen.findByRole("button", { name: /sync catalog now/i }))

    expect(await screen.findByText(/models.dev unreachable/i)).toBeInTheDocument()
    expect(screen.getByText(/the previous metadata is still serving/i)).toBeInTheDocument()
    // A failed sync changed nothing the catalog cache needs to hear about.
    expect(fetchMock.mock.calls.some(([u, i]) => u === "/api/models" && (i as RequestInit)?.method !== "POST")).toBe(
      false,
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
