# Configuration in the Database — Phase 2 (write path) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Make the console able to write every stored setting, through one transactional critical section and one validator, so a save is judged against the rows it is about to replace rather than against a snapshot another save may already have invalidated.

**Architecture:** `config.Patch` carries a save's three intents per key — set, reset, untouched — and reaches `internal/store` through a writer function injected onto `config.Store`, the same way the alias overlay already is. `config.Store.Update` holds `reloadMu` across the commit and the republish; `store.WriteConfig` opens one immediate transaction, builds the base configuration from the rows inside it, applies the patch strictly, validates the whole, and commits. The registry gains a per-key validator so the bound a save enforces is the bound a later load enforces. `PUT /api/policy` and `PUT /api/aliases` become shapes over that one path.

**Tech Stack:** Go 1.x, SQLite via `internal/store` (modernc.org/sqlite), `net/http`.

**Spec:** `docs/superpowers/specs/2026-09-07-config-in-database-design.md` — §4 is this phase; §6 lists the tests it owes.

**Phase 1:** `docs/superpowers/plans/2026-09-07-config-in-database-phase-1.md`, merged at `c1a7b587`.

## Global Constraints

- Keys keep their dotted block form: `catalog.sync_interval`, never `config.catalog.sync_interval`.
- Reads of the `settings` table filter by registry membership. Never `SELECT *`.
- An absent row means the compiled default. Writing an empty value is a `DELETE`.
- Settings content never fails startup. Structural failures (database will not open, migration fails) still abort.
- One validator. `config.Validate` plus the registry's per-key validators are authoritative on both the load path and the write path; the two differ only in disposition — a load reverts, a write is refused whole.
- A cross-key rule failure reverts every key in that rule together; a single-key failure reverts only the key its message names. That is the load path's rule and this phase does not change it.
- `DARKROUTER_PROXY_TOKEN` must keep authenticating. `internal/server/server.go:418-421` admits every unauthenticated request when the shared token is empty and no per-client tokens exist.
- No endpoint returns credential material. `server.proxy_token` is never echoed and never writable.
- Every test must be proven able to fail: break the implementation, watch it go red, restore it.
- English only, in code, comments, commits and tests.
- Commit style: `<type>(<scope>): <subject>`, subject ≤50 chars, imperative, no period. Sign off with the two trailer lines the session requires.
- Typography rule for any console change: `text-sm` floor, no custom sizes. This phase changes no rendered console text.

## What this phase deliberately does not do

- **The console is untouched apart from four stale comments.** Spec §5 is phase 3. Nothing here changes a rendered string, an editor, or a source chip, so the settings screen behaves exactly as it does today.
- **`sourceOf` keeps returning `"environment"`, and `databaseOwned` stays.** Spec §4 retires both, but the strings are read by `web/src/lib/api-types.ts` and the badge in `settings-screen.tsx`. Changing them without the console that renders them is a regression, so they move in phase 3 with their reader.
- **Providers stay out.** They have their own tables and encryption and are already database-owned.

## File structure

| File | Responsibility | Task |
|---|---|---|
| `internal/store/configreg.go` | The 32-key registry; gains a per-key validator | 1 |
| `internal/store/configload.go` | Row reads and the reverting builder both paths share | 2 |
| `internal/store/db.go` | Handle DSNs; the write handles begin immediately | 3 |
| `internal/config/patch.go` | `Patch`, `RejectedError`, `PublishError`, bootstrap variable names | 4 |
| `internal/config/store.go` | `SetWriter`, `Update`, the reload lock | 4 |
| `internal/store/configwrite.go` | `WriteConfig`: one transaction, one validation, one commit | 5 |
| `internal/admin/configapi.go` | `PUT /api/config` and the one `commitConfig` helper | 6 |
| `internal/admin/aliasapi.go` | `PUT /api/policy` and `PUT /api/aliases` as shapes over it | 7 |
| `internal/provider/providertest/source.go` | A fixed provider source for tests | 10 |

---

### Task 1: A per-key validator in the registry

**Files:**
- Modify: `internal/store/configreg.go`
- Test: `internal/store/configreg_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `configField.validate func(*config.Config) error`, run by `ApplyConfigRows` after a successful `set` and by the write path in Task 5. The `policy.retry.max_attempts` entry carries the only one this phase adds.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: the registry's closure-per-type shape is already established in this file, and the validator is one more field on it

There are two validators today with different rules. `admin.validatePolicy` caps `policy.retry.max_attempts` at ten (`internal/admin/policyvalidate.go:13,24`, documented at `docs/design/configuration.md:94`); `config.validate` only requires it to be at least one. So a save of 20 is refused while a row of 20 written by hand loads without complaint. Spec §4 moves the cap into the registry entry for that key, which makes the bound the same on both paths.

`ApplyConfigRows` must validate on a copy. `f.set` has already landed the value by the time a validator can see it, and putting the old one back through `f.set` would turn a nil `*bool` into a pointer to false. A shallow copy of the `Config` is enough: every setter writes a scalar or a freshly allocated pointer, so nothing it touches is shared with the original.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/configreg_test.go`. Check its existing imports and add `strings` if it is not already there.

```go
// The bound used to live in the admin handler, where a save was refused and a
// row written by hand was not. One validator means the loader refuses it too.
func TestApplyConfigRowsRevertsAnOutOfRangeRetryCount(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	warnings := ApplyConfigRows(c, map[string]string{"policy.retry.max_attempts": "20"})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	// The prefix LoadConfig matches on to find the key it must revert.
	if !strings.HasPrefix(warnings[0], "stored policy.retry.max_attempts ") {
		t.Errorf("warning = %q, want it to open with the key", warnings[0])
	}
	if c.Policy.Retry.MaxAttempts != 4 {
		t.Errorf("MaxAttempts = %d, want the compiled default", c.Policy.Retry.MaxAttempts)
	}
}

func TestApplyConfigRowsAcceptsTheRetryCountAtTheCap(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	if w := ApplyConfigRows(c, map[string]string{"policy.retry.max_attempts": "10"}); len(w) != 0 {
		t.Fatalf("warnings = %v, want none", w)
	}
	if c.Policy.Retry.MaxAttempts != 10 {
		t.Errorf("MaxAttempts = %d, want 10", c.Policy.Retry.MaxAttempts)
	}
}

// A rejected value must leave nothing behind. The setter runs before the
// validator can see the result, so the pass has to work on a copy rather than
// undo itself afterwards.
func TestApplyConfigRowsLeavesAnUnrelatedKeyAlone(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	warnings := ApplyConfigRows(c, map[string]string{
		"policy.retry.max_attempts": "20",
		"log.retention":             "96h",
	})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if c.Log.Retention != 96*time.Hour {
		t.Errorf("log.retention = %s, want 96h", c.Log.Retention)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/store/ -run 'TestApplyConfigRows(RevertsAnOutOfRangeRetryCount|AcceptsTheRetryCountAtTheCap|LeavesAnUnrelatedKeyAlone)' -v
```

Expected: `TestApplyConfigRowsRevertsAnOutOfRangeRetryCount` FAILs with `warnings = [], want exactly one`, and `TestApplyConfigRowsLeavesAnUnrelatedKeyAlone` FAILs the same way. `TestApplyConfigRowsAcceptsTheRetryCountAtTheCap` passes already, which is correct — it is the boundary that must keep working.

- [ ] **Step 3: Add the validator field and the cap**

In `internal/store/configreg.go`, extend the struct:

```go
type configField struct {
	key string
	get func(*config.Config) string
	set func(*config.Config, string) error
	// validate is the bound this key carries on its own, checked against the
	// Config the setter has already written to. Nil for a key whose only rules
	// are in config.Validate, which is most of them.
	//
	// It runs on both paths, which is the point: a bound the save enforced and
	// the loader did not would let a row into the database that every later
	// start silently accepted.
	validate func(*config.Config) error
}
```

Below the `domain` helper inside `buildConfigRegistry`, add:

```go
	withValidate := func(f configField, v func(*config.Config) error) configField {
		f.validate = v
		return f
	}
```

Replace the `policy.retry.max_attempts` entry with:

```go
		withValidate(
			integer("policy.retry.max_attempts", func(c *config.Config) *int { return &c.Policy.Retry.MaxAttempts }),
			func(c *config.Config) error {
				if n := c.Policy.Retry.MaxAttempts; n < 1 || n > maxRetryAttempts {
					return fmt.Errorf("policy.retry.max_attempts must be between 1 and %d", maxRetryAttempts)
				}
				return nil
			}),
```

Above `var configRegistry = buildConfigRegistry()`, add:

```go
// maxRetryAttempts bounds policy.retry.max_attempts. Past ten, a failing
// request walks the whole candidate list several times over and a client waits
// minutes for an error it could have had in seconds.
const maxRetryAttempts = 10
```

- [ ] **Step 4: Run the per-key validator in ApplyConfigRows**

Replace the loop body in `ApplyConfigRows`:

```go
func ApplyConfigRows(c *config.Config, rows map[string]string) []string {
	var warnings []string
	for _, f := range configRegistry {
		v, ok := rows[f.key]
		if !ok {
			continue
		}
		// Judged on a copy. The setter has already landed the value by the
		// time a validator can look at it, and putting the old one back
		// through the setter would turn a nil *bool into a pointer to false.
		// A shallow copy is enough: every setter writes a scalar or a freshly
		// allocated pointer, so it shares nothing with the original.
		scratch := *c
		if err := f.set(&scratch, v); err != nil {
			warnings = append(warnings,
				fmt.Sprintf("stored %s is unusable (%v); using the default", f.key, err))
			continue
		}
		if f.validate != nil {
			if err := f.validate(&scratch); err != nil {
				warnings = append(warnings,
					fmt.Sprintf("stored %s is unusable (%v); using the default", f.key, err))
				continue
			}
		}
		*c = scratch
	}
	return warnings
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/store/ ./internal/config/ ./internal/admin/
```

Expected: PASS. `internal/admin` still has its own `validatePolicy` with the same cap; Task 7 removes it.

- [ ] **Step 6: Prove the tests can fail**

Change `maxRetryAttempts` to `100`, run `go test ./internal/store/ -run TestApplyConfigRowsRevertsAnOutOfRangeRetryCount`, confirm it goes red, restore `10`.

- [ ] **Step 7: Commit**

```bash
git add internal/store/configreg.go internal/store/configreg_test.go
git commit -m "$(cat <<'EOF'
feat(config-registry): validate each key on the way in

The retry cap lived only in the admin handler, so a save of 20 was
refused while a row of 20 written by hand loaded without complaint.
The registry is the one validator both paths run.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 2: Extract the configuration builder from LoadConfig

**Files:**
- Modify: `internal/store/configload.go`
- Test: `internal/store/configload_test.go`

**Interfaces:**
- Consumes: nothing from Task 1 beyond the package it shares.
- Produces: `buildConfig(rows map[string]string, boot config.Bootstrap, aliases map[string][]string) (*config.Config, []string, []string, error)` — the assembled config, its warnings, the keys it reverted, and an error only when the compiled defaults themselves do not validate. Also `configRows(ctx context.Context, q rowsQueryer) (map[string]string, error)`, which now takes the queryer rather than the `*DB`, so the write path can read inside its own transaction.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: a straight extraction of an existing body, with the seam the spec already names

The write path has to build its base from the rows inside its transaction (spec §4, step 3). That assembly — defaults, bootstrap, rows, the revert loop, validation — is `LoadConfig`'s body today and is not reachable without a database. This task moves it behind a function that takes rows, and changes nothing about what it does.

The one addition is `aliases`. `LoadConfig` passes nil, exactly as today: the alias table reaches a snapshot through `OverlayConfig`, after loading. The write path passes the effective alias set so `ValidateAliases` runs as part of the whole.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/configload_test.go`:

