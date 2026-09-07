# Configuration

## Precedence

Four steps, in order:

1. **Defaults**, compiled in.
2. **The environment**, for the bootstrap set the process needs before the
   database is open, or in order to be reachable at all:
   `DARKROUTER_PROXY_LISTEN` and `DARKROUTER_ADMIN_LISTEN` (defaults `:18080`
   and `:18081`), `DARKROUTER_PROXY_TOKEN`, `DARKROUTER_MASTER_KEY`,
   `DARKROUTER_ADMIN_PASSWORD_HASH`, `DARKROUTER_LOG_LEVEL`,
   `DARKROUTER_LOG_FORMAT`, and the database path (`-db`, or `DARKROUTER_DB`,
   defaulting to `darkrouter.db` in the working directory). None of these is a
   row in `settings`, and a change to one takes a restart.
3. **The database** — the `settings` table for scalar keys, and the provider,
   alias and policy tables for those blocks. This is the source of truth for
   everything else, and a key absent from it is on its compiled default.
4. **Restart-only warnings**, for fields that changed but cannot take effect.

There is **no configuration file.** `-config` is accepted and ignored for one
release, and a `darkrouter.yaml` left beside the database is warned about at
startup and never read.

A reload re-reads the database and republishes the whole snapshot; it is
validated before any of it is published, and a load that fails leaves the
previous snapshot serving.

## Reload versus restart

A **reload** that picks up a changed restart-only field *warns*: the value is
already stored, and a warning is the only honest answer. A **`PUT` to
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

### `server.public_url`

The listen addresses say what the process binds. They stop describing what a
client dials the moment anything sits in between, and everything that does is
invisible from inside the process: `-p 8090:18080` renumbers the port, a
reverse proxy replaces the host and scheme, a prefix route prepends a path.
`server.public_url` is how a deployment states the answer, because no amount of
inspection can recover it.

A bare domain is the expected form — `llm.example.com` — and https is supplied
for it, because a domain reachable from outside this machine has TLS terminated
in front of it and guessing http would put a client's token on the wire in the
clear. Write the scheme out to override that, and add a port or a path prefix
where one exists; both are carried through as written.

The Connect page then lists **both** addresses: the configured public one and
the LAN one it works out from the page's own address plus `proxy_listen`'s
port. A gateway with a domain still answers on the LAN, and a client inside the
network usually wants the address that does not leave it. The client snippets
default to the public address and carry a toggle for the other. With nothing
configured the page shows the LAN address alone, and says it was worked out
rather than told.

It is validated at load: a host is required, and a query or fragment is
refused. It is hot-reloadable, since nothing but the console reads it.

## Keys

| Key | Default | Notes |
|---|---|---|
| `server.proxy_listen` | `:18080` | Restart-only. |
| `server.admin_listen` | `:18081` | Restart-only. |
| `server.public_url` | *empty* | The public domain clients reach the gateway at. A bare domain is assumed https. No query or fragment. Empty means the console shows only the LAN address. |
| `server.proxy_token` | *empty* | Shared inbound secret, from `DARKROUTER_PROXY_TOKEN`. Restart-only. |
| `server.max_body_bytes` | 33554432 | Applies on reload. |
| `server.shutdown_grace` | `10s` | |
| `server.sse.max_line_bytes` | 1048576 | |
| `server.sse.max_precommit_bytes` | 1048576 | |
| `providers[]` | — | id, kind, preset, base_url, api_key, priority, models. Overlaid from the database. |
| `aliases` | — | Ordered chains. Overlaid from the database. |
| `policy.cooldown.trip_after` | 3 | |
| `policy.cooldown.max` | `15m` | |
| `policy.retry.max_attempts` | 4 | The loader enforces only `>= 1`; the admin API additionally caps it at 10. |
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
one; it never existed.

There is no example file to keep in step with this table: every key here is a
row in `settings`, written by the console where a write endpoint exists and
left at its default where one does not.
