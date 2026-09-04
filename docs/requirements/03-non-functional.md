# Non-functional requirements

## Performance — `NFR-PRF`

| id | Requirement |
|---|---|
| NFR-PRF-1 | Gateway overhead must not be measurable against a provider's own latency variance on a warm connection. |
| NFR-PRF-2 | Route resolution must perform no I/O — no clock read, no database query, no network call. |
| NFR-PRF-3 | The request path must read its configuration from a lock-free snapshot load. |
| NFR-PRF-4 | Streaming must not buffer a committed response; bytes reach the client as they arrive. |
| NFR-PRF-5 | Pre-commit buffering must be bounded, and the bound must be reachable regardless of event shape. |

## Reliability — `NFR-REL`

| id | Requirement |
|---|---|
| NFR-REL-1 | A background worker that panics must restart without taking the process down. |
| NFR-REL-2 | Health state must survive a restart, including the failure counters behind an expired cooldown. |
| NFR-REL-3 | Schema migrations are forward-only, each in its own transaction; a database newer than the binary must refuse to start rather than half-apply. |
| NFR-REL-4 | Shutdown must let a committed stream finish or emit a terminal event before sockets are forced down. |
| NFR-REL-5 | A configuration reload must validate the whole document before publishing any of it. |
| NFR-REL-6 | Every attempt must emit exactly one health signal, and a claimed circuit-breaker probe must always be released. |

## Security — `NFR-SEC`

| id | Requirement |
|---|---|
| NFR-SEC-1 | Credentials are encrypted at rest with a key derived from an operator-supplied master key; the key is never stored. |
| NFR-SEC-2 | A wrong master key must be detected at startup, not on first use. |
| NFR-SEC-3 | No endpoint, log line, trace or error message may contain credential material. |
| NFR-SEC-4 | Inbound credential comparison must be constant-time and must not leak length. |
| NFR-SEC-5 | The admin session must be unusable on the proxy listener. |
| NFR-SEC-6 | Every mutating admin route requires both a session and a session-bound CSRF token; neither can be had without the other. |
| NFR-SEC-7 | Login must be rate-limited independently of any reverse proxy. |
| NFR-SEC-8 | Media fetched on the gateway's behalf must not reach a private address, follow a redirect, or exceed a size cap. |
| NFR-SEC-9 | A discovery or listing endpoint that redirects must not be followed, so a credential is never sent to a host the operator did not name. |

## Observability — `NFR-OBS`

| id | Requirement |
|---|---|
| NFR-OBS-1 | Liveness, readiness and metrics must be reachable without a session. |
| NFR-OBS-2 | Readiness must fail while the store or configuration is unusable; liveness must not. |
| NFR-OBS-3 | Every response carries a request identifier before anything about the request is known. |
| NFR-OBS-4 | Money is stored as integer micro-dollars. No floating-point value touches money. |
| NFR-OBS-5 | An unpriced model records no cost rather than a zero one. |

## Compatibility — `NFR-CMP`

| id | Requirement |
|---|---|
| NFR-CMP-1 | A client that works against a vendor directly must work against Darkrouter with only its base URL changed. |
| NFR-CMP-2 | An unknown field on a forwarded request must not be silently dropped where the fast path can carry it. |
| NFR-CMP-3 | Removing an authentication mechanism is a breaking change; both the shared secret and per-client tokens are accepted while both exist. |
| NFR-CMP-4 | Below version 1.0.0 a breaking change bumps the minor version. Reaching 1.0.0 is a deliberate manual act. |

## Operability — `NFR-OPS`

| id | Requirement |
|---|---|
| NFR-OPS-1 | The gateway ships as a single container image with no external service dependency. |
| NFR-OPS-2 | Configuration that can be reloaded without a restart must be; configuration that cannot must say so. |
| NFR-OPS-3 | A backup is the data directory plus the master key, stored separately; either alone is useless. |
| NFR-OPS-4 | The console is embedded in the binary, so deploying the gateway deploys the console. |
| NFR-OPS-5 | The container runs read-only, with all capabilities dropped and bounded memory and process limits. |
