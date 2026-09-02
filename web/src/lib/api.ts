// The one place a request leaves the SPA.
//
// Every mutation carries the CSRF header spec §3 requires, and every response
// funnels through one 401 handler — a dashboard that renders empty tables after
// a session expires reads as "everything is broken" rather than "log in again".

let csrfToken = ""

export function setCsrfToken(t: string) {
  csrfToken = t
}

export function getCsrfToken() {
  return csrfToken
}

/** Listeners notified when the server says the session is gone. */
const unauthorizedListeners = new Set<() => void>()

export function onUnauthorized(fn: () => void) {
  unauthorizedListeners.add(fn)
  return () => {
    unauthorizedListeners.delete(fn)
  }
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

/** A failure the next attempt might not repeat: the network dropped, or the
 *  server answered 5xx. A 4xx is the server's verdict on the request itself
 *  and comes back identical however often it is retried. */
export function isTransient(err: unknown): boolean {
  if (err instanceof ApiError) return err.status >= 500
  // fetch rejects with a TypeError when the connection itself fails.
  return err instanceof TypeError
}

function loggedOut(): never {
  csrfToken = ""
  unauthorizedListeners.forEach((fn) => fn())
  throw new ApiError(401, "not authenticated")
}

/**
 * Classifies a non-OK response from the executor and throws — either as a
 * session death (also firing the shared logout side effect) or as an
 * ordinary ApiError carrying the response's own message.
 *
 * Every admin-issued rejection (a dead session included) shapes its body as
 * {"error": "<string>"}; only a body that reached a dialect writer nests an
 * object there instead, which means the request cleared the session check
 * and something downstream — a provider, or the executor itself — is
 * calling the request bad, not the console logging the operator out.
 *
 * Shared by stream() and by any caller that talks to the executor with its
 * own fetch instead of request() — the playground's aux and count calls,
 * which need response headers request() cannot expose. They hit the same
 * dialect-writer error shape stream() was fixed for, so the classification
 * lives once here rather than as two hand-synced copies.
 */
export async function throwOnExecutorError(res: Response): Promise<never> {
  let message = res.statusText
  let sessionDead = res.status === 401
  try {
    const parsed = (await res.json()) as { error?: unknown }
    if (typeof parsed.error === "string") {
      message = parsed.error
    } else if (parsed.error && typeof parsed.error === "object") {
      sessionDead = false
      const nested = parsed.error as { message?: string }
      if (nested.message) message = nested.message
    }
  } catch {
    // A non-JSON error body means something upstream of the API answered.
    // The status line is all there is to report.
  }
  if (sessionDead) loggedOut()
  throw new ApiError(res.status, message)
}

export type RequestOptions = {
  /**
   * Some routes answer 401 for two unrelated reasons: the session died
   * before the handler ran (requireSession, always "not authenticated"), or
   * the handler itself rejected the request on its merits — the password
   * endpoint answers 401 with exactly "invalid password" for a wrong current
   * password, session very much intact. Naming that one message here is how
   * a caller opts only *that* rejection out of the global logout; any other
   * 401, including a session that actually died on this same call, still
   * goes through loggedOut() same as every other request.
   */
  expectedRejection?: string
  /** TanStack Query's, so a query whose screen unmounted stops mid-flight
   *  rather than landing in a cache nobody is reading. */
  signal?: AbortSignal
}

/** Peeks at a 401 body without consuming it, to tell the two reasons apart. */
async function isExpectedRejection(res: Response, expected: string): Promise<boolean> {
  try {
    const parsed = (await res.clone().json()) as { error?: string }
    return parsed.error === expected
  } catch {
    return false
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  opts?: RequestOptions,
): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers["Content-Type"] = "application/json"
  if (method !== "GET") {
    // Spec §3: bound to the session by HMAC, so a token from another session
    // is worthless and one the client never received cannot be guessed.
    headers["X-CSRF-Token"] = csrfToken
  }

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    // Same-origin: the SPA is served by the API's own port, so the cookie
    // travels without any cross-origin machinery.
    credentials: "same-origin",
    signal: opts?.signal,
  })

  if (res.status === 401) {
    const expected = opts?.expectedRejection !== undefined && (await isExpectedRejection(res, opts.expectedRejection))
    if (!expected) loggedOut()
  }
  if (!res.ok) {
    let message = res.statusText
    try {
      const parsed = (await res.json()) as { error?: string }
      if (parsed.error) message = parsed.error
    } catch {
      // A non-JSON error body means something upstream of the API answered.
      // The status line is all there is to report.
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string, opts?: RequestOptions) => request<T>("GET", path, undefined, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("POST", path, body, opts),
  patch: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("PATCH", path, body, opts),
  put: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("PUT", path, body, opts),
  del: <T>(path: string, opts?: RequestOptions) => request<T>("DELETE", path, undefined, opts),
}

/** What a streamed call reports back before its body starts arriving. */
export type StreamStart = {
  /** X-Darkrouter-Request, which the trace link needs. */
  requestId: string
}

/**
 * stream sends a mutating request and yields the response body in chunks.
 *
 * EventSource cannot set headers and spec §4 requires the CSRF header on
 * /api/playground, so the playground reads a ReadableStream instead.
 *
 * onStart fires once the response headers land, which is how the caller gets
 * the request id before the body it is rendering as it arrives.
 */
export async function* stream(
  path: string,
  body: unknown,
  onStart?: (s: StreamStart) => void,
  signal?: AbortSignal,
): AsyncGenerator<string, void, unknown> {
  const res = await fetch(path, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrfToken,
    },
    body: JSON.stringify(body),
    credentials: "same-origin",
    signal,
  })
  if (!res.ok || !res.body) {
    // A 401 here is ambiguous in a way request() never sees: this path also
    // carries a live executor run, and a bad credential answers 401 too —
    // that is the playground's whole reason to exist. throwOnExecutorError
    // tells the two apart.
    return await throwOnExecutorError(res)
  }
  onStart?.({ requestId: res.headers.get("X-Darkrouter-Request") ?? "" })

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) {
        // A multi-byte character split across the last two chunks is still
        // buffered in the decoder; flushing is what emits it.
        const tail = decoder.decode()
        if (tail) yield tail
        return
      }
      yield decoder.decode(value, { stream: true })
    }
  } finally {
    // A consumer that stops early — the playground's Stop button, or a
    // component unmounting mid-answer — leaves the body unread otherwise,
    // and the connection open behind it. Cancelling is a no-op once the
    // stream has ended on its own.
    void reader.cancel().catch(() => {})
  }
}

/** Poll intervals, spec §5. Stated here so "near real time" is one edit. */
export const POLL = {
  /** The overview and the requests first page. */
  fast: 3000,
  /** The catalog and usage: they change on a discovery sweep, not per request. */
  slow: 30000,
} as const
