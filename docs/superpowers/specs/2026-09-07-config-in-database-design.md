# Configuration in the database

**Date:** 2026-09-07
**Status:** design, awaiting approval

Remove `darkrouter.yaml`. The database becomes the single source of truth for
configuration, and the console is where it is changed. A small bootstrap set
stays in the environment, because a value needed to open the database or to
reach the console cannot live inside either.

## Decisions taken before this design

1. The listen addresses stay out of the database. A bad save must never make
   the console that would fix it unreachable.
2. File support is dropped outright. There is no one-time import; settings
   that lived only in the file revert to defaults on upgrade.
3. Every other restart-only field does live in the database and is editable,
   keeping the "takes effect on restart" badge the console already draws.

Decision 2 is the expensive one and §7 states its cost in full.

## 1. Where each setting lives

### Bootstrap: environment only

| Value | Source | Default |
|---|---|---|
| database path | `-db` flag, `DARKROUTER_DB` | `darkrouter.db` in the working directory |
| master key | `DARKROUTER_MASTER_KEY` | required |
| proxy listen | `DARKROUTER_PROXY_LISTEN` | `:18080` |
| admin listen | `DARKROUTER_ADMIN_LISTEN` | `:18081` |
| shared proxy token | `DARKROUTER_PROXY_TOKEN` | empty |
| admin password hash | `DARKROUTER_ADMIN_PASSWORD_HASH` | unset |
| log level, log format | `DARKROUTER_LOG_LEVEL`, `DARKROUTER_LOG_FORMAT` | info, text |

The last three already work this way. The database path default moves from
"beside the config file" to the working directory, which `WORKDIR /data`
(`Dockerfile:116`) already makes correct in the container. This also ends an
inconsistency: `rotate-key` already defaults `-db` to a bare `darkrouter.db`
(`cmd/darkrouter/main.go:246`), so the two subcommands currently disagree about
where the database is when `-config` is not passed.

`server.proxy_token` is in this table rather than the database because it is a
secret, because the process already reads its other secrets from the
environment, and because storing it encrypted would need both a new table and
a change to `RotateMasterKey`, which walks only `provider_keys`
(`internal/store/rotate.go:64`). Its only route into the process today is
`${VAR}` interpolation, which this design deletes; without an explicit
replacement, `cfg.Server.ProxyToken` becomes empty and
`internal/server/server.go:418-421` then admits every unauthenticated request.
That is the single most dangerous consequence of this change and this row is
what prevents it.

### Database: everything else

Thirty-two scalar keys, plus the alias and provider blocks that are already
stored.

- `server`: `public_url`, `max_body_bytes`, `shutdown_grace`,
  `sse.max_line_bytes`, `sse.max_precommit_bytes`
- `policy`: `cooldown.trip_after`, `cooldown.max`, `retry.max_attempts`,
  `timeout.connect`, `timeout.first_byte`, `timeout.total`, `timeout.idle`
- `log`: `retention`
- `capture`: `bodies`, `max_bytes`, `retention`
- `catalog`: `models_dev_url`, `sync_interval`, `sync_timeout`,
  `free_catalog_url`, `free_catalog_interval`, `free_catalog_sync`,
  `litellm_url`, `litellm_interval`, `litellm_sync`, `seed_free_providers`,
  `discovery.enabled`, `discovery.interval`, `discovery.timeout`,
  `discovery.concurrency`
- `media`: `inline`
- `playground`: `save_conversations`

Fifteen are hot-reloadable and seventeen take effect on restart. Nine of them
are not in the console's current `configFields` allowlist and become visible
for the first time: the six free-catalogue and LiteLLM keys,
`discovery.timeout`, `discovery.concurrency` and `seed_free_providers`.

`seed_free_providers` is consumed once at `cmd/darkrouter/main.go:147` and is
absent from `restartOnlyFields`. That is an existing gap — the console would
otherwise offer it as hot — and this design adds it to the table. The two
listen addresses leave `restartOnlyFields`, since an environment variable
cannot change under a running process.