```go
// The write path builds its base from the rows inside its own transaction, so
// the assembly has to be reachable without a database. This is that seam.
func TestBuildConfigRevertsAKeyItCannotParse(t *testing.T) {
	c, warnings, skipped, err := buildConfig(
		map[string]string{"log.retention": "not-a-duration", "capture.max_bytes": "4096"},
		config.Bootstrap{}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "log.retention" {
		t.Fatalf("skipped = %v, want just log.retention", skipped)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want one", warnings)
	}
	if c.Log.Retention != 720*time.Hour {
		t.Errorf("log.retention = %s, want the compiled default", c.Log.Retention)
	}
	// The unrelated row still applies. A bad key reverts itself, not the save.
	if c.Capture.MaxBytes != 4096 {
		t.Errorf("capture.max_bytes = %d, want 4096", c.Capture.MaxBytes)
	}
}

func TestBuildConfigTakesItsListenAddressesFromTheBootstrap(t *testing.T) {
	c, _, _, err := buildConfig(nil, config.Bootstrap{
		ProxyListen: "127.0.0.1:1", AdminListen: "127.0.0.1:2", ProxyToken: "sekrit",
	}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if c.Server.ProxyListen != "127.0.0.1:1" || c.Server.AdminListen != "127.0.0.1:2" {
		t.Errorf("listen = %q/%q, want the bootstrap's", c.Server.ProxyListen, c.Server.AdminListen)
	}
	if c.Server.ProxyToken != "sekrit" {
		t.Errorf("ProxyToken = %q, want the bootstrap's", c.Server.ProxyToken)
	}
}

// Aliases are validated with everything else when they are supplied. Nothing
// can revert them, so a broken set exhausts the loop and comes back as the
// error the caller must not paper over -- which is why the write path checks
// them before it gets here.
func TestBuildConfigFailsOnAnAliasSetNoKeyCanFix(t *testing.T) {
	_, _, _, err := buildConfig(nil, config.Bootstrap{},
		map[string][]string{"fast": {}})
	if err == nil {
		t.Fatal("buildConfig accepted an alias chain with no targets")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/store/ -run TestBuildConfig -v
```

Expected: compile failure, `undefined: buildConfig`.

- [ ] **Step 3: Extract the builder**

Rewrite the top of `internal/store/configload.go`. `LoadConfig` keeps its doc comment; everything after the row read moves into `buildConfig`.

```go
func LoadConfig(ctx context.Context, d *DB, boot config.Bootstrap) (*config.Config, error) {
	rows, err := configRows(ctx, d.Read)
	if err != nil {
		return nil, err
	}
	// nil aliases: the alias table reaches a snapshot through OverlayConfig,
	// after this runs. The write path is the caller that has them in hand.
	c, warnings, skipped, err := buildConfig(rows, boot, nil)
	if err != nil {
		return nil, err
	}
	c.Warnings = append(c.Warnings, warnings...)
	c.Skipped = append(c.Skipped, skipped...)
	return c, nil
}

// buildConfig assembles a Config from stored rows and reports what it could
// not use: the warnings to surface and the keys it reverted to their compiled
// defaults.
//
// Separated from LoadConfig because the write path needs the same assembly
// against the rows inside its own transaction. A base built from the running
// snapshot is what lets two concurrent saves each pass the cross-key rule and
// together commit a state the next load refuses.
//
// The only error it returns is the compiled defaults failing validation, which
// is a bug in the defaults rather than in anything an operator stored.
func buildConfig(rows map[string]string, boot config.Bootstrap,
	aliases map[string][]string) (*config.Config, []string, []string, error) {

	build := func(skip map[string]bool) (*config.Config, []string) {
		c := &config.Config{}
		config.ApplyDefaults(c)
		c.Server.ProxyListen = boot.ProxyListen
		c.Server.AdminListen = boot.AdminListen
		c.Server.ProxyToken = boot.ProxyToken
		c.Aliases = aliases
		use := make(map[string]string, len(rows))
		for k, v := range rows {
			if !skip[k] {
				use[k] = v
			}
		}
		return c, ApplyConfigRows(c, use)
	}

	skip := map[string]bool{}
	c, warnings := build(skip)
	// skipped names every key actually reverted, as distinct from warnings:
	// a restart-pending notice lands in warnings too, and must not make the
	// configuration report invalid the way a reverted key does.
	var skipped []string

	// Parse failures first: the pass above reported every key that would not
	// parse, and this one rebuilds without them. Rule failures are handled
	// below, where a key can only be identified from the message.
	if len(warnings) > 0 {
		for _, w := range warnings {
			for _, k := range ConfigKeys() {
				// A prefix, in the exact shape ApplyConfigRows builds. Under a
				// substring match a stored value spelling another key's name
				// reverted that key too, because strconv and ParseDuration
				// quote the offending value into the message.
				if strings.HasPrefix(w, "stored "+k+" ") && !skip[k] {
					skip[k] = true
					skipped = append(skipped, k)
				}
			}
		}
		c, _ = build(skip)
	}

	// The bound is one iteration per key plus one. Each iteration retires at
	// least one key -- a rule's whole set, or the single key its message names
	// -- and the last iteration validates the compiled defaults, which must
	// pass. A fixed two would have been enough only while a single-key failure
	// reverted everything at once.
	for attempt := 0; attempt <= len(configRegistry); attempt++ {
		err := config.Validate(c)
		if err == nil {
			return c, warnings, skipped, nil
		}

		var re config.RuleError
		if errors.As(err, &re) {
			if added := addAny(skip, re.Keys); len(added) > 0 {
				warnings = append(warnings,
					fmt.Sprintf("stored %v broke the %s rule; all of them reverted to their defaults", re.Keys, re.Rule))
				skipped = append(skipped, added...)
				c, _ = build(skip)
				continue
			}
		}
		// A single-key rule. Every message validate produces for one names the
		// setting it is about, so the key to revert can be read out of it
		// rather than guessed.
		if k, ok := keyNamedIn(err.Error(), skip); ok {
			skip[k] = true
			skipped = append(skipped, k)
			warnings = append(warnings,
				fmt.Sprintf("stored %s is unusable (%v); using the default", k, err))
			c, _ = build(skip)
			continue
		}
		// Nothing identifiable, or a rule whose keys are all reverted already.
		// Everything goes rather than the process refusing to start.
		if added := addAny(skip, ConfigKeys()); len(added) > 0 {
			skipped = append(skipped, added...)
		}
		c, _ = build(skip)
		warnings = append(warnings,
			fmt.Sprintf("stored configuration is unusable (%v); every key reverted to its default", err))
	}

	// Reaching here means the compiled defaults themselves do not validate,
	// which is a bug in the defaults rather than in anything an operator
	// stored. Only that is allowed to fail a start.
	if err := config.Validate(c); err != nil {
		return nil, nil, nil, fmt.Errorf("compiled defaults do not validate: %w", err)
	}
	return c, warnings, skipped, nil
}
```

- [ ] **Step 4: Let the row read run inside a transaction**

In the same file, replace `configRows` and update `StoredConfigKeys`:

```go
// rowsQueryer is the part of *sql.DB and *sql.Tx a settings read needs. The
// write path reads its rows inside its own transaction, which is the whole
// point of its critical section, so this cannot be pinned to d.Read.
type rowsQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// configRows reads only the keys this binary knows. The settings table is
// shared with the keyring, the CSRF secret and the import markers, so a
// SELECT * would hand foreign rows to the registry.
func configRows(ctx context.Context, q rowsQueryer) (map[string]string, error) {
	out := map[string]string{}
	rows, err := q.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("read stored configuration: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan stored configuration: %w", err)
		}
		if ConfigKeyKnown(k) {
			out[k] = v
		}
	}
	return out, rows.Err()
}
```

`StoredConfigKeys` changes its one call to `configRows(ctx, d.Read)`. Add `"database/sql"` to the imports.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/store/ ./internal/config/ ./cmd/...
```

Expected: PASS, including every existing `LoadConfig` test unchanged — the extraction must not alter behaviour.

- [ ] **Step 6: Prove the tests can fail**

Delete the `c.Aliases = aliases` line, run `go test ./internal/store/ -run TestBuildConfigFailsOnAnAliasSetNoKeyCanFix`, confirm it goes red, restore it.

- [ ] **Step 7: Commit**

```bash
git add internal/store/configload.go internal/store/configload_test.go
git commit -m "$(cat <<'EOF'
refactor(store): extract the config builder

The write path has to assemble a config from the rows inside its own
transaction, and that assembly was locked inside LoadConfig.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 3: Begin every write transaction immediately

