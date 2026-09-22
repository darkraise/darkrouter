# Configuration

## Precedence

Four steps, in order:

1. **Defaults**, compiled in.
2. **The environment**, for the bootstrap set the process needs before the
   database is open, or in order to be reachable at all:
   `DARKROUTER_PROXY_LISTEN` and `DARKROUTER_ADMIN_LISTEN` (defaults `:18080`
   and `:18081`), `DARKROUTER_PROXY_TOKEN`, `DARKROUTER_MASTER_KEY`,
   `DARKROUTER_LOG_LEVEL`, `DARKROUTER_LOG_FORMAT`, and the database path
   (`-db`, or `DARKROUTER_DB`, defaulting to `darkrouter.db` in the working
   directory). None of these is a row in `settings`, and a change to one
   takes a restart.
3. **The database** — the `settings` table for every key in the table below,
   and the providers and aliases tables for those two blocks. This is the
   source of truth for everything else, and a key absent from `settings` is on
   its compiled default.
4. **Restart-only warnings**, for fields that changed but cannot take effect.

There is **no configuration file.** `-config` is accepted and ignored for one
release, and a `darkrouter.yaml` left beside the database is warned about at
startup and never read.

A reload re-reads the database and republishes the whole snapshot; it is
validated before any of it is published, and a load that fails leaves the
previous snapshot serving.

## Reload versus restart

A **reload** that picks up a changed restart-only field *warns*: the value is
already stored, and a warning is the only honest answer. A **`PUT` to the API**
that names one is *accepted*, and the response lists the written keys that take
effect on restart — the value belongs in the database either way, and refusing
it would leave no way to set it at all.

Restart-only, because each is captured once when something is constructed:

```
policy.timeout.connect
policy.timeout.first_byte
catalog.models_dev_url
catalog.sync_interval
catalog.sync_timeout
catalog.free_catalog_interval
catalog.free_catalog_url
catalog.free_catalog_sync
catalog.litellm_interval
catalog.litellm_url
catalog.litellm_sync
catalog.seed_free_providers
catalog.discovery.interval
catalog.discovery.timeout
catalog.discovery.concurrency
catalog.discovery.enabled
media.inline
```

That list is checked against `config.RestartOnly` by a test in
`internal/config/docs_test.go`, because it has drifted from the code twice. The
test locates the block by searching for the sentence above it rather than by
taking the first fence in the file, so that sentence's opening words — up to
`each is captured once` — are part of the fixture: reword them and the test
fails saying which sentence it wanted. It also fails if those words come to
appear more than once in this file, because then it cannot tell which fence is
the list. The rest of that sentence, and the order of the keys inside the
block, are free.

The listen addresses are **not** on it. They come from the environment, and a
variable cannot change under a running process, so there is no reload that
could warn about one — the settings screen says `environment` instead.

`policy.timeout.connect` and `first_byte` are on it because they configure a
shared HTTP transport built once. `server.max_body_bytes` deliberately is
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

Set this in **Connect → Public base URL → Save address**, or in Settings as
`server.public_url`. It is stored in the database and applies immediately.
For a Docker mapping of `18080:8080`, enter `http://your-host:18080`.
For a reverse proxy domain, enter `https://llm.example.com`. Include any
proxy path prefix, but omit dialect suffixes such as `/v1` and `/v1beta`.

The Connect page lists the configured address first and uses it for client
snippets by default. It also shows an estimated address built from the console
hostname and the internal proxy listen port. That estimate may be unreachable
when ports are remapped or a reverse proxy is used. Clearing the public URL
returns the page to using the estimate.

It is validated at load: a host is required, and a query or fragment is
refused. It is hot-reloadable, since nothing but the console reads it.

## Bootstrap variables

These are read from the environment before the database is open, so none is a
row in `settings` and none can be changed from the console. A change takes a
restart.

| Variable | Default | What it sets |
|---|---|---|
| `DARKROUTER_PROXY_LISTEN` | `:18080` | The proxy listen address. |
| `DARKROUTER_ADMIN_LISTEN` | `:18081` | The admin and console listen address. |
| `DARKROUTER_PROXY_TOKEN` | *empty* | Shared inbound secret. Never returned by any endpoint. |
| `DARKROUTER_MASTER_KEY` | — | Encrypts every stored credential. The process refuses to start without one. |
| `DARKROUTER_LOG_LEVEL` | `info` | The lowest level logged. |
| `DARKROUTER_LOG_FORMAT` | text | `json` selects structured output. |
| `DARKROUTER_DB` | `darkrouter.db` | The database path. `-db` overrides it. |

The settings screen shows the two listen addresses read-only, with an
`environment` source badge and a chip naming the variable that owns each. The
API reports them as not hot-reloadable — nothing captures them at construction,
but a variable cannot change under a running process, so calling them hot would
promise an edit that is impossible.

## Keys

Every key below is a row in `settings`, on its compiled default until
something writes it.

The converse does not hold: `settings` also carries rows that are not
configuration — the keyring's salt and verifier, the CSRF secret — which is
why the loader reads only the keys the registry names rather than the whole
table.

Providers and aliases are not in this table. Each has its own tables — providers
carry encrypted credentials, aliases are ordered chains — and `store.OverlayConfig`
merges the alias set onto every published snapshot. They are edited on the
Providers and Routing screens rather than in Settings.

| Key | Default | Notes |
|---|---|---|
| `server.public_url` | *empty* | The public domain clients reach the gateway at. A bare domain is assumed https. No query or fragment. Empty means the console shows only the LAN address. |
| `server.max_body_bytes` | 33554432 | Applies on reload. For a multipart upload it bounds the part values; boundaries, part headers and a 1 KiB charge per part get a further 64 KiB. |
| `server.shutdown_grace` | `10s` | Applies on reload. The shipped compose files stop the container with SIGKILL 30s after SIGTERM (`stop_grace_period`), and shutdown needs a few seconds past this grace to close streams and flush the request log, so a value above 25s raises a configuration warning. Raise `stop_grace_period` to at least this value plus 5s before going past it. |
| `server.sse.max_line_bytes` | 1048576 | |
| `server.sse.max_precommit_bytes` | 1048576 | |
| `policy.cooldown.trip_after` | 3 | |
| `policy.cooldown.max` | `15m` | |
| `policy.retry.max_attempts` | 4 | Between 1 and 10. The registry enforces it, so a save and a later load agree. |
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
| `catalog.seed_free_providers` | `true` | Restart-only: the seed runs once at startup. Adds a provider for every hosted preset that needs no credential. |
| `catalog.discovery.enabled` | `true` | Restart-only. |
| `catalog.discovery.interval` | `15m` | Restart-only. |
| `catalog.discovery.timeout` | `15s` | Restart-only. |
| `catalog.discovery.concurrency` | 8 | Restart-only. Global across the fleet, not per provider. |
| `media.inline` | `true` | Restart-only. |
| `playground.save_conversations` | `true` | |

There is **no `policy.concurrency` block.** Earlier documentation described
one; it never existed.

There is no example file to keep in step with this table: every key here is a
row in `settings`, edited on the Settings screen, and absent from the table in
the database until something writes it.