## 2. Storage

The `settings` table already exists and already holds this kind of row:

```sql
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;
```

Policy overrides live in it today behind `policyFields`, a typed registry of
`{key, get, set}` over `*config.PolicyConfig`
(`internal/store/configstore.go:105`). This design generalises that registry
from one block to the whole `Config`: one table of `{key, get, set, validate}`
covering all thirty-two keys. No new schema, and no migration.

**Keys keep their dotted block form** — `catalog.sync_interval`, not
`config.catalog.sync_interval`. A `config.` prefix would sit on top of the
existing `config.imported_at` marker, and the block form is what `policy.*`
already uses.

**Reads filter by registry membership**, never `SELECT *`. The table is shared
with the keyring (`kdf_salt`, `kdf_iterations`, `kdf_verifier_ciphertext`,
`kdf_verifier_nonce`), the import markers (`providers_imported_at`,
`config.imported_at`, `seeded_providers`), the CSRF secret, and the admin
password rows. `PolicyOverrides` already filters this way
(`internal/store/configstore.go:213-217`) and the generalised read keeps that
rule.

**An absent row means the compiled default.** That absence is what lets the
console distinguish a value someone chose from one it fell back to, and it is
what makes reset-to-default a `DELETE` rather than a write of the current
default.

**Empty is not a value.** `value TEXT NOT NULL` will happily store `""`, which
would read back as "set". Writing an empty value is a `DELETE`.

### Tri-state writes

A write carries one of three intents per key: **set** to a value, **reset** to
the default, or **untouched**. This is not decoration. `policyFields.get`
reports "set" whenever a value is non-zero, and `applyDefaults` has already
made every field non-zero by the time it runs, so `ImportConfigOnce`
materialised all seven policy keys as explicit rows on every deployment that
has ever started. Without tri-state writes the set-versus-default indicator
reads *set* for all seven on every upgraded install, and `putPolicyTx`
re-materialises all seven on every save
(`internal/store/configstore.go:269-284`), so a reset could never take.

A startup reconciliation pass deletes any stored row whose value equals the
compiled default, so an upgraded database and a fresh one describe the same
state. It also drops the now-meaningless `config.imported_at` marker.

This makes startup **write** to the database, which it does not do today. That
is safe here — one process, one SQLite file — but it is a real change in
character and the pass must be a no-op on a database that was just created by
migration, where there is nothing to reconcile.

This deliberately loses one distinction: a value an operator set explicitly,
which happens to equal the default, is indistinguishable afterwards from one
never set. The cost is that such a key floats if a future release changes that
default, rather than staying pinned. That is the right trade here — the
alternative is carrying a "set to the default on purpose" flag through the
registry, the API and the console to serve a case nobody has asked for, and
without the pass every upgraded install shows seven policy keys as *set* that
the operator never chose.

The `*bool` fields need care: `applyDefaults` pins `Discovery.Enabled` and
`SaveConversations` to `&true` but leaves `FreeCatalogSync`, `LiteLLMSync`,
`SeedFreeProviders` and `Media.Inline` nil (`internal/config/load.go:146-162`).
`restartOnlyWarnings` compares through `optionalBool`, so writing
`media.inline: true` over a nil default emits a spurious "changed; takes effect
on restart" unless the registry normalises before comparing.

## 3. Read path

`config.Store` keeps its shape: an atomic snapshot, `Current()`, one snapshot
taken per request. Nothing on the request path changes.

`NewStore(path string, lookup func(string) (string, bool))` becomes
`NewStore(load func() (*Config, error))`. Injection rather than a direct
database call, for the same reason `SetOverlay` is injected: `internal/config`
may not import `internal/store`, because `store` already imports `config` and
the reverse edge closes a cycle. `main` injects the database loader; the
twenty-seven call sites across seventeen files inject a literal `Config`. The
e2e harness is the one that needs thought — it writes `proxy_listen: ":0"`
into a temporary file to get an ephemeral port, and now sets that through the
bootstrap environment instead.