**Files:**
- Modify: `internal/store/db.go`
- Test: `internal/store/db_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing named. The write and sync handles begin their transactions with `BEGIN IMMEDIATE`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 2 = 3
**Approach:** inline - skip 2: a DSN parameter the driver already supports, alongside the four pragmas the file already sets this way

Spec §4 step 2 says `BEGIN IMMEDIATE`, and the reason is the shape of the write path: it reads the rows, decides against them, and then writes. A deferred transaction takes its read snapshot at the first read and its write lock at the first mutation. If another handle commits in between, the mutation fails with a busy error that no `busy_timeout` waits out, because the snapshot is gone rather than the lock being held.

The write handle is capped at one connection, so the competing writer is the sync handle, which carries credential writes and key rotation. An operator saving settings while an OAuth flow completes is enough.

`modernc.org/sqlite` v1.57.0 accepts `_txlock=immediate` as a DSN parameter (`sqlite.go:385`). The read pool must not get it: a read transaction would then take the write lock.

- [ ] **Step 1: Write the failing test**

Append to `internal/store/db_test.go`. Check its imports for `context`, `time` and `testing`, and for how it opens a database — reuse the helper already there rather than writing a second one.

```go
// The configuration write path reads its rows and then replaces them inside
// one transaction. A deferred transaction takes its snapshot at the read and
// its lock at the write, so another handle committing in between kills it with
// a busy error no timeout waits out. Beginning immediately makes the other
// writer wait instead.
func TestAWriteTransactionIsNotOvertakenMidway(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()

	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	// The read that would take the snapshot.
	if _, _, err := getSetting(ctx, tx, "absent"); err != nil {
		t.Fatalf("read inside the transaction: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- putSetting(ctx, db.Sync, "other", "value") }()
	// Long enough for the other writer to reach the lock. With an immediate
	// transaction it waits there; with a deferred one it commits, and the
	// write below is the one that fails.
	time.Sleep(50 * time.Millisecond)

	if err := putSetting(ctx, tx, "mine", "value"); err != nil {
		t.Fatalf("write after read in the same transaction: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the interleaved write never landed: %v", err)
	}
}
```

If `storetest.Migrated` is not already imported in this file, add `"github.com/darkraise/darkrouter/internal/store/storetest"`. If `db_test.go` is in package `store` and `storetest` imports `store`, use whatever helper the existing tests in that file use instead.

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/store/ -run TestAWriteTransactionIsNotOvertakenMidway -v
```

Expected: FAIL with a busy error on `write after read in the same transaction`.

- [ ] **Step 3: Add the DSN parameter to the write handles**

In `internal/store/db.go`, add below `commonPragmas`:

```go
// writeTxLock makes every transaction on a write handle take its lock at BEGIN
// rather than at the first mutation. A transaction that reads and then writes
// -- which the configuration write path does deliberately, to decide against
// the rows it is about to replace -- otherwise has its snapshot invalidated by
// any other handle's commit and fails with a busy error no timeout waits out.
//
// The read pool must not carry it: a read transaction would take the write
// lock and serialise the pool behind itself.
const writeTxLock = "&_txlock=immediate"
```

Change `dsn` and its three calls:

```go
func dsn(path, synchronous, extra string) string {
	// EscapedPath leaves separators intact while escaping the characters the
	// DSN's query string would otherwise consume, notably '?' and '#'.
	// url.PathEscape is wrong here: it escapes '/' too, which breaks every
	// absolute path.
	escaped := (&url.URL{Path: path}).EscapedPath()
	return "file:" + escaped +
		"?" + commonPragmas + "&_pragma=synchronous(" + synchronous + ")" + extra
}
```

In `Open`: `dsn(path, "NORMAL", writeTxLock)` for the write handle, `dsn(path, "NORMAL", "")` for the read pool, `dsn(path, "FULL", writeTxLock)` for the sync handle.

- [ ] **Step 4: Run the test to verify it passes**

```bash
go test ./internal/store/
```

Expected: PASS. Run the package twice; a lock change that deadlocks shows up as a timeout rather than a failure.

- [ ] **Step 5: Prove the test can fail**

Change the write handle back to `dsn(path, "NORMAL", "")`, run the test, confirm it goes red, restore `writeTxLock`.

- [ ] **Step 6: Commit**

```bash
git add internal/store/db.go internal/store/db_test.go
git commit -m "$(cat <<'EOF'
fix(store): begin write transactions immediately

A transaction that reads and then writes loses its snapshot to any
other handle's commit. The config write path is exactly that shape.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 4: The patch, and the store method that commits one

**Files:**
- Create: `internal/config/patch.go`
- Modify: `internal/config/store.go`
- Test: `internal/config/store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `config.Patch{Set map[string]string; Reset []string; Aliases map[string][]string}`
  - `config.RejectedError{Msg string}` with `config.Rejected(format string, args ...any) error`
  - `config.PublishError{Err error}`
  - `config.BootstrapVar(key string) (string, bool)`
  - `(*config.Store).SetWriter(fn func(context.Context, Patch) ([]string, error))`
  - `(*config.Store).Update(ctx context.Context, p Patch) (written []string, err error)`

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: `SetWriter` is `SetOverlay`'s established injection shape, for the same import-cycle reason

`Update` is the critical section spec §4 describes. It holds `reloadMu` across the commit and the republish, because those two are one operation: a reload landing between them publishes a snapshot the write has already superseded, and two saves racing can each validate against a state the other is about to replace.

The writer is injected for the reason `SetOverlay` is: `internal/config` may not import `internal/store`, since `store` already imports `config`.

Three error dispositions have to be distinguishable by the handler, so each gets a type: a refusal the operator can fix (400), a publish that failed after a durable commit (200 with the old config still serving), and anything else (500).

This task also fixes `NewStoreOf`, which returns the same `*Config` pointer on every load, so `Reload` mutates the snapshot a request may already be holding.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/store_test.go`. Add `context`, `errors`, `sync` and `time` to its imports as needed.

```go
// The write and the snapshot it produces are one operation. A reload landing
// between them publishes a configuration the write has already superseded.
func TestUpdateBlocksAConcurrentReload(t *testing.T) {
	base := &Config{}
	ApplyDefaults(base)
	s, err := NewStoreFrom(func() (*Config, error) { dup := *base; return &dup, nil })
	if err != nil {
		t.Fatal(err)
	}

	inWrite, release := make(chan struct{}), make(chan struct{})
	s.SetWriter(func(context.Context, Patch) ([]string, error) {
		close(inWrite)
		<-release
		return []string{"log.retention"}, nil
	})

	updated := make(chan error, 1)
	go func() { _, err := s.Update(context.Background(), Patch{}); updated <- err }()
	<-inWrite

	reloaded := make(chan error, 1)
	go func() { reloaded <- s.Reload() }()
	select {
	case <-reloaded:
		t.Fatal("a reload ran while a write was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-updated; err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := <-reloaded; err != nil {
		t.Fatalf("Reload: %v", err)
	}
}

// The rows are durable whether or not the republish worked, so "the write was
// refused" and "the write landed and the old config is still serving" are two
// different answers and the caller has to be able to tell them apart.
func TestUpdateReportsAPublishFailureSeparately(t *testing.T) {
	boom := errors.New("boom")
	var loads int
	s, err := NewStoreFrom(func() (*Config, error) {
		loads++
		if loads == 1 {
			c := &Config{}
			ApplyDefaults(c)
			return c, nil
		}
		return nil, boom
	})
	if err != nil {
		t.Fatal(err)
	}
	s.SetWriter(func(context.Context, Patch) ([]string, error) {
		return []string{"log.retention"}, nil
	})

	written, err := s.Update(context.Background(), Patch{})
	var pub PublishError
	if !errors.As(err, &pub) {
		t.Fatalf("err = %v, want a PublishError", err)
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to carry the load failure", err)
	}
	// Reported even so: those keys are in the database now.
	if len(written) != 1 || written[0] != "log.retention" {
		t.Errorf("written = %v, want the keys the write committed", written)
	}
}

func TestUpdateWithoutAWriterIsRefused(t *testing.T) {
	c := &Config{}
	ApplyDefaults(c)
	if _, err := NewStoreOf(c).Update(context.Background(), Patch{}); err == nil {
		t.Fatal("Update succeeded with no writer installed")
	}
}

// Reload publishes what load returns. Returning the same pointer every time
// mutates the snapshot an in-flight request is already using.
func TestNewStoreOfPublishesAFreshSnapshotPerReload(t *testing.T) {
	c := &Config{}
	ApplyDefaults(c)
	s := NewStoreOf(c)
	before := s.Current()
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Current() == before {
		t.Error("Reload republished the Config a request may already hold")
	}
}

func TestRejectedErrorIsMatchable(t *testing.T) {
	err := Rejected("policy.retry.max_attempts must be between 1 and %d", 10)
	var rejected RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if rejected.Error() != "policy.retry.max_attempts must be between 1 and 10" {
		t.Errorf("message = %q", rejected.Error())
	}
}

func TestBootstrapVarNamesTheVariableThatOwnsAKey(t *testing.T) {
	if name, ok := BootstrapVar("server.proxy_token"); !ok || name != "DARKROUTER_PROXY_TOKEN" {
		t.Errorf("BootstrapVar(server.proxy_token) = %q, %v", name, ok)
	}
	if _, ok := BootstrapVar("log.retention"); ok {
		t.Error("log.retention is a stored key, not a bootstrap one")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/config/ -run 'TestUpdate|TestNewStoreOfPublishes|TestRejectedError|TestBootstrapVar' -v
```

Expected: compile failure, `undefined: Patch`.

- [ ] **Step 3: Write internal/config/patch.go**

```go
package config

import "fmt"

// Patch is one save. A key carries one of three intents -- set to a value,
// reset to its compiled default, or untouched -- and the third is why this is
// two collections rather than one map: a map can say "this key is now X" and
// "this key is gone", but not "leave this key alone".
//
// The distinction is not decoration. Every deployment that has ever started
// materialised all seven policy rows, so without it a reset could never take
// and every one of them would read as a value the operator chose.
type Patch struct {
	// Set carries a new value per key, in the string form the registry parses.
	// An empty value is a reset: `value TEXT NOT NULL` would store "" happily
	// and it would read back as "set".
	Set map[string]string
	// Reset names keys whose stored row is deleted, which is what returns a
	// key to its compiled default. Writing the default instead would leave a
	// row that pins the value if a later release changes that default.
	Reset []string
	// Aliases replaces the whole alias set. Nil leaves the alias table alone;
	// an empty non-nil map deletes every alias, which is what an operator who
	// removed the last one meant.
	Aliases map[string][]string
}

// RejectedError is a write refused for what it says rather than for a failure
// to store it. The admin API answers 400 for it and 500 for everything else:
// one is the operator's to fix, the other is not.
type RejectedError struct{ Msg string }

func (e RejectedError) Error() string { return e.Msg }

// Rejected builds a RejectedError. The write path returns several of these and
// a constructor keeps them from drifting into wrapped or unwrapped variants.
func Rejected(format string, args ...any) error {
	return RejectedError{Msg: fmt.Sprintf(format, args...)}
}

// PublishError is a reload that failed after its write committed. The rows are
// durable and the previous configuration is still serving, which is a
// different answer from "the write was refused" and has to reach the operator
// as one.
type PublishError struct{ Err error }

func (e PublishError) Error() string { return e.Err.Error() }
func (e PublishError) Unwrap() error { return e.Err }

// bootstrapVars names the settings the environment owns. A save that names one
// is told where the value actually lives rather than that the key is unknown,
// which would send an operator looking for a typo they did not make.
var bootstrapVars = map[string]string{
	"server.proxy_listen": "DARKROUTER_PROXY_LISTEN",
	"server.admin_listen": "DARKROUTER_ADMIN_LISTEN",
	"server.proxy_token":  "DARKROUTER_PROXY_TOKEN",
	"log.level":           "DARKROUTER_LOG_LEVEL",
	"log.format":          "DARKROUTER_LOG_FORMAT",
}

// BootstrapVar names the environment variable that owns a key, if one does.
func BootstrapVar(key string) (string, bool) {
	name, ok := bootstrapVars[key]
	return name, ok
}
```

- [ ] **Step 4: Add SetWriter and Update to the store**

In `internal/config/store.go`, add `"context"` and `"errors"` to the imports and a field beside `overlay`:

```go
	// writer commits a Patch. Injected for the same reason overlay is: this
	// package may not import internal/store, because store already imports
	// config and the reverse edge would close a cycle.
	writer atomic.Pointer[func(context.Context, Patch) ([]string, error)]
```

Add below `applyOverlay`:

```go
// SetWriter installs the transactional commit Update runs under the reload
// lock. Injected rather than called directly, for the same import-cycle reason
// SetOverlay is.
func (s *Store) SetWriter(fn func(context.Context, Patch) ([]string, error)) {
	s.writer.Store(&fn)
}

// Update commits a patch and republishes, holding the reload lock across both.
//
// The lock spans the write because the commit and the snapshot it produces are
// one operation. A reload landing between them publishes a configuration the
// write has already superseded; two saves racing outside it can each validate
// against a state the other is about to replace, which is how two writes that
// each satisfy total >= connect + first_byte commit a pair that does not.
//
// It returns the keys the write committed even when the republish fails: they
// are in the database either way, and a caller that treated a publish failure
// as "nothing happened" would be wrong about durable rows.
func (s *Store) Update(ctx context.Context, p Patch) ([]string, error) {
	fn := s.writer.Load()
	if fn == nil || *fn == nil {
		return nil, errors.New("configuration is read-only: no writer is installed")
	}
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	written, err := (*fn)(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := s.reloadLocked(); err != nil {
		return written, PublishError{Err: err}
	}
	return written, nil
}
```

Split `Reload` so `Update` can republish without re-taking the lock. Replace it with:

```go
// Reload builds and validates the configuration in full, swapping only on
// success. A load that fails is rejected wholesale and the previous
// configuration stays live: a broken edit must never take the gateway down.
func (s *Store) Reload() error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()
	return s.reloadLocked()
}

// reloadLocked is Reload with the lock already held, which is what lets Update
// keep the commit and the republish inside one critical section.
func (s *Store) reloadLocked() error {
	next, err := s.loadNext()
	if err != nil {
		s.lastErr.Store(&err)
		return err
	}
	// Before publishing, not after: a snapshot carrying pre-overlay aliases for
	// even an instant is one a request could be routed by.
	if err := s.applyOverlay(next); err != nil {
		s.lastErr.Store(&err)
		return err
	}
	prev := s.cur.Load()
	next.Warnings = append(next.Warnings, restartOnlyWarnings(prev, next)...)
	s.cur.Store(next)
	s.lastErr.Store(nil)
	return nil
}
```

Fix `NewStoreOf` in the same file:

```go
// NewStoreOf builds a store over a fixed Config. Tests that need a store over
// a Config they built by hand use this; nothing in production calls it.
func NewStoreOf(c *Config) *Store {
	// A copy per load. Reload publishes whatever load returns, so handing back
	// the same pointer every time would have a reload mutate the snapshot an
	// in-flight request is already reading.
	s := &Store{load: func() (*Config, error) { dup := *c; return &dup, nil }}
	s.cur.Store(c)
	s.boot.Store(c)
	return s
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/config/ ./internal/admin/ ./internal/server/ ./internal/store/
go test ./... -count=1
```

Expected: PASS everywhere. `NewStoreOf` now returns a fresh snapshot per reload, which some test may have depended on; if one fails, it is asserting the old aliasing and should be read carefully before it is changed.

- [ ] **Step 6: Prove the tests can fail**

Move `s.reloadMu.Lock()` in `Update` to after the `(*fn)(ctx, p)` call, run `go test ./internal/config/ -run TestUpdateBlocksAConcurrentReload`, confirm it goes red, restore it.

- [ ] **Step 7: Commit**

```bash
git add internal/config/patch.go internal/config/store.go internal/config/store_test.go
git commit -m "$(cat <<'EOF'
feat(config): commit a patch under the reload lock

Update holds reloadMu across the write and the republish, so a save
and the snapshot it produces are one operation rather than two.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 5: WriteConfig — one transaction, one validation, one commit

**Files:**
- Create: `internal/store/configwrite.go`
- Test: `internal/store/configwrite_test.go`

**Interfaces:**
- Consumes: `buildConfig` and `configRows(ctx, q)` from Task 2, `configField.validate` from Task 1, `config.Patch`, `config.Rejected` and `config.BootstrapVar` from Task 4, `putAliasesTx` and `putSetting` as they are.
- Produces: `store.WriteConfig(ctx context.Context, d *DB, boot config.Bootstrap, p config.Patch) ([]string, error)` — the keys the save touched, sorted, or a `config.RejectedError` for a patch that cannot be kept.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: the transaction shape follows `PutConfig` and the spec fixes the step order

This is spec §4 in full. The base comes from the rows inside the transaction, built with the loader's reverting discipline so a row that was already unusable cannot refuse a save that has nothing to do with it. The patch's own values go on strictly, so a value this save introduces is judged rather than quietly reverted — a reverted save would commit a row every later start throws away.

Aliases are checked before the base is built. `buildConfig` reverts keys until the configuration validates, and no key can fix a broken alias chain, so an unchecked bad set would exhaust the loop and come back as "compiled defaults do not validate", which names nothing an operator can act on.

- [ ] **Step 1: Write the failing test**

Create `internal/store/configwrite_test.go`:

```go
package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

func TestWriteConfigStoresAKeyAndReportsIt(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	written, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"log.retention": "96h"},
	})
	if err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	if len(written) != 1 || written[0] != "log.retention" {
		t.Fatalf("written = %v", written)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if rows["log.retention"] != "96h" {
		t.Errorf("stored row = %q, want 96h", rows["log.retention"])
	}
}

// An absent row is what makes reset-to-default work, so a reset deletes rather
// than writing the current default into the table.
func TestWriteConfigResetDeletesTheRow(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"log.retention": "96h"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Reset: []string{"log.retention"},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["log.retention"]; ok {
		t.Error("the row survived a reset")
	}
}

// The settings table would store "" happily and it would read back as a value
// the operator chose.
func TestWriteConfigTreatsAnEmptyValueAsAReset(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.public_url": "https://example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.public_url": ""},
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["server.public_url"]; ok {
		t.Error("an empty value left a row behind")
	}
}

func TestWriteConfigRefusesABootstrapKeyByName(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.proxy_token": "sekrit"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if !strings.Contains(err.Error(), "DARKROUTER_PROXY_TOKEN") {
		t.Errorf("the refusal does not name the variable: %v", err)
	}
}

func TestWriteConfigRefusesAnUnknownKey(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.nonsense": "1"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
}

// Refused whole. A person is waiting and can be told what is wrong, which is
// the whole difference between this path and the loader's.
func TestWriteConfigCommitsNothingWhenOneKeyIsBad(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{
			"log.retention":     "96h",
			"capture.retention": "not-a-duration",
		},
	})
	if err == nil {
		t.Fatal("WriteConfig accepted an unparseable value")
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a refused write left rows behind: %v", rows)
	}
}

func TestWriteConfigRefusesABrokenCrossKeyRule(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"policy.timeout.total": "5s"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if !strings.Contains(err.Error(), "policy.timeout.total") {
		t.Errorf("the refusal does not name the rule it broke: %v", err)
	}
}

// The bug this phase exists to close. Both writes pass against a snapshot
// taken before either ran; only a base read inside the transaction sees the
// other one land.
func TestConcurrentWritesCannotBreakTheTimeoutBudgetTogether(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()

	var wg sync.WaitGroup
	errs := make([]error, 2)
	patches := []config.Patch{
		{Set: map[string]string{"policy.timeout.total": "70s"}},
		{Set: map[string]string{"policy.timeout.first_byte": "65s"}},
	}
	for i := range patches {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = WriteConfig(ctx, db, config.Bootstrap{}, patches[i])
		}(i)
	}
	wg.Wait()

	// Whichever order they ran in, what is in the database now must load.
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	c, _, skipped, err := buildConfig(rows, config.Bootstrap{}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("the committed rows do not load: %v (errs %v)", skipped, errs)
	}
	if c.Policy.Timeout.Total < c.Policy.Timeout.Connect+c.Policy.Timeout.FirstByte {
		t.Errorf("total %s does not cover connect %s plus first_byte %s",
			c.Policy.Timeout.Total, c.Policy.Timeout.Connect, c.Policy.Timeout.FirstByte)
	}
	// One of them had to lose, or the rule was never enforced.
	if errs[0] == nil && errs[1] == nil {
		t.Error("both writes committed a pair that together breaks the rule")
	}
}

func TestWriteConfigWritesAliasesInTheSameTransaction(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Aliases: map[string][]string{"fast": {"groq/llama"}},
		Set:     map[string]string{"log.retention": "96h"},
	}); err != nil {
		t.Fatal(err)
	}
	aliases, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases["fast"]) != 1 {
		t.Errorf("aliases = %v", aliases)
	}
}

// A save that breaks nothing must not be refused because of a row that was
// already unusable before it ran.
func TestWriteConfigIgnoresAnUnusableRowItDoesNotTouch(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	if err := putSetting(ctx, db.Write, "capture.retention", "not-a-duration"); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"log.retention": "96h"},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if rows["log.retention"] != "96h" {
		t.Errorf("the save did not land: %v", rows)
	}
	// Untouched, not repaired. Fixing it is a save that names it.
	if rows["capture.retention"] != "not-a-duration" {
		t.Errorf("the save rewrote a row it was not given: %v", rows)
	}
}

func TestWriteConfigRefusesAKeySetAndResetAtOnce(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set:   map[string]string{"log.retention": "96h"},
		Reset: []string{"log.retention"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
}

// The per-key validator from the registry, on the path a person is waiting on.
func TestWriteConfigRefusesAnOutOfRangeRetryCount(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"policy.retry.max_attempts": "20"},
	})
	var rejected config.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("err = %v, want a RejectedError", err)
	}
	if !strings.Contains(err.Error(), "between 1 and 10") {
		t.Errorf("the refusal does not state the bound: %v", err)
	}
}

// A bare domain is how an operator writes this setting; the registry
// normalises it on the way into the Config, and the row keeps what was typed.
func TestWriteConfigAcceptsABareDomainForThePublicURL(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	if _, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Set: map[string]string{"server.public_url": "llm.example.test"},
	}); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _, err := buildConfig(rows, config.Bootstrap{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.PublicURL != "https://llm.example.test" {
		t.Errorf("public_url = %q, want the normalised URL", c.Server.PublicURL)
	}
}

var _ = time.Second
```

Drop the `var _ = time.Second` line and the `time` import if nothing else in the file needs them.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/store/ -run TestWriteConfig -v
```

Expected: compile failure, `undefined: WriteConfig`.

- [ ] **Step 3: Write internal/store/configwrite.go**

```go
package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/config"
)

// WriteConfig commits one save: the whole of it or none of it. It returns the
// keys the save touched, sorted, so the caller can say which of them wait for
// a restart.
//
// The base configuration comes from the rows inside this transaction rather
// than from the running snapshot. Two saves that are each valid against the
// same snapshot can together break a cross-key rule -- total >= connect +
// first_byte is the live example -- and validating against a snapshot lets
// both commit a state the next load would refuse.
//
// The untouched rows are assembled with the loader's reverting discipline, so
// a row that was already unusable cannot refuse a save that has nothing to do
// with it. The save's own values go on strictly: a value this write introduces
// must be judged, never quietly reverted, or the row it commits is one every
// later start throws away.
func WriteConfig(ctx context.Context, d *DB, boot config.Bootstrap, p config.Patch) ([]string, error) {
	set, del, err := effective(p)
	if err != nil {
		return nil, err
	}

	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin config write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := configRows(ctx, tx)
	if err != nil {
		return nil, err
	}

	aliases := p.Aliases
	if aliases == nil {
		if aliases, err = aliasesTx(ctx, tx); err != nil {
			return nil, err
		}
	}
	// Checked before the base is built. buildConfig reverts keys until the
	// configuration validates and no key can fix a broken alias chain, so an
	// unchecked bad set exhausts the loop and comes back as "compiled defaults
	// do not validate", which names nothing an operator can act on.
	if err := config.ValidateAliases(aliases); err != nil {
		return nil, config.RejectedError{Msg: err.Error()}
	}

	base := make(map[string]string, len(rows))
	for k, v := range rows {
		if _, replaced := set[k]; replaced || del[k] {
			continue
		}
		base[k] = v
	}
	cfg, _, _, err := buildConfig(base, boot, aliases)
	if err != nil {
		return nil, err
	}

	// Sorted, so a patch with two bad values always names the same one first.
	for _, key := range sortedKeys(set) {
		f := configByKey[key]
		if err := f.set(cfg, set[key]); err != nil {
			return nil, config.Rejected("%s: %v", key, err)
		}
		if f.validate != nil {
			if err := f.validate(cfg); err != nil {
				return nil, config.RejectedError{Msg: err.Error()}
			}
		}
	}
	if err := config.Validate(cfg); err != nil {
		return nil, config.RejectedError{Msg: err.Error()}
	}

	for _, key := range sortedKeys(set) {
		if err := putSetting(ctx, tx, key, set[key]); err != nil {
			return nil, err
		}
	}
	for _, key := range sortedFlags(del) {
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return nil, fmt.Errorf("clear setting %q: %w", key, err)
		}
	}
	if p.Aliases != nil {
		if err := putAliasesTx(ctx, tx, p.Aliases); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit config write: %w", err)
	}

	written := append(sortedKeys(set), sortedFlags(del)...)
	sort.Strings(written)
	return written, nil
}

// effective reduces a patch to the rows to write and the rows to delete, and
// refuses anything that cannot become either. It runs before the transaction
// opens: a patch naming a key that does not exist has nothing to read rows for.
func effective(p config.Patch) (map[string]string, map[string]bool, error) {
	set := map[string]string{}
	del := map[string]bool{}

	known := func(key string) error {
		if ConfigKeyKnown(key) {
			return nil
		}
		if name, ok := config.BootstrapVar(key); ok {
			return config.Rejected("%s is set by %s and cannot be written here", key, name)
		}
		return config.Rejected("unknown setting %q", key)
	}

	for key, value := range p.Set {
		if err := known(key); err != nil {
			return nil, nil, err
		}
		// An empty value is a delete. The column is NOT NULL and would store
		// "" happily, which then reads back as a value the operator chose.
		if strings.TrimSpace(value) == "" {
			del[key] = true
			continue
		}
		set[key] = value
	}
	for _, key := range p.Reset {
		if err := known(key); err != nil {
			return nil, nil, err
		}
		if _, both := set[key]; both {
			return nil, nil, config.Rejected("%s is both set and reset in one save", key)
		}
		del[key] = true
	}
	return set, del, nil
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedFlags(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// aliasesTx reads the alias table inside the write transaction, so a save that
// does not carry aliases still validates against the set that will be live
// beside it rather than against one another write may have replaced.
func aliasesTx(ctx context.Context, q rowsQueryer) (map[string][]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name, target FROM aliases ORDER BY name, seq`)
	if err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]string{}
	for rows.Next() {
		var name, target string
		if err := rows.Scan(&name, &target); err != nil {
			return nil, fmt.Errorf("scan alias: %w", err)
		}
		out[name] = append(out[name], target)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read aliases: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/store/ -run 'TestWriteConfig|TestConcurrentWrites' -v
go test ./internal/store/ -count=1
```

Expected: PASS. If `TestConcurrentWritesCannotBreakTheTimeoutBudgetTogether` is flaky, run it with `-count=20`; both goroutines serialise on the single write connection, so the result is a genuine either-order outcome rather than a race.

- [ ] **Step 5: Prove the tests can fail**

Replace the base build with the running defaults — change `buildConfig(base, boot, aliases)` to `buildConfig(nil, boot, aliases)` — run `go test ./internal/store/ -run TestConcurrentWritesCannotBreakTheTimeoutBudgetTogether -count=10`, confirm it goes red, restore it.

- [ ] **Step 6: Commit**

```bash
git add internal/store/configwrite.go internal/store/configwrite_test.go
git commit -m "$(cat <<'EOF'
feat(store): commit a config patch in one transaction

The base is read inside the transaction, so two saves cannot each
pass the timeout rule against a snapshot and commit a pair that fails.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 6: PUT /api/config on the write path

**Files:**
- Modify: `internal/admin/configapi.go`
- Modify: `cmd/darkrouter/main.go`
- Modify: `internal/admin/fixtures_test.go`
- Test: `internal/admin/configapi_test.go`

**Interfaces:**
- Consumes: `config.Patch`, `config.RejectedError`, `config.PublishError`, `(*config.Store).Update` and `SetWriter` from Task 4; `store.WriteConfig` from Task 5.
- Produces: `(*Server).commitConfig(w http.ResponseWriter, r *http.Request, p config.Patch)` — the one place a configuration write is committed, which Task 7 hangs the alias and policy endpoints on. Also `restartRequired(written []string) []string`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 2: the handler shape follows the existing decode-validate-write-republish sequence in this file

The endpoint's shape changes. `{"aliases": …, "policy": {…}}` becomes `{"set": {…}, "reset": […], "aliases": {…}}`, which is the tri-state a save needs. Nothing in `web/` sends the old shape: the console writes through `PUT /api/policy` and `PUT /api/aliases`, both of which Task 7 moves onto the same path without changing their wire shapes.

Restart-only keys are accepted rather than refused, per spec §4. The value belongs in the database either way; the answer names which of the written keys wait for a restart. The two console comments that state the old refusal as a fact are corrected in Task 9 — they are code comments, not rendered text, so nothing an operator sees changes.

`mergedPolicy`, `applyPolicyWrite`, `policyWrite` and `restartOnlyIn` stay untouched here: `handlePutPolicy` still calls them and Task 7 is where they go.

- [ ] **Step 1: Install the writer in the fixtures and in main**

In `internal/admin/fixtures_test.go`, inside `storeOverDatabase`, add after the `cfg.SetOverlay(...)` block and before the first `cfg.Reload()`:

```go
	cfg.SetWriter(func(ctx context.Context, p config.Patch) ([]string, error) {
		return store.WriteConfig(ctx, db, boot, p)
	})
```

In `cmd/darkrouter/main.go`, add the same beside the existing `cfgStore.SetOverlay(...)` call, before the `cfgStore.Reload()` below it:

```go
	// Installed with the overlay, before the first reload: the admin API is
	// reachable as soon as the server starts, and a write that arrived with no
	// writer installed would be refused for a reason nobody could act on.
	cfgStore.SetWriter(func(ctx context.Context, p config.Patch) ([]string, error) {
		return store.WriteConfig(ctx, db, boot, p)
	})
```

- [ ] **Step 2: Write the failing test**

Replace `TestPutConfigRefusesARestartOnlyField` and `TestPutConfigWritesAHotReloadablePolicyField` in `internal/admin/configapi_test.go` with the block below, and add the rest. `TestPutConfigWritesAliasesAndTheyTakeEffect`, `TestPutConfigRejectsAnUnknownProvider` and `TestPutConfigNeedsASession` keep their bodies — the `aliases` field is unchanged.

```go
func TestPutConfigWritesASettingAndItTakesEffect(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"96h"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if got := s.deps.Config.Current().Log.Retention; got != 96*time.Hour {
		t.Errorf("log.retention = %s, want 96h", got)
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !stored["log.retention"] {
		t.Error("the row is not in the database")
	}
}

// Accepted, and the answer says so. The value belongs in the database whether
// or not this process can apply it; refusing it would leave the operator no
// way to set it at all.
func TestPutConfigAcceptsARestartOnlyFieldAndNamesIt(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"policy.timeout.connect":"5s"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Valid           bool     `json:"valid"`
		RestartRequired []string `json:"restart_required"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Valid {
		t.Fatalf("valid = false: %s", w.Body.String())
	}
	if len(body.RestartRequired) != 1 || body.RestartRequired[0] != "policy.timeout.connect" {
		t.Errorf("restart_required = %v", body.RestartRequired)
	}
}

// Never null: a client cannot tell a JSON null from a field an older build did
// not serve.
func TestPutConfigRestartRequiredIsAlwaysAnArray(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"96h"}}`)
	if !strings.Contains(w.Body.String(), `"restart_required":[]`) {
		t.Errorf("body = %s", w.Body.String())
	}
}

func TestPutConfigResetsAKeyToItsDefault(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"96h"}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"reset":["log.retention"]}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["log.retention"] {
		t.Error("the row survived a reset")
	}
	if got := s.deps.Config.Current().Log.Retention; got != 720*time.Hour {
		t.Errorf("log.retention = %s, want the compiled default", got)
	}
}

func TestPutConfigRefusesABootstrapKey(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"server.proxy_token":"sekrit"}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "DARKROUTER_PROXY_TOKEN") {
		t.Errorf("the refusal does not name the variable: %s", w.Body.String())
	}
}

func TestPutConfigRefusesAValueTheLoaderWouldReject(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"log.retention":"1h"}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["log.retention"] {
		t.Error("a refused write left a row behind")
	}
}

// The one endpoint that must never echo credential material, on the path that
// now accepts writes for everything else.
func TestPutConfigNeverEchoesTheProxyToken(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"set":{"server.proxy_token":"sekrit"}}`)
	if strings.Contains(w.Body.String(), "sekrit") {
		t.Errorf("the response echoed the value: %s", w.Body.String())
	}
}
```

Add `encoding/json`, `time` and `github.com/darkraise/darkrouter/internal/store` to the file's imports if they are not there.

- [ ] **Step 3: Run the tests to verify they fail**

```bash
go test ./internal/admin/ -run TestPutConfig -v
```

Expected: FAILs. `TestPutConfigWritesASettingAndItTakesEffect` returns 200 with nothing written, because `set` is an unknown JSON field the old handler ignores.

- [ ] **Step 4: Rewrite the handler**

In `internal/admin/configapi.go`, replace `configWrite`, `handleConfigPut` and add `commitConfig` and `restartRequired`. Leave `restartOnlyIn`, `mergedPolicy` and `applyPolicyWrite` in place for Task 7.

```go
// configWrite is the accepted shape of PUT /api/config. Set and Reset are two
// collections rather than one map because a save has three intents per key and
// a map cannot express the third: a key in neither is untouched.
type configWrite struct {
	Set     map[string]string   `json:"set"`
	Reset   []string            `json:"reset"`
	Aliases map[string][]string `json:"aliases"`
}

func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil || s.deps.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	var body configWrite
	if !decodeJSON(w, r, 64<<10, &body) {
		return
	}
	s.commitConfig(w, r, config.Patch{
		Set: body.Set, Reset: body.Reset, Aliases: body.Aliases,
	})
}

// commitConfig is the one place a configuration write is committed. The alias
// and policy endpoints are shapes over it rather than paths of their own, so a
// value refused on one is refused on all three and there is a single critical
// section to reason about.
func (s *Server) commitConfig(w http.ResponseWriter, r *http.Request, p config.Patch) {
	if p.Aliases != nil {
		// Admin-side because it needs provider rows. The loader cannot make
		// this check and internal/store's write path has no business reading
		// a table that belongs to another part of the console.
		if err := s.aliasTargetsExist(r.Context(), p.Aliases); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	// WithoutCancel: the write is about to become durable, and a client that
	// disconnects mid-commit must not leave the gateway serving a snapshot
	// that predates rows it now holds.
	written, err := s.deps.Config.Update(afterCommit(r), p)
	var rejected config.RejectedError
	var publish config.PublishError
	switch {
	case errors.As(err, &rejected):
		writeError(w, http.StatusBadRequest, rejected.Error())
	case errors.As(err, &publish):
		// 200 rather than 500: the write was performed and its outcome is the
		// answer. The rows are durable; what failed is the republish, and the
		// previous configuration is still serving.
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false, "error": publish.Error(),
			"serving": "the previous configuration is still serving",
		})
	case err != nil:
		internalError(w, r, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": true, "restart_required": restartRequired(written),
		})
	}
}

// restartRequired names the written keys a running process cannot apply. They
// are accepted rather than refused: the value belongs in the database either
// way, and refusing it would leave an operator no way to set it at all.
//
// Never nil: a client cannot tell a JSON null from a field an older build did
// not serve.
func restartRequired(written []string) []string {
	out := []string{}
	for _, k := range written {
		if slices.Contains(config.RestartOnly, k) {
			out = append(out, k)
		}
	}
	return out
}
```

Add `"errors"` to the imports.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/admin/ ./cmd/... ./internal/e2e/
```

Expected: PASS. `internal/e2e` exercises `POST /api/config/reload`, which is unchanged.

- [ ] **Step 6: Prove the tests can fail**

Change `commitConfig`'s rejected branch to `writeError(w, http.StatusOK, …)`, run `go test ./internal/admin/ -run TestPutConfigRefusesABootstrapKey`, confirm it goes red, restore it.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/configapi.go internal/admin/configapi_test.go internal/admin/fixtures_test.go cmd/darkrouter/main.go
git commit -m "$(cat <<'EOF'
feat(config-api): write any stored setting through PUT

The endpoint takes set, reset and aliases, commits them in one
transaction, and names the written keys that wait for a restart.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 7: Policy and aliases as shapes over the one path

**Files:**
- Modify: `internal/admin/aliasapi.go`
- Modify: `internal/admin/configapi.go`
- Delete: `internal/admin/policyvalidate.go`
- Test: `internal/admin/policyvalidate_test.go`

**Interfaces:**
- Consumes: `commitConfig` from Task 6.
- Produces: `policyPatch(p *policyWrite) map[string]string`. `validatePolicy`, `maxRetryAttempts` (the admin copy), `mergedPolicy`, `applyPolicyWrite`, `restartOnlyIn` and `republish` are gone.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5
**Approach:** inline - skip 2: a translation function into the shape Task 6 already accepts

`PUT /api/policy` keeps its wire shape, because the console sends it. What changes is what happens behind it: the seven keys become registry keys like any other, and `mergedPolicy`'s merge against `Current()` — the concurrency bug spec §4 names — goes with it.

`PUT /api/aliases` keeps its shape too and moves inside the same transaction.

One behaviour changes for the console, and it is the one the spec asks for: emptying a duration box sends `""`, which was a 400 (`time.ParseDuration("")`) and is now a reset to the compiled default. That is what "clearing a value restores the default" has always meant in the copy.

- [ ] **Step 1: Write the failing test**

Rewrite `internal/admin/policyvalidate_test.go` around the endpoint rather than the deleted function:

```go
package admin

import (
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

func TestPolicyWriteRefusesEachInvalidValue(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
	}{
		{"trip_after below one", `{"cooldown":{"trip_after":0}}`, "trip_after"},
		{"retry count of zero", `{"retry":{"max_attempts":0}}`, "max_attempts"},
		{"retry count past the cap", `{"retry":{"max_attempts":20}}`, "between 1 and 10"},
		{"total under connect plus first_byte", `{"timeout":{"total":"5s"}}`, "policy.timeout.total"},
		{"a duration that will not parse", `{"timeout":{"idle":"soon"}}`, "policy.timeout.idle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testServerFull(t)
			cookie, token := login(t, s)
			w := do(t, s, cookie, token, "PUT", "/api/policy", tc.body)
			if w.Code != 400 {
				t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), tc.want) {
				t.Errorf("the refusal does not say why: %s", w.Body.String())
			}
		})
	}
}

