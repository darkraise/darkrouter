# Security

## Credentials at rest

`DARKROUTER_MASTER_KEY` → PBKDF2-HMAC-SHA256, 600,000 iterations, with a
16-byte per-database salt → a 32-byte AES-256-GCM key.

The salt and the iteration count are stored in `settings`; the master key
never is. A fresh salt is generated on rotation.

Each credential is sealed with a fresh nonce, using the **credential row id as
additional authenticated data**, so a sealed value cannot be moved between
rows.

A wrong master key is caught at startup by a stored verifier sealed under a
fixed AAD, rather than surfacing as a decryption failure on first use.

## What is never returned

No handler serialises credential material. A credential is exposed as a label
plus a masked four-character suffix. Service-account JSON and OAuth refresh
tokens appear in no response.

One deliberate exception: **minting a proxy token returns its secret once**.
That is a self-issued client token, not upstream credential material, and it
is stored only as a SHA-256 digest — there is nothing to return on a later
read.

Proxy tokens are hashed with SHA-256 rather than the admin password's KDF: the
token is 256 bits this process generated, so there is nothing to brute-force,
and a slow hash on the proxy hot path would be a self-inflicted denial of
service.

## Inbound authentication

The proxy accepts each dialect's native credential form — `Authorization:
Bearer`, `x-api-key`, `x-goog-api-key`, `?key=`. Both a shared
`server.proxy_token` and per-client proxy tokens are accepted; authentication
is off only when **neither** exists. A gateway with tokens issued does not
accept an empty header just because the shared secret is unset.

Comparison hashes both sides before a constant-time compare, specifically so
the comparison's length-based early return cannot leak token length.

Token acceptance is cached for five seconds; refusals are never cached. That
window is simultaneously the `last_used_at` write throttle and the window in
which a revoked token keeps working.

## The admin surface

Password login, bcrypt at cost 12, failing closed on an empty hash. The
session is a sliding 30-day `HttpOnly`, `SameSite=Lax` cookie, stored hashed —
so the identifier in a session listing, and in the revoke path, is a digest
prefix, never the cookie value.

Every mutating route requires **both** a session and a session-bound CSRF
token — an HMAC of the session id under a stored secret — plus an Origin or
`Sec-Fetch-Site` check. The two guards are composed structurally, so a
mutating route cannot acquire one without the other.

`GET /api/oauth/callback` uses the session guard rather than the CSRF guard: a
top-level navigation carries no header to check, and the OAuth `state`
parameter does that work instead.

Login is rate-limited by Darkrouter itself, per IP, because Caddy's standard
build ships no rate limiter and a custom build is deliberately not assumed.

**The proxy port never honours cookies.** No proxy dialect reads one. This
holds by construction rather than by a runtime guard — nothing rejects an
inbound cookie; it is simply never consulted.

## Outbound safety

Media fetched on the gateway's behalf is restricted to http and https, follows
no redirects, refuses a private address at dial time, times out at ten seconds
and caps at 20 MiB. The fetcher's enabled flag defaults to off in its zero
value, which is what makes `media.inline` restart-only rather than merely
inconvenient to change.

Discovery and listing endpoints do not follow redirects either: a redirecting
endpoint is misconfigured, and following it would send the credential to
whatever host it names.

## OAuth

State is single-use, expiring and session-bound. A session mismatch
deliberately does **not** consume the state — letting a blocked attempt
invalidate the operator's own callback would turn the block into a denial of
service.

## No CORS

There is none configured anywhere, and none is needed, because each surface
gets a whole origin. What would break that: splitting the console host from
the API host, or mounting under a subpath, since the bundle references its
assets from the site root.

A cross-site mutating request refused with 403 is the CSRF check working, not
a CORS problem to configure away.
