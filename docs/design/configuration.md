# Configuration

## Precedence

Five steps, in order:

1. **Defaults**, compiled in.
2. **The file** — one path, `-config`, defaulting to `darkrouter.yaml`. There
   is no search chain. Unknown keys are rejected, so a typo is an error rather
   than a silently ignored setting. `${VAR}` is interpolated in exactly two
   string fields: `server.proxy_token` and a provider's `api_key`.
3. **The database overlay**, for `providers`, `aliases` and `policy` — applied
   *before* a snapshot is published, not after.
4. **Hot reload**, on a debounced file watch. The whole document is validated
   before any of it is published.
5. **Restart-only warnings**, for fields that changed but cannot take effect.

An unresolved `${VAR}` in `server.proxy_token` means no token, with a warning.
An unresolved one in a provider's `api_key` is a hard error.

`DARKROUTER_MASTER_KEY` and `DARKROUTER_ADMIN_PASSWORD_HASH` are read directly
from the environment and are not part of the configuration document.

## Reload versus restart

A **file reload** that changes a restart-only field *warns*: the operator has
already made the change, and a warning is the only honest answer. A **`PUT` to
the API** that names one is *refused*, because a request can be rejected
before anything happens.

Restart-only, because each is captured once when something is constructed:

`server.proxy_listen`, `server.admin_listen`, `policy.timeout.connect`,
`policy.timeout.first_byte`, `catalog.models_dev_url`,
`catalog.sync_interval`, `catalog.sync_timeout`,
`catalog.free_catalog_url`, `catalog.free_catalog_interval`,
`catalog.free_catalog_sync`, `catalog.litellm_url`,
`catalog.litellm_interval`, `catalog.litellm_sync`,
`catalog.discovery.enabled`, `catalog.discovery.interval`,
`catalog.discovery.timeout`, `catalog.discovery.concurrency`, `media.inline`.

`policy.timeout.connect` and `first_byte` are on that list because they
configure a shared HTTP transport built once. `max_body_bytes` deliberately is
**not**: the executor reads it from a per-request snapshot.

## Keys

| Key | Default | Notes |
|---|---|---|
| `server.proxy_listen` | `:8080` | Restart-only. |
| `server.admin_listen` | `:8081` | Restart-only. |
| `server.proxy_token` | *empty* | Shared inbound secret. Interpolated. |
| `server.max_body_bytes` | 33554432 | Applies on reload. |
| `server.shutdown_grace` | `10s` | |
| `server.sse.max_line_bytes` | 1048576 | |
| `server.sse.max_precommit_bytes` | 1048576 | |
| `providers[]` | — | id, kind, preset, base_url, api_key, priority, models. Overlaid from the database. |
| `aliases` | — | Ordered chains. Overlaid from the database. |
| `policy.cooldown.trip_after` | 3 | |
| `policy.cooldown.max` | `15m` | |
| `policy.retry.max_attempts` | 4 | The file loader enforces only `>= 1`; the admin API additionally caps it at 10. |
| `policy.timeout.connect` | `10s` | Restart-only. |
| `policy.timeout.first_byte` | `60s` | Restart-only. |
| `policy.timeout.total` | `10m` | Must be at least `connect + first_byte`. |
| `policy.timeout.idle` | `120s` | Governs a stream after commit. |
| `log.retention` | `720h` | Hard floor of 48h. |
| `capture.bodies` | `false` | |
| `capture.max_bytes` | 256000 | |
| `capture.retention` | `72h` | |
| `catalog.models_dev_url` | models.dev | Restart-only. |
| `catalog.sync_interval` | `12h` | Restart-only. |
| `catalog.sync_timeout` | `30s` | Restart-only. |
| `catalog.free_catalog_url` | upstream register | Restart-only. |
| `catalog.free_catalog_interval` | `24h` | Restart-only. |
| `catalog.free_catalog_sync` | `true` | Restart-only. |
| `catalog.litellm_url` | upstream index | Restart-only. |
| `catalog.litellm_interval` | `24h` | Restart-only. |
| `catalog.litellm_sync` | `true` | Restart-only. |
| `catalog.seed_free_providers` | `true` | Consumed once at startup, but **not** on the restart-only list, so a reload accepts a change that cannot take effect. Known gap. |
| `catalog.discovery.enabled` | `true` | Restart-only. |
| `catalog.discovery.interval` | `15m` | Restart-only. |
| `catalog.discovery.timeout` | `15s` | Restart-only. |
| `catalog.discovery.concurrency` | 8 | Restart-only. Global across the fleet, not per provider. |
| `media.inline` | `true` | Restart-only. |
| `playground.save_conversations` | `true` | |

There is **no `policy.concurrency` block.** Earlier documentation described
one; it never existed, and because unknown keys are rejected, a configuration
copied from that documentation failed to parse.

`darkrouter.example.yaml` at the repository root is the annotated reference
copy and is kept in step with this table.
