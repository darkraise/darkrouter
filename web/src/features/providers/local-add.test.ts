import { describe, it, expect } from "vitest"
import { addLocalRuntime, testLocalRuntime, type ProviderApi } from "./local-add"

/** Records what reached the network, and answers from a script. The network is
 *  the boundary being faked; everything above it is the real code. */
function fakeApi(script: {
  create?: () => unknown
  probe?: () => unknown
  remove?: () => unknown
}): ProviderApi & { calls: string[] } {
  const calls: string[] = []
  return {
    calls,
    post: async (path: string, body?: unknown) => {
      if (path.endsWith("/test")) {
        calls.push(`POST ${path}`)
        return (script.probe?.() ?? { ok: true, probe: "listing", latency_ms: 1 }) as never
      }
      calls.push(`POST ${path} ${JSON.stringify(body)}`)
      return (script.create?.() ?? { id: "ollama" }) as never
    },
    del: async (path: string) => {
      calls.push(`DELETE ${path}`)
      return (script.remove?.() ?? null) as never
    },
  }
}

const draft = { presetId: "ollama", baseUrl: "http://host.docker.internal:11434/v1" }

describe("testLocalRuntime", () => {
  it("creates the provider disabled so a probe cannot put it into routing", async () => {
    const api = fakeApi({})
    await testLocalRuntime(api, draft)
    expect(api.calls[0]).toBe(
      'POST /api/providers {"id":"ollama","preset":"ollama","base_url":"http://host.docker.internal:11434/v1","enabled":false}',
    )
  })

  it("removes the provider whether the probe passed or failed", async () => {
    const passed = fakeApi({})
    await testLocalRuntime(passed, draft)
    expect(passed.calls).toEqual([
      expect.stringContaining("POST /api/providers"),
      "POST /api/providers/ollama/test",
      "DELETE /api/providers/ollama",
    ])

    const failed = fakeApi({ probe: () => ({ ok: false, probe: "listing", latency_ms: 1, error: "refused" }) })
    await testLocalRuntime(failed, draft)
    expect(failed.calls.at(-1)).toBe("DELETE /api/providers/ollama")
  })

  it("reports the model count the probe found", async () => {
    const api = fakeApi({ probe: () => ({ ok: true, probe: "listing", latency_ms: 12, model_count: 14 }) })
    expect(await testLocalRuntime(api, draft)).toEqual({ ok: true, modelCount: 14 })
  })

  it("reports the probe's own error rather than a generic one", async () => {
    const api = fakeApi({ probe: () => ({ ok: false, probe: "listing", latency_ms: 1, error: "connection refused" }) })
    expect(await testLocalRuntime(api, draft)).toEqual({ ok: false, error: "connection refused" })
  })

  it("does not delete a provider it failed to create", async () => {
    // A 409 means the id was already there. Deleting it would destroy a
    // provider the operator configured earlier.
    const api = fakeApi({
      create: () => {
        throw new Error("provider already exists")
      },
    })
    const result = await testLocalRuntime(api, draft)
    expect(result).toEqual({ ok: false, error: "provider already exists" })
    expect(api.calls.some((c) => c.startsWith("DELETE"))).toBe(false)
  })
})

describe("addLocalRuntime", () => {
  it("keeps the provider when the probe passes", async () => {
    const api = fakeApi({ probe: () => ({ ok: true, probe: "listing", latency_ms: 5, model_count: 3 }) })
    expect(await addLocalRuntime(api, draft)).toEqual({ ok: true, modelCount: 3 })
    expect(api.calls.some((c) => c.startsWith("DELETE"))).toBe(false)
  })

  it("creates the provider enabled", async () => {
    const api = fakeApi({})
    await addLocalRuntime(api, draft)
    expect(api.calls[0]).toContain('"enabled":true')
  })

  it("deletes the provider when the probe fails, so only a reachable one survives", async () => {
    const api = fakeApi({ probe: () => ({ ok: false, probe: "listing", latency_ms: 1, error: "no route to host" }) })
    expect(await addLocalRuntime(api, draft)).toEqual({ ok: false, error: "no route to host" })
    expect(api.calls.at(-1)).toBe("DELETE /api/providers/ollama")
  })

  it("deletes the provider when the probe request itself throws", async () => {
    const api = fakeApi({
      probe: () => {
        throw new Error("gateway timeout")
      },
    })
    expect(await addLocalRuntime(api, draft)).toEqual({ ok: false, error: "gateway timeout" })
    expect(api.calls.at(-1)).toBe("DELETE /api/providers/ollama")
  })

  it("still reports the probe failure when the rollback also fails", async () => {
    // The operator needs to know why the endpoint was rejected; that the
    // cleanup then failed is the lesser of the two facts.
    const api = fakeApi({
      probe: () => ({ ok: false, probe: "listing", latency_ms: 1, error: "connection refused" }),
      remove: () => {
        throw new Error("delete failed")
      },
    })
    expect(await addLocalRuntime(api, draft)).toEqual({ ok: false, error: "connection refused" })
  })
})