func TestInvalidPolicyIsNeverWritten(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"retry":{"max_attempts":20}}`); w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["policy.retry.max_attempts"] {
		t.Error("a refused write left a row behind")
	}
}

func TestPolicyWriteTakesEffectOnTheNextSnapshot(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"idle":"90s"}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if got := s.deps.Config.Current().Policy.Timeout.Idle; got != 90*time.Second {
		t.Errorf("idle = %s, want 90s", got)
	}
}

// The seven policy keys are registry rows, so a restart-only one is accepted
// here for the same reason it is on PUT /api/config.
func TestPolicyWriteAcceptsARestartOnlyField(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"connect":"5s"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "policy.timeout.connect") {
		t.Errorf("the answer does not name the key that waits: %s", w.Body.String())
	}
}

// Emptying a box means "use the default", which is a deleted row rather than a
// write of whatever the default currently is.
func TestPolicyWriteWithAnEmptyDurationResetsTheKey(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"idle":"90s"}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "PUT", "/api/policy",
		`{"timeout":{"idle":""}}`); w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	stored, err := store.StoredConfigKeys(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if stored["policy.timeout.idle"] {
		t.Error("the row survived an emptied field")
	}
	if got := s.deps.Config.Current().Policy.Timeout.Idle; got != 120*time.Second {
		t.Errorf("idle = %s, want the compiled default", got)
	}
}

// Both blocks or neither, on one transaction. A save that wrote its aliases
// and then failed on its policy would leave the screen half-applied.
func TestPutConfigWritesNothingWhenTheSettingIsInvalid(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["groq/llama"]},"set":{"policy.retry.max_attempts":"20"}}`)
	if w.Code != 400 {
		t.Fatalf("PUT = %d, want 400: %s", w.Code, w.Body.String())
	}
	aliases, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Errorf("the alias half of a refused save landed: %v", aliases)
	}
}

