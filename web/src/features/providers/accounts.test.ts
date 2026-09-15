import { beforeEach, describe, expect, it, vi } from "vitest"
import { toast } from "darkraise-ui"
import { addCredentials, reportAdded, retryDraft } from "./accounts"
import { draftAccounts, emptyAccounts } from "./account-fields"

const ROUTING =
  "the change was saved, but the gateway could not load it and is still routing with the previous settings; see the server log"

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })

type Route = (url: string, method: string) => Response | undefined

function stubFetch(route: Route) {
  const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
    const method = (init as RequestInit | undefined)?.method ?? "GET"
    return route(String(url), method) ?? json({})
  })
  vi.stubGlobal("fetch", fetchMock)
  return fetchMock
}

const draft = { ...emptyAccounts, label: "work", secret: "sk-work" }

beforeEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe("addCredentials when the gateway did not load the change", () => {
  it("counts a key the server stored as added, not as one to send again", async () => {
    stubFetch((url, method) => {
      if (url.endsWith("/keys") && method === "POST") {
        return json({ id: "cred-1", label: "work", routing_updated: false, warning: ROUTING }, 201)
      }
      if (url.includes("/test")) return json({ ok: true, probe: "listing", latency_ms: 1 })
    })

    const result = await addCredentials("groq", draft, false)

    expect(result.added).toBe(1)
    expect(result.retry).toEqual([])
    expect(result.routingNotUpdated).toBe(ROUTING)
  })

  it("treats a refused key whose delete committed as deleted", async () => {
    // The delete answers 500 because the router still holds the key, but the
    // row is gone: reporting it as kept would misdescribe what is stored.
    const fetchMock = stubFetch((url, method) => {
      if (url.endsWith("/keys") && method === "POST") {
        return json({ id: "cred-1", label: "work", routing_updated: true }, 201)
      }
      if (url.includes("/test")) {
        return json({ ok: false, probe: "listing", latency_ms: 1, error: "401", rejected: true })
      }
      if (method === "DELETE") return json({ error: ROUTING, routing_updated: false }, 500)
    })

    const result = await addCredentials("groq", draft, false)

    expect(fetchMock.mock.calls.some(([, init]) => (init as RequestInit)?.method === "DELETE")).toBe(true)
    expect(result.added).toBe(0)
    expect(result.failed).toEqual([])
    expect(result.rejected.map((r) => r.label)).toEqual(["work"])
    expect(result.retry.map((r) => r.label)).toEqual(["work"])
    expect(result.routingNotUpdated).toBe(ROUTING)
  })

  it("still keeps a refused key whose delete did not happen", async () => {
    stubFetch((url, method) => {
      if (url.endsWith("/keys") && method === "POST") return json({ id: "cred-1", label: "work" }, 201)
      if (url.includes("/test")) {
        return json({ ok: false, probe: "listing", latency_ms: 1, error: "401", rejected: true })
      }
      if (method === "DELETE") return json({ error: "database is locked" }, 500)
    })

    const result = await addCredentials("groq", draft, false)

    expect(result.added).toBe(1)
    expect(result.failed.map((f) => f.error)).toEqual(["kept unverified: database is locked"])
    expect(result.routingNotUpdated).toBeUndefined()
  })
})

describe("retryDraft", () => {
  const failedOn = (secrets: string[], d: typeof draft, needsAccount = false) =>
    draftAccounts(d, needsAccount)
      .filter((a) => secrets.includes(a.secret))
      .map((account) => ({ label: account.label, error: "boom", account }))

  it("gives back an auto-named line as the operator typed it", () => {
    const bulk = { ...emptyAccounts, mode: "bulk" as const, bulk: "sk-one\nsk-two\nsk-three" }

    const next = retryDraft(bulk, failedOn(["sk-two"], bulk), false)

    expect(next.bulk).toBe("sk-two")
  })

  it("does not split a label prefix carrying a pipe into the secret", () => {
    const bulk = { ...emptyAccounts, mode: "bulk" as const, label: "team|a", bulk: "sk-one\nsk-two" }

    const next = retryDraft(bulk, failedOn(["sk-one", "sk-two"], bulk), false)

    expect(draftAccounts(next).map((a) => a.secret)).toEqual(["sk-one", "sk-two"])
  })

  it("gives back lines carrying an account unchanged", () => {
    const bulk = {
      ...emptyAccounts,
      mode: "bulk" as const,
      bulk: "alpha | acct-1 | sk-one\n  acct-2|sk-two  \nbeta|acct-3|sk-three",
    }

    const next = retryDraft(bulk, failedOn(["sk-two", "sk-three"], bulk, true), true)

    expect(next.bulk).toBe("  acct-2|sk-two  \nbeta|acct-3|sk-three")
  })
})

describe("reportAdded", () => {
  it("says routing was not updated even when every key went in", () => {
    const success = vi.spyOn(toast, "success")
    const warning = vi.spyOn(toast, "warning")

    reportAdded({ added: 1, failed: [], rejected: [], retry: [], routingNotUpdated: ROUTING })

    expect(success).not.toHaveBeenCalled()
    expect(warning).toHaveBeenCalledWith(expect.stringContaining(ROUTING))
  })
})
