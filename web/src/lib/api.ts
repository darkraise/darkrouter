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

function loggedOut(): never {
  csrfToken = ""
  unauthorizedListeners.forEach((fn) => fn())
  throw new ApiError(401, "not authenticated")
}

type RequestOptions = {
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
    // Belt and braces with the browser's own header: the server accepts either,
    // and an older client that sends neither is refused.
    headers["Sec-Fetch-Site"] = "same-origin"
  }

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    // Same-origin: the SPA is served by the API's own port, so the cookie
    // travels without any cross-origin machinery.
    credentials: "same-origin",
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
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) => request<T>("POST", path, body, opts),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
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
): AsyncGenerator<string, void, unknown> {
  const res = await fetch(path, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "X-CSRF-Token": csrfToken,
      "Sec-Fetch-Site": "same-origin",
    },
    body: JSON.stringify(body),
    credentials: "same-origin",
  })
  if (res.status === 401) loggedOut()
  if (!res.ok || !res.body) {
    let message = res.statusText
    try {
      const parsed = (await res.json()) as { error?: string }
      if (parsed.error) message = parsed.error
    } catch {
      // Same reasoning as above.
    }
    throw new ApiError(res.status, message)
  }
  onStart?.({ requestId: res.headers.get("X-Darkrouter-Request") ?? "" })

  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  for (;;) {
    const { done, value } = await reader.read()
    if (done) return
    yield decoder.decode(value, { stream: true })
  }
}

/** Poll intervals, spec §5. Stated here so "near real time" is one edit. */
export const POLL = {
  /** The overview and the requests first page. */
  fast: 3000,
  /** The catalog and usage: they change on a discovery sweep, not per request. */
  slow: 30000,
} as const
