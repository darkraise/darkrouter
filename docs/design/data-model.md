# Data model

One SQLite file. WAL, foreign keys on, incremental auto-vacuum, STRICT tables.

Three handles: a single writer at `synchronous=NORMAL`, a read pool of four,
and a `synchronous=FULL` handle used only for credential writes and key
rotation.

## Units

The schema mixes units, and getting one wrong is silent. Stated once, here:

| Kind | Unit |
|---|---|
| Request and attempt timestamps | Unix **milliseconds** |
| `provider_keys.expires_at`, `proxy_tokens` timestamps | Unix **seconds** |
| `usage_daily.day` | `YYYY-MM-DD`, UTC, keyed on request start |
| Money (`cost_micros`) | Integer **micro-dollars** |
| Catalogue prices | Integer micro-dollars **per million tokens** |

No floating-point value touches money anywhere.

A unit mismatch of exactly this kind has already shipped once: session rows
were written in milliseconds and read as seconds, landing every row in the
year 58633 and listing expired sessions as revocable browsers.

## Tables

Seventeen application tables, plus `schema_version` created by the migrator
itself.

| Table | Holds |
|---|---|
| `providers` | The configured fleet, its priority and its per-provider switches. |
| `provider_keys` | Sealed credentials, their kind, scope and expiry. |
| `models` | The merged catalogue: capabilities, prices, price provenance, lifecycle state. |
| `model_overrides` | Operator corrections, which outrank every other source. |
| `requests` | One row per client request: outcome, timing, tokens, cost, serving path. |
| `request_attempts` | One row per attempt within a request, with its own tokens and cost. |
| `request_bodies` | Captured request and response bodies, when capture is enabled. |
| `health` | Persisted breaker state. |
| `usage_daily` | Daily rollup by day, provider, model and alias. |
| `provider_discovery` | Per-provider discovery health and filtering. |
| `aliases` | Ordered alias chains. |
| `proxy_tokens` | Per-client proxy tokens, stored as SHA-256 digests. |
| `sessions` | Admin sessions, stored as digests. |
| `settings` | Key derivation salt and iteration count, CSRF secret, password hash. |
| `playground_presets` | Saved console playground configurations. |
| `playground_conversations`, `playground_messages` | Saved playground conversations. |

`requests` and `request_attempts` carry **no foreign key onto `providers`**,
deliberately: deleting a provider must not delete its history.

`request_attempts` has no cache-token columns, so an attempt row can carry a
cost with zero tokens — and that is correct. A fully cached prompt burns
cache-read tokens the attempt row cannot express, but the money was real, and
the aggregates read cost rather than tokens.

## Migrations

Numbered SQL files, embedded, forward-only, each in its own transaction.
`schema_version` is a **single-row pointer**, not a migration log. Versions
must be contiguous from 1 — a skipped number is a startup error — and a
database newer than the binary refuses to start rather than half-applying.

Twenty-one migrations exist. Two of them rebuild a table rather than altering
it, so the migration files mention two transient `*_new` names that are not
part of the schema. Two are worth knowing about because their names
do not say what they do:

- **0013** is data-only: it rewrites four presets' authentication style.
- **0017** **wipes the sessions table**, because session identifiers became
  digests. Every operator is logged out by that upgrade.

Because migrations are forward-only, restoring and downgrading are the same
operation: restore the data directory and the master key that was current when
the backup was taken.

## Retention

Request history expires on `log.retention`, which has a hard floor of 48
hours. The floor exists because the rollup finalises a day before freezing it,
which needs the previous day still present when the sweeper runs.

Captured bodies expire on their own, shorter retention.

The usage rollup is idempotent recomputation, not incremental accumulation, so
running it twice cannot double-count. A failed attempt's spend is attributed
to the provider that burned it, and the serving attempt is identified by its
outcome, never by matching the request's final provider — a pre-commit retry
can re-attempt the same provider and model.
