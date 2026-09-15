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
    expect(result.failed.map((f) => f.error)).toEqual([
      "refused by the provider, but deleting it failed, so it is still in use: database is locked",
    ])
    expect(result.routingNotUpdated).toBeUndefined()
  })
})

describe("addCredentials when the provider refuses a secret that cannot be downloaded again", () => {
  const refused = (auth_style: string) =>
    json({ ok: false, probe: "signature", latency_ms: 1, error: "403 InvalidSignatureException", rejected: true, auth_style })
  const methods = (fetchMock: ReturnType<typeof stubFetch>) =>
    fetchMock.mock.calls.map(([url, init]) => `${(init as RequestInit | undefined)?.method ?? "GET"} ${String(url)}`)

  for (const style of ["sigv4", "gcp-sa"]) {
    it(`disables a refused ${style} key rather than deleting it`, async () => {
      const fetchMock = stubFetch((url, method) => {
        if (url.endsWith("/keys") && method === "POST") return json({ id: "cred-1", label: "work" }, 201)
        if (url.includes("/test")) return refused(style)
        if (method === "PATCH") return json({ id: "cred-1", enabled: false })
      })

      const result = await addCredentials("bedrock", draft, false)

      const calls = methods(fetchMock)
      expect(calls.some((c) => c.startsWith("DELETE"))).toBe(false)
      const patch = fetchMock.mock.calls.find(([, init]) => (init as RequestInit)?.method === "PATCH")
      expect(String(patch?.[0])).toMatch(/\/api\/providers\/bedrock\/keys\/cred-1$/)
      expect(JSON.parse(String((patch?.[1] as RequestInit).body))).toEqual({ enabled: false })
      expect(result.disabled.map((d) => d.label)).toEqual(["work"])
      expect(result.rejected).toEqual([])
      expect(result.added).toBe(0)
      // Stored, so sending it again would store it twice.
      expect(result.retry).toEqual([])
    })
  }

  it("still deletes a refused key the operator can copy again", async () => {
    const fetchMock = stubFetch((url, method) => {
      if (url.endsWith("/keys") && method === "POST") return json({ id: "cred-1", label: "work" }, 201)
      if (url.includes("/test")) return refused("bearer")
      if (method === "DELETE") return new Response(null, { status: 204 })
    })

    const result = await addCredentials("groq", draft, false)

    const calls = methods(fetchMock)
    expect(calls.some((c) => c.startsWith("DELETE"))).toBe(true)
    expect(calls.some((c) => c.startsWith("PATCH"))).toBe(false)
    expect(result.rejected.map((r) => r.label)).toEqual(["work"])
    expect(result.disabled).toEqual([])
  })

  it("counts a disable that committed but was not routed as disabled", async () => {
    stubFetch((url, method) => {
      if (url.endsWith("/keys") && method === "POST") return json({ id: "cred-1", label: "work" }, 201)
      if (url.includes("/test")) return refused("sigv4")
      if (method === "PATCH") return json({ error: ROUTING, routing_updated: false }, 500)
    })

    const result = await addCredentials("bedrock", draft, false)

    expect(result.disabled.map((d) => d.label)).toEqual(["work"])
    expect(result.failed).toEqual([])
    expect(result.added).toBe(0)
    expect(result.routingNotUpdated).toBe(ROUTING)
  })

  it("reports a refused key whose disable did not happen as still in use", async () => {
    stubFetch((url, method) => {
      if (url.endsWith("/keys") && method === "POST") return json({ id: "cred-1", label: "work" }, 201)
      if (url.includes("/test")) return refused("gcp-sa")
      if (method === "PATCH") return json({ error: "database is locked" }, 500)
    })

    const result = await addCredentials("vertex", draft, false)

    expect(result.disabled).toEqual([])
    expect(result.added).toBe(1)
    expect(result.failed.map((f) => f.label)).toEqual(["work"])
    expect(result.failed[0].error).toContain("database is locked")
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

    reportAdded({ added: 1, failed: [], rejected: [], disabled: [], retry: [], routingNotUpdated: ROUTING })

    expect(success).not.toHaveBeenCalled()
    expect(warning).toHaveBeenCalledWith(expect.stringContaining(ROUTING))
  })

  it("says a refused key was disabled, not deleted, and is still stored", () => {
    const error = vi.spyOn(toast, "error")
    const warning = vi.spyOn(toast, "warning")

    reportAdded({
      added: 0,
      failed: [],
      rejected: [],
      disabled: [{ label: "work", error: "403 InvalidSignatureException" }],
      retry: [],
    })

    expect(error).not.toHaveBeenCalled()
    const message = String(warning.mock.calls[0]?.[0])
    expect(message).not.toMatch(/No credential kept/)
    expect(message).toMatch(/disabled/i)
    expect(message).toContain("work: 403 InvalidSignatureException")
    expect(message).toMatch(/delete it/i)
  })

  it("names disabled keys beside added and refused ones", () => {
    const warning = vi.spyOn(toast, "warning")

    reportAdded({
      added: 1,
      failed: [],
      rejected: [{ label: "old", error: "401" }],
      disabled: [
        { label: "a", error: "refused" },
        { label: "b", error: "refused" },
      ],
      retry: [],
    })

    const message = String(warning.mock.calls[0]?.[0])
    expect(message).toContain("1 added")
    expect(message).toContain("1 refused (old: 401)")
    expect(message).toMatch(/2 disabled/)
  })
})