Loading is: zero `Config` → `applyDefaults` → bootstrap environment → stored
rows through the registry → validate.

### Startup must tolerate a bad row

This is a correction to the file-era rule, not a refinement of it. Today
`NewStore` returns its load error and `main` exits (`internal/config/store.go:52-60`,
`cmd/darkrouter/main.go:102-105`). Rejecting a whole document was right when a
document was a file an operator could edit; it is wrong when the document is a
table inside a container that runs `read_only` with `cap_drop: ALL`
(`compose.prod.yml:68-72`). A crash loop there is fixed only with the sqlite
CLI.

So: **settings content never fails startup.** A row that fails to parse is
skipped with a warning appended to `cfg.Warnings` and the compiled default
used for that key. Structural failures — the database will not open,
migrations fail — still abort, as they should.

A **cross-key** rule needs a stated tie-break, because the failure names no
single culprit: `total >= connect + first_byte` involves three keys, and
dropping the wrong one produces a config the operator did not ask for either.
The rule is that **every key participating in a failed rule reverts to its
default together**, with one warning naming all of them. Deterministic,
explainable, and independent of the order the rows were written — a
"drop the most recently written" rule would make the outcome depend on history
the operator cannot see.

`config_valid` on `/healthz` becomes false whenever any key was skipped, with
the reasons in `warnings`. Reporting a config as valid when the process
quietly substituted three defaults would make the field useless, and this is
the only signal that a stored value is being ignored.

This matters more than it first appears because `/readyz` returns 503 on
`store.LastError()` (`internal/server/server.go:552-555`) and `/readyz` is the
Docker `HEALTHCHECK` (`Dockerfile:120-121`). A config error is not just a log
line; it takes the container out of rotation.

### Pending restart survives a save

`restartOnlyWarnings` diffs consecutive snapshots (`internal/config/store.go:113-124`),
so the next unrelated save clears the warning while the process is still
running the old value. The store keeps its boot snapshot and derives "pending
restart" from boot versus current, surfaced on `GET /api/config` and
`/healthz`.

### Deleted

The YAML loader, `interpolate` and `${VAR}` substitution, `documentKeys` and
`FileKeys`, and the fsnotify watcher with its debounce and ConfigMap
symlink-swap handling (`internal/config/store.go:126-184`). `fsnotify` leaves
`go.mod`. `Config.Providers`, `ProviderConfig`, and the provider loop in
`validate` become dead outside tests and go with them.

`ImportConfigOnce`, `ConfigImportedAt` and `ImportFromConfig` lose their
purpose; `OverlayConfig` becomes the loader rather than an overlay.

`POST /api/config/reload` stays, redefined as "re-read the database". It backs
the console's Reload button and three e2e tests
(`internal/e2e/phase8_test.go:381,431,481`); keeping it is cheaper than
removing it and it remains genuinely useful after a direct database edit.

## 4. Write path

`PUT /api/config` today accepts only aliases and policy and refuses
restart-only keys outright (`internal/admin/configapi.go:229-232`). It becomes
a single operation on the store:

1. Take `reloadMu`.
2. `BEGIN IMMEDIATE` on the write connection.
3. Build the base config **from the rows inside that transaction**, not from
   `Current()`.
4. Apply the patch.
5. Validate the merged whole with one validator.
6. Commit, then publish the new snapshot.

Step 3 is the correction. `mergedPolicy` bases its merge on `Current()`
(`internal/admin/configapi.go:267`), so two concurrent writes validate against
the same stale snapshot; with the cross-key rule
`total >= connect + first_byte` (`internal/config/load.go:282`) both can pass
and commit a state the process would refuse on the next load. That is a live
bug at seven keys today and this design would widen it to thirty-two.