func TestPutConfigWritesBothBlocksTogether(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "groq", "http://127.0.0.1:1")
	w := do(t, s, cookie, token, "PUT", "/api/config",
		`{"aliases":{"fast":["groq/llama"]},"set":{"policy.retry.max_attempts":"5"}}`)
	if w.Code != 200 {
		t.Fatalf("PUT = %d: %s", w.Code, w.Body.String())
	}
	aliases, err := db.Aliases(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases["fast"]) != 1 {
		t.Errorf("aliases = %v", aliases)
	}
	if got := s.deps.Config.Current().Policy.Retry.MaxAttempts; got != 5 {
		t.Errorf("max_attempts = %d, want 5", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/admin/ -run 'TestPolicyWrite|TestInvalidPolicy|TestPutConfigWrites' -v
```

Expected: FAILs — `TestPolicyWriteAcceptsARestartOnlyField` gets 400 from the surviving `restartOnlyIn` guard, and `TestPolicyWriteWithAnEmptyDurationResetsTheKey` gets 400 from `time.ParseDuration("")`.

- [ ] **Step 3: Move the two endpoints onto commitConfig**

In `internal/admin/aliasapi.go`, replace `handlePutAliases`, `handlePutPolicy` and `republish`:

```go
func (s *Server) handlePutAliases(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil || s.deps.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	var aliases map[string][]string
	if !decodeJSON(w, r, 64<<10, &aliases) {
		return
	}
	// Not nil: the write path reads nil as "leave the alias table alone", and
	// an operator who deleted the last chain meant the opposite.
	if aliases == nil {
		aliases = map[string][]string{}
	}
	s.commitConfig(w, r, config.Patch{Aliases: aliases})
}

func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil || s.deps.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	var body policyWrite
	if !decodeJSON(w, r, 16<<10, &body) {
		return
	}
	s.commitConfig(w, r, config.Patch{Set: policyPatch(&body)})
}

// policyPatch turns the policy endpoint's shape into registry keys. It is the
// whole of what makes that endpoint a view rather than a second write path: a
// field it does not mention is untouched, and one it mentions as empty is a
// reset, which is what the console's "clear the box" has always meant.
func policyPatch(p *policyWrite) map[string]string {
	set := map[string]string{}
	if p.Cooldown != nil {
		if p.Cooldown.TripAfter != nil {
			set["policy.cooldown.trip_after"] = strconv.Itoa(*p.Cooldown.TripAfter)
		}
		if p.Cooldown.Max != nil {
			set["policy.cooldown.max"] = *p.Cooldown.Max
		}
	}
	if p.Retry != nil && p.Retry.MaxAttempts != nil {
		set["policy.retry.max_attempts"] = strconv.Itoa(*p.Retry.MaxAttempts)
	}
	if p.Timeout != nil {
		for key, v := range map[string]*string{
			"policy.timeout.connect":    p.Timeout.Connect,
			"policy.timeout.first_byte": p.Timeout.FirstByte,
			"policy.timeout.total":      p.Timeout.Total,
			"policy.timeout.idle":       p.Timeout.Idle,
		} {
			if v != nil {
				set[key] = *v
			}
		}
	}
	return set
}
```

Add `"strconv"` to that file's imports and drop `"github.com/darkraise/darkrouter/internal/store"` if nothing else in it still uses the package.

- [ ] **Step 4: Delete what the merge path needed**

Delete `internal/admin/policyvalidate.go` entirely. In `internal/admin/configapi.go`, delete `restartOnlyIn`, `mergedPolicy` and `applyPolicyWrite`; keep `policyWrite` and its nested structs, which `policyPatch` reads, and keep `joinFields` only if something still calls it — otherwise delete it too.

```bash
grep -rn "validatePolicy\|mergedPolicy\|applyPolicyWrite\|restartOnlyIn\|joinFields\|republish" internal/ cmd/
```

Every hit must be a definition you are removing or a caller you have already rewritten.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/admin/ ./internal/e2e/ ./cmd/...
go test ./... -count=1
```

Expected: PASS. Also run the console suite, which asserts nothing about these endpoints but is cheap insurance:

```bash
cd web && npm test -- --run && cd ..
```

- [ ] **Step 6: Prove the tests can fail**

Make `policyPatch` skip `p.Timeout.Idle`, run `go test ./internal/admin/ -run TestPolicyWriteTakesEffectOnTheNextSnapshot`, confirm it goes red, restore it.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/
git rm internal/admin/policyvalidate.go
git commit -m "$(cat <<'EOF'
refactor(admin): route policy and aliases via config

Both endpoints keep their shapes and become views over the one write
path, which retires the merge against Current() and its second validator.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 8: Retire the policy-only write path

**Files:**
- Modify: `internal/store/configstore.go`
- Delete: `internal/store/import.go`
- Test: `internal/store/configstore_test.go`, `internal/store/configstore_tx_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. `policyFields`, `putPolicyTx`, `PutPolicy`, `PutConfig`, `ImportedAt`, `ImportResult` and `settingProvidersImportedAt` are gone. `PutAliases` stays: tests and fixtures build alias state with it, and it is the same transaction shape `putAliasesTx` gives the write path.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 1 = 3
**Approach:** inline - skip 2: deletion of code with no remaining callers

`policyFields` and `putPolicyTx` must go with the new write path rather than after it. `putPolicyTx` re-materialises all seven policy rows on every save, because `policyFields.get` reports "set" for any non-zero value and `applyDefaults` has already made every field non-zero. `ReconcileConfig` deletes the default-equal ones on the next start. Left in place beside the registry, the two would flip a key's reported source between `database` and `default` across a restart.

`internal/store/import.go` has no callers at all: `ImportedAt` is unreferenced, and `providers_imported_at` is written by nothing since the YAML import was deleted at `d8c46480`.

- [ ] **Step 1: Confirm nothing calls them**

```bash
grep -rn "PutPolicy\|PutConfig\|policyFields\|putPolicyTx\|ImportedAt\|ImportResult\|settingProvidersImportedAt" --include='*.go' . 
```

Expected hits: the definitions themselves, and the two store tests this task rewrites. Anything else means Task 7 is incomplete — stop and fix that first.

- [ ] **Step 2: Delete the code**

Remove from `internal/store/configstore.go`: the `policyField` type, the `policyFields` table, `PutPolicy`, `PutConfig` and `putPolicyTx`. Keep `Aliases`, `PutAliases`, `putAliasesTx`, `settingConfigImportedAt`, `OverlayConfig` and the model-override functions. Drop `strconv` and `time` from the imports if nothing else in the file uses them.

```bash
git rm internal/store/import.go
```

- [ ] **Step 3: Replace the tests that covered them**

In `internal/store/configstore_tx_test.go`, replace `TestPutConfigWritesBothBlocksOrNeither` with the equivalent over the surviving path:

```go
// Both blocks or neither. The write path is where this lives now; the check
// stays because a half-applied save is the failure, not the function that
// used to make it.
func TestWriteConfigWritesBothBlocksOrNeither(t *testing.T) {
	db, ctx := storetest.Migrated(t), context.Background()
	_, err := WriteConfig(ctx, db, config.Bootstrap{}, config.Patch{
		Aliases: map[string][]string{"fast": {"groq/llama"}},
		Set:     map[string]string{"policy.retry.max_attempts": "20"},
	})
	if err == nil {
		t.Fatal("WriteConfig accepted a retry count past the cap")
	}
	aliases, err := db.Aliases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 0 {
		t.Errorf("the alias half of a refused write landed: %v", aliases)
	}
}
```

In `internal/store/configstore_test.go`, remove any test that exercises `PutPolicy` or `PutConfig` directly. `TestOverlayConfigReplacesAliasesOnly` stays as it is.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go build ./... && go test ./... -count=1
```

Expected: PASS, and a clean build — a surviving caller shows up here as a compile error.

- [ ] **Step 5: Prove the test can fail**

Change the retry count in the new test to `"5"`, run it, confirm it fails on `WriteConfig accepted a retry count past the cap`, restore `"20"`.

- [ ] **Step 6: Commit**

```bash
git add internal/store/
git commit -m "$(cat <<'EOF'
refactor(store): retire the policy-only write path

putPolicyTx re-materialised all seven rows on every save while
ReconcileConfig deleted the default-equal ones on the next start, so
a key's reported source flipped across a restart.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 9: Retire the file-era wording

**Files:**
- Modify: `internal/admin/configapi.go`, `internal/server/server.go`, `internal/config/config.go`, `internal/config/store.go`
- Modify: `web/src/features/settings/settings-catalog.ts`, `web/src/features/settings/settings-screen.tsx`, `web/src/features/settings/settings-catalog.test.ts`, `web/src/features/settings/settings-screen.test.tsx`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. Comments and struct tags only; no behaviour changes.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 3 - spec 0 - coupling 0 - risk 0 = 3
**Approach:** inline - skip 2: each edit is an exact string replacement given below

Comments that describe a file the process no longer reads send the next reader looking for it. The `yaml:"…"` struct tags on `Config` are the same thing in another form: nothing unmarshals YAML into it any more, and a tag is a claim that something does. `internal/catalog` and `tools/presetgen` keep their own YAML types and are untouched.

All four `web/` edits are comments — JSDoc and JSX comment blocks — so nothing an operator sees changes.

- [ ] **Step 1: Fix the Go comments**

`internal/admin/configapi.go`, in `handleConfigReload`: replace

```
		// is that the file is invalid and the old config is still serving.
```

with

```
		// is that the new configuration is invalid and the old one is serving.
```

`internal/admin/configapi.go`, above `aliasTargetsExist`: replace

```
// aliasTargetsExist rejects a chain naming a provider that is not configured.
// The file loader cannot make this check -- at load time the providers block
// may not have been imported yet -- but the API can, because by then the
// provider set is in the database.
```

with

```
// aliasTargetsExist rejects a chain naming a provider that is not configured.
// It stays admin-side because it needs provider rows: the write path in
// internal/store has no business reading a table that belongs to another part
// of the console.
```

`internal/server/server.go`, above `authed`: replace

```
// authed enforces the optional proxy token in the route's own dialect. The
// token is read live because proxy_token is hot-reloadable, unlike the listen
// addresses, and a rejection is written in the dialect the client speaks so its
// existing error handling applies.
```

with

```
// authed enforces the optional proxy token in the route's own dialect. The
// token is read from the live snapshot rather than captured at construction,
// though it reaches that snapshot from DARKROUTER_PROXY_TOKEN and so cannot
// change under a running process. A rejection is written in the dialect the
// client speaks, so the client's existing error handling applies.
```

`internal/server/server.go`, in the `/healthz` handler: replace

```
		// Startup warnings first: they explain state the config file cannot,
		// such as a providers block that is no longer the source of truth.
```

with

```
		// Startup warnings first: they explain state the stored settings
		// cannot, such as a leftover file that is no longer read.
```

`internal/server/server.go`, above `/readyz`: replace

```
	// Ready means able to serve: the database answers and the live config is
	// the one on disk. An orchestrator routes on this, so a gateway that would
	// fail every request must not look ready.
```

with

```
	// Ready means able to serve: the database answers and the live config
	// loaded. An orchestrator routes on this, so a gateway that would fail
	// every request must not look ready.
```

`internal/config/config.go`, in the `Config.Warnings` comment: replace `rather than rejecting the document` with `rather than rejecting the configuration`.

`internal/config/config.go`, in `PlaygroundConfig.SaveConversations`: replace

```
	// SaveConversations is a pointer for the same reason Discovery.Enabled is:
	// the default is on, so an explicit false in the file has to be
	// distinguishable from a key the file never mentioned.
```

with

```
	// SaveConversations is a pointer for the same reason Discovery.Enabled is:
	// the default is on, so an explicit false has to be distinguishable from a
	// key with no stored row.
```

- [ ] **Step 2: Remove the decorative yaml tags**

Delete every `yaml:"…"` struct tag in `internal/config/config.go`, including the `yaml:"-"` ones on `Warnings` and `Skipped`. Nothing unmarshals YAML into these types; the loader reads the registry.

```bash
grep -rn 'yaml:"' internal/config/
```

Expected after the edit: no hits.

- [ ] **Step 3: Fix the console comments**

`web/src/features/settings/settings-catalog.ts`, in the `EditableSetting` JSDoc: replace

```
 * `policy.timeout.connect` and `policy.timeout.first_byte` are absent for the
 * same reason: both configure the one shared transport built at startup, and
 * `PUT /api/policy` refuses a write that touches either.
```

with

```
 * `policy.timeout.connect` and `policy.timeout.first_byte` are absent because
 * both configure the one shared transport built at startup, so a save of
 * either waits for a restart. The API accepts them; this screen does not offer
 * them yet.
```

`web/src/features/settings/settings-screen.tsx`, in the write's JSDoc: replace

```
 * `connect` and `first_byte` never enter it. Both configure the one shared
 * transport built at startup, so no reload can apply them and `PUT
 * /api/policy` refuses a write that touches either — they are omitted rather
 * than sent and rejected.
```

with

```
 * `connect` and `first_byte` never enter it. Both configure the one shared
 * transport built at startup, so no reload can apply them: the API accepts a
 * write and names them as waiting for a restart, and this screen has no editor
 * for that yet.
```

`web/src/features/settings/settings-screen.tsx`, in the badge's JSX comment: replace

```
          {/* Stated as a fact rather than offered and refused: PUT /api/policy
              will not accept a write to a restart-only field.
```

with

```
          {/* Stated as a fact rather than offered: a restart-only field is
              accepted by the API and takes effect on the next start.
```

`web/src/features/settings/settings-catalog.test.ts`: replace

```
    // Both configure the one shared transport built at startup, and
    // PUT /api/policy refuses a write that touches either.
```

with

```
    // Both configure the one shared transport built at startup, so a save of
    // either waits for a restart and this screen does not offer it yet.
```

`web/src/features/settings/settings-screen.test.tsx`: replace

```
    // connect and first_byte configure the one shared transport built at
    // startup, so PUT /api/policy refuses them — which is exactly why they
    // belong in the read-only view rather than nowhere.
```

with

```
    // connect and first_byte configure the one shared transport built at
    // startup, so a save of either waits for a restart — which is why they
    // belong in the read-only view rather than nowhere.
```

- [ ] **Step 4: Verify nothing broke**

```bash
go build ./... && go test ./... -count=1
cd web && npm run lint && npm test -- --run && cd ..
grep -rni "darkrouter.yaml\|config file\|the file's" internal/ cmd/ --include='*.go'
```

Expected: PASS, and the final grep hits only the deliberate leftover-file warning in `cmd/darkrouter/main.go` and the `-config` no-op flag, both of which name the file on purpose.

- [ ] **Step 5: Commit**

```bash
git add internal/ web/src/features/settings/
git commit -m "$(cat <<'EOF'
docs(comments): drop the file-era wording

Comments describing a file the process no longer reads, and yaml tags
claiming a decoder that no longer exists.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 10: A fixed provider source for tests

**Files:**
- Create: `internal/provider/providertest/source.go`
- Modify: `internal/exec/exec_test.go`, `internal/exec/count_test.go`, `internal/exec/capture_test.go`, `internal/exec/crossdialect_test.go`, `internal/exec/surface_test.go`

**Interfaces:**
- Consumes: `provider.Provider`, `provider.Credential`, `provider.Source`.
- Produces: `providertest.NewSource(ps ...provider.Provider) *providertest.Source`, satisfying `provider.Source`. `Providers` returns the fixed set; `Revision` hashes id, base URL and models the way `YAMLSource.Revision` did, so a caller that caches on it still sees a change when the set changes.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 0 = 3
**Approach:** inline - skip 2: `Revision` is copied from the source being retired, and the migration is the same edit repeated

`provider.YAMLSource` is the last reader of `config.Config.Providers`, which has had no producer since phase 1 deleted the YAML loader. Nine test files still build executors over it, so the source has to be replaced before the field can go. This task does the `internal/exec` half.

The replacement lives in its own package rather than in `internal/provider`, so nothing that ships carries a type only tests use.

- [ ] **Step 1: Write the source**

Create `internal/provider/providertest/source.go`:

```go
// Package providertest supplies a fixed provider set to tests that need one.
//
// It replaces provider.YAMLSource, which read config.Config.Providers -- a
// field with no producer since the configuration moved into the database.
package providertest

import (
	"context"
	"hash/fnv"

	"github.com/darkraise/darkrouter/internal/provider"
)

// Source is a provider.Source over a fixed set.
type Source struct {
	providers []provider.Provider
}

func NewSource(ps ...provider.Provider) *Source { return &Source{providers: ps} }

func (s *Source) Providers(context.Context) ([]provider.Provider, error) {
	return s.providers, nil
}

// Revision changes when the provider set changes, so a caller that caches on
// it behaves the way it does against the SQL source.
func (s *Source) Revision() uint64 {
	h := fnv.New64a()
	for _, p := range s.providers {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		for _, m := range p.Models {
			_, _ = h.Write([]byte(m))
		}
	}
	return h.Sum64()
}

var _ provider.Source = (*Source)(nil)

// Keyed builds a provider with one enabled credential, which is the shape
// every test that used the retired YAML source had: a config provider carried
// exactly one key and no row, so its credential id was empty.
func Keyed(id, kind, baseURL, secret string, models ...string) provider.Provider {
	return provider.Provider{
		ID: id, Kind: kind, BaseURL: baseURL, Models: models,
		Credentials: []provider.Credential{{Secret: secret, Enabled: true}},
	}
}
```

- [ ] **Step 2: Migrate the exec tests**

In each of the five files, replace every `provider.NewYAMLSource(cfgStore)` with a `providertest.NewSource(...)` built from the `config.ProviderConfig` fixtures that fed it, then delete those fixtures from the `config.Config` literal.

The mechanical shape, using `internal/exec/count_test.go:29` as the example. Before:

```go
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{
			ID: "p1", Kind: "openaicompat", BaseURL: srv.URL,
			APIKey: "k", Models: []string{"m"},
		}},
	}
	config.ApplyDefaults(cfg)
	cfgStore := config.NewStoreOf(cfg)
	return New(cfgStore, provider.NewYAMLSource(cfgStore), map[string]adapter.Adapter{...}, deps)
```

After:

```go
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	cfgStore := config.NewStoreOf(cfg)
	src := providertest.NewSource(
		providertest.Keyed("p1", "openaicompat", srv.URL, "k", "m"),
	)
	return New(cfgStore, src, map[string]adapter.Adapter{...}, deps)
```

Carry `Priority` and `Preset` across when a fixture sets them; those are fields on `provider.Provider` with the same names. Add `"github.com/darkraise/darkrouter/internal/provider/providertest"` to each file's imports and drop `"github.com/darkraise/darkrouter/internal/provider"` where nothing else in the file uses it.

- [ ] **Step 3: Run the tests to verify they pass**

```bash
go test ./internal/exec/ -count=1
```

Expected: PASS, with the same set of tests as before. Compare against `git stash`-ing the change and running `go test ./internal/exec/ -v 2>&1 | grep -c '^=== RUN'` if the count is in doubt — no test may be lost in the migration.

- [ ] **Step 4: Prove the migration is faithful**

Pick one migrated test that asserts routing to a named provider, change its `providertest.Keyed` id to something else, run it, confirm it goes red, restore it. A source that returns an empty set would pass nothing, so this checks the fixtures actually arrived.

- [ ] **Step 5: Commit**

```bash
git add internal/provider/providertest/ internal/exec/
git commit -m "$(cat <<'EOF'
test(provider): add a fixed source for tests

The YAML source reads config.Config.Providers, a field with no
producer. The exec tests move off it first.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 11: Move the remaining tests off the YAML source

**Files:**
- Modify: `internal/catalog/orphan_test.go`, `internal/golden/differential_test.go`, `internal/admin/fixtures_test.go`, `internal/server/run_test.go`, `internal/server/server_test.go`, `internal/store/configstore_test.go`
- Delete: the `TestYAMLSourceCarriesThePreset` case in `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: `providertest.NewSource` and `providertest.Keyed` from Task 10.
- Produces: nothing. After this task, `grep -rn "NewYAMLSource\|config.ProviderConfig" --include='*.go' .` hits only the definitions Task 12 deletes.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 0 = 3
**Approach:** inline - skip 2: the same edit Task 10 established, in the remaining packages

- [ ] **Step 1: Find every remaining site**

```bash
grep -rn "NewYAMLSource\|config.ProviderConfig" --include='*.go' .
```

- [ ] **Step 2: Migrate each one**

Apply Task 10's shape. Three need a note:

- `internal/store/configstore_test.go` sets `Providers` on a `config.Config` it hands to `OverlayConfig`. The overlay only replaces aliases, so the field is decoration there: delete it rather than replacing it with a source.
- `internal/server/server_test.go` and `internal/server/run_test.go` build a server whose provider source comes from `server.New`. Check what that path actually reads — if the fixture's `Providers` never reaches a source, delete the field the way the store test does.
- `internal/provider/provider_test.go`'s `TestYAMLSourceCarriesThePreset` tests the type being deleted. Remove the test; `providertest.Keyed` carrying `Preset` is covered wherever a migrated test asserts on it.

- [ ] **Step 3: Run the tests to verify they pass**

```bash
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 4: Prove the migration is faithful**

In `internal/catalog/orphan_test.go`, change the migrated provider's id, run that test, confirm it goes red, restore it.

- [ ] **Step 5: Commit**

```bash
git add internal/
git commit -m "$(cat <<'EOF'
test(provider): move the rest off the YAML source

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 12: Delete the providers block from Config

**Files:**
- Modify: `internal/provider/provider.go`, `internal/config/config.go`, `internal/config/load.go`
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: Tasks 10 and 11, which leave no reader.
- Produces: nothing. `provider.YAMLSource`, `provider.NewYAMLSource`, `config.Config.Providers`, `config.ProviderConfig` and the provider loop in `config.validate` are gone.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: deletion of code with no remaining reader, guarded by the build

The provider loop matters beyond tidiness now. `config.Validate` runs on every save, and its provider rules produce messages that name no registry key — `keyNamedIn` cannot attribute them, so the loader's fallback reverts every key. With no producer for the field, the loop is dead weight that can only misfire. Removing it makes the one validator strictly about settings.

The loop also appends an ambiguity warning ("model %q is offered by …"), which nothing can produce any more; it goes with the loop.

- [ ] **Step 1: Write the failing test**

Add to `internal/config/load_test.go`:

```go
// The one validator is about settings. Providers have their own tables, their
// own encryption and their own endpoints, and a rule here could only produce a
// message the loader cannot attribute to any key -- which reverts all of them.
func TestValidateHasNoProviderRules(t *testing.T) {
	c := &Config{}
	ApplyDefaults(c)
	if err := Validate(c); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", c.Warnings)
	}
}
```

This passes before the change too — it is the pin, not the driver. The driver is the build: after Step 2 nothing compiles until every reference is gone.

- [ ] **Step 2: Delete**

- `internal/provider/provider.go`: remove `YAMLSource`, `NewYAMLSource`, its `Providers` and `Revision` methods, and the `var _ Source = (*YAMLSource)(nil)` line. Drop the `config` and `hash/fnv` imports if nothing else in the file uses them.
- `internal/config/config.go`: remove the `Providers` field from `Config` and the whole `ProviderConfig` type.
- `internal/config/load.go`: in `validate`, remove everything from `seen := make(map[string]bool, len(c.Providers))` through the ambiguity-warning loop, leaving the `ValidateAliases` call as the last thing before `return nil`. Drop `strings` from the imports if `normalizeDomain` no longer needs it — it does, so it stays.

- [ ] **Step 3: Run the tests to verify they pass**

```bash
go build ./... && go vet ./... && go test ./... -count=1
```

Expected: PASS. A compile error names a reader Tasks 10 and 11 missed.

- [ ] **Step 4: Prove the test can fail**

Add `c.Warnings = append(c.Warnings, "x")` to the top of `validate`, run `go test ./internal/config/ -run TestValidateHasNoProviderRules`, confirm it goes red, remove it.

- [ ] **Step 5: Commit**

```bash
git add internal/
git commit -m "$(cat <<'EOF'
refactor(config): delete the providers block

The field has had no producer since the loader went, and its rules
produce messages the load path cannot attribute to any key.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01JFoaahRFwkXb3GoXtvDxA9
EOF
)"
```

---

### Task 13: Verify against the running console

**Files:**
- None. This task changes nothing; it proves the phase.

**Interfaces:**
- Consumes: every task above.
- Produces: nothing.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 1 - coupling 1 - risk 0 = 2
**Approach:** inline - skip 2: the build and deploy procedure is written down in `docs/operations/deploy.md`

CLAUDE.md requires a redeploy after a finished change, and requires looking at the console rather than trusting the suite. This phase changes no rendered text, so what is being checked is that the settings screen still works and that a write now lands — which it could not before this phase, and which is why this machine's `server.public_url` is still unset.

Note for whoever runs this: subagents on this machine cannot launch a browser, so the console check is done by the session that dispatched them.

- [ ] **Step 1: Full suite, both sides**

```bash
go build ./... && go vet ./... && go test ./... -count=1
cd web && npm run lint && npm test -- --run && npm run build && cd ..
```

- [ ] **Step 2: Build and deploy**

Follow "Local build (UAT)" in `docs/operations/deploy.md`. The `compose.uat.yml` overlay is required or the published image is pulled over the local build. The admin port on this machine is 8091.

- [ ] **Step 3: Set the public URL through the API that now exists**

Read the password from `.uat-credentials`, log in (the login needs `Origin` and `Sec-Fetch-Site` headers or CSRF returns 403), then:

```bash
curl -sS -X PUT http://localhost:8091/api/config \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $TOKEN" -H 'Sec-Fetch-Site: same-origin' \
  -b "$COOKIE" \
  -d '{"set":{"server.public_url":"http://192.168.0.250:8090"}}'
```

Expected: `{"valid":true,"restart_required":[]}`. `server.public_url` is hot-reloadable, so no restart is needed and the change is live on the next request.

- [ ] **Step 4: Look at the console**

Log in at http://localhost:8091 and check three screens:

- **Connect** shows `192.168.0.250:8090` rather than the guessed `:18080`. That is the write path working end to end, and it is the state the sqlite CLI workaround existed for.
- **Settings** renders as before: the policy editors save, the source badges read `database` for the keys with rows and `default` for the rest, and Reload config still returns valid.
- **Routing** saves an alias and it survives a reload.

- [ ] **Step 5: Confirm the leftover file is still only a warning**

`data/darkrouter.yaml` is still on disk and must stay ignored. Check the container log for the one-line warning naming it, and confirm `/healthz` reports `config_valid: true`.

- [ ] **Step 6: Report**

State what was checked and what was seen. If a screen is wrong, that is a finding for this phase, not for phase 3.

---

## Self-review

**Spec coverage (§4 and the §6 tests it owes):**

| Spec requirement | Task |
|---|---|
| Take `reloadMu` | 4 |
| `BEGIN IMMEDIATE` | 3 |
| Base config from the rows inside the transaction | 2, 5 |
| Apply the patch, validate the merged whole, commit, publish | 5, 4 |
| One validator; the admin-only retry cap moves into the registry | 1, 7 |
| `aliasTargetsExist` stays admin-side | 6 |
| A restart-only key is accepted and named in the response | 6, 7 |
| A bootstrap key is refused, naming its variable | 4, 5, 6 |
| `PUT /api/policy` becomes a view over the registry | 7 |
| Aliases stay a block operation sharing the transaction | 5, 7 |
| Tri-state writes; an empty value is a `DELETE` | 4, 5 |
| Retire `putPolicyTx` and `policyFields` with the new path | 8 |
| A rejected write commits nothing | 5, 7 |
| Two concurrent writes cannot both commit a broken budget | 5 |
| `proxy_token` is never echoed | 6 |
| Pending restart survives an unrelated save | phase 1, unchanged |

`databaseOwned` and `sourceOf`'s `"environment"` string are the two §4 items deliberately left to phase 3; the reasoning is under "What this phase deliberately does not do".

**Type consistency:** `config.Patch`, `config.RejectedError`, `config.PublishError`, `config.BootstrapVar`, `(*config.Store).SetWriter`, `(*config.Store).Update`, `store.WriteConfig`, `buildConfig`, `configRows(ctx, q)`, `rowsQueryer`, `configField.validate`, `commitConfig`, `restartRequired`, `policyPatch`, `providertest.NewSource`, `providertest.Keyed` — each is defined in exactly one task and used with the same signature everywhere after it.

**Rule S:** every task has `files + spec + coupling <= 3` and `spec <= 2`. No task scores 3 on spec completeness.
