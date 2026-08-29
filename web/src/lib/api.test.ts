import { describe, it, expect, vi, afterEach } from "vitest"
import { api, stream, onUnauthorized, ApiError, throwOnExecutorError } from "./api"

afterEach(() => {
  vi.unstubAllGlobals()
})

async function drain(gen: AsyncGenerator<string, void, unknown>): Promise<void> {
  for await (const _chunk of gen) {
    // Draining is enough: these tests only care that the generator throws
    // before yielding anything, and what it throws.
  }
}

describe("stream", () => {
  it("logs out on a session-dead 401", async () => {
    // requireSession's own rejection: {"error": "<string>"}, exactly what
    // every other admin-issued 401 looks like.
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(async () =>
        new Response(JSON.stringify({ error: "not authenticated" }), {
          status: 401,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    )
    let loggedOut = false
    const unsub = onUnauthorized(() => {
      loggedOut = true
    })

    await expect(drain(stream("/api/playground", {}))).rejects.toBeInstanceOf(ApiError)
    unsub()

    expect(loggedOut).toBe(true)
  })

  it("does not log out on a provider's own 401, and surfaces its message", async () => {
    // A dialect writer's shape: every ir.ErrorType nests error as an object,
    // never a bare string. This only reaches the SPA once a request has
    // cleared the session check, so the console session is fine — the 401
    // is the provider calling the credential bad.
    vi.stubGlobal(
      "fetch",
      vi.fn<typeof fetch>(async () =>
        new Response(
          JSON.stringify({ error: { message: "invalid api key", type: "authentication_error" } }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        ),
      ),
    )
    let loggedOut = false
    const unsub = onUnauthorized(() => {
      loggedOut = true
    })

    let caught: unknown
    try {
      await drain(stream("/api/playground", {}))
    } catch (err) {
      caught = err
    }
    unsub()

    expect(loggedOut).toBe(false)
    expect(caught).toBeInstanceOf(ApiError)
    expect((caught as ApiError).message).toBe("invalid api key")
  })
})

describe("throwOnExecutorError", () => {
  it("logs out and reports the message on a session-dead 401", async () => {
    // requireSession's own rejection: {"error": "<string>"}, exactly what
    // every other admin-issued 401 looks like.
    const res = new Response(JSON.stringify({ error: "not authenticated" }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    })
    let loggedOut = false
    const unsub = onUnauthorized(() => {
      loggedOut = true
    })

    let caught: unknown
    try {
      await throwOnExecutorError(res)
    } catch (err) {
      caught = err
    }
    unsub()

    expect(loggedOut).toBe(true)
    expect(caught).toBeInstanceOf(ApiError)
    expect((caught as ApiError).message).toBe("not authenticated")
  })

  it("does not log out on an object-shaped 401, and surfaces the nested message", async () => {
    // A dialect writer's shape: every ir.ErrorType nests error as an object,
    // never a bare string. This only reaches the SPA once a request has
    // cleared the session check, so the console session is fine — the 401
    // is a provider, or the executor itself, calling the request bad.
    const res = new Response(
      JSON.stringify({ error: { message: "invalid api key", type: "authentication_error" } }),
      { status: 401, headers: { "Content-Type": "application/json" } },
    )
    let loggedOut = false
    const unsub = onUnauthorized(() => {
      loggedOut = true
    })

    let caught: unknown
    try {
      await throwOnExecutorError(res)
    } catch (err) {
      caught = err
    }
    unsub()

    expect(loggedOut).toBe(false)
    expect(caught).toBeInstanceOf(ApiError)
    expect((caught as ApiError).message).toBe("invalid api key")
  })

  it("never logs out on a 400", async () => {
    // Not 401 at all — status alone rules out session death regardless of
    // body shape.
    const res = new Response(JSON.stringify({ error: "model and prompt are required" }), {
      status: 400,
      headers: { "Content-Type": "application/json" },
    })
    let loggedOut = false
    const unsub = onUnauthorized(() => {
      loggedOut = true
    })

    await expect(throwOnExecutorError(res)).rejects.toBeInstanceOf(ApiError)
    unsub()

    expect(loggedOut).toBe(false)
  })

  it("never logs out on a 503", async () => {
    const res = new Response(JSON.stringify({ error: "no executor" }), {
      status: 503,
      headers: { "Content-Type": "application/json" },
    })
    let loggedOut = false
    const unsub = onUnauthorized(() => {
      loggedOut = true
    })

    await expect(throwOnExecutorError(res)).rejects.toBeInstanceOf(ApiError)
    unsub()

    expect(loggedOut).toBe(false)
  })
})

describe("the expected-rejection exemption", () => {
  function stub401(error: string) {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error }), {
            status: 401,
            headers: { "Content-Type": "application/json" },
          }),
      ),
    )
  }

  it("keeps the operator on the page when their own password was wrong", async () => {
    // A mistyped current password is a legitimate rejection, not a dead
    // session. Bouncing to the login screen over a typo is the bug this
    // exemption exists for, and it shipped once already.
    stub401("invalid password")
    const seen = vi.fn()
    const off = onUnauthorized(seen)

    await expect(
      api.post("/api/auth/password", {}, { expectedRejection: "invalid password" }),
    ).rejects.toBeInstanceOf(ApiError)

    expect(seen).not.toHaveBeenCalled()
    off()
  })

  it("still logs out when the session really died on that same call", async () => {
    // The exemption is scoped to one message, not to the endpoint: a genuine
    // 401 here has to log out like any other.
    stub401("unauthorized")
    const seen = vi.fn()
    const off = onUnauthorized(seen)

    await expect(
      api.post("/api/auth/password", {}, { expectedRejection: "invalid password" }),
    ).rejects.toBeInstanceOf(Error)

    expect(seen).toHaveBeenCalled()
    off()
  })

  it("logs out on a 401 from a call that named no exemption", async () => {
    stub401("invalid password")
    const seen = vi.fn()
    const off = onUnauthorized(seen)

    await expect(api.get("/api/providers")).rejects.toBeInstanceOf(Error)

    expect(seen).toHaveBeenCalled()
    off()
  })

  it("carries the server's message through for the page to render inline", async () => {
    stub401("invalid password")
    const off = onUnauthorized(() => {})
    await expect(
      api.post("/api/auth/password", {}, { expectedRejection: "invalid password" }),
    ).rejects.toMatchObject({ status: 401, message: "invalid password" })
    off()
  })
})