**One validator.** There are two with different rules: `config.validate` and
`admin.validatePolicy`, the latter capping `retry.max_attempts` at ten
(`internal/admin/policyvalidate.go:13,24`, documented at
`docs/design/configuration.md:94`). The registry's per-key `validate` plus the
whole-config `validate` become authoritative; the admin-only cap moves into
the registry entry for that key. `aliasTargetsExist` stays admin-side because
it needs provider rows (`internal/admin/configapi.go:281`).

The same validator runs on both paths; only what it does on failure differs. A
**write** is refused whole, because a person is waiting and can be told what is
wrong. A **load** reverts the offending keys and carries on, because there is
nobody to tell and the alternative is a container that will not start. Those
are not two policies, they are one validator and two dispositions.

A restart-only key is accepted and the response names which written keys need
a restart. A bootstrap key is refused, naming the environment variable that
owns it.

`PUT /api/policy` (`internal/admin/admin.go:265-266`) becomes a view over the
registry: its seven keys are registry keys like any other.

**Aliases do not join the registry.** They are a `map[string][]string` in
their own table, not a scalar, and forcing them through a `{key, get, set}`
shape would buy nothing. `PUT /api/aliases` keeps `PutAliases` and its own
validation, but moves inside the same critical section and transaction, so
"one place where a config write is committed" holds even though there are two
kinds of payload. Providers are likewise out of scope: they have their own
tables and encryption and are already database-owned.

`databaseOwned` (`internal/admin/configapi.go:25`) loses its meaning once every
block is database-owned, and goes. `sourceOf` returns `env` for the bootstrap
keys, `database` for a key with a stored row, and `default` for one without.

## 5. Console

The settings screen is a rewrite, not an extension. It splits its rows today
by a five-entry `EDITABLE` list (`web/src/features/settings/settings-catalog.ts:381`)
and renders everything else through `readOnlyGroups`
(`web/src/features/settings/settings-screen.tsx:42-50`), and
`PolicySettings` writes through `/api/policy` with a bespoke `policyWrite`
shape.

- Field editors typed from the registry: duration, bytes, integer, boolean,
  string, URL. Server-side validation surfaced inline.
- A set-versus-default indicator per row, and reset-to-default.
- Bootstrap fields render read-only with an *env* chip naming the variable.
- Restart-only fields keep their badge and show "takes effect on restart"
  after a save, driven by the boot-versus-current comparison rather than by a
  diff that the next save erases.
- `ConfigSource` changes from `"file" | "database" | "default"`
  (`web/src/lib/api-types.ts:492`) to `"env" | "database" | "default"`.
- Copy that names the file goes: `SOURCE_NOTE` and `SOURCE_LABEL`
  (`web/src/features/settings/settings-catalog.ts:345-357`), the header text,
  and the server-group blurb "Every one of these needs a restart"
  (`settings-catalog.ts:55`), which is already wrong for `public_url`,
  `shutdown_grace` and `max_body_bytes`.

Typography follows the repo rule: `text-sm` floor, no custom sizes.

## 6. Testing

Behavioural, and each proven able to fail by breaking the code and watching it
go red.

**Store and registry.** Every key round-trips through `get`/`set`. An unknown
stored key is ignored. An empty write deletes the row. A reset deletes rather
than writing the default. The reconciliation pass deletes rows equal to the
default, and is a no-op on a freshly migrated database.

**Read path.** Defaults, environment and stored rows compose in that
precedence. A row that will not parse is skipped with a warning and the
default used — **startup still succeeds**. A stored pair that breaks a
cross-key rule reverts every key in that rule together, and `config_valid`
goes false with all of them named. `/readyz` stays 200 through both — a
skipped key is a warning, not an unready process. A `-config` flag is accepted
and ignored with a warning.

**Write path.** A rejected write commits nothing. Two concurrent writes that
would together break `total >= connect + first_byte` cannot both commit. A
bootstrap key is refused and names its variable. The response names
restart-only keys. `proxy_token` is never echoed by any endpoint.

**Pending restart.** A restart-only save, then an unrelated save, and the
pending-restart flag is still set.

