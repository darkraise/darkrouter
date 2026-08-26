# Aliases, Policy and the Config API — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Move aliases and policy out of `darkrouter.yaml` and into SQLite so the console can edit them, and make `GET`/`PUT /api/config` tell the truth about where every value came from and whether changing it does anything.

**Architecture:** No reader changes. `router`, `exec`, `server` and `admin` all reach aliases and policy through `config.Store.Current()`, and they keep doing so — the values are simply overlaid from SQLite before each snapshot is published. `internal/store` already imports `internal/config`, so the overlay lives there; the reverse edge would close a cycle and the package would not build.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` §8.1, §8.2 (the `/api/config` pair), §13 step 3(b).

**Prior plans:** `2026-08-25-phase11a-*` and `2026-08-25-phase11c-*` are slice 3(a). This is the slice no plan was ever written for — see `docs/PROGRESS.md`.

## Global Constraints

- TDD: a failing test precedes the implementation.
- Race-clean: `go test -race ./...` passes, and `go vet ./...` is clean before any commit.
- `export PATH=/usr/local/go/bin:$PATH` before any Go command.
- **Migrations are append-only.** Next free number is `0008`. Tables are `STRICT`. Never edit a shipped migration.
- **`internal/config` may not import `internal/store`.** `store` imports `config` today; the reverse closes a cycle. The overlay therefore lives in `store` and is injected into `config.Store` as a function.
- Comments explain WHY, never WHAT. No comment may reference this plan or task.
- Commit subjects: `<type>(<scope>): <subject>`, imperative, **50 characters or fewer**, no trailing period.
- English only. Stage explicit paths; never `git add -A`.

## The decision this plan implements

**Aliases and policy live in SQLite, with the YAML block imported once on first run.** This is what `providers:` already does, and having two config concerns follow two different rules would be worse than either rule alone. After the first run, editing `aliases:` or `policy:` in the file has no effect — exactly as editing `providers:` already has none.

That consequence is stated rather than discovered: the import logs what it took, `darkrouter.example.yaml` carries a comment block recording it, and `GET /api/config` marks each value's source so the console can say so at the point of display.

---

### Task 1: Name the two restart-only workers

**Files:**
- Modify: `internal/config/config.go` — `RestartOnly`
- Modify: `internal/config/store.go` — `restartOnlyWarnings`
- Test: `internal/config/store_test.go`

The catalog sync worker and the discovery sweeper each capture their interval into an options struct at construction (`internal/catalog/sync.go:96`, `internal/catalog/discovery.go:151`), so they are restart-only in behaviour. Neither is listed in `RestartOnly` (`internal/config/config.go:99`), so a reload that changes one is accepted, warns about nothing, and takes effect at the next process start. That is the exact failure `RestartOnly` exists to prevent, and the phase 10 mockups already recorded it.

This lands first because it is independent of everything else here and `PUT /api/config` needs it to be right.

- [ ] **Step 1: Write the failing test**

Assert `RestartOnly` contains `catalog.sync_interval` and `catalog.discovery.interval`, and that `restartOnlyWarnings` emits a warning when a reload changes either. Cover both, not one: they are separate captures in separate files.

- [ ] **Step 2: Run it, watch it fail**

- [ ] **Step 3: Add the two entries and their warnings**

Keep the comment above `RestartOnly` accurate — it explains why `max_body_bytes` is deliberately absent, and the same reasoning is what puts these two in.

- [ ] **Step 4: Run everything, commit**

Subject: `fix(config): name the restart-only worker intervals`

---

### Task 2: Give aliases a table

**Files:**
- Add: `internal/store/migrations/0008_aliases.sql`
- Test: `internal/store/migration_test.go` (or alongside the existing migration tests)

Aliases are ordered — the chain order *is* the fallback order — so the table carries an explicit sequence rather than relying on insertion order.

```sql
CREATE TABLE aliases (
  name   TEXT    NOT NULL,
  seq    INTEGER NOT NULL,
  target TEXT    NOT NULL,
  PRIMARY KEY (name, seq)
) STRICT;
```

Policy needs no table: `settings(key, value)` already exists from `0001_init.sql` and already has read and write helpers in `keyring.go`. Policy values go in under `policy.*` keys, and the first-run marker under `config.imported`.

- [ ] **Step 1: Write the failing test**

Assert the migration applies to a fresh database and to one already at `0007`, and that an alias round-trips with its order intact. The order assertion is the point: a test that only counts rows would pass on a table that shuffles a chain.

- [ ] **Step 2: Run it, watch it fail**

- [ ] **Step 3: Write the migration**

- [ ] **Step 4: Run everything, commit**

Subject: `feat(store): give aliases a table`

---

### Task 3: Read and write aliases and policy

**Files:**
- Add: `internal/store/configstore.go`
- Test: `internal/store/configstore_test.go`

- [ ] **Step 1: Write the failing tests**

Four behaviours:

- `Aliases(ctx)` returns `map[string][]string` with each chain in `seq` order.
- `PutAliases(ctx, map[string][]string)` replaces the whole set in one transaction. Replace rather than merge: a chain the operator deleted must actually disappear, and a partial write would leave a chain half-rewritten.
- `Policy(ctx)` returns only the keys that are set, so a caller can tell "not overridden" from "set to zero".
- `PutPolicy(ctx, ...)` writes through `settings`.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Implement**

Policy is a handful of durations and ints. Store them under dotted keys in `settings` — `policy.retry.max_attempts`, `policy.timeout.connect` and so on — rather than adding a column per field. A future policy field then needs no migration, and "which keys are overridden" is exactly what §8.2 asks `GET /api/config` to report.

Durations serialize as the string `time.Duration` parses, not as a raw nanosecond count: the value is read by a human in the settings screen and written back by one.

- [ ] **Step 4: Run everything, commit**

Subject: `feat(store): read and write aliases and policy`

---

### Task 4: Import once, then let the database win

**Files:**
- Add to: `internal/store/configstore.go` — `ImportConfigOnce`, `OverlayConfig`
- Modify: `internal/config/store.go` — an overlay hook
- Modify: `cmd/darkrouter/main.go` — the wiring
- Test: `internal/store/configstore_test.go`, `internal/config/store_test.go`

- [ ] **Step 1: Write the failing tests**

- `ImportConfigOnce` on a fresh database copies the YAML's aliases and policy in, and reports what it took so the caller can log it.
- Run a second time with different YAML, it changes nothing and reports that it imported nothing.
- `OverlayConfig` replaces a loaded `Config`'s aliases and policy with the database's, leaving every other block alone.
- A policy key that is *not* set in the database leaves the loaded value in place, so a half-populated table cannot zero a timeout.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Add the overlay hook to `config.Store`**

`config` cannot import `store`, so the hook is a function the wiring injects:

```go
// SetOverlay installs a transform applied to every freshly-loaded Config
// before it is published. It is how aliases and policy reach a snapshot from
// SQLite without this package importing the store, which would close an
// import cycle.
func (s *Store) SetOverlay(fn func(*Config) error)
```

It must run on the initial load, on `Reload`, and on every watch-triggered reload — a reload that dropped the overlay would silently restore the file's aliases until the next restart. Test that path explicitly.

- [ ] **Step 4: Wire it in `main.go`**

Order matters: `config.NewStore` runs before `store.Open` today. Open the database, run `ImportConfigOnce`, install the overlay, then `Reload` so the first published snapshot already carries the database's values. Log what the import took, per §8.1.

- [ ] **Step 5: Record the consequence in the example config**

`darkrouter.example.yaml` gets a comment block above `aliases:` and `policy:` saying they are imported once and edited thereafter through the admin API, exactly as `providers:` already is. An operator editing the file and seeing nothing happen is the failure this prevents.

- [ ] **Step 6: Run everything, commit**

Subject: `feat(config): source aliases and policy from sqlite`

---

### Task 5: Return every block, and say where it came from

**Files:**
- Modify: `internal/admin/configapi.go`
- Test: `internal/admin/configapi_test.go`

`handleConfig` returns `aliases`, `policy` and `server` and stops. `log`, `capture` and `catalog`/`discovery` all appear on the settings screen and have no data source at all.

- [ ] **Step 1: Write the failing tests**

- Every block is present: `server`, `log`, `capture`, `catalog` (with `discovery` nested), `aliases`, `policy`.
- Each value carries its source — `file`, `database` or `default` — and whether it is hot-reloadable.
- A `RestartOnly` field reports `hot_reloadable: false`, and the two new entries from Task 1 are among them.
- No credential material appears anywhere in the response, per phase 7 §4.1. `server.proxy_token` is a secret and must not be echoed; assert that explicitly.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Implement**

Source is derivable, not guessed: aliases and policy are `database` once imported; everything else is `file` if the YAML set it and `default` if `Load` filled it in. If distinguishing file from default needs `Load` to record which keys the document actually carried, add that — a source marker that says `file` for a value nobody wrote is worse than no marker.

- [ ] **Step 4: Run everything, commit**

Subject: `feat(admin): return every config block with its source`

---

### Task 6: Accept only what a running worker rereads

**Files:**
- Modify: `internal/admin/configapi.go` — a `PUT` handler
- Modify: `internal/admin/admin.go` — the route
- Test: `internal/admin/configapi_test.go`

- [ ] **Step 1: Write the failing tests**

- `PUT /api/config` with aliases writes them and the next `Current()` snapshot carries them.
- A chain naming a provider that does not exist is rejected with a useful message rather than stored. Validation is the same one `Load` applies; call it, do not restate it.
- A write to a `RestartOnly` field is **refused**, not accepted-and-warned. The endpoint's contract differs from a file reload's on purpose: a reload is an operator editing a file the process is watching, while this is an API accepting a request it can honour or cannot.
- Session, CSRF and origin checks apply, per phase 7 §3. Assert an unauthenticated write is rejected.
- Nothing in the response echoes a credential.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run everything, commit**

Subject: `feat(admin): accept config writes that take effect`

---

## Notes for the executor

**The reader surface does not change.** If a task finds itself editing `router`, `exec` or `server` to reach aliases or policy differently, stop: the overlay exists so those files stay as they are, and a change there means the overlay is in the wrong place.

**Aliases are validated, not trusted.** `internal/config/load.go:177` already walks `c.Aliases` and validates each chain. Both the import and the `PUT` path must run the same validation, or the database becomes a way to store a configuration the file would have rejected.

**"Imported once" needs a marker, not an emptiness check.** An operator who deletes every alias through the console must not have the file's aliases silently reimported on the next restart. `settings['config.imported']` is what separates "never imported" from "imported, then emptied".