**Authentication.** With `DARKROUTER_PROXY_TOKEN` set and no per-client
tokens, an unauthenticated request is still refused. This is the regression
test for §1's hazard.

**Console.** Each editor type renders and submits. Bootstrap fields are not
editable. The restart notice appears. Nothing renders the word "file" as a
source.

## 7. Breaking changes

**Settings revert to defaults.** Anything that lived only in the file — the
`server`, `log`, `capture`, `catalog`, `media` and `playground` blocks —
returns to its compiled default on upgrade and is re-entered in the console.
Aliases, policy and providers are unaffected; they are already in SQLite.

**`DARKROUTER_PROXY_TOKEN` keeps working**, and must, or upgrading turns
authentication off. It is the one file-era value that survives, because it
moves to the environment rather than to the database.

**The entrypoint changes in the same commit.** The published image runs
`ENTRYPOINT ["darkrouter", "-config", "/data/darkrouter.yaml"]`
(`Dockerfile:122`) and `flag.ExitOnError` (`cmd/darkrouter/main.go:92`) means
an unknown flag exits non-zero. Removing `-config` from the flag set without
removing it from the entrypoint means the image cannot start at all. The
Dockerfile, `compose.yml`, `compose.prod.yml` and `.env.example` move with the
code, not in a later documentation phase.

That fixes the image we ship, but not an operator who overrode `command:` or
built their own entrypoint — for them the flag is still passed and the
container still refuses to start, with a flag-parse error that explains
nothing. So **`-config` is kept for one release as an accepted no-op** that
logs "ignored; configuration now lives in the database". Three lines, and it
turns a hard failure into a warning for the one group that cannot be reached
by changing our own Dockerfile.

**Downgrading.** A pre-change binary started against a migrated database finds
no configuration file, which is not fatal — the file has been optional since
`6f068a47` — so it starts on compiled defaults plus the `policy.*` and alias
rows it already understands. Keys written by the new build sit unread in
`settings`. The practical effect is that a rollback loses the same settings
the upgrade did, and does not corrupt anything.

**A leftover file is called out.** If `darkrouter.yaml` is still present beside
the database, startup logs one warning naming it as no longer read. Five
lines, and it turns a confusing morning into an obvious one.

**This machine's UAT instance** carries `public_url: http://192.168.0.250:8090`
and `shutdown_grace: 30s` in `data/darkrouter.yaml`. Both revert on redeploy:
the Connect page falls back to the guessed `:18080`, and `public_url` has to be
set again in the console. Its `proxy_token: ${DARKROUTER_PROXY_TOKEN}` line
stops being read, but `.env` already sets that variable, so authentication
survives.

**Docs to rewrite:** `docs/design/configuration.md` (precedence, the keys
table, the hot/restart contract), `docs/operations/deploy.md:8-12,22-24,63-65,159-166`, and
`darkrouter.example.yaml` is deleted. `README.md:30` already says "There is no
configuration file to write" — true since the file became optional — so it
needs a touch, not a rewrite. `presetgen` has no
dependency on `internal/config` and needs nothing.

## 8. Sequence

**Phase 1 — flag day.** Registry, database read path, bootstrap from the
environment, deletion of the loader and watcher, Dockerfile and compose. These
cannot be split: any intermediate state has the file and the database both
authoritative for the same key, which is the one state nobody should ship.

**Phase 2 — write path.** The single-critical-section update, one validator,
`/api/policy` and `/api/aliases` folded onto it. Shippable alone: the console
stays read-only and simply reports `database` everywhere.

**Phase 3 — console.** Editors, source chips, reset-to-default, restart
notices.

**Phase 4 — docs and deploy.** The rewrites listed in §7 that are not already
carried by phase 1.

## Open question

Whether to retire `server.proxy_token` altogether, now that per-client proxy
tokens exist and are the better mechanism. This design keeps it, because
retiring it is a behaviour removal and keeping it in the environment costs
nothing. If it were retired, the release would have to refuse to start while
`DARKROUTER_PROXY_TOKEN` is set, rather than ignoring it silently.
