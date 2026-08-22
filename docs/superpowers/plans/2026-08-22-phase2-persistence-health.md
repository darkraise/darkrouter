# Darkrouter Phase 2 — Persistence and Health Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Make Darkrouter's state durable — provider connections, encrypted credentials, the request and attempt log, circuit-breaker health, and usage rollups all live in SQLite and survive a restart.

**Architecture:** One SQLite file, opened as three handles: a single-writer handle for all mutations, a pooled reader that WAL makes safe against it, and a `synchronous=FULL` writer used only for credentials and key rotation. Request logging is fire-and-forget onto a buffered channel drained by a batching writer, so the request path never waits on a disk write. Circuit-breaker state is authoritative in a mutex-guarded in-memory map and persisted asynchronously, because the router will consult it on every request in Phase 3.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (pure Go, keeps `CGO_ENABLED=0`), stdlib `crypto/pbkdf2` and `crypto/aes`, plus the Phase 1 dependencies. No ORM and no migration library; `database/sql` and embedded SQL only.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase2-persistence-health.md` (master design: `docs/superpowers/specs/2026-08-22-darkrouter-design.md`)

## Global Constraints

- Go 1.26. Module path `github.com/darkraise/darkrouter`.
- `CGO_ENABLED=0` everywhere. `modernc.org/sqlite` is chosen precisely so the binary stays static; never substitute `mattn/go-sqlite3`.
- English only in code, comments, commits, and errors.
- Commits are `<type>(<scope>): <subject>`, subject ≤50 chars, imperative, no period.
- No floating point touches money at any layer. Prices are `int64` micro-dollars **per million tokens**; realized cost is `int64` micro-dollars. `cost_micros` is `NULL`, never `0`, until pricing exists for the model — that arrives in Phase 6.
- All connection pragmas are set **in the DSN**, never with a `PRAGMA` statement after opening. A pragma applied to one pooled connection leaves the others with foreign keys silently off.
- Every mutation goes through the single write handle (`MaxOpenConns(1)`). SQLite permits one writer; contending goroutines produce `SQLITE_BUSY` under exactly the load where it hurts most.
- Timestamps stored in SQLite are Unix milliseconds in `INTEGER` columns. `usage_daily.day` is a UTC `YYYY-MM-DD` `TEXT` key.
- Logging must never block, slow, or fail a request. A full channel drops the record and increments a counter.
- Every context is created with `context.WithCancelCause` or `context.WithTimeoutCause`. Health classification reads the cause; without it, a client pressing Ctrl-C trips breakers on healthy providers.
- Migrations are forward-only and each runs in its own transaction. A database whose schema version exceeds the binary's fails startup loudly.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/store/db.go` | Open the three handles, own DSN pragmas, close cleanly |
| `internal/store/migrate.go` | Embedded forward-only migrations, `schema_version` gate |
| `internal/store/migrations/0001_init.sql` | The whole Phase 2 schema |
| `internal/store/keyring.go` | Salt, iteration count, and KDF verifier in `settings` |
| `internal/store/credentials.go` | Encrypted credential rows on the `FULL`-sync handle |
| `internal/store/rotate.go` | Atomic re-encryption of every credential |
| `internal/store/import.go` | First-run import of the YAML `providers:` block |
| `internal/store/log.go` | Request/attempt record types and the async batching writer |
| `internal/store/rollup.go` | Hourly `usage_daily` recomputation |
| `internal/store/retention.go` | Batched pruning plus incremental vacuum |
| `internal/crypto/crypto.go` | PBKDF2 derivation and AES-GCM with row-id AAD |
| `internal/health/breaker.go` | In-memory circuit breaker, ladder, half-open claim |
| `internal/health/retryafter.go` | `Retry-After` parsing in both wire forms |
| `internal/health/persist.go` | Debounced persistence and startup rehydration |
| `internal/provider/sqlsource.go` | `provider.Source` backed by SQLite, credentials decrypted once |
| `cmd/darkrouter/main.go` | Gains the `rotate-key` subcommand |

---

### Task 1: Configuration for cooldown, log retention, and body capture

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/load.go:46-74` (`applyDefaults`), `internal/config/load.go:110-147` (`validate`)
- Test: `internal/config/load_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.CooldownConfig{TripAfter *int, Max time.Duration}` reachable as `cfg.Policy.Cooldown`; `config.LogConfig{Retention time.Duration}` as `cfg.Log`; `config.CaptureConfig{Bodies bool, MaxBytes int64, Retention time.Duration}` as `cfg.Capture`. Consumers read the trip count as `*cfg.Policy.Cooldown.TripAfter`, which is never nil after `Parse` returns.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

These blocks are hot-reloadable. Do not add them to `config.RestartOnly`: every
consumer reads them from a fresh snapshot on each worker tick.

`TripAfter` is a `*int` rather than an `int` so that an explicitly written
`trip_after: 0` is distinguishable from an omitted key. With a plain `int`,
`applyDefaults` turns both into 3 and the operator's nonsensical value is
silently replaced instead of rejected.

- [x] **Step 1: Write the failing test**

Append to `internal/config/load_test.go`:

```go
func TestParseAppliesPhase2Defaults(t *testing.T) {
	c, err := Parse([]byte(minimal), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if *c.Policy.Cooldown.TripAfter != 3 {
		t.Errorf("TripAfter = %d, want 3", *c.Policy.Cooldown.TripAfter)
	}
	if c.Policy.Cooldown.Max != 15*time.Minute {
		t.Errorf("Cooldown.Max = %s, want 15m", c.Policy.Cooldown.Max)
	}
	if c.Log.Retention != 720*time.Hour {
		t.Errorf("Log.Retention = %s, want 720h", c.Log.Retention)
	}
	if c.Capture.Bodies {
		t.Error("Capture.Bodies must default to false")
	}
	if c.Capture.MaxBytes != 256000 {
		t.Errorf("Capture.MaxBytes = %d, want 256000", c.Capture.MaxBytes)
	}
	if c.Capture.Retention != 72*time.Hour {
		t.Errorf("Capture.Retention = %s, want 72h", c.Capture.Retention)
	}
}

func TestParseReadsExplicitPhase2Blocks(t *testing.T) {
	body := minimal + `
policy:
  cooldown: { trip_after: 5, max: 30m }
log:
  retention: 168h
capture:
  bodies: true
  max_bytes: 1024
  retention: 1h
`
	c, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	if *c.Policy.Cooldown.TripAfter != 5 || c.Policy.Cooldown.Max != 30*time.Minute {
		t.Errorf("cooldown = %d/%s", *c.Policy.Cooldown.TripAfter, c.Policy.Cooldown.Max)
	}
	if c.Log.Retention != 168*time.Hour {
		t.Errorf("Log.Retention = %s", c.Log.Retention)
	}
	if !c.Capture.Bodies || c.Capture.MaxBytes != 1024 || c.Capture.Retention != time.Hour {
		t.Errorf("capture = %+v", c.Capture)
	}
}

func TestParseRejectsExplicitZeroTripAfter(t *testing.T) {
	body := minimal + "\npolicy:\n  cooldown: { trip_after: 0 }\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected trip_after: 0 to be rejected")
	}
}

func TestParseRejectsNonPositiveRetention(t *testing.T) {
	body := minimal + "\nlog:\n  retention: -1h\n"
	if _, err := Parse([]byte(body), env(map[string]string{"GROQ_KEY": "sk-x"})); err == nil {
		t.Fatal("expected a negative retention to be rejected")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'Phase2|TripAfter|Retention' -v`
Expected: FAIL — compile error, `c.Policy.Cooldown` and `c.Log` undefined.

- [x] **Step 3: Add the types**

In `internal/config/config.go`, add two fields to `Config`:

```go
type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Providers []ProviderConfig `yaml:"providers"`
	Policy    PolicyConfig     `yaml:"policy"`
	Log       LogConfig        `yaml:"log"`
	Capture   CaptureConfig    `yaml:"capture"`

	// Warnings are non-fatal findings from validation. They are surfaced on
	// /healthz rather than rejecting the document.
	Warnings []string `yaml:"-"`
}
```

Add `Cooldown` to `PolicyConfig` and define the three new structs:

```go
type PolicyConfig struct {
	Cooldown CooldownConfig `yaml:"cooldown"`
	Timeout  TimeoutConfig  `yaml:"timeout"`
}

// CooldownConfig governs the circuit breaker. TripAfter counts consecutive
// failures rather than a rate, because a rate needs a window that a homelab's
// traffic never fills. It is a pointer so that an explicit 0 can be rejected
// rather than silently replaced by the default.
type CooldownConfig struct {
	TripAfter *int          `yaml:"trip_after"`
	Max       time.Duration `yaml:"max"`
}

type LogConfig struct {
	Retention time.Duration `yaml:"retention"`
}

// CaptureConfig controls request and response body capture, off by default
// because bodies carry whatever the user sent.
type CaptureConfig struct {
	Bodies    bool          `yaml:"bodies"`
	MaxBytes  int64         `yaml:"max_bytes"`
	Retention time.Duration `yaml:"retention"`
}
```

- [x] **Step 4: Add the defaults**

In `applyDefaults` in `internal/config/load.go`, append before the closing brace:

```go
	if c.Policy.Cooldown.TripAfter == nil {
		n := 3
		c.Policy.Cooldown.TripAfter = &n
	}
	if c.Policy.Cooldown.Max == 0 {
		c.Policy.Cooldown.Max = 15 * time.Minute
	}
	if c.Log.Retention == 0 {
		c.Log.Retention = 720 * time.Hour
	}
	if c.Capture.MaxBytes == 0 {
		c.Capture.MaxBytes = 256000
	}
	if c.Capture.Retention == 0 {
		c.Capture.Retention = 72 * time.Hour
	}
```

- [x] **Step 5: Add the validation**

In `validate` in `internal/config/load.go`, insert these checks at the very top
of the function, before the provider loop:

```go
	if *c.Policy.Cooldown.TripAfter < 1 {
		return fmt.Errorf("policy.cooldown.trip_after must be at least 1")
	}
	if c.Policy.Cooldown.Max <= 0 {
		return fmt.Errorf("policy.cooldown.max must be positive")
	}
	if c.Log.Retention <= 0 {
		return fmt.Errorf("log.retention must be positive")
	}
	if c.Capture.Retention <= 0 {
		return fmt.Errorf("capture.retention must be positive")
	}
	if c.Capture.MaxBytes < 0 {
		return fmt.Errorf("capture.max_bytes must not be negative")
	}
```

Dereferencing `TripAfter` here is safe: `Parse` calls `applyDefaults` before
`validate`, so the pointer is always set by this point.

- [x] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, including the eight pre-existing tests.

- [x] **Step 7: Update the example configuration**

Replace the `policy:` block in `darkrouter.example.yaml` and append two top-level
blocks, so the shipped example documents every knob:

```yaml
policy:
  cooldown:
    trip_after: 3
    max: 15m
  timeout:
    connect: 10s
    first_byte: 60s
    total: 10m
    idle: 120s

log:
  retention: 720h

capture:
  bodies: false
  max_bytes: 256000
  retention: 72h
```

- [x] **Step 8: Commit**

```bash
git add internal/config/ darkrouter.example.yaml
git commit -m "feat(config): add cooldown, log, and capture blocks"
```

---

### Task 2: Open the database with three handles

**Files:**
- Create: `internal/store/db.go`
- Test: `internal/store/db_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.DB{Write, Read, Sync *sql.DB, Path string}`, `func store.Open(path string) (*DB, error)`, `func (*DB) Close() error`. Every later store task takes a `*store.DB`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Three handles is not premature optimization. SQLite permits exactly one writer,
so every mutation must share one connection or they contend and produce
`SQLITE_BUSY` under load. WAL is what makes a separate reader pool safe against
that writer. The third handle exists because `synchronous=FULL` is the right
durability trade for credentials and the wrong one for a request log.

- [x] **Step 1: Add the SQLite dependency**

```bash
go get modernc.org/sqlite@latest
```

Do **not** run `go mod tidy` here. Nothing imports the driver until Step 4, so
tidy removes the requirement it was just given. Run `go get` again after Step 4
and tidy then. *(Correction applied during execution.)*

`modernc.org/sqlite` is a pure-Go translation of SQLite. It is chosen so
`CGO_ENABLED=0` still produces a static binary, which is what the Dockerfile
depends on. Never substitute `mattn/go-sqlite3`; it needs cgo.

- [x] **Step 2: Write the failing test**

Create `internal/store/db_test.go`:

```go
package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenAppliesPragmasToEveryPooledConnection(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	// Hold several connections open at once so the pool is forced to create
	// more than one. A pragma set with a PRAGMA statement after Open would
	// apply to whichever connection ran it and leave the rest with foreign
	// keys off — a bug that surfaces as missing constraint enforcement rather
	// than as an error.
	const n = 4
	conns := make([]*sql.Conn, 0, n)
	for i := 0; i < n; i++ {
		c, err := db.Read.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	for i, c := range conns {
		var fk int
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			t.Fatal(err)
		}
		if fk != 1 {
			t.Errorf("connection %d: foreign_keys = %d, want 1", i, fk)
		}
		var busy int
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if busy != 5000 {
			t.Errorf("connection %d: busy_timeout = %d, want 5000", i, busy)
		}
	}
}

func TestOpenUsesWALAndIncrementalVacuum(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()

	var mode string
	if err := db.Write.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	// auto_vacuum only takes effect on a database with no tables yet, so this
	// asserts it was set in the DSN of the first connection rather than later.
	var av int
	if err := db.Write.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&av); err != nil {
		t.Fatal(err)
	}
	if av != 2 {
		t.Errorf("auto_vacuum = %d, want 2 (incremental)", av)
	}
}

func TestOpenSerializesWrites(t *testing.T) {
	db := openTest(t)
	if got := db.Write.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("write handle MaxOpenConnections = %d, want 1", got)
	}
	if got := db.Sync.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("sync handle MaxOpenConnections = %d, want 1", got)
	}
}

func TestSyncHandleRunsFullSynchronous(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	var s int
	if err := db.Sync.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != 2 {
		t.Errorf("sync handle synchronous = %d, want 2 (FULL)", s)
	}
	if err := db.Write.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != 1 {
		t.Errorf("write handle synchronous = %d, want 1 (NORMAL)", s)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("second Close returned %v, want nil", err)
	}
}
```

- [x] **Step 3: Run test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL — `undefined: Open`, `undefined: DB`.

- [x] **Step 4: Write the database handles**

Create `internal/store/db.go`:

```go
// Package store owns the SQLite database: its handles, its schema, and the
// background workers that read and write it.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sync"

	_ "modernc.org/sqlite"
)

// DB holds the three handles Phase 2 needs.
//
// Splitting them is a correctness requirement rather than a tuning choice.
// SQLite permits a single writer, so every mutation shares one connection;
// letting many goroutines contend produces SQLITE_BUSY under exactly the load
// where you least want it. WAL is what makes the reader pool safe against that
// writer.
type DB struct {
	// Write carries every mutation except credentials, at synchronous=NORMAL.
	// An OS crash can lose the last few log rows, which costs nothing.
	Write *sql.DB
	// Read is a pool. Queries must be short-lived: a long-running read
	// transaction pins the WAL and blocks checkpointing.
	Read *sql.DB
	// Sync is a second single-writer handle at synchronous=FULL, used only for
	// credential writes and key rotation. Without it, a power loss after a
	// rotation commits but before the WAL syncs rolls the rotation back while
	// the operator has already changed DARKROUTER_MASTER_KEY — and the next
	// startup fails the verifier with no way to tell why.
	Sync *sql.DB

	Path string

	closeOnce sync.Once
	closeErr  error
}

// commonPragmas are applied through the DSN so every pooled connection carries
// them. Setting them once after opening leaves the pool's other connections
// with foreign keys silently off.
//
// auto_vacuum is here rather than in a migration because it only takes effect
// on a database that has no tables yet; applying it later is a no-op and
// retention deletes would then grow the file without bound.
const commonPragmas = "_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(on)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=auto_vacuum(incremental)"

func dsn(path, synchronous string) string {
	// EscapedPath leaves separators intact while escaping the characters the
	// DSN's query string would otherwise consume, notably '?' and '#'.
	// url.PathEscape is wrong here: it escapes '/' too, which breaks every
	// absolute path.
	escaped := (&url.URL{Path: path}).EscapedPath()
	return "file:" + escaped +
		"?" + commonPragmas + "&_pragma=synchronous(" + synchronous + ")"
}

// Open creates the database file if it does not exist and returns all three
// handles. It does not apply migrations; call Migrate.
func Open(path string) (*DB, error) {
	write, err := sql.Open("sqlite", dsn(path, "NORMAL"))
	if err != nil {
		return nil, fmt.Errorf("open write handle: %w", err)
	}
	write.SetMaxOpenConns(1)

	read, err := sql.Open("sqlite", dsn(path, "NORMAL"))
	if err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open read handle: %w", err)
	}
	read.SetMaxOpenConns(4)

	syncH, err := sql.Open("sqlite", dsn(path, "FULL"))
	if err != nil {
		_ = write.Close()
		_ = read.Close()
		return nil, fmt.Errorf("open sync handle: %w", err)
	}
	syncH.SetMaxOpenConns(1)

	db := &DB{Write: write, Read: read, Sync: syncH, Path: path}
	// sql.Open is lazy, so a bad path surfaces here rather than at the first
	// query, where it would be reported as a request failure.
	if err := write.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return db, nil
}

// Close closes all three handles. It is safe to call more than once, because
// the server's shutdown path and a failed Open both reach it.
func (d *DB) Close() error {
	d.closeOnce.Do(func() {
		d.closeErr = errors.Join(d.Sync.Close(), d.Read.Close(), d.Write.Close())
	})
	return d.closeErr
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, five tests.

If `TestOpenUsesWALAndIncrementalVacuum` reports `auto_vacuum = 0`, the pragma
reached a database that already had tables. Confirm no earlier step created the
file before `Open` ran.

- [x] **Step 6: Commit**

```bash
git add go.mod go.sum internal/store/
git commit -m "feat(store): open sqlite with pooled and single-writer handles"
```

---

### Task 3: Embedded forward-only migrations and the Phase 2 schema

**Files:**
- Create: `internal/store/migrate.go`
- Create: `internal/store/migrations/0001_init.sql`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: `store.Open` from Task 2.
- Produces: `func (*DB) Migrate(ctx context.Context) error`. Every table in master design §11 exists after it returns.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Forward-only, each migration in its own transaction, gated by a `schema_version`
row. The recovery story for a single-operator homelab is a file copy, not a
reversible migration.

A database whose version is newer than the binary fails startup loudly. That
happens on a rollback deploy, and running an old binary against a new schema
corrupts quietly.

- [x] **Step 1: Write the failing test**

Create `internal/store/migrate_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func migrated(t *testing.T) *DB {
	t.Helper()
	db := openTest(t)
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestMigrateCreatesEveryTable(t *testing.T) {
	db := migrated(t)
	want := []string{
		"providers", "provider_keys", "models", "model_overrides",
		"requests", "request_attempts", "request_bodies",
		"health", "usage_daily", "sessions", "settings",
	}
	for _, table := range want {
		var name string
		err := db.Read.QueryRowContext(context.Background(),
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Migrate(ctx); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		var v int
		if err := db.Read.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		if v != 1 {
			t.Errorf("run %d: version = %d, want 1", i, v)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	// Simulate a rollback deploy: the file was written by a future binary.
	if _, err := db.Write.ExecContext(ctx, `UPDATE schema_version SET version = 99`); err != nil {
		t.Fatal(err)
	}
	err := db.Migrate(ctx)
	if err == nil {
		t.Fatal("expected a newer schema to be refused")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Errorf("error should name the cause, got %v", err)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	// provider_keys references providers(id); this provider does not exist.
	_, err := db.Write.ExecContext(ctx,
		`INSERT INTO provider_keys (id, provider_id, ciphertext, nonce)
		 VALUES ('k1', 'nope', x'00', x'00')`)
	if err == nil {
		t.Fatal("expected the foreign key to reject an orphan credential")
	}
}

func TestMigrationsAreContiguous(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no migrations were embedded")
	}
	for i, m := range ms {
		if m.version != i+1 {
			t.Errorf("migration %d has version %d, want %d", i, m.version, i+1)
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Migrate -v`
Expected: FAIL — `db.Migrate undefined`.

- [x] **Step 3: Write the schema**

Create `internal/store/migrations/0001_init.sql`:

```sql
-- Phase 2 schema, per master design section 11.
--
-- STRICT is used throughout: without it SQLite accepts a string into an INTEGER
-- column, and a token count silently stored as text breaks the rollup rather
-- than the insert.
--
-- Money is INTEGER micro-dollars. Prices are per million tokens because
-- models.dev publishes dollars per million as floats, and micro-dollars per
-- token would truncate a $0.14/M model to zero.

CREATE TABLE providers (
  id            TEXT    PRIMARY KEY,
  name          TEXT    NOT NULL DEFAULT '',
  preset        TEXT    NOT NULL DEFAULT '',
  kind          TEXT    NOT NULL,
  base_url      TEXT    NOT NULL,
  auth_style    TEXT    NOT NULL DEFAULT 'bearer',
  enabled       INTEGER NOT NULL DEFAULT 1,
  priority      INTEGER NOT NULL DEFAULT 0,
  region        TEXT    NOT NULL DEFAULT '',
  project       TEXT    NOT NULL DEFAULT '',
  location      TEXT    NOT NULL DEFAULT '',
  settings_json TEXT    NOT NULL DEFAULT '{}',
  created_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE provider_keys (
  id           TEXT    PRIMARY KEY,
  provider_id  TEXT    NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  label        TEXT    NOT NULL DEFAULT '',
  kind         TEXT    NOT NULL DEFAULT 'static',
  ciphertext   BLOB    NOT NULL,
  nonce        BLOB    NOT NULL,
  expires_at   INTEGER,
  scope        TEXT    NOT NULL DEFAULT '',
  enabled      INTEGER NOT NULL DEFAULT 1,
  last_used_at INTEGER
) STRICT;

CREATE INDEX idx_provider_keys_provider ON provider_keys(provider_id);

CREATE TABLE models (
  provider_id                      TEXT    NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model_id                         TEXT    NOT NULL,
  publisher                        TEXT    NOT NULL DEFAULT '',
  surfaces                         TEXT    NOT NULL DEFAULT '["chat"]',
  capabilities                     TEXT    NOT NULL DEFAULT '{}',
  capabilities_source              TEXT    NOT NULL DEFAULT 'inferred',
  context_window                   INTEGER,
  max_output_tokens                INTEGER,
  input_price_micros_per_mtok      INTEGER,
  output_price_micros_per_mtok     INTEGER,
  cache_read_price_micros_per_mtok INTEGER,
  discovered_at                    INTEGER,
  state                            TEXT    NOT NULL DEFAULT 'active',
  PRIMARY KEY (provider_id, model_id)
) STRICT;

CREATE TABLE model_overrides (
  provider_id    TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  model_id       TEXT NOT NULL,
  surfaces       TEXT,
  capabilities   TEXT,
  context_window INTEGER,
  PRIMARY KEY (provider_id, model_id)
) STRICT;

-- requests and request_attempts deliberately carry no foreign key onto
-- providers: deleting a provider in the UI must preserve history rather than
-- cascading it away.
CREATE TABLE requests (
  id                 TEXT    PRIMARY KEY,
  ts                 INTEGER NOT NULL,
  dialect            TEXT    NOT NULL,
  surface            TEXT    NOT NULL,
  requested_model    TEXT    NOT NULL,
  resolved_alias     TEXT    NOT NULL DEFAULT '',
  candidates_json    TEXT    NOT NULL DEFAULT '[]',
  final_provider_id  TEXT    NOT NULL DEFAULT '',
  final_model        TEXT    NOT NULL DEFAULT '',
  status             TEXT    NOT NULL,
  tokens_in          INTEGER NOT NULL DEFAULT 0,
  tokens_out         INTEGER NOT NULL DEFAULT 0,
  cache_read_tokens  INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  reasoning_tokens   INTEGER NOT NULL DEFAULT 0,
  -- NULL, never 0, until pricing for the model exists. That arrives in phase 6.
  cost_micros        INTEGER,
  ttft_ms            INTEGER,
  total_ms           INTEGER,
  error_code         TEXT    NOT NULL DEFAULT '',
  warnings_json      TEXT    NOT NULL DEFAULT '[]'
) STRICT;

CREATE INDEX idx_requests_ts ON requests(ts);

CREATE TABLE request_attempts (
  request_id  TEXT    NOT NULL,
  seq         INTEGER NOT NULL,
  provider_id TEXT    NOT NULL,
  key_id      TEXT    NOT NULL DEFAULT '',
  model       TEXT    NOT NULL,
  outcome     TEXT    NOT NULL,
  status_code INTEGER NOT NULL DEFAULT 0,
  latency_ms  INTEGER NOT NULL DEFAULT 0,
  error       TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (request_id, seq)
) STRICT;

CREATE TABLE request_bodies (
  request_id    TEXT    PRIMARY KEY,
  request_json  TEXT    NOT NULL,
  response_json TEXT    NOT NULL DEFAULT '',
  expires_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_request_bodies_expires ON request_bodies(expires_at);

-- Keyed on (provider, credential, model). An empty model is the
-- credential-level entry, used for cooldowns that apply across every model a
-- credential serves.
CREATE TABLE health (
  provider_id          TEXT    NOT NULL,
  key_id               TEXT    NOT NULL DEFAULT '',
  model                TEXT    NOT NULL DEFAULT '',
  cooling_until        INTEGER,
  backoff_level        INTEGER NOT NULL DEFAULT 0,
  consecutive_failures INTEGER NOT NULL DEFAULT 0,
  updated_at           INTEGER NOT NULL,
  PRIMARY KEY (provider_id, key_id, model)
) STRICT;

-- day is a UTC YYYY-MM-DD key on request start. Finalization is idempotent
-- recomputation, so a request spanning midnight lands once, in the day it began.
CREATE TABLE usage_daily (
  day         TEXT    NOT NULL,
  provider_id TEXT    NOT NULL,
  model       TEXT    NOT NULL,
  requests    INTEGER NOT NULL DEFAULT 0,
  tokens_in   INTEGER NOT NULL DEFAULT 0,
  tokens_out  INTEGER NOT NULL DEFAULT 0,
  cost_micros INTEGER,
  PRIMARY KEY (day, provider_id, model)
) STRICT;

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;
```

- [x] **Step 4: Write the migrator**

Create `internal/store/migrate.go`:

```go
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads the embedded files and asserts their versions are
// contiguous from 1. Contiguity is what lets len(migrations) stand in for "the
// newest version this binary understands"; with a gap, a database at version 3
// against files 1 and 4 would look current.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		digits, name, ok := strings.Cut(strings.TrimSuffix(e.Name(), ".sql"), "_")
		if !ok {
			return nil, fmt.Errorf("migration %s: expected <version>_<description>.sql", e.Name())
		}
		v, err := strconv.Atoi(digits)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf(
				"migrations must be contiguous from 1: found version %d at position %d",
				m.version, i+1)
		}
	}
	return out, nil
}

// Migrate applies every migration newer than the recorded version. It is safe
// to call on every startup.
func (d *DB) Migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}

	// schema_version is created by the migrator rather than by a migration,
	// because the version has to be readable before any migration has run.
	if _, err := d.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	err = d.Write.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&current)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := d.Write.ExecContext(ctx,
			`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("seed schema_version: %w", err)
		}
		current = 0
	case err != nil:
		return fmt.Errorf("read schema_version: %w", err)
	}

	if latest := len(ms); current > latest {
		return fmt.Errorf(
			"database schema version %d is newer than this binary understands (%d); "+
				"this is a rollback deploy, and an old binary against a new schema "+
				"corrupts data quietly rather than failing",
			current, latest)
	}

	for _, m := range ms {
		if m.version <= current {
			continue
		}
		if err := d.applyMigration(ctx, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// applyMigration runs one migration and its version bump in a single
// transaction, so an interrupted migration leaves the version unchanged and the
// next startup retries it from a consistent state.
func (d *DB) applyMigration(ctx context.Context, m migration) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE schema_version SET version = ?`, m.version); err != nil {
		return err
	}
	return tx.Commit()
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, ten tests.

- [x] **Step 6: Commit**

```bash
git add internal/store/
git commit -m "feat(store): add forward-only migrations and schema"
```

---

### Task 4: Credential encryption primitives

**Files:**
- Create: `internal/crypto/crypto.go`
- Test: `internal/crypto/crypto_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `crypto.Key`, `func crypto.DeriveKey(master string, salt []byte, iterations int) (*Key, error)`, `func (*Key) Seal(plaintext, aad []byte) (ciphertext, nonce []byte, err error)`, `func (*Key) Open(ciphertext, nonce, aad []byte) ([]byte, error)`, `func crypto.NewSalt() ([]byte, error)`, and the constants `crypto.DefaultIterations` and `crypto.SaltBytes`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`DARKROUTER_MASTER_KEY` → PBKDF2-HMAC-SHA256 → a 32-byte AES key → AES-GCM per
stored credential with a fresh random nonce and the credential row id as
**additional authenticated data**, so a ciphertext cannot be swapped between
rows undetected.

`crypto/pbkdf2` is in the standard library as of Go 1.24. Do not add
`golang.org/x/crypto` for this.

- [x] **Step 1: Write the failing test**

Create `internal/crypto/crypto_test.go`:

```go
package crypto

import (
	"bytes"
	"testing"
)

// testIterations keeps the suite fast. Production uses DefaultIterations.
const testIterations = 1000

func testKey(t *testing.T, master string) *Key {
	t.Helper()
	k, err := DeriveKey(master, []byte("0123456789abcdef"), testIterations)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenRoundTrip(t *testing.T) {
	k := testKey(t, "correct horse battery staple")
	plaintext := []byte("sk-live-abcdef")
	ct, nonce, err := k.Seal(plaintext, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, plaintext) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := k.Open(ct, nonce, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("round trip = %q, want %q", got, plaintext)
	}
}

func TestSealUsesAFreshNonceEveryTime(t *testing.T) {
	k := testKey(t, "master")
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		ct, nonce, err := k.Seal([]byte("same plaintext"), []byte("row-1"))
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(nonce)] {
			t.Fatal("nonce reused; GCM loses all confidentiality on nonce reuse")
		}
		seen[string(nonce)] = true
		if seen[string(ct)] {
			t.Fatal("identical ciphertext for identical plaintext")
		}
		seen[string(ct)] = true
	}
}

// A ciphertext moved from one credential row to another must fail to
// authenticate. Without AAD binding, an attacker with write access to the
// database file could promote a low-privilege key into a high-privilege row.
func TestOpenRejectsACiphertextSwappedBetweenRows(t *testing.T) {
	k := testKey(t, "master")
	ct, nonce, err := k.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Open(ct, nonce, []byte("row-2")); err == nil {
		t.Fatal("expected a different AAD to fail authentication")
	}
}

func TestOpenRejectsTheWrongMasterKey(t *testing.T) {
	good := testKey(t, "right")
	bad := testKey(t, "wrong")
	ct, nonce, err := good.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Open(ct, nonce, []byte("row-1")); err == nil {
		t.Fatal("expected the wrong key to fail authentication")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	k := testKey(t, "master")
	ct, nonce, err := k.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	ct[0] ^= 0xff
	if _, err := k.Open(ct, nonce, []byte("row-1")); err == nil {
		t.Fatal("expected a flipped bit to fail authentication")
	}
}

func TestDeriveKeyIsDeterministic(t *testing.T) {
	salt := []byte("0123456789abcdef")
	a, err := DeriveKey("master", salt, testIterations)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DeriveKey("master", salt, testIterations)
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := a.Seal([]byte("secret"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	// Determinism is what makes a restart able to read yesterday's credentials.
	if _, err := b.Open(ct, nonce, []byte("row-1")); err != nil {
		t.Fatalf("a second derivation could not open the first's output: %v", err)
	}
}

func TestDeriveKeyRejectsEmptyInput(t *testing.T) {
	salt := []byte("0123456789abcdef")
	if _, err := DeriveKey("", salt, testIterations); err == nil {
		t.Error("expected an empty master key to be rejected")
	}
	if _, err := DeriveKey("master", nil, testIterations); err == nil {
		t.Error("expected an empty salt to be rejected")
	}
	if _, err := DeriveKey("master", salt, 0); err == nil {
		t.Error("expected a zero iteration count to be rejected")
	}
}

func TestNewSaltIsRandomAndCorrectLength(t *testing.T) {
	a, err := NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != SaltBytes {
		t.Fatalf("salt length = %d, want %d", len(a), SaltBytes)
	}
	b, err := NewSalt()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("two salts were identical")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/crypto/ -v`
Expected: FAIL — `undefined: DeriveKey`.

- [x] **Step 3: Write the package**

Create `internal/crypto/crypto.go`:

```go
// Package crypto derives the credential-encryption key from the master key and
// seals individual credentials with it.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	// DefaultIterations is deliberately high. The master key may be a human
	// passphrase — nothing forces it to be 32 random bytes — and a low count
	// makes offline attack of a stolen database file cheap. It runs once at
	// startup and once per rotation, so the cost is invisible in operation.
	//
	// The count is stored alongside the salt so it can be raised later without
	// breaking existing databases.
	DefaultIterations = 600000

	// SaltBytes is 16, the size RFC 8018 recommends as a minimum.
	SaltBytes = 16

	keyBytes = 32 // AES-256
)

// Key is a derived encryption key. Derive it once at startup and reuse it;
// deriving per credential would run PBKDF2 on the request path.
type Key struct {
	aead cipher.AEAD
}

// DeriveKey stretches master into an AES-256-GCM key.
func DeriveKey(master string, salt []byte, iterations int) (*Key, error) {
	if master == "" {
		return nil, errors.New("crypto: master key is empty")
	}
	if len(salt) == 0 {
		return nil, errors.New("crypto: salt is empty")
	}
	if iterations < 1 {
		return nil, fmt.Errorf("crypto: iteration count %d is not positive", iterations)
	}
	raw, err := pbkdf2.Key(sha256.New, master, salt, iterations, keyBytes)
	if err != nil {
		return nil, fmt.Errorf("crypto: derive: %w", err)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, fmt.Errorf("crypto: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}
	return &Key{aead: aead}, nil
}

// Seal encrypts plaintext under a fresh random nonce, binding the result to
// aad. Callers pass the credential row id as aad, which is what stops a
// ciphertext being moved from one row to another undetected.
//
// The nonce is returned rather than prepended so it lands in its own column and
// stays legible when someone inspects the database by hand.
func (k *Key) Seal(plaintext, aad []byte) (ciphertext, nonce []byte, err error) {
	nonce = make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	return k.aead.Seal(nil, nonce, plaintext, aad), nonce, nil
}

// Open reverses Seal. A failure here means the master key is wrong, the row was
// tampered with, or a ciphertext was swapped between rows; the three are
// indistinguishable by design, so the message stays generic.
func (k *Key) Open(ciphertext, nonce, aad []byte) ([]byte, error) {
	if len(nonce) != k.aead.NonceSize() {
		return nil, fmt.Errorf("crypto: nonce is %d bytes, want %d", len(nonce), k.aead.NonceSize())
	}
	out, err := k.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, errors.New("crypto: authentication failed")
	}
	return out, nil
}

// NewSalt generates a per-database salt. It is stored in settings on first run.
func NewSalt() ([]byte, error) {
	s := make([]byte, SaltBytes)
	if _, err := rand.Read(s); err != nil {
		return nil, fmt.Errorf("crypto: salt: %w", err)
	}
	return s, nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/crypto/ -race -v`
Expected: PASS, eight tests.

- [x] **Step 5: Commit**

```bash
git add internal/crypto/
git commit -m "feat(crypto): add pbkdf2 derivation and aes-gcm sealing"
```

---

### Task 5: The keyring — salt, iteration count, and KDF verifier

**Files:**
- Create: `internal/store/keyring.go`
- Test: `internal/store/keyring_test.go`

**Interfaces:**
- Consumes: `store.Open` and `(*DB).Migrate` from Tasks 2 and 3; the whole `internal/crypto` package from Task 4.
- Produces: `func store.OpenKeyring(ctx context.Context, d *DB, master string) (*crypto.Key, error)`, plus the unexported `getSetting`/`putSetting` helpers that later store tasks reuse.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The salt and iteration count are generated on first run and stored in
`settings`. Storing the count is what allows raising it later without breaking
existing databases.

The verifier is a known plaintext encrypted under the derived key. It lets
startup detect a wrong or changed master key and fail loudly, rather than
emitting garbled credentials at request time. GCM authentication makes that
detection reliable.

The verifier proves only that the master key is right. It does not prove every
credential row is intact — that surfaces separately, at provider-load time.

- [x] **Step 1: Write the failing test**

Create `internal/store/keyring_test.go`:

```go
package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenKeyringInitializesOnFirstRun(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	k, err := OpenKeyring(ctx, db, "master-passphrase")
	if err != nil {
		t.Fatal(err)
	}
	if k == nil {
		t.Fatal("nil key")
	}
	salt, ok, err := getSetting(ctx, db.Read, settingKDFSalt)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || salt == "" {
		t.Fatal("salt was not persisted")
	}
	iters, ok, err := getSetting(ctx, db.Read, settingKDFIterations)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || iters == "" {
		t.Fatal("iteration count was not persisted")
	}
}

func TestOpenKeyringAcceptsTheSameMasterKeyOnRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	ctx := context.Background()

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	k1, err := OpenKeyring(ctx, first, "master")
	if err != nil {
		t.Fatal(err)
	}
	ct, nonce, err := k1.Seal([]byte("sk-x"), []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	k2, err := OpenKeyring(ctx, second, "master")
	if err != nil {
		t.Fatal(err)
	}
	// The re-derived key must open what the first process sealed, or every
	// credential becomes unreadable across a restart.
	got, err := k2.Open(ct, nonce, []byte("row-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "sk-x" {
		t.Errorf("round trip across restart = %q", got)
	}
}

func TestOpenKeyringRejectsTheWrongMasterKey(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := OpenKeyring(ctx, db, "right"); err != nil {
		t.Fatal(err)
	}
	_, err := OpenKeyring(ctx, db, "wrong")
	if err == nil {
		t.Fatal("expected a wrong master key to be rejected")
	}
	if !strings.Contains(err.Error(), "DARKROUTER_MASTER_KEY") {
		t.Errorf("the message must name the variable to fix, got: %v", err)
	}
}

func TestOpenKeyringRequiresAMasterKey(t *testing.T) {
	db := migrated(t)
	_, err := OpenKeyring(context.Background(), db, "")
	if err == nil {
		t.Fatal("expected an empty master key to be rejected")
	}
	if !strings.Contains(err.Error(), "DARKROUTER_MASTER_KEY") {
		t.Errorf("the message must name the variable to set, got: %v", err)
	}
}

func TestOpenKeyringHonoursTheStoredIterationCount(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := OpenKeyring(ctx, db, "master"); err != nil {
		t.Fatal(err)
	}
	got, _, err := getSetting(ctx, db.Read, settingKDFIterations)
	if err != nil {
		t.Fatal(err)
	}
	// A database written with the default must keep using the default even if
	// the constant is raised in a later release.
	if got != "600000" {
		t.Errorf("iterations = %q, want 600000", got)
	}
}

func TestOpenKeyringReportsAnIncompleteKeyring(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if _, err := OpenKeyring(ctx, db, "master"); err != nil {
		t.Fatal(err)
	}
	// Simulate a partially written keyring: salt present, verifier gone.
	if _, err := db.Write.ExecContext(ctx,
		`DELETE FROM settings WHERE key = ?`, settingKDFVerifierCiphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenKeyring(ctx, db, "master"); err == nil {
		t.Fatal("expected an incomplete keyring to be reported rather than silently re-initialized")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Keyring -v`
Expected: FAIL — `undefined: OpenKeyring`.

- [x] **Step 3: Write the keyring**

Create `internal/store/keyring.go`:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkrouter/internal/crypto"
)

const (
	settingKDFSalt               = "kdf_salt"
	settingKDFIterations         = "kdf_iterations"
	settingKDFVerifierCiphertext = "kdf_verifier_ciphertext"
	settingKDFVerifierNonce      = "kdf_verifier_nonce"
)

// verifierPlaintext is a fixed known value. Encrypting it under the derived key
// and checking it at startup is what turns a wrong master key into a clear
// failure instead of credentials that decrypt into garbage at request time.
const verifierPlaintext = "darkrouter-kdf-verifier-v1"

// verifierAAD binds the verifier to its purpose, so its ciphertext cannot be
// pasted into a credential row.
const verifierAAD = "kdf-verifier"

// queryer is satisfied by both *sql.DB and *sql.Tx, so settings can be read
// inside or outside a transaction.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func getSetting(ctx context.Context, q queryer, key string) (string, bool, error) {
	var v string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %q: %w", key, err)
	}
	return v, true, nil
}

func putSetting(ctx context.Context, e execer, key, value string) error {
	_, err := e.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}

// OpenKeyring derives the credential-encryption key. On first run it generates
// a salt, records the iteration count, and stores a verifier. On every later
// run it re-derives from the stored parameters and checks the verifier.
func OpenKeyring(ctx context.Context, d *DB, master string) (*crypto.Key, error) {
	if master == "" {
		return nil, errors.New(
			"DARKROUTER_MASTER_KEY is not set; it is required from phase 2 onward " +
				"because provider credentials are encrypted at rest")
	}

	saltHex, ok, err := getSetting(ctx, d.Read, settingKDFSalt)
	if err != nil {
		return nil, err
	}
	if !ok {
		return initKeyring(ctx, d, master)
	}

	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, fmt.Errorf("stored kdf salt is not valid hex: %w", err)
	}
	itersRaw, ok, err := getSetting(ctx, d.Read, settingKDFIterations)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New(
			"the kdf salt is present but the iteration count is missing; " +
				"this database's keyring is incomplete and cannot be derived from")
	}
	iterations, err := strconv.Atoi(itersRaw)
	if err != nil {
		return nil, fmt.Errorf("stored kdf iteration count is not a number: %w", err)
	}

	key, err := crypto.DeriveKey(master, salt, iterations)
	if err != nil {
		return nil, err
	}
	if err := checkVerifier(ctx, d, key); err != nil {
		return nil, err
	}
	return key, nil
}

func checkVerifier(ctx context.Context, d *DB, key *crypto.Key) error {
	ctHex, okCT, err := getSetting(ctx, d.Read, settingKDFVerifierCiphertext)
	if err != nil {
		return err
	}
	nonceHex, okNonce, err := getSetting(ctx, d.Read, settingKDFVerifierNonce)
	if err != nil {
		return err
	}
	if !okCT || !okNonce {
		return errors.New(
			"the kdf verifier is missing; this database's keyring is incomplete, " +
				"so a wrong DARKROUTER_MASTER_KEY could not be detected")
	}
	ciphertext, err := hex.DecodeString(ctHex)
	if err != nil {
		return fmt.Errorf("stored kdf verifier is not valid hex: %w", err)
	}
	nonce, err := hex.DecodeString(nonceHex)
	if err != nil {
		return fmt.Errorf("stored kdf verifier nonce is not valid hex: %w", err)
	}

	plaintext, err := key.Open(ciphertext, nonce, []byte(verifierAAD))
	if err != nil || string(plaintext) != verifierPlaintext {
		return errors.New(
			"DARKROUTER_MASTER_KEY does not match this database; " +
				"every stored credential was encrypted under a different key. " +
				"Restore the original value, or run 'darkrouter rotate-key' with the old one")
	}
	return nil
}

// initKeyring runs on first use. It writes the salt, the iteration count, and
// the verifier in one transaction on the FULL-sync handle: a half-written
// keyring would be indistinguishable from a wrong master key on the next start.
func initKeyring(ctx context.Context, d *DB, master string) (*crypto.Key, error) {
	salt, err := crypto.NewSalt()
	if err != nil {
		return nil, err
	}
	key, err := crypto.DeriveKey(master, salt, crypto.DefaultIterations)
	if err != nil {
		return nil, err
	}
	ciphertext, nonce, err := key.Seal([]byte(verifierPlaintext), []byte(verifierAAD))
	if err != nil {
		return nil, err
	}

	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin keyring init: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, kv := range [][2]string{
		{settingKDFSalt, hex.EncodeToString(salt)},
		{settingKDFIterations, strconv.Itoa(crypto.DefaultIterations)},
		{settingKDFVerifierCiphertext, hex.EncodeToString(ciphertext)},
		{settingKDFVerifierNonce, hex.EncodeToString(nonce)},
	} {
		if err := putSetting(ctx, tx, kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit keyring init: %w", err)
	}
	return key, nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, sixteen tests.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): derive and verify the credential key"
```

---

### Task 6: Encrypted credential rows

**Files:**
- Create: `internal/store/credentials.go`
- Test: `internal/store/credentials_test.go`

**Interfaces:**
- Consumes: `store.OpenKeyring` and the `execer`/`queryer` helpers from Task 5.
- Produces: `store.Credential{ID, ProviderID, Label, Kind, Secret string, Enabled bool, Scope string}`, `func (*DB) AddCredential(ctx, key *crypto.Key, c Credential) (string, error)`, `func (*DB) Credentials(ctx, key *crypto.Key, providerID string) ([]Credential, error)`, `func newID() string`, and the transaction-scoped `insertCredentialTx` that Task 9's import reuses.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The row id is the AAD, so the id must be generated before the secret is sealed.
Sealing first and assigning an id afterwards would bind the ciphertext to
nothing.

- [x] **Step 1: Write the failing test**

Create `internal/store/credentials_test.go`:

```go
package store

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func seededProvider(t *testing.T, db *DB, id string) {
	t.Helper()
	_, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES (?, 'openaicompat', 'https://x', 0)`, id)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")

	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "groq", Label: "primary", Secret: "sk-secret-value", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("no id returned")
	}

	got, err := db.Credentials(ctx, key, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d credentials, want 1", len(got))
	}
	if got[0].Secret != "sk-secret-value" {
		t.Errorf("secret = %q", got[0].Secret)
	}
	if got[0].ID != id || got[0].Label != "primary" || !got[0].Enabled {
		t.Errorf("credential = %+v", got[0])
	}
}

// A done criterion: credentials must be unreadable in the raw database file.
func TestCredentialIsNotReadableInTheDatabaseFile(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	const secret = "sk-plaintext-must-not-appear"
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "groq", Secret: secret, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	// Checkpoint so the row is in the main database file rather than only the WAL.
	if _, err := db.Write.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(db.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatal("the plaintext secret appears in the database file")
	}
}

// A done criterion: a swapped ciphertext must fail to decrypt.
func TestSwappedCiphertextFailsToDecrypt(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	idA, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "aaa", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	idB, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "bbb", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// Move A's sealed bytes into B's row. Both are valid ciphertexts under the
	// same key, so only the AAD binding can catch this.
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE provider_keys SET ciphertext = (SELECT ciphertext FROM provider_keys WHERE id = ?),
		                          nonce      = (SELECT nonce FROM provider_keys WHERE id = ?)
		 WHERE id = ?`, idA, idA, idB); err != nil {
		t.Fatal(err)
	}

	_, err = db.Credentials(ctx, key, "groq")
	if err == nil {
		t.Fatal("expected a swapped ciphertext to be rejected")
	}
	if !strings.Contains(err.Error(), idB) {
		t.Errorf("the error should name the offending row, got: %v", err)
	}
}

func TestCredentialsFailsUnderTheWrongKey(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "sk", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// Derive a key that never wrote anything here.
	other, err := deriveForTest(ctx, db, "different-master")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Credentials(ctx, other, "groq"); err == nil {
		t.Fatal("expected decryption under a foreign key to fail")
	}
}

func TestCredentialsAreOrderedAndScopedToTheProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	seededProvider(t, db, "other")
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "groq", Secret: "a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{ProviderID: "other", Secret: "b", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Credentials(ctx, key, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Secret != "a" {
		t.Fatalf("expected only groq's credential, got %+v", got)
	}
}
```

Add this helper to `internal/store/keyring_test.go` — it derives a key from the
database's stored salt without touching the verifier, which is the only way to
build a "wrong key" that is still structurally valid:

```go
func deriveForTest(ctx context.Context, d *DB, master string) (*crypto.Key, error) {
	saltHex, _, err := getSetting(ctx, d.Read, settingKDFSalt)
	if err != nil {
		return nil, err
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return nil, err
	}
	itersRaw, _, err := getSetting(ctx, d.Read, settingKDFIterations)
	if err != nil {
		return nil, err
	}
	iterations, err := strconv.Atoi(itersRaw)
	if err != nil {
		return nil, err
	}
	return crypto.DeriveKey(master, salt, iterations)
}
```

Its imports are `encoding/hex`, `strconv`, and `github.com/darkraise/darkrouter/internal/crypto`.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Credential -v`
Expected: FAIL — `db.AddCredential undefined`.

- [x] **Step 3: Write the credential store**

Create `internal/store/credentials.go`:

```go
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkrouter/internal/crypto"
)

// Credential is a provider credential. Secret is plaintext and exists only in
// memory: it is sealed on the way in and unsealed on the way out.
type Credential struct {
	ID         string
	ProviderID string
	Label      string
	Kind       string
	Secret     string
	Enabled    bool
	Scope      string
}

// newID returns a ULID. Application-generated ids matter here for the same
// reason they do for requests: the id must exist before the row does, because
// it is the AAD the ciphertext is bound to.
func newID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// AddCredential seals and inserts one credential on the FULL-sync handle.
func (d *DB) AddCredential(ctx context.Context, key *crypto.Key, c Credential) (string, error) {
	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin credential insert: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	id, err := insertCredentialTx(ctx, tx, key, c)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit credential insert: %w", err)
	}
	return id, nil
}

// insertCredentialTx seals and inserts within a caller-provided transaction, so
// the first-run import can write every provider and credential atomically.
func insertCredentialTx(ctx context.Context, e execer, key *crypto.Key, c Credential) (string, error) {
	if c.Secret == "" {
		return "", fmt.Errorf("credential for provider %q has an empty secret", c.ProviderID)
	}
	id := c.ID
	if id == "" {
		id = newID()
	}
	kind := c.Kind
	if kind == "" {
		kind = "static"
	}

	// The id is the additional authenticated data, which is what stops this
	// ciphertext being moved to another row undetected.
	ciphertext, nonce, err := key.Seal([]byte(c.Secret), []byte(id))
	if err != nil {
		return "", err
	}

	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err = e.ExecContext(ctx,
		`INSERT INTO provider_keys (id, provider_id, label, kind, ciphertext, nonce, scope, enabled)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, c.ProviderID, c.Label, kind, ciphertext, nonce, c.Scope, enabled)
	if err != nil {
		return "", fmt.Errorf("insert credential for provider %q: %w", c.ProviderID, err)
	}
	return id, nil
}

// Credentials returns every credential for a provider with its secret
// decrypted. A row that fails authentication is an error rather than a skip:
// silently dropping it would present as a provider with no credentials, which
// is indistinguishable from one that was never configured.
func (d *DB) Credentials(ctx context.Context, key *crypto.Key, providerID string) ([]Credential, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, provider_id, label, kind, ciphertext, nonce, scope, enabled
		   FROM provider_keys
		  WHERE provider_id = ?
		  ORDER BY id`, providerID)
	if err != nil {
		return nil, fmt.Errorf("list credentials for %q: %w", providerID, err)
	}
	defer rows.Close()

	var out []Credential
	for rows.Next() {
		var (
			c          Credential
			ciphertext []byte
			nonce      []byte
			enabled    int
		)
		if err := rows.Scan(&c.ID, &c.ProviderID, &c.Label, &c.Kind,
			&ciphertext, &nonce, &c.Scope, &enabled); err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		plaintext, err := key.Open(ciphertext, nonce, []byte(c.ID))
		if err != nil {
			return nil, fmt.Errorf("credential %s on provider %q could not be decrypted: %w",
				c.ID, providerID, err)
		}
		c.Secret = string(plaintext)
		c.Enabled = enabled == 1
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credentials: %w", err)
	}
	return out, nil
}

// allCredentialRows returns every sealed row across all providers. Rotation
// needs the raw bytes rather than the plaintext, so it does not share
// Credentials' decryption path.
func allCredentialRows(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}) ([]sealedRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, ciphertext, nonce FROM provider_keys ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list sealed credentials: %w", err)
	}
	defer rows.Close()

	var out []sealedRow
	for rows.Next() {
		var r sealedRow
		if err := rows.Scan(&r.id, &r.ciphertext, &r.nonce); err != nil {
			return nil, fmt.Errorf("scan sealed credential: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type sealedRow struct {
	id         string
	ciphertext []byte
	nonce      []byte
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, twenty-one tests.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): seal and load provider credentials"
```

---

### Task 7: Atomic master-key rotation and the rotate-key subcommand

**Files:**
- Create: `internal/store/rotate.go`
- Test: `internal/store/rotate_test.go`
- Modify: `cmd/darkrouter/main.go`

**Interfaces:**
- Consumes: `allCredentialRows`, `sealedRow`, `putSetting`, and the verifier constants from Tasks 5 and 6.
- Produces: `func store.RotateMasterKey(ctx context.Context, d *DB, oldKey *crypto.Key, newMaster string) error`, and the `darkrouter rotate-key` subcommand.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Rotation is a CLI subcommand, not an API endpoint, because it needs both the old
and new keys simultaneously — which an endpoint authenticated by the running
process cannot supply.

Everything happens in one `synchronous=FULL` transaction. A crash mid-rotation
rolls back; credentials are never half-rotated. Half-rotated credentials would
be unrecoverable, because neither key opens the whole set.

- [x] **Step 1: Write the failing test**

Create `internal/store/rotate_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"
)

func TestRotateReEncryptsEveryCredential(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	seededProvider(t, db, "other")
	if _, err := db.AddCredential(ctx, oldKey, Credential{ProviderID: "groq", Secret: "sk-a", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, oldKey, Credential{ProviderID: "other", Secret: "sk-b", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	if err := RotateMasterKey(ctx, db, oldKey, "new-master"); err != nil {
		t.Fatal(err)
	}

	// The new master must now open the keyring and every credential.
	newKey, err := OpenKeyring(ctx, db, "new-master")
	if err != nil {
		t.Fatalf("new master key rejected after rotation: %v", err)
	}
	got, err := db.Credentials(ctx, newKey, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Secret != "sk-a" {
		t.Errorf("groq credential after rotation = %+v", got)
	}
	got, err = db.Credentials(ctx, newKey, "other")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Secret != "sk-b" {
		t.Errorf("other credential after rotation = %+v", got)
	}

	// The old master key must no longer open the database.
	if _, err := OpenKeyring(ctx, db, "old-master"); err == nil {
		t.Error("the old master key still opens the database after rotation")
	}
}

func TestInterruptedRotationRollsBack(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	seededProvider(t, db, "groq")
	for _, s := range []string{"sk-a", "sk-b", "sk-c"} {
		if _, err := db.AddCredential(ctx, oldKey, Credential{ProviderID: "groq", Secret: s, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}

	boom := errors.New("simulated crash")
	err = rotateWithHook(ctx, db, oldKey, "new-master", func(i int) error {
		if i == 1 { // fail after the second credential is re-sealed
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected the injected failure, got %v", err)
	}

	// Nothing may have changed: the old key must still open the keyring and
	// all three credentials, or a crash would leave them unrecoverable.
	if _, err := OpenKeyring(ctx, db, "old-master"); err != nil {
		t.Fatalf("old master key no longer works after a rolled-back rotation: %v", err)
	}
	got, err := db.Credentials(ctx, oldKey, "groq")
	if err != nil {
		t.Fatalf("credentials unreadable after a rolled-back rotation: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d credentials, want 3", len(got))
	}
	if _, err := OpenKeyring(ctx, db, "new-master"); err == nil {
		t.Error("the new master key works despite the rotation being rolled back")
	}
}

func TestRotateRejectsAnEmptyNewKey(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	if err := RotateMasterKey(ctx, db, oldKey, ""); err == nil {
		t.Fatal("expected an empty new master key to be rejected")
	}
}

func TestRotateWithNoCredentialsStillRewritesTheVerifier(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	oldKey, err := OpenKeyring(ctx, db, "old-master")
	if err != nil {
		t.Fatal(err)
	}
	if err := RotateMasterKey(ctx, db, oldKey, "new-master"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenKeyring(ctx, db, "new-master"); err != nil {
		t.Errorf("verifier was not rotated on an empty credential set: %v", err)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Rotat -v`
Expected: FAIL — `undefined: RotateMasterKey`.

- [x] **Step 3: Write the rotation**

Create `internal/store/rotate.go`:

```go
package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkrouter/internal/crypto"
)

// RotateMasterKey re-encrypts every credential and rewrites the verifier under
// a key derived from newMaster with a fresh salt.
//
// A fresh salt matters: reusing the old one would leave two databases derived
// from related material, and the salt costs nothing to regenerate.
func RotateMasterKey(ctx context.Context, d *DB, oldKey *crypto.Key, newMaster string) error {
	return rotateWithHook(ctx, d, oldKey, newMaster, nil)
}

// rotateWithHook is RotateMasterKey with an injection point after each
// credential is re-sealed, so a test can prove that an interruption rolls back.
func rotateWithHook(ctx context.Context, d *DB, oldKey *crypto.Key, newMaster string,
	afterEach func(i int) error) error {

	if newMaster == "" {
		return errors.New("the new master key is empty")
	}
	salt, err := crypto.NewSalt()
	if err != nil {
		return err
	}
	newKey, err := crypto.DeriveKey(newMaster, salt, crypto.DefaultIterations)
	if err != nil {
		return err
	}

	// The FULL-sync handle is required here. Without it, a power loss after the
	// commit but before the WAL syncs rolls the rotation back while the operator
	// has already changed DARKROUTER_MASTER_KEY, and the next startup fails the
	// verifier with no way to tell why.
	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin rotation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	rows, err := allCredentialRows(ctx, tx)
	if err != nil {
		return err
	}
	for i, r := range rows {
		plaintext, err := oldKey.Open(r.ciphertext, r.nonce, []byte(r.id))
		if err != nil {
			return fmt.Errorf("credential %s could not be decrypted with the current key; "+
				"rotation aborted and nothing was changed: %w", r.id, err)
		}
		ciphertext, nonce, err := newKey.Seal(plaintext, []byte(r.id))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE provider_keys SET ciphertext = ?, nonce = ? WHERE id = ?`,
			ciphertext, nonce, r.id); err != nil {
			return fmt.Errorf("re-encrypt credential %s: %w", r.id, err)
		}
		if afterEach != nil {
			if err := afterEach(i); err != nil {
				return err
			}
		}
	}

	verifierCT, verifierNonce, err := newKey.Seal([]byte(verifierPlaintext), []byte(verifierAAD))
	if err != nil {
		return err
	}
	for _, kv := range [][2]string{
		{settingKDFSalt, hex.EncodeToString(salt)},
		{settingKDFIterations, strconv.Itoa(crypto.DefaultIterations)},
		{settingKDFVerifierCiphertext, hex.EncodeToString(verifierCT)},
		{settingKDFVerifierNonce, hex.EncodeToString(verifierNonce)},
	} {
		if err := putSetting(ctx, tx, kv[0], kv[1]); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit rotation: %w", err)
	}
	return nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, twenty-five tests.

- [x] **Step 5: Add the subcommand**

Rewrite `cmd/darkrouter/main.go` so a subcommand is dispatched before the
server's flags are parsed:

```go
// Command darkrouter runs the gateway.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/server"
	"github.com/darkraise/darkrouter/internal/store"
)

func main() {
	// Subcommands are dispatched before flag.Parse, which would otherwise
	// reject the bare verb as an unknown flag.
	if len(os.Args) > 1 && os.Args[1] == "rotate-key" {
		if err := runRotateKey(os.Args[2:]); err != nil {
			log.Fatalf("rotate-key: %v", err)
		}
		return
	}
	if err := runServer(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("darkrouter", flag.ExitOnError)
	path := fs.String("config", "darkrouter.yaml", "path to the configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store, err := config.NewStore(*path, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := store.Current()
	log.Printf("darkrouter %s listening: proxy %s admin %s",
		server.Version, cfg.Server.ProxyListen, cfg.Server.AdminListen)
	for _, w := range cfg.Warnings {
		log.Printf("config warning: %s", w)
	}

	if err := server.New(store).Run(ctx); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	log.Print("darkrouter stopped")
	return nil
}

// runRotateKey re-encrypts every credential under a new master key. The old key
// comes from the environment and the new one from stdin, because rotation needs
// both at once and only a CLI can hold both.
func runRotateKey(args []string) error {
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	dbPath := fs.String("db", "darkrouter.db", "path to the database file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	oldMaster := os.Getenv("DARKROUTER_MASTER_KEY")
	if oldMaster == "" {
		return errors.New("DARKROUTER_MASTER_KEY must hold the current master key")
	}

	fmt.Fprint(os.Stderr, "New master key: ")
	newMaster, err := readLine(os.Stdin)
	if err != nil {
		return err
	}
	if newMaster == "" {
		return errors.New("the new master key is empty")
	}
	if newMaster == oldMaster {
		return errors.New("the new master key is identical to the current one")
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	oldKey, err := store.OpenKeyring(ctx, db, oldMaster)
	if err != nil {
		return err
	}
	if err := store.RotateMasterKey(ctx, db, oldKey, newMaster); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr,
		"Rotation complete. Set DARKROUTER_MASTER_KEY to the new value before restarting.")
	return nil
}

func readLine(f *os.File) (string, error) {
	s := bufio.NewScanner(f)
	if !s.Scan() {
		if err := s.Err(); err != nil {
			return "", err
		}
		return "", errors.New("no input on stdin")
	}
	return strings.TrimRight(s.Text(), "\r\n"), nil
}
```

Note the shadowing trap: the local variable `store` in `runServer` holds a
`*config.Store` while the package `store` is also imported. Rename the local to
`cfgStore` and update its three uses.

- [x] **Step 6: Verify the subcommand builds and reports a missing key**

```bash
go build ./cmd/darkrouter
DARKROUTER_MASTER_KEY= ./darkrouter rotate-key -db /tmp/nonexistent.db
```
Expected: exits non-zero with `rotate-key: DARKROUTER_MASTER_KEY must hold the current master key`.

- [x] **Step 7: Commit**

```bash
git add internal/store/ cmd/darkrouter/
git commit -m "feat(store): add atomic master-key rotation"
```

---

### Task 8: A SQLite-backed provider source

**Files:**
- Create: `internal/provider/sqlsource.go`
- Modify: `internal/provider/provider.go` (add `KeyID` to `Provider`)
- Test: `internal/provider/sqlsource_test.go`

**Interfaces:**
- Consumes: `store.DB`, `store.OpenKeyring`, `(*DB).Credentials` from Tasks 2, 5, and 6.
- Produces: `provider.Provider.KeyID string`, `func provider.NewSQLSource(db *store.DB, key *crypto.Key) *SQLSource`, `func (*SQLSource) Reload(ctx context.Context) error`, and the `provider.Source` implementation.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Credentials are decrypted once when the provider set is loaded and held in
memory, not per request. A ciphertext that fails authentication therefore
surfaces at load time as a provider-level error rather than as a mysterious
request failure.

`Provider` gains `KeyID` because the circuit breaker is keyed on
`(provider_id, key_id, model)`. Phase 2 still selects one credential per
provider — the first enabled one, by id — because choosing among credentials is
Phase 3's attempt loop. `KeyID` is what lets Phase 2 record health against the
right triple anyway, so Phase 3 inherits correct state rather than a blank slate.

- [x] **Step 1: Write the failing test**

Create `internal/provider/sqlsource_test.go`:

```go
package provider

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/store"
)

func newTestDB(t *testing.T) (*store.DB, *crypto.Key) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	return db, key
}

func seed(t *testing.T, db *store.DB, key *crypto.Key, id string, priority int, enabled bool, models ...string) string {
	t.Helper()
	ctx := context.Background()
	on := 0
	if enabled {
		on = 1
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, priority, enabled, created_at)
		 VALUES (?, 'openaicompat', ?, ?, ?, 0)`,
		id, "https://"+id+".example/v1", priority, on); err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO models (provider_id, model_id) VALUES (?, ?)`, id, m); err != nil {
			t.Fatal(err)
		}
	}
	keyID, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: id, Secret: "sk-" + id, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return keyID
}

func TestSQLSourceLoadsProvidersWithDecryptedCredentials(t *testing.T) {
	db, key := newTestDB(t)
	keyID := seed(t, db, key, "groq", 10, true, "model-a", "model-b")

	src := NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ps, err := src.Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d providers, want 1", len(ps))
	}
	p := ps[0]
	if p.ID != "groq" || p.APIKey != "sk-groq" || p.KeyID != keyID {
		t.Errorf("provider = %+v", p)
	}
	if p.Priority != 10 || p.BaseURL != "https://groq.example/v1" {
		t.Errorf("provider = %+v", p)
	}
	if len(p.Models) != 2 || p.Models[0] != "model-a" || p.Models[1] != "model-b" {
		t.Errorf("models = %v", p.Models)
	}
}

func TestSQLSourceSkipsDisabledProviders(t *testing.T) {
	db, key := newTestDB(t)
	seed(t, db, key, "on", 0, true, "m")
	seed(t, db, key, "off", 0, false, "m")

	src := NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(context.Background())
	if len(ps) != 1 || ps[0].ID != "on" {
		t.Fatalf("got %+v, want only the enabled provider", ps)
	}
}

// A provider with no usable credential cannot serve. Including it would make
// every request against it fail at the transport layer instead of being skipped.
func TestSQLSourceSkipsProvidersWithNoEnabledCredential(t *testing.T) {
	db, key := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('bare', 'openaicompat', 'https://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('bare', 'm')`); err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps) != 0 {
		t.Fatalf("got %+v, want no providers", ps)
	}
}

func TestSQLSourceReportsAnUndecryptableCredential(t *testing.T) {
	db, key := newTestDB(t)
	keyID := seed(t, db, key, "groq", 0, true, "m")
	// Corrupt the ciphertext. This must fail loudly at load time, not present
	// as a provider that mysteriously fails every request.
	if _, err := db.Write.ExecContext(context.Background(),
		`UPDATE provider_keys SET ciphertext = x'deadbeef' WHERE id = ?`, keyID); err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	err := src.Reload(context.Background())
	if err == nil {
		t.Fatal("expected a corrupt credential to fail the load")
	}
	if !strings.Contains(err.Error(), "groq") {
		t.Errorf("the error should name the provider, got: %v", err)
	}
}

func TestSQLSourceRevisionChangesWithTheProviderSet(t *testing.T) {
	db, key := newTestDB(t)
	seed(t, db, key, "groq", 0, true, "m")
	ctx := context.Background()

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	before := src.Revision()

	// A reload with no change must keep the revision stable, or every consumer
	// cache is invalidated on every reload.
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if src.Revision() != before {
		t.Error("revision changed without a change to the provider set")
	}

	seed(t, db, key, "second", 0, true, "m2")
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if src.Revision() == before {
		t.Error("revision did not change after a provider was added")
	}
}

func TestSQLSourceProvidersBeforeReloadIsEmptyNotNil(t *testing.T) {
	db, key := newTestDB(t)
	src := NewSQLSource(db, key)
	ps, err := src.Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ps == nil {
		t.Fatal("Providers must return an empty slice, not nil, before the first Reload")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/provider/ -run SQLSource -v`
Expected: FAIL — `undefined: NewSQLSource`.

- [x] **Step 3: Add KeyID to Provider**

In `internal/provider/provider.go`, add one field:

```go
type Provider struct {
	ID      string
	Kind    string
	BaseURL string
	APIKey  string
	// KeyID identifies the credential row the APIKey came from. The circuit
	// breaker is keyed on (provider_id, key_id, model), so health recorded in
	// phase 2 stays valid once phase 3 starts choosing among credentials.
	// YAMLSource leaves it empty: config credentials have no row.
	KeyID    string
	Priority int
	Models   []string
}
```

- [x] **Step 4: Write the source**

Create `internal/provider/sqlsource.go`:

```go
package provider

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"sync"

	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/store"
)

// SQLSource serves providers from SQLite. The set is loaded and decrypted once
// by Reload and held in memory, because Providers is on the request path and
// running PBKDF2-derived decryption per request would be absurd.
type SQLSource struct {
	db  *store.DB
	key *crypto.Key

	mu        sync.RWMutex
	providers []Provider
	rev       uint64
}

func NewSQLSource(db *store.DB, key *crypto.Key) *SQLSource {
	// providers starts as an empty slice rather than nil so Providers has a
	// sane answer before the first Reload.
	return &SQLSource{db: db, key: key, providers: []Provider{}}
}

// Reload re-reads the provider set. It replaces the cache only on full success,
// so a failure leaves the previous set live — the same rule the config store
// applies to a broken edit.
func (s *SQLSource) Reload(ctx context.Context) error {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT id, kind, base_url, priority
		   FROM providers
		  WHERE enabled = 1
		  ORDER BY priority DESC, id`)
	if err != nil {
		return fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	type row struct {
		id, kind, baseURL string
		priority          int
	}
	var raw []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.kind, &r.baseURL, &r.priority); err != nil {
			return fmt.Errorf("scan provider: %w", err)
		}
		raw = append(raw, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate providers: %w", err)
	}

	out := make([]Provider, 0, len(raw))
	for _, r := range raw {
		creds, err := s.db.Credentials(ctx, s.key, r.id)
		if err != nil {
			return fmt.Errorf("provider %q: %w", r.id, err)
		}
		chosen, ok := firstEnabled(creds)
		if !ok {
			// A provider with no usable credential cannot serve. Skipping it
			// here is what stops every request against it failing at the
			// transport layer instead.
			continue
		}
		models, err := s.models(ctx, r.id)
		if err != nil {
			return err
		}
		out = append(out, Provider{
			ID: r.id, Kind: r.kind, BaseURL: r.baseURL,
			APIKey: chosen.Secret, KeyID: chosen.ID,
			Priority: r.priority, Models: models,
		})
	}

	rev := revisionOf(out)
	s.mu.Lock()
	s.providers, s.rev = out, rev
	s.mu.Unlock()
	return nil
}

func (s *SQLSource) models(ctx context.Context, providerID string) ([]string, error) {
	rows, err := s.db.Read.QueryContext(ctx,
		`SELECT model_id FROM models WHERE provider_id = ? AND state = 'active' ORDER BY model_id`,
		providerID)
	if err != nil {
		return nil, fmt.Errorf("list models for %q: %w", providerID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func firstEnabled(creds []store.Credential) (store.Credential, bool) {
	// Credentials arrive ordered by id, which for ULIDs is insertion order.
	// Phase 3 replaces this with a loop over all of them.
	for _, c := range creds {
		if c.Enabled {
			return c, true
		}
	}
	return store.Credential{}, false
}

// Providers returns the cached set. The slice is never mutated after Reload
// builds it, so returning it directly is safe.
func (s *SQLSource) Providers(context.Context) ([]Provider, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.providers, nil
}

// Revision changes when the provider set changes, so callers can cache.
func (s *SQLSource) Revision() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rev
}

// revisionOf hashes the identity of the set rather than a counter, so two
// reloads that read the same rows produce the same revision and consumer caches
// survive a no-op reload.
func revisionOf(ps []Provider) uint64 {
	sorted := make([]Provider, len(ps))
	copy(sorted, ps)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].ID < sorted[b].ID })

	h := fnv.New64a()
	for _, p := range sorted {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		_, _ = h.Write([]byte(p.KeyID))
		_, _ = h.Write([]byte(strconv.Itoa(p.Priority)))
		for _, m := range p.Models {
			_, _ = h.Write([]byte(m))
		}
	}
	return h.Sum64()
}

var _ Source = (*SQLSource)(nil)
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/provider/ -race -v`
Expected: PASS, including the pre-existing `Resolve` and `YAMLSource` tests.

- [x] **Step 6: Commit**

```bash
git add internal/provider/
git commit -m "feat(provider): serve providers from sqlite"
```

---

### Task 9: First-run import of the YAML provider block

**Files:**
- Create: `internal/store/import.go`
- Test: `internal/store/import_test.go`

**Interfaces:**
- Consumes: `insertCredentialTx`, `putSetting`, `getSetting` from Tasks 5 and 6; `config.Config` from Phase 1.
- Produces: `store.ImportResult{Imported bool, Providers int, At time.Time}`, `func store.ImportFromConfig(ctx, d *DB, key *crypto.Key, cfg *config.Config) (ImportResult, error)`, `func store.ImportedAt(ctx, d *DB) (time.Time, bool, error)`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The import runs when **all three** hold: no marker row exists in `settings`, the
`providers` table is empty, and `darkrouter.yaml` still carries a `providers:`
block. Any one alone is insufficient — the empty-table guard in particular is
falsified by a crash mid-import, which is why everything including the marker
goes in one transaction.

- [x] **Step 1: Write the failing test**

Create `internal/store/import_test.go`:

```go
package store

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkrouter/internal/config"
)

func cfgWithProviders(ids ...string) *config.Config {
	c := &config.Config{}
	for _, id := range ids {
		c.Providers = append(c.Providers, config.ProviderConfig{
			ID: id, Kind: "openaicompat", BaseURL: "https://" + id + ".example/v1",
			APIKey: "sk-" + id, Priority: 7, Models: []string{id + "-model"},
		})
	}
	return c
}

func TestImportRunsOnAVirginDatabase(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}

	res, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq", "cerebras"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Imported || res.Providers != 2 {
		t.Fatalf("result = %+v", res)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("providers = %d, want 2", n)
	}
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM models`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("models = %d, want 2", n)
	}

	creds, err := db.Credentials(ctx, key, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 || creds[0].Secret != "sk-groq" {
		t.Errorf("credentials = %+v", creds)
	}

	if _, ok, err := ImportedAt(ctx, db); err != nil || !ok {
		t.Errorf("marker not written: ok=%v err=%v", ok, err)
	}
}

func TestImportDoesNotRunTwice(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	if _, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq")); err != nil {
		t.Fatal(err)
	}
	// A second run with a different config must be a no-op: the marker is set.
	res, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatal("import ran a second time")
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("providers = %d, want 1 — the second import must not add rows", n)
	}
}

func TestImportSkipsWhenProvidersTableIsNotEmpty(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")
	seededProvider(t, db, "already-here")

	res, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatal("import ran against a populated providers table")
	}
}

func TestImportSkipsWhenConfigHasNoProviders(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	res, err := ImportFromConfig(ctx, db, key, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatal("import ran with no providers: block")
	}
	if _, ok, _ := ImportedAt(ctx, db); ok {
		t.Fatal("a marker was written even though nothing was imported")
	}
}

// A crash mid-import must leave nothing behind. Otherwise the partial rows
// falsify the empty-table guard and the remaining providers are stranded
// forever, with no error to explain why half of them are missing.
func TestImportIsAtomicUnderFailure(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	boom := errors.New("simulated crash")
	_, err := importWithHook(ctx, db, key, cfgWithProviders("a", "b", "c"), func(i int) error {
		if i == 1 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected the injected failure, got %v", err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("providers = %d after a failed import, want 0", n)
	}
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM provider_keys`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("provider_keys = %d after a failed import, want 0", n)
	}
	if _, ok, _ := ImportedAt(ctx, db); ok {
		t.Error("the marker survived a failed import")
	}
}

func TestImportAbortsOnAnEmptyCredential(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	cfg := cfgWithProviders("groq", "broken")
	cfg.Providers[1].APIKey = "" // an unresolved ${ENV} reduces to this

	_, err := ImportFromConfig(ctx, db, key, cfg)
	if err == nil {
		t.Fatal("expected an empty credential to abort the import")
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("providers = %d, want 0 — an aborted import imports nothing", n)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Import -v`
Expected: FAIL — `undefined: ImportFromConfig`.

- [x] **Step 3: Write the import**

Create `internal/store/import.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
)

const settingProvidersImportedAt = "providers_imported_at"

// ImportResult reports what the import decided. Imported is false for the
// ordinary case of a database that has already been through it.
type ImportResult struct {
	Imported  bool
	Providers int
	At        time.Time
}

// ImportedAt returns when the first-run import ran, if it ever did.
func ImportedAt(ctx context.Context, d *DB) (time.Time, bool, error) {
	raw, ok, err := getSetting(ctx, d.Read, settingProvidersImportedAt)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("stored import marker is not a timestamp: %w", err)
	}
	return t, true, nil
}

// ImportFromConfig moves the YAML providers block into SQLite, once.
func ImportFromConfig(ctx context.Context, d *DB, key *crypto.Key, cfg *config.Config) (ImportResult, error) {
	return importWithHook(ctx, d, key, cfg, nil)
}

// importWithHook is ImportFromConfig with an injection point after each
// provider, so a test can prove the whole import is one transaction.
func importWithHook(ctx context.Context, d *DB, key *crypto.Key, cfg *config.Config,
	afterEach func(i int) error) (ImportResult, error) {

	// All three conditions must hold. The empty-table guard alone is not
	// enough, because a crash mid-import would falsify it.
	if len(cfg.Providers) == 0 {
		return ImportResult{}, nil
	}
	if _, ok, err := ImportedAt(ctx, d); err != nil {
		return ImportResult{}, err
	} else if ok {
		return ImportResult{}, nil
	}
	var existing int
	if err := d.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&existing); err != nil {
		return ImportResult{}, fmt.Errorf("count providers: %w", err)
	}
	if existing > 0 {
		return ImportResult{}, nil
	}

	// Validate before opening the transaction so an abort costs nothing. An
	// unresolvable ${ENV} reference reaches here as an empty string; importing
	// a provider with no credential would produce a provider that fails every
	// request instead of a clear startup error.
	for _, p := range cfg.Providers {
		if p.APIKey == "" {
			return ImportResult{}, fmt.Errorf(
				"provider %q has no api_key after environment resolution; "+
					"nothing was imported. Set the referenced variable and start again", p.ID)
		}
	}

	now := time.Now().UTC()
	tx, err := d.Sync.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, fmt.Errorf("begin import: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for i, p := range cfg.Providers {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO providers (id, name, kind, base_url, priority, enabled, created_at)
			 VALUES (?, ?, ?, ?, ?, 1, ?)`,
			p.ID, p.ID, p.Kind, p.BaseURL, p.Priority, now.UnixMilli()); err != nil {
			return ImportResult{}, fmt.Errorf("import provider %q: %w", p.ID, err)
		}
		for _, m := range p.Models {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO models (provider_id, model_id, capabilities_source)
				 VALUES (?, ?, 'inferred')`, p.ID, m); err != nil {
				return ImportResult{}, fmt.Errorf("import model %q on %q: %w", m, p.ID, err)
			}
		}
		if _, err := insertCredentialTx(ctx, tx, key, Credential{
			ProviderID: p.ID, Label: "imported", Kind: "static",
			Secret: p.APIKey, Enabled: true,
		}); err != nil {
			return ImportResult{}, err
		}
		if afterEach != nil {
			if err := afterEach(i); err != nil {
				return ImportResult{}, err
			}
		}
	}

	// The marker is written inside the same transaction as the rows it
	// describes. Written afterwards, a crash between them would leave providers
	// present with no marker, and the next start would see a non-empty table
	// and skip the import silently.
	if err := putSetting(ctx, tx, settingProvidersImportedAt, now.Format(time.RFC3339)); err != nil {
		return ImportResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ImportResult{}, fmt.Errorf("commit import: %w", err)
	}

	return ImportResult{Imported: true, Providers: len(cfg.Providers), At: now}, nil
}

// StaleBlockWarning returns the warning to show when darkrouter.yaml still
// carries a providers block that is no longer the source of truth. Editing it
// and expecting effect is the obvious mistake, and silence is the wrong answer.
func StaleBlockWarning(ctx context.Context, d *DB, cfg *config.Config) (string, error) {
	if len(cfg.Providers) == 0 {
		return "", nil
	}
	at, ok, err := ImportedAt(ctx, d)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return fmt.Sprintf(
		"providers were imported into the database on %s; the providers: block in "+
			"darkrouter.yaml is now ignored and can be deleted. Manage providers "+
			"through the database instead",
		at.Format("2006-01-02")), nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, thirty-one tests.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): import the yaml provider block once"
```

---

### Task 10: The asynchronous request-log writer

**Files:**
- Create: `internal/store/log.go`
- Test: `internal/store/log_test.go`

**Interfaces:**
- Consumes: `store.DB` from Task 2.
- Produces: `store.RequestRecord`, `store.AttemptRecord`, `store.LogOptions`, `func store.NewLogWriter(db *DB, o LogOptions) *LogWriter`, `func (*LogWriter) Log(r *RequestRecord)`, `func (*LogWriter) Run(ctx context.Context) error`, `func (*LogWriter) Dropped() int64`, `func (*LogWriter) Written() int64`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Logging never slows or blocks a request. The handler builds a complete record in
memory, sends it on a buffered channel, and returns.

A full channel **drops the record** and increments a counter. Blocking the
request path to guarantee a log line is the wrong trade. The consequence is
explicit rather than implicit: the log is the sole input to `usage_daily`, so
dropped records mean spend figures are a lower bound. The counter reports how
many records vanished, not how many tokens or dollars.

On shutdown the writer drains the channel before exiting. Without that, every
graceful restart loses a channel's worth of records and the drop counter lies by
omission.

- [x] **Step 1: Write the failing test**

Create `internal/store/log_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func rec(id string) *RequestRecord {
	return &RequestRecord{
		ID: id, TS: time.Unix(1700000000, 0).UTC(),
		Dialect: "openai", Surface: "chat", RequestedModel: "m",
		FinalProviderID: "groq", FinalModel: "m", Status: "success",
		TokensIn: 10, TokensOut: 20,
		Attempts: []AttemptRecord{
			{Seq: 0, ProviderID: "groq", KeyID: "k1", Model: "m", Outcome: "success", StatusCode: 200, LatencyMs: 42},
		},
	}
}

func countRows(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	if err := db.Read.QueryRowContext(context.Background(),
		"SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLogWriterPersistsRequestsAndAttempts(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 16, BatchSize: 2, FlushEvery: 10 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	w.Log(rec("r1"))
	w.Log(rec("r2"))

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, "requests"); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
	if got := countRows(t, db, "request_attempts"); got != 2 {
		t.Errorf("request_attempts = %d, want 2", got)
	}
	if w.Written() != 2 {
		t.Errorf("Written = %d, want 2", w.Written())
	}
}

// A done criterion: shutdown must not lose a channel's worth of records.
func TestLogWriterDrainsOnShutdown(t *testing.T) {
	db := migrated(t)
	// A large batch and a long timer mean nothing flushes before the drain.
	w := NewLogWriter(db, LogOptions{Buffer: 128, BatchSize: 1000, FlushEvery: time.Hour})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	for i := 0; i < 50; i++ {
		w.Log(rec(newID()))
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, "requests"); got != 50 {
		t.Errorf("requests = %d after drain, want 50", got)
	}
}

// Log must never block, even with nothing consuming the channel.
func TestLogDropsRatherThanBlocking(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 2, BatchSize: 10, FlushEvery: time.Hour})
	// Run is deliberately not started: the channel fills and stays full.

	blocked := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			w.Log(rec(newID()))
		}
		close(blocked)
	}()

	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("Log blocked on a full channel; the request path must never wait on the log")
	}
	if w.Dropped() < 90 {
		t.Errorf("Dropped = %d, want at least 90", w.Dropped())
	}
}

func TestLogWriterFlushesOnTheTimer(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 16, BatchSize: 1000, FlushEvery: 20 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = w.Run(ctx) }()

	w.Log(rec("r1"))

	deadline := time.After(3 * time.Second)
	for {
		if countRows(t, db, "requests") == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("the timer never flushed a partial batch")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestLogWriterWritesNullCostWhenPricingIsUnknown(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 4, BatchSize: 1, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	w.Log(rec("r1")) // CostMicros is nil
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	var cost *int64
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT cost_micros FROM requests WHERE id = 'r1'`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	// NULL, not 0. Zero would read as "this request was free".
	if cost != nil {
		t.Errorf("cost_micros = %d, want NULL", *cost)
	}
}

func TestLogWriterSurvivesADuplicateID(t *testing.T) {
	db := migrated(t)
	w := NewLogWriter(db, LogOptions{Buffer: 8, BatchSize: 10, FlushEvery: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	w.Log(rec("dup"))
	w.Log(rec("dup"))
	cancel()
	// One bad record must not lose the batch or kill the writer.
	if err := <-done; err != nil {
		t.Fatalf("the writer died on a duplicate id: %v", err)
	}
	if got := countRows(t, db, "requests"); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Log -v`
Expected: FAIL — `undefined: NewLogWriter`.

- [x] **Step 3: Write the writer**

Create `internal/store/log.go`:

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"
)

// AttemptRecord is one upstream attempt. Phase 2 produces exactly one per
// request; phase 3's loop produces several.
type AttemptRecord struct {
	Seq        int
	ProviderID string
	KeyID      string
	Model      string
	Outcome    string
	StatusCode int
	LatencyMs  int64
	Error      string
}

// RequestRecord is a complete request, built in memory by the handler and
// handed off. Nothing here may reference the http.Request: the record outlives
// the request it describes.
type RequestRecord struct {
	ID              string
	TS              time.Time
	Dialect         string
	Surface         string
	RequestedModel  string
	ResolvedAlias   string
	Candidates      []string
	FinalProviderID string
	FinalModel      string
	Status          string

	TokensIn         int64
	TokensOut        int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64

	// CostMicros is nil until pricing for the model exists, which is phase 6.
	// Zero would read as "this request was free".
	CostMicros *int64
	TTFTMs     *int64
	TotalMs    *int64

	ErrorCode string
	Warnings  []string

	Attempts []AttemptRecord
}

type LogOptions struct {
	Buffer     int
	BatchSize  int
	FlushEvery time.Duration
}

const (
	defaultLogBuffer     = 4096
	defaultLogBatchSize  = 128
	defaultLogFlushEvery = 250 * time.Millisecond
)

// LogWriter batches records into one transaction on a short timer or when the
// batch fills.
type LogWriter struct {
	db  *DB
	ch  chan *RequestRecord
	opt LogOptions

	dropped atomic.Int64
	written atomic.Int64
}

func NewLogWriter(db *DB, o LogOptions) *LogWriter {
	if o.Buffer <= 0 {
		o.Buffer = defaultLogBuffer
	}
	if o.BatchSize <= 0 {
		o.BatchSize = defaultLogBatchSize
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = defaultLogFlushEvery
	}
	return &LogWriter{db: db, ch: make(chan *RequestRecord, o.Buffer), opt: o}
}

// Log hands a record to the writer. It never blocks: a full channel drops the
// record and increments the counter reported on /healthz and /metrics.
//
// The trade is deliberate. Blocking the request path to guarantee a log line
// would make the gateway slower under exactly the load that produces the most
// interesting logs.
func (w *LogWriter) Log(r *RequestRecord) {
	if r == nil {
		return
	}
	select {
	case w.ch <- r:
	default:
		w.dropped.Add(1)
	}
}

// Dropped reports how many records were discarded. It counts records, not
// tokens or dollars: with a non-zero value, usage_daily is a lower bound.
func (w *LogWriter) Dropped() int64 { return w.dropped.Load() }

// Written reports how many records reached the database.
func (w *LogWriter) Written() int64 { return w.written.Load() }

// Run batches and writes until ctx is cancelled, then drains what is buffered
// and returns. The caller must not send on the channel after cancelling.
func (w *LogWriter) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.opt.FlushEvery)
	defer ticker.Stop()

	batch := make([]*RequestRecord, 0, w.opt.BatchSize)
	for {
		select {
		case <-ctx.Done():
			w.drain(&batch)
			return nil
		case r := <-w.ch:
			batch = append(batch, r)
			if len(batch) >= w.opt.BatchSize {
				w.flush(&batch)
			}
		case <-ticker.C:
			w.flush(&batch)
		}
	}
}

// drain empties the channel after cancellation. Shutdown ordering guarantees
// in-flight requests have finished by the time this runs, so the channel has a
// bounded amount left in it and the loop terminates.
func (w *LogWriter) drain(batch *[]*RequestRecord) {
	for {
		select {
		case r := <-w.ch:
			*batch = append(*batch, r)
			if len(*batch) >= w.opt.BatchSize {
				w.flush(batch)
			}
		default:
			w.flush(batch)
			return
		}
	}
}

func (w *LogWriter) flush(batch *[]*RequestRecord) {
	if len(*batch) == 0 {
		return
	}
	// A failed flush is logged and dropped rather than retried. Retrying would
	// grow the batch without bound while the cause persists, and the drop
	// counter is what tells the operator the log is incomplete.
	if err := w.writeBatch(context.Background(), *batch); err != nil {
		w.dropped.Add(int64(len(*batch)))
		log.Printf("request log: dropped %d records: %v", len(*batch), err)
	} else {
		w.written.Add(int64(len(*batch)))
	}
	*batch = (*batch)[:0]
}

// writeBatch uses a background context rather than the cancelled shutdown one:
// the drain runs after cancellation, and a cancelled context would abort the
// very write the drain exists to perform.
func (w *LogWriter) writeBatch(ctx context.Context, batch []*RequestRecord) error {
	tx, err := w.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	reqStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO requests (
		    id, ts, dialect, surface, requested_model, resolved_alias, candidates_json,
		    final_provider_id, final_model, status,
		    tokens_in, tokens_out, cache_read_tokens, cache_write_tokens, reasoning_tokens,
		    cost_micros, ttft_ms, total_ms, error_code, warnings_json
		 ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer reqStmt.Close()

	attStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO request_attempts
		    (request_id, seq, provider_id, key_id, model, outcome, status_code, latency_ms, error)
		 VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer attStmt.Close()

	for _, r := range batch {
		if err := insertOne(ctx, reqStmt, attStmt, r); err != nil {
			// One malformed record must not cost the batch. Duplicate ids are
			// the realistic case, and they mean a bug elsewhere, not here.
			log.Printf("request log: skipped record %s: %v", r.ID, err)
			continue
		}
	}
	return tx.Commit()
}

func insertOne(ctx context.Context, reqStmt, attStmt *sql.Stmt, r *RequestRecord) error {
	candidates, err := json.Marshal(nonNil(r.Candidates))
	if err != nil {
		return err
	}
	warnings, err := json.Marshal(nonNil(r.Warnings))
	if err != nil {
		return err
	}
	if _, err := reqStmt.ExecContext(ctx,
		r.ID, r.TS.UnixMilli(), r.Dialect, r.Surface, r.RequestedModel, r.ResolvedAlias,
		string(candidates), r.FinalProviderID, r.FinalModel, r.Status,
		r.TokensIn, r.TokensOut, r.CacheReadTokens, r.CacheWriteTokens, r.ReasoningTokens,
		r.CostMicros, r.TTFTMs, r.TotalMs, r.ErrorCode, string(warnings),
	); err != nil {
		return err
	}
	for _, a := range r.Attempts {
		if _, err := attStmt.ExecContext(ctx,
			r.ID, a.Seq, a.ProviderID, a.KeyID, a.Model, a.Outcome,
			a.StatusCode, a.LatencyMs, a.Error,
		); err != nil {
			return err
		}
	}
	return nil
}

// nonNil keeps a nil slice out of the JSON columns, which are NOT NULL and
// default to an empty array rather than "null".
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
```

Note on `TestLogWriterSurvivesADuplicateID`: the second insert fails inside the
transaction, `insertOne` returns the error, and the loop skips that record. The
transaction still commits the first. SQLite does not abort a transaction on a
constraint violation from a statement whose error was handled, so this works —
but verify it in Step 4 rather than trusting the description.

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, thirty-seven tests.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): add the async request log writer"
```

---

### Task 11: Emit a request record from the executor

**Files:**
- Modify: `internal/exec/exec.go`
- Modify: `internal/server/server.go:41` (the `exec.New` call)
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `store.RequestRecord`, `store.AttemptRecord` from Task 10.
- Produces: `exec.Logger` interface, `exec.Deps{Log Logger}`, and the new signature `func exec.New(cfgStore *config.Store, src provider.Source, ad adapter.Adapter, deps Deps) *Executor`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

This closes the Phase 1 done criterion that was left unmet: nothing in the
request path logged, so a client disconnect was indistinguishable from a timeout.

`Deps` is a struct rather than a fourth parameter so Task 14 can add health
recording by adding a field, not by changing the signature again.

Streaming needs a tap over the event sequence. Time-to-first-token is the first
content delta, and usage arrives on a late event, so both are only observable
by wrapping the iterator that `WriteStream` consumes.

- [x] **Step 1: Write the failing test**

Extend the existing helper in `internal/exec/exec_test.go` so a test can supply
`Deps` and a short total timeout, keeping the current signature working:

```go
func newExecutor(t *testing.T, upstreamURL string) *Executor {
	t.Helper()
	return newExecutorWith(t, upstreamURL, Deps{}, 0)
}

// newExecutorWith is newExecutor with the knobs later tests need. A zero total
// leaves the default of 10m in place.
func newExecutorWith(t *testing.T, upstreamURL string, deps Deps, total time.Duration) *Executor {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\nproviders:\n" +
		"  - id: fake\n    kind: openaicompat\n    base_url: " + upstreamURL +
		"\n    api_key: ${K}\n    models: [m]\n"
	if total > 0 {
		body += "policy:\n  timeout:\n    total: " + total.String() + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "sk", true })
	if err != nil {
		t.Fatal(err)
	}
	return New(cfgStore, provider.NewYAMLSource(cfgStore), openaicompat.New(), deps)
}
```

Then append the tests:

```go
type captureLogger struct {
	mu      sync.Mutex
	records []*store.RequestRecord
}

func (c *captureLogger) Log(r *store.RequestRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r)
}

func (c *captureLogger) only(t *testing.T) *store.RequestRecord {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.records) != 1 {
		t.Fatalf("got %d records, want 1", len(c.records))
	}
	return c.records[0]
}

func unaryUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","model":"m","choices":[{"message":
			{"content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5}}`))
	}))
}

func streamUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":5,\"total_tokens\":8}}\n\n" +
				"data: [DONE]\n\n"))
	}))
}

func TestHandleLogsASuccessfulRequest(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	logger := &captureLogger{}
	e := newExecutorWith(t, up.URL, Deps{Log: logger}, 0)

	rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}

	r := logger.only(t)
	if r.Status != "success" {
		t.Errorf("Status = %q, want success", r.Status)
	}
	if r.ID == "" || r.ID != rec.Header().Get("X-Darkrouter-Request") {
		t.Errorf("record id %q does not match the response header", r.ID)
	}
	if r.Dialect != "openai" || r.Surface != "llm" {
		t.Errorf("dialect/surface = %q/%q", r.Dialect, r.Surface)
	}
	if r.RequestedModel != "m" || r.FinalModel != "m" || r.FinalProviderID != "fake" {
		t.Errorf("record = %+v", r)
	}
	if r.TokensIn != 3 || r.TokensOut != 5 {
		t.Errorf("tokens: in=%d out=%d, want 3/5", r.TokensIn, r.TokensOut)
	}
	if r.CostMicros != nil {
		t.Error("CostMicros must stay nil until phase 6 supplies pricing")
	}
	if r.TotalMs == nil || r.TTFTMs == nil {
		t.Error("TotalMs and TTFTMs must both be recorded")
	}
	if len(r.Attempts) != 1 {
		t.Fatalf("got %d attempts, want 1", len(r.Attempts))
	}
	a := r.Attempts[0]
	if a.Seq != 0 || a.ProviderID != "fake" || a.Outcome != "success" || a.StatusCode != 200 {
		t.Errorf("attempt = %+v", a)
	}
}

func TestHandleLogsAnUpstreamFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer up.Close()
	logger := &captureLogger{}
	e := newExecutorWith(t, up.URL, Deps{Log: logger}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if r.Status != "error" {
		t.Errorf("Status = %q, want error", r.Status)
	}
	if len(r.Attempts) != 1 || r.Attempts[0].Outcome != "retryable_provider" {
		t.Fatalf("attempts = %+v", r.Attempts)
	}
	if r.Attempts[0].StatusCode != 503 {
		t.Errorf("StatusCode = %d, want 503", r.Attempts[0].StatusCode)
	}
}

// A request that never reaches an upstream still produces a record, or the
// spend figures silently omit every misrouted request.
func TestHandleLogsAnUnknownModelWithNoAttempts(t *testing.T) {
	logger := &captureLogger{}
	e := newExecutorWith(t, "https://unused.example/v1", Deps{Log: logger}, 0)

	post(t, e, `{"model":"nope","messages":[]}`)

	r := logger.only(t)
	if r.Status != "error" {
		t.Errorf("Status = %q", r.Status)
	}
	if len(r.Attempts) != 0 {
		t.Errorf("got %d attempts, want 0", len(r.Attempts))
	}
	if r.ErrorCode == "" {
		t.Error("ErrorCode was not recorded")
	}
}

func TestHandleRecordsTTFTAndUsageOnAStream(t *testing.T) {
	up := streamUpstream()
	defer up.Close()
	logger := &captureLogger{}
	e := newExecutorWith(t, up.URL, Deps{Log: logger}, 0)

	post(t, e, `{"model":"m","stream":true,"messages":[{"role":"user","content":"ping"}]}`)

	r := logger.only(t)
	if r.Status != "success" {
		t.Errorf("Status = %q", r.Status)
	}
	if r.TTFTMs == nil {
		t.Fatal("TTFT was not recorded on a stream")
	}
	if r.TotalMs == nil || *r.TTFTMs > *r.TotalMs {
		t.Errorf("TTFT %v exceeds total %v", r.TTFTMs, r.TotalMs)
	}
	// Usage arrives on a late event; without the tap it would be lost.
	if r.TokensOut != 5 {
		t.Errorf("stream usage not captured: TokensOut = %d, want 5", r.TokensOut)
	}
}

func TestHandleWithNoLoggerDoesNotPanic(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	e := newExecutorWith(t, up.URL, Deps{}, 0)
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}
```

Add `sync` and `github.com/darkraise/darkrouter/internal/store` to the test
file's imports.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -v`
Expected: FAIL — `too many arguments to New`, `undefined: Deps`.

- [x] **Step 3: Add Deps and the logging hooks**

In `internal/exec/exec.go`, add the interface and dependency struct above
`Executor`, and thread them through `New`:

```go
// Logger receives one record per request. It must never block: the
// implementation in internal/store drops rather than waiting.
type Logger interface {
	Log(*store.RequestRecord)
}

// Deps carries the optional collaborators. A zero Deps is valid and disables
// the corresponding behavior, which is what keeps the phase 1 tests running
// unchanged.
type Deps struct {
	Log Logger
}

type Executor struct {
	store  *config.Store
	src    provider.Source
	ad     adapter.Adapter
	client *http.Client
	deps   Deps
}
```

Change `New` to accept and store `deps`:

```go
func New(store *config.Store, src provider.Source, ad adapter.Adapter, deps Deps) *Executor {
	t := store.Current().Policy.Timeout
	return &Executor{
		store: store, src: src, ad: ad, deps: deps,
		client: &http.Client{ /* unchanged */ },
	}
}

func (e *Executor) log(rec *store.RequestRecord) {
	if e.deps.Log == nil {
		return
	}
	e.deps.Log.Log(rec)
}
```

- [x] **Step 4: Rewrite Handle to build the record**

Replace `Handle` in `internal/exec/exec.go`:

```go
func (e *Executor) Handle(w http.ResponseWriter, r *http.Request, d edge.Dialect) {
	start := time.Now()
	cfg := e.store.Current() // one snapshot for this request's whole lifetime
	reqID := ulid.MustNew(ulid.Timestamp(start), rand.Reader).String()

	// The record is built as the request proceeds and emitted exactly once, on
	// every exit path. Status starts as "error" so an early return that forgets
	// to set it is recorded as a failure rather than a silent success.
	rec := &store.RequestRecord{
		ID: reqID, TS: start, Dialect: d.Name(), Surface: "llm", Status: "error",
	}
	defer func() {
		total := time.Since(start).Milliseconds()
		rec.TotalMs = &total
		e.log(rec)
	}()

	// Written up front so every error path carries them, per master design §10.
	// The count is overwritten once an attempt has actually been made.
	w.Header().Set("X-Darkrouter-Request", reqID)
	w.Header().Set("X-Darkrouter-Attempts", "0")

	req, pt, err := d.ParseRequest(r, cfg.Server.MaxBodyBytes)
	if err != nil {
		rec.ErrorCode = string(ir.ErrInvalidRequest)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrInvalidRequest, Message: err.Error()})
		return
	}
	if pt != nil && pt.Surface != "" {
		rec.Surface = pt.Surface
	}
	rec.RequestedModel = req.Model

	providers, err := e.src.Providers(r.Context())
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}
	p, ok := provider.Resolve(providers, req.Model)
	if !ok {
		rec.ErrorCode = string(ir.ErrNotFound)
		_ = d.WriteError(w, &ir.Error{
			Type:    ir.ErrNotFound,
			Message: fmt.Sprintf("no configured provider offers model %q", req.Model),
		})
		return
	}
	rec.Candidates = []string{p.ID}
	rec.FinalProviderID = p.ID
	rec.FinalModel = req.Model

	// The upstream context derives from the inbound one, so a client hanging up
	// cancels the upstream call. WithCancelCause is used because the cause is
	// the only way to tell a disconnect from a Darkrouter deadline.
	ctx, cancel := context.WithCancelCause(r.Context())
	defer cancel(nil)
	// Phase 1 applies the total budget to the whole request. Phase 3 replaces
	// this with commit semantics plus policy.timeout.idle for committed streams.
	ctx, cancelTimeout := context.WithTimeoutCause(ctx, cfg.Policy.Timeout.Total,
		errDarkrouterTimeout)
	defer cancelTimeout()

	tgt := &adapter.Target{BaseURL: p.BaseURL, APIKey: p.APIKey, Model: req.Model}
	hr, err := e.ad.BuildRequest(ctx, tgt, req)
	if err != nil {
		rec.ErrorCode = string(ir.ErrDarkrouter)
		_ = d.WriteError(w, &ir.Error{Type: ir.ErrDarkrouter, Message: err.Error()})
		return
	}

	attemptStart := time.Now()
	resp, doErr := e.client.Do(hr)
	outcome := e.classify(r.Context(), resp, doErr)

	attempt := store.AttemptRecord{
		Seq: 0, ProviderID: p.ID, KeyID: p.KeyID, Model: req.Model,
		Outcome:   string(outcome),
		LatencyMs: time.Since(attemptStart).Milliseconds(),
	}
	if resp != nil {
		attempt.StatusCode = resp.StatusCode
	}
	if doErr != nil {
		attempt.Error = doErr.Error()
	}
	rec.Attempts = append(rec.Attempts, attempt)

	w.Header().Set("X-Darkrouter-Provider", p.ID)
	w.Header().Set("X-Darkrouter-Model", req.Model)
	w.Header().Set("X-Darkrouter-Attempts", strconv.Itoa(1))

	if outcome != adapter.OutcomeSuccess {
		if resp != nil {
			resp.Body.Close()
		}
		e := errorFor(outcome, doErr)
		rec.ErrorCode = string(e.Type)
		if outcome == adapter.OutcomeClientCancelled {
			rec.Status = "cancelled"
		}
		_ = d.WriteError(w, e)
		return
	}

	if req.Stream {
		defer resp.Body.Close()
		// TTFT is the first content delta rather than the response header, so
		// it measures what the client actually waited for.
		events := tapStream(e.ad.ParseStream(resp.Body, cfg.Server.SSE.MaxLineBytes),
			func() {
				ttft := time.Since(start).Milliseconds()
				rec.TTFTMs = &ttft
			},
			func(u *ir.Usage) { applyUsage(rec, u) },
		)
		_ = d.WriteStream(w, events)
		rec.Status = "success"
		return
	}

	// For a non-streaming response the client waits for the whole body, so
	// first token and last token are the same moment.
	ttft := time.Since(start).Milliseconds()
	rec.TTFTMs = &ttft

	out, err := e.ad.ParseResponse(resp)
	if err != nil {
		// Design §8.2: a read or parse failure on a 2xx is a provider fault, so
		// it goes through the outcome path rather than around it.
		e2 := errorFor(adapter.OutcomeRetryableProvider, err)
		rec.ErrorCode = string(e2.Type)
		rec.Attempts[0].Outcome = string(adapter.OutcomeRetryableProvider)
		rec.Attempts[0].Error = err.Error()
		_ = d.WriteError(w, e2)
		return
	}
	applyUsage(rec, &out.Usage)
	rec.Status = "success"
	_ = d.WriteResponse(w, out)
}

func applyUsage(rec *store.RequestRecord, u *ir.Usage) {
	if u == nil {
		return
	}
	rec.TokensIn = int64(u.InputTokens)
	rec.TokensOut = int64(u.OutputTokens)
	rec.CacheReadTokens = int64(u.CacheReadTokens)
	rec.CacheWriteTokens = int64(u.CacheWriteTokens)
	rec.ReasoningTokens = int64(u.ReasoningTokens)
	// CostMicros stays nil. Phase 6 supplies pricing; zero would read as free.
}

// tapStream observes events on their way to the edge writer without buffering
// them. Usage arrives on a late event and the first content delta defines TTFT,
// so neither is visible unless the sequence is wrapped.
func tapStream(events iter.Seq2[ir.StreamEvent, error],
	onFirstContent func(), onUsage func(*ir.Usage)) iter.Seq2[ir.StreamEvent, error] {

	return func(yield func(ir.StreamEvent, error) bool) {
		seenContent := false
		for ev, err := range events {
			if err == nil {
				if !seenContent && ev.Type == ir.EventContentDelta {
					seenContent = true
					onFirstContent()
				}
				if ev.Usage != nil {
					onUsage(ev.Usage)
				}
			}
			if !yield(ev, err) {
				return
			}
		}
	}
}
```

Add `iter` and `github.com/darkraise/darkrouter/internal/store` to the imports.

- [x] **Step 5: Update the server's call site**

In `internal/server/server.go`, the `New` function currently calls
`exec.New(store, src, openaicompat.New())`. Change it to
`exec.New(store, src, openaicompat.New(), exec.Deps{})`. Task 17 fills the
`Deps` in properly; leaving it empty here keeps this task's diff to one concern.

- [x] **Step 6: Run tests to verify they pass**

Run: `go test ./... -race`
Expected: PASS across every package.

- [x] **Step 7: Commit**

```bash
git add internal/exec/ internal/server/
git commit -m "feat(exec): emit a request record per request"
```

---

### Task 12: The circuit breaker

**Files:**
- Create: `internal/health/breaker.go`
- Create: `internal/health/retryafter.go`
- Test: `internal/health/breaker_test.go`

**Interfaces:**
- Consumes: `adapter.Outcome` from Phase 1.
- Produces: `health.Key{ProviderID, KeyID, Model string}`, `health.Signal{Outcome adapter.Outcome, StatusCode int, RetryAfter time.Duration, HasRetryAfter bool}`, `health.Entry`, `func health.New(tripAfter int, max time.Duration) *Breaker`, `func (*Breaker) Available(k Key) bool`, `func (*Breaker) Record(k Key, s Signal)`, `func (*Breaker) Snapshot() []Entry`, `func (*Breaker) Rehydrate(entries []Entry)`, `func (*Breaker) TakeDirty() bool`, `func health.ParseRetryAfter(h string, now time.Time) (time.Duration, bool)`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

In-memory is authoritative on the hot path. A mutex-guarded map answers
availability without touching SQLite, because Phase 3's router consults it on
every request.

Two rules are easy to get backwards and the spec is explicit about both:

- `Fatal` resets the ladder alongside `Success`, because a 400 proves the
  provider is reachable and functioning and says something about the request.
- `RetryableCredential` **never** resets. An earlier draft had "any response
  proving reachability resets" firing against "cool on 401/402/403" — so a
  billing-exhausted key cooled by a 402 would be resurrected by any client's
  malformed request and retried indefinitely.

`Available` performs the half-open claim itself. Exactly one caller at expiry
gets through; the rest see the candidate as unavailable rather than all becoming
probes.

- [x] **Step 1: Write the failing test**

Create `internal/health/breaker_test.go`:

```go
package health

import (
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

func newTestBreaker(t *testing.T) (*Breaker, *time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	b := New(3, 15*time.Minute)
	b.now = func() time.Time { return now }
	return b, &now
}

var triple = Key{ProviderID: "groq", KeyID: "k1", Model: "m"}

func TestBreakerStartsAvailable(t *testing.T) {
	b, _ := newTestBreaker(t)
	if !b.Available(triple) {
		t.Fatal("an unseen triple must be available")
	}
}

// The spec is explicit: a single 5xx does not cool a candidate.
func TestSingleRetryableFailureDoesNotCool(t *testing.T) {
	b, _ := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if !b.Available(triple) {
		t.Fatal("one 5xx cooled the triple; trip_after is 3")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if !b.Available(triple) {
		t.Fatal("two 5xx cooled the triple; trip_after is 3")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if b.Available(triple) {
		t.Fatal("three consecutive 5xx must cool the triple")
	}
}

func TestOutcomeTable(t *testing.T) {
	cases := []struct {
		name      string
		signals   []Signal
		wantCool  bool
		checkKey  Key
	}{
		{
			name:     "429 cools immediately without waiting for trip_after",
			signals:  []Signal{{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 429}},
			wantCool: true, checkKey: triple,
		},
		{
			name: "success resets the ladder",
			signals: []Signal{
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeSuccess, StatusCode: 200},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
			},
			wantCool: false, checkKey: triple,
		},
		{
			name: "fatal resets the ladder too",
			signals: []Signal{
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
				{Outcome: adapter.OutcomeFatal, StatusCode: 400},
				{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503},
			},
			wantCool: false, checkKey: triple,
		},
		{
			name:     "401 cools the credential across every model",
			signals:  []Signal{{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 401}},
			wantCool: true, checkKey: Key{ProviderID: "groq", KeyID: "k1", Model: "other-model"},
		},
		{
			name: "a 400 after a 402 leaves the credential cooling",
			signals: []Signal{
				{Outcome: adapter.OutcomeRetryableCredential, StatusCode: 402},
				{Outcome: adapter.OutcomeFatal, StatusCode: 400},
			},
			wantCool: true, checkKey: triple,
		},
		{
			name:     "client cancellation touches nothing",
			signals:  []Signal{{Outcome: adapter.OutcomeClientCancelled}},
			wantCool: false, checkKey: triple,
		},
		{
			name: "three cancellations still touch nothing",
			signals: []Signal{
				{Outcome: adapter.OutcomeClientCancelled},
				{Outcome: adapter.OutcomeClientCancelled},
				{Outcome: adapter.OutcomeClientCancelled},
			},
			wantCool: false, checkKey: triple,
		},
		{
			name:     "an unknown model does not penalize the provider",
			signals:  []Signal{{Outcome: adapter.OutcomeRetryableModel, StatusCode: 404}},
			wantCool: false, checkKey: triple,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := newTestBreaker(t)
			for _, s := range tc.signals {
				b.Record(triple, s)
			}
			if got := !b.Available(tc.checkKey); got != tc.wantCool {
				t.Errorf("cooling = %v, want %v", got, tc.wantCool)
			}
		})
	}
}

func TestRetryAfterIsHonouredAndClamped(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{
		Outcome: adapter.OutcomeRetryableProvider, StatusCode: 429,
		RetryAfter: 24 * time.Hour, HasRetryAfter: true,
	})
	if b.Available(triple) {
		t.Fatal("a 429 with Retry-After must cool the triple")
	}
	// Clamped to policy.cooldown.max. Without the clamp a provider sending
	// Retry-After: 86400 removes itself for a day.
	*now = now.Add(15*time.Minute + time.Second)
	if !b.Available(triple) {
		t.Fatal("Retry-After was not clamped to 15m")
	}
}

// A Retry-After cooldown never tripped a ladder, so it closes on expiry with no
// probe: the very next caller is admitted.
func TestRetryAfterCooldownClosesWithoutAProbe(t *testing.T) {
	b, now := newTestBreaker(t)
	b.Record(triple, Signal{
		Outcome: adapter.OutcomeRetryableProvider, StatusCode: 429,
		RetryAfter: 10 * time.Second, HasRetryAfter: true,
	})
	*now = now.Add(11 * time.Second)
	if !b.Available(triple) {
		t.Fatal("first caller after expiry was refused")
	}
	if !b.Available(triple) {
		t.Fatal("second caller was refused; a Retry-After expiry admits everyone")
	}
}

func TestLadderEscalatesAndClamps(t *testing.T) {
	want := []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
		15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second,
		240 * time.Second, 480 * time.Second, 900 * time.Second, // 900s = the 15m clamp
		900 * time.Second,
	}
	for level, w := range want {
		if got := cooldownFor(level, 15*time.Minute); got != w {
			t.Errorf("level %d: cooldown = %s, want %s", level, got, w)
		}
	}
}

func TestLadderDoesNotOverflowAtHighLevels(t *testing.T) {
	if got := cooldownFor(500, 15*time.Minute); got != 15*time.Minute {
		t.Errorf("cooldown at level 500 = %s, want the clamp", got)
	}
}

func TestHalfOpenAdmitsExactlyOneProbeUnderConcurrency(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	if b.Available(triple) {
		t.Fatal("the triple should be cooling")
	}
	*now = now.Add(2 * time.Second) // past the level-0 cooldown of 1s

	const goroutines = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if b.Available(triple) {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()

	if admitted != 1 {
		t.Fatalf("%d probes admitted at expiry, want exactly 1", admitted)
	}
}

func TestProbeSuccessClosesTheBreaker(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	*now = now.Add(2 * time.Second)
	if !b.Available(triple) {
		t.Fatal("no probe was admitted")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeSuccess, StatusCode: 200})
	if !b.Available(triple) {
		t.Fatal("a successful probe must close the breaker")
	}
	if !b.Available(triple) {
		t.Fatal("the breaker did not stay closed")
	}
}

func TestProbeFailureReTripsAtTheNextLadderLevel(t *testing.T) {
	b, now := newTestBreaker(t)
	for i := 0; i < 3; i++ {
		b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	*now = now.Add(2 * time.Second)
	if !b.Available(triple) {
		t.Fatal("no probe was admitted")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})

	// Level 1 is 2s: still cooling at +1s, open again at +3s.
	*now = now.Add(time.Second)
	if b.Available(triple) {
		t.Fatal("the breaker re-opened before the level-1 cooldown elapsed")
	}
	*now = now.Add(3 * time.Second)
	if !b.Available(triple) {
		t.Fatal("the level-1 cooldown never expired")
	}
}

func TestSnapshotAndRehydrateRetainFailureCounts(t *testing.T) {
	b, _ := newTestBreaker(t)
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})

	snap := b.Snapshot()
	if len(snap) != 1 || snap[0].ConsecutiveFailures != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}

	// A restart must not hand a flapping provider a clean slate.
	restored := New(3, 15*time.Minute)
	restored.Rehydrate(snap)
	restored.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if restored.Available(triple) {
		t.Fatal("the third failure after a restart should have cooled the triple")
	}
}

func TestRehydrateDropsExpiredCooldowns(t *testing.T) {
	b, _ := newTestBreaker(t)
	// Relative to the breaker's clock, not the wall clock: mixing the two makes
	// the test depend on the date it runs.
	past := b.now().Add(-time.Hour)
	b.Rehydrate([]Entry{{
		Key: triple, CoolingUntil: past, BackoffLevel: 4, ConsecutiveFailures: 7,
	}})
	if !b.Available(triple) {
		t.Fatal("an expired cooldown must not survive rehydration")
	}
	snap := b.Snapshot()
	if len(snap) != 1 || snap[0].ConsecutiveFailures != 7 {
		t.Fatalf("failure count must be retained across rehydration, got %+v", snap)
	}
}

func TestTakeDirtyReportsAndClears(t *testing.T) {
	b, _ := newTestBreaker(t)
	if b.TakeDirty() {
		t.Fatal("a fresh breaker must not be dirty")
	}
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if !b.TakeDirty() {
		t.Fatal("recording a failure must mark the breaker dirty")
	}
	if b.TakeDirty() {
		t.Fatal("TakeDirty must clear the flag")
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		in       string
		want     time.Duration
		wantOK   bool
	}{
		{"", 0, false},
		{"30", 30 * time.Second, true},
		{"  30  ", 30 * time.Second, true},
		{"0", 0, true},
		{"-5", 0, false},
		{"nonsense", 0, false},
		{"Sat, 22 Aug 2026 12:01:00 GMT", time.Minute, true},
		// A date already in the past means "retry now", not a negative wait.
		{"Sat, 22 Aug 2026 11:59:00 GMT", 0, true},
	}
	for _, tc := range cases {
		got, ok := ParseRetryAfter(tc.in, now)
		if ok != tc.wantOK || got != tc.want {
			t.Errorf("ParseRetryAfter(%q) = %s, %v; want %s, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/health/ -v`
Expected: FAIL — `undefined: New`.

- [x] **Step 3: Write Retry-After parsing**

Create `internal/health/retryafter.go`:

```go
package health

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfter reads both wire forms RFC 9110 permits: delta-seconds and an
// HTTP-date. Providers use both, and treating a date as unparseable would
// silently downgrade a precise instruction to the generic ladder.
func ParseRetryAfter(h string, now time.Time) (time.Duration, bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(h); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	t, err := http.ParseTime(h)
	if err != nil {
		return 0, false
	}
	d := t.Sub(now)
	if d < 0 {
		// A date already in the past means retry now, not a negative wait.
		d = 0
	}
	return d, true
}
```

- [x] **Step 4: Write the breaker**

Create `internal/health/breaker.go`:

```go
// Package health holds the circuit breaker. State is authoritative in memory
// because the router consults it on every request; SQLite is a durable copy.
package health

import (
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

// Key identifies a breaker entry. An empty Model is the credential-level entry,
// used for cooldowns that apply across every model a credential serves.
type Key struct {
	ProviderID string
	KeyID      string
	Model      string
}

// Signal is one observed outcome. StatusCode is carried separately from Outcome
// because 429 and 503 both classify as RetryableProvider but cool differently:
// a rate limit is precise and immediate, a 5xx needs trip_after failures first.
type Signal struct {
	Outcome       adapter.Outcome
	StatusCode    int
	RetryAfter    time.Duration
	HasRetryAfter bool
}

// Entry is a persistable view of one breaker entry.
type Entry struct {
	Key                 Key
	CoolingUntil        time.Time
	BackoffLevel        int
	ConsecutiveFailures int
}

type state struct {
	coolingUntil        time.Time
	backoffLevel        int
	consecutiveFailures int

	// probing is set when a half-open probe has been admitted, so concurrent
	// callers at expiry see the candidate as unavailable instead of all
	// becoming probes.
	probing bool

	// retryAfterOnly marks a cooldown that came from a Retry-After header
	// rather than the ladder. Such a cooldown closes on expiry with no probe,
	// because nothing was ever tripped.
	retryAfterOnly bool
}

// ladder is the escalation sequence from master design §9. It continues
// doubling past the last entry until the configured maximum clamps it.
var ladder = []time.Duration{
	1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
	15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second,
}

func cooldownFor(level int, max time.Duration) time.Duration {
	if level < 0 {
		level = 0
	}
	if level < len(ladder) {
		if d := ladder[level]; d < max {
			return d
		}
		return max
	}
	d := ladder[len(ladder)-1]
	for i := len(ladder); i <= level; i++ {
		// Checked before doubling so a large level cannot overflow int64.
		if d >= max/2 {
			return max
		}
		d *= 2
	}
	if d > max {
		return max
	}
	return d
}

type Breaker struct {
	mu        sync.Mutex
	m         map[Key]*state
	tripAfter int
	max       time.Duration

	// now is swappable so tests can advance time without sleeping.
	now func() time.Time

	dirty bool
}

func New(tripAfter int, max time.Duration) *Breaker {
	if tripAfter < 1 {
		tripAfter = 1
	}
	if max <= 0 {
		max = 15 * time.Minute
	}
	return &Breaker{
		m: make(map[Key]*state), tripAfter: tripAfter, max: max, now: time.Now,
	}
}

// Available reports whether the triple may be attempted now, and performs the
// half-open claim itself: at expiry of a ladder cooldown exactly one caller is
// admitted as the probe.
func (b *Breaker) Available(k Key) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// The credential-level entry gates every model the credential serves, so it
	// is checked first and independently.
	if k.Model != "" {
		if !b.availableLocked(Key{ProviderID: k.ProviderID, KeyID: k.KeyID}) {
			return false
		}
	}
	return b.availableLocked(k)
}

func (b *Breaker) availableLocked(k Key) bool {
	st, ok := b.m[k]
	if !ok || st.coolingUntil.IsZero() {
		return true
	}
	now := b.now()
	if now.Before(st.coolingUntil) {
		return false
	}

	// Expired.
	if st.retryAfterOnly {
		// No ladder was tripped, so there is nothing to probe: reopen fully.
		st.coolingUntil = time.Time{}
		st.retryAfterOnly = false
		b.dirty = true
		return true
	}
	if st.probing {
		return false
	}
	st.probing = true
	return true
}

// Record applies one outcome. It is the only place breaker state changes, so
// the rules from spec §7.1 live here and nowhere else.
func (b *Breaker) Record(k Key, s Signal) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()

	switch s.Outcome {
	case adapter.OutcomeClientCancelled:
		// Never counts against any provider. Marking a provider unhealthy
		// because someone pressed Ctrl-C is a self-inflicted outage.
		return

	case adapter.OutcomeRetryableModel:
		// The provider is reachable and the credential is fine; only this model
		// is wrong. Phase 6 counts these per target to surface a permanently
		// misconfigured base URL.
		return

	case adapter.OutcomeSuccess, adapter.OutcomeFatal:
		// Both prove the provider is reachable and functioning. A 400 says
		// something about the request, not about the provider.
		if _, ok := b.m[k]; ok {
			delete(b.m, k)
			b.dirty = true
		}
		return

	case adapter.OutcomeRetryableCredential:
		// Cools the credential across every model it serves. This never resets
		// any ladder: a billing-exhausted key must not be resurrected by a
		// client's malformed request.
		ck := Key{ProviderID: k.ProviderID, KeyID: k.KeyID}
		st := b.getLocked(ck)
		b.coolLocked(st, now, st.backoffLevel)
		return

	case adapter.OutcomeRetryableProvider:
		st := b.getLocked(k)
		if s.StatusCode == 429 {
			if s.HasRetryAfter {
				d := s.RetryAfter
				if d > b.max {
					d = b.max
				}
				st.coolingUntil = now.Add(d)
				st.retryAfterOnly = true
				st.probing = false
				b.dirty = true
				return
			}
			b.coolLocked(st, now, st.backoffLevel)
			return
		}
		// Everything else retryable: a single failure must not cool.
		st.consecutiveFailures++
		b.dirty = true
		if st.consecutiveFailures >= b.tripAfter {
			b.coolLocked(st, now, st.backoffLevel)
		}
		return
	}
}

func (b *Breaker) getLocked(k Key) *state {
	st, ok := b.m[k]
	if !ok {
		st = &state{}
		b.m[k] = st
	}
	return st
}

// coolLocked cools at the given level and advances the ladder, so the next trip
// escalates. consecutiveFailures is deliberately not reset: it is what makes a
// probe failure re-trip immediately at the next level.
func (b *Breaker) coolLocked(st *state, now time.Time, level int) {
	st.coolingUntil = now.Add(cooldownFor(level, b.max))
	st.backoffLevel = level + 1
	st.retryAfterOnly = false
	st.probing = false
	b.dirty = true
}

// Snapshot returns every entry for the persister. It is a copy: the caller
// writes it to SQLite without holding the lock.
func (b *Breaker) Snapshot() []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Entry, 0, len(b.m))
	for k, st := range b.m {
		out = append(out, Entry{
			Key: k, CoolingUntil: st.coolingUntil,
			BackoffLevel: st.backoffLevel, ConsecutiveFailures: st.consecutiveFailures,
		})
	}
	return out
}

// Rehydrate restores state at startup. Entries whose cooldown has passed are
// reopened, but their failure counters are retained: a provider that was
// flapping before a restart must not get a clean slate.
func (b *Breaker) Rehydrate(entries []Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	for _, e := range entries {
		st := &state{
			backoffLevel:        e.BackoffLevel,
			consecutiveFailures: e.ConsecutiveFailures,
		}
		if !e.CoolingUntil.IsZero() && now.Before(e.CoolingUntil) {
			st.coolingUntil = e.CoolingUntil
		}
		b.m[e.Key] = st
	}
}

// TakeDirty reports whether state changed since the last call and clears the
// flag, so the persister writes only when there is something to write.
func (b *Breaker) TakeDirty() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	d := b.dirty
	b.dirty = false
	return d
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/health/ -race -v`
Expected: PASS. `TestHalfOpenAdmitsExactlyOneProbeUnderConcurrency` is the one
that matters most under `-race`.

- [x] **Step 6: Commit**

```bash
git add internal/health/
git commit -m "feat(health): add the circuit breaker and ladder"
```

---

### Task 13: Persist and reload breaker state

**Files:**
- Create: `internal/store/health.go`
- Test: `internal/store/health_test.go`

**Interfaces:**
- Consumes: `health.Entry` and `health.Key` from Task 12.
- Produces: `func (*DB) SaveHealth(ctx context.Context, entries []health.Entry) error`, `func (*DB) LoadHealth(ctx context.Context) ([]health.Entry, error)`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

`SaveHealth` replaces the whole table in one transaction rather than upserting.
Entries disappear when a breaker closes, and an upsert would leave stale rows
that rehydration would resurrect as phantom cooldowns. At homelab scale the row
count is small enough that a full rewrite is the cheaper correct answer.

- [x] **Step 1: Write the failing test**

Create `internal/store/health_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

func TestHealthRoundTrip(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	cooling := time.Now().Add(time.Minute).UTC().Truncate(time.Millisecond)

	in := []health.Entry{
		{Key: health.Key{ProviderID: "groq", KeyID: "k1", Model: "m"},
			CoolingUntil: cooling, BackoffLevel: 2, ConsecutiveFailures: 5},
		{Key: health.Key{ProviderID: "groq", KeyID: "k1"},
			BackoffLevel: 1, ConsecutiveFailures: 3},
	}
	if err := db.SaveHealth(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := db.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2", len(out))
	}

	byKey := map[health.Key]health.Entry{}
	for _, e := range out {
		byKey[e.Key] = e
	}
	got := byKey[health.Key{ProviderID: "groq", KeyID: "k1", Model: "m"}]
	if !got.CoolingUntil.Equal(cooling) {
		t.Errorf("CoolingUntil = %s, want %s", got.CoolingUntil, cooling)
	}
	if got.BackoffLevel != 2 || got.ConsecutiveFailures != 5 {
		t.Errorf("entry = %+v", got)
	}

	// The credential-level entry has no cooldown; it must come back as a zero
	// time rather than the Unix epoch, or rehydration treats it as expired-long-ago
	// and the distinction stops being visible.
	cred := byKey[health.Key{ProviderID: "groq", KeyID: "k1"}]
	if !cred.CoolingUntil.IsZero() {
		t.Errorf("CoolingUntil = %s, want the zero time", cred.CoolingUntil)
	}
}

func TestSaveHealthReplacesPreviousState(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.SaveHealth(ctx, []health.Entry{
		{Key: health.Key{ProviderID: "a", KeyID: "k", Model: "m"}, ConsecutiveFailures: 1},
		{Key: health.Key{ProviderID: "b", KeyID: "k", Model: "m"}, ConsecutiveFailures: 1},
	}); err != nil {
		t.Fatal(err)
	}
	// Provider b's breaker closed, so it is absent from the second snapshot.
	if err := db.SaveHealth(ctx, []health.Entry{
		{Key: health.Key{ProviderID: "a", KeyID: "k", Model: "m"}, ConsecutiveFailures: 2},
	}); err != nil {
		t.Fatal(err)
	}
	out, err := db.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Key.ProviderID != "a" || out[0].ConsecutiveFailures != 2 {
		t.Fatalf("got %+v, want only provider a at 2 failures", out)
	}
}

func TestSaveHealthWithNoEntriesClearsTheTable(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.SaveHealth(ctx, []health.Entry{
		{Key: health.Key{ProviderID: "a", KeyID: "k", Model: "m"}, ConsecutiveFailures: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.SaveHealth(ctx, nil); err != nil {
		t.Fatal(err)
	}
	out, err := db.LoadHealth(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("got %+v, want none", out)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Health -v`
Expected: FAIL — `db.SaveHealth undefined`.

- [x] **Step 3: Write the queries**

Create `internal/store/health.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

// SaveHealth replaces the persisted breaker state with the given snapshot.
func (d *DB) SaveHealth(ctx context.Context, entries []health.Entry) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin health save: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// A full replace rather than an upsert: an entry vanishes when its breaker
	// closes, and a stale row would be rehydrated as a phantom cooldown.
	if _, err := tx.ExecContext(ctx, `DELETE FROM health`); err != nil {
		return fmt.Errorf("clear health: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO health
		   (provider_id, key_id, model, cooling_until, backoff_level, consecutive_failures, updated_at)
		 VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()
	for _, e := range entries {
		var cooling *int64
		if !e.CoolingUntil.IsZero() {
			v := e.CoolingUntil.UnixMilli()
			cooling = &v
		}
		if _, err := stmt.ExecContext(ctx,
			e.Key.ProviderID, e.Key.KeyID, e.Key.Model,
			cooling, e.BackoffLevel, e.ConsecutiveFailures, now,
		); err != nil {
			return fmt.Errorf("insert health for %s/%s/%s: %w",
				e.Key.ProviderID, e.Key.KeyID, e.Key.Model, err)
		}
	}
	return tx.Commit()
}

// LoadHealth reads the persisted state for rehydration at startup.
func (d *DB) LoadHealth(ctx context.Context) ([]health.Entry, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, key_id, model, cooling_until, backoff_level, consecutive_failures
		   FROM health`)
	if err != nil {
		return nil, fmt.Errorf("load health: %w", err)
	}
	defer rows.Close()

	var out []health.Entry
	for rows.Next() {
		var (
			e       health.Entry
			cooling *int64
		)
		if err := rows.Scan(&e.Key.ProviderID, &e.Key.KeyID, &e.Key.Model,
			&cooling, &e.BackoffLevel, &e.ConsecutiveFailures); err != nil {
			return nil, fmt.Errorf("scan health: %w", err)
		}
		if cooling != nil {
			// UTC because the breaker compares against time.Now(), and a
			// location-carrying value would still compare correctly but read
			// confusingly in the admin UI later.
			e.CoolingUntil = time.UnixMilli(*cooling).UTC()
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS, forty tests.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): persist and reload breaker state"
```

---

### Task 14: The debounced health persister

**Files:**
- Create: `internal/health/persist.go`
- Test: `internal/health/persist_test.go`

**Interfaces:**
- Consumes: `Breaker.Snapshot`, `Breaker.Rehydrate`, `Breaker.TakeDirty` from Task 12; `(*store.DB).SaveHealth`/`LoadHealth` from Task 13 satisfy the `health.Store` interface.
- Produces: `health.Store` interface, `func health.NewPersister(b *Breaker, s Store, interval time.Duration) *Persister`, `func (*Persister) Restore(ctx context.Context) error`, `func (*Persister) Run(ctx context.Context) error`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The interval is the debounce: a provider failing fast changes state many times
between ticks and produces one write, not one write per failure.

Shutdown flushes unconditionally, without consulting the dirty flag. A restart
that dropped the last interval's changes would hand a flapping provider a clean
slate, which is exactly what rehydration exists to prevent.

- [x] **Step 1: Write the failing test**

Create `internal/health/persist_test.go`:

```go
package health

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
)

type fakeStore struct {
	mu     sync.Mutex
	saves  [][]Entry
	loaded []Entry
	err    error
}

func (f *fakeStore) SaveHealth(_ context.Context, e []Entry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	cp := make([]Entry, len(e))
	copy(cp, e)
	f.saves = append(f.saves, cp)
	return nil
}

func (f *fakeStore) LoadHealth(context.Context) ([]Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loaded, f.err
}

func (f *fakeStore) saveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.saves)
}

func (f *fakeStore) last() []Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.saves) == 0 {
		return nil
	}
	return f.saves[len(f.saves)-1]
}

func TestPersisterWritesWhenDirty(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{}
	p := NewPersister(b, fs, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})

	deadline := time.After(3 * time.Second)
	for fs.saveCount() == 0 {
		select {
		case <-deadline:
			cancel()
			t.Fatal("the persister never wrote a dirty snapshot")
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done

	last := fs.last()
	if len(last) != 1 || last[0].ConsecutiveFailures < 1 {
		t.Errorf("snapshot = %+v", last)
	}
}

// Debouncing is the point: many failures between ticks must not produce many
// writes.
func TestPersisterDoesNotWriteWhenClean(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{}
	p := NewPersister(b, fs, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	time.Sleep(60 * time.Millisecond) // many ticks, no state changes
	cancel()
	<-done

	// Only the unconditional shutdown flush may have happened.
	if n := fs.saveCount(); n > 1 {
		t.Errorf("%d writes with no state changes; the dirty flag is not being consulted", n)
	}
}

func TestPersisterFlushesOnShutdownEvenIfClean(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{}
	p := NewPersister(b, fs, time.Hour) // the ticker will never fire

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()

	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if fs.saveCount() != 1 {
		t.Fatalf("shutdown wrote %d snapshots, want 1", fs.saveCount())
	}
	if len(fs.last()) != 1 {
		t.Errorf("shutdown snapshot = %+v", fs.last())
	}
}

func TestRestoreRehydratesTheBreaker(t *testing.T) {
	b := New(3, time.Minute)
	fs := &fakeStore{loaded: []Entry{
		{Key: triple, ConsecutiveFailures: 2},
	}}
	p := NewPersister(b, fs, time.Hour)
	if err := p.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Two failures were restored, so the next one trips at trip_after 3.
	b.Record(triple, Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	if b.Available(triple) {
		t.Fatal("restored failure counters were not applied")
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/health/ -run Persist -v`
Expected: FAIL — `undefined: NewPersister`.

- [x] **Step 3: Write the persister**

Create `internal/health/persist.go`:

```go
package health

import (
	"context"
	"log"
	"time"
)

// Store is the durable side of the breaker. internal/store satisfies it; the
// interface exists so this package does not depend on SQLite.
type Store interface {
	SaveHealth(ctx context.Context, entries []Entry) error
	LoadHealth(ctx context.Context) ([]Entry, error)
}

// Persister copies breaker state to the database on an interval, and once more
// on shutdown.
type Persister struct {
	b        *Breaker
	s        Store
	interval time.Duration
}

func NewPersister(b *Breaker, s Store, interval time.Duration) *Persister {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &Persister{b: b, s: s, interval: interval}
}

// Restore rehydrates the breaker at startup, so a restart does not stampede a
// provider that is still rate-limited.
func (p *Persister) Restore(ctx context.Context) error {
	entries, err := p.s.LoadHealth(ctx)
	if err != nil {
		return err
	}
	p.b.Rehydrate(entries)
	return nil
}

// Run writes changed state until ctx is cancelled, then flushes unconditionally.
func (p *Persister) Run(ctx context.Context) error {
	t := time.NewTicker(p.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			// A background context, because the shutdown context is already
			// cancelled and would abort the write this flush exists to perform.
			return p.s.SaveHealth(context.Background(), p.b.Snapshot())
		case <-t.C:
			// The interval is the debounce. A provider failing fast changes
			// state many times between ticks and costs one write.
			if !p.b.TakeDirty() {
				continue
			}
			if err := p.s.SaveHealth(ctx, p.b.Snapshot()); err != nil {
				// Logged rather than fatal: losing a health write costs a
				// restart's worth of accuracy, not correctness.
				log.Printf("health persister: %v", err)
			}
		}
	}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/health/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/health/
git commit -m "feat(health): persist breaker state on an interval"
```

---

### Task 15: Record outcomes against the breaker, and separate a disconnect from a deadline

**Files:**
- Modify: `internal/exec/exec.go`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `health.Key`, `health.Signal`, `health.ParseRetryAfter` from Task 12.
- Produces: `exec.HealthRecorder` interface and `Deps.Health`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The upstream context derives from the inbound one, so a client hanging up
surfaces as `context.Canceled` — indistinguishable at the transport layer from a
deadline the executor imposed itself. The cause is the discriminator. Without
it, pressing Ctrl-C in Claude Code trips breakers on perfectly healthy providers.

Check the Darkrouter deadline **first**. A total-timeout expiry cancels the
derived context, and if the client also went away in the same instant the
disconnect check would otherwise win and a genuine provider timeout would go
unrecorded.

- [x] **Step 1: Write the failing test**

Append to `internal/exec/exec_test.go`:

```go
type captureHealth struct {
	mu      sync.Mutex
	keys    []health.Key
	signals []health.Signal
}

func (c *captureHealth) Record(k health.Key, s health.Signal) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, k)
	c.signals = append(c.signals, s)
}

func (c *captureHealth) only(t *testing.T) (health.Key, health.Signal) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.signals) != 1 {
		t.Fatalf("got %d signals, want 1", len(c.signals))
	}
	return c.keys[0], c.signals[0]
}

func TestHandleRecordsHealthOnAProviderFailure(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer up.Close()
	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	k, s := h.only(t)
	if k.ProviderID != "fake" || k.Model != "m" {
		t.Errorf("key = %+v", k)
	}
	if s.Outcome != adapter.OutcomeRetryableProvider || s.StatusCode != 503 {
		t.Errorf("signal = %+v", s)
	}
	if s.HasRetryAfter {
		t.Error("no Retry-After was sent, but one was recorded")
	}
}

func TestHandleForwardsRetryAfter(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(429)
	}))
	defer up.Close()
	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	_, s := h.only(t)
	if s.StatusCode != 429 || !s.HasRetryAfter || s.RetryAfter != 42*time.Second {
		t.Errorf("signal = %+v", s)
	}
}

func TestHandleRecordsSuccess(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	_, s := h.only(t)
	if s.Outcome != adapter.OutcomeSuccess {
		t.Errorf("Outcome = %q, want success", s.Outcome)
	}
}

// A done criterion: a client disconnect leaves every provider healthy.
func TestClientDisconnectIsNotAProviderFailure(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // hold the upstream open until the client has gone
		w.WriteHeader(200)
	}))
	defer up.Close()
	defer close(release)

	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 0)

	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"ping"}]}`))
	ctx, cancel := context.WithCancel(r.Context())
	r = r.WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Handle(rec, r, openaiedge.New())
	}()
	time.Sleep(50 * time.Millisecond)
	cancel() // the client hung up
	<-done

	_, s := h.only(t)
	if s.Outcome != adapter.OutcomeClientCancelled {
		t.Fatalf("Outcome = %q, want client_cancelled — a disconnect must never "+
			"count against a provider", s.Outcome)
	}
}

// A Darkrouter-imposed deadline is a provider timeout and must be recorded.
func TestDarkrouterDeadlineIsAProviderFailure(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(200)
	}))
	defer up.Close()
	defer close(release)

	h := &captureHealth{}
	e := newExecutorWith(t, up.URL, Deps{Health: h}, 80*time.Millisecond)

	post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`)

	_, s := h.only(t)
	if s.Outcome != adapter.OutcomeRetryableProvider {
		t.Fatalf("Outcome = %q, want retryable_provider — a Darkrouter deadline "+
			"is a provider timeout, not a client disconnect", s.Outcome)
	}
}

func TestHandleWithNoHealthRecorderDoesNotPanic(t *testing.T) {
	up := unaryUpstream()
	defer up.Close()
	e := newExecutorWith(t, up.URL, Deps{}, 0)
	if rec := post(t, e, `{"model":"m","messages":[{"role":"user","content":"ping"}]}`); rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
}
```

Add `github.com/darkraise/darkrouter/internal/adapter` and
`github.com/darkraise/darkrouter/internal/health` to the test file's imports.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -v`
Expected: FAIL — `unknown field Health in struct literal`.

- [x] **Step 3: Add the recorder to Deps**

In `internal/exec/exec.go`:

```go
// HealthRecorder receives one signal per attempt. It must not block: the
// breaker answers under a mutex and returns immediately.
type HealthRecorder interface {
	Record(k health.Key, s health.Signal)
}

type Deps struct {
	Log    Logger
	Health HealthRecorder
}

func (e *Executor) recordHealth(k health.Key, s health.Signal) {
	if e.deps.Health == nil {
		return
	}
	e.deps.Health.Record(k, s)
}
```

- [x] **Step 4: Fix classification and record the signal**

Replace `classify` in `internal/exec/exec.go`:

```go
// classify asks the adapter, then overrides for the two cases no adapter can
// see: a Darkrouter-imposed deadline, and a cancellation whose origin is the
// inbound request rather than the upstream.
//
// The deadline is checked first. Both cancel the same derived context, and if
// the client also disappears in that instant, checking the disconnect first
// would silently reclassify a genuine provider timeout.
func (e *Executor) classify(inbound, upstream context.Context, resp *http.Response, err error) adapter.Outcome {
	if err == nil {
		return e.ad.Classify(resp, nil)
	}
	if errors.Is(context.Cause(upstream), errDarkrouterTimeout) {
		return adapter.OutcomeRetryableProvider
	}
	if errors.Is(inbound.Err(), context.Canceled) {
		return adapter.OutcomeClientCancelled
	}
	return e.ad.Classify(resp, err)
}
```

In `Handle`, change the call site and record the signal. Replace the block from
`resp, doErr := e.client.Do(hr)` down to the end of the attempt record with:

```go
	attemptStart := time.Now()
	resp, doErr := e.client.Do(hr)
	outcome := e.classify(r.Context(), ctx, resp, doErr)

	attempt := store.AttemptRecord{
		Seq: 0, ProviderID: p.ID, KeyID: p.KeyID, Model: req.Model,
		Outcome:   string(outcome),
		LatencyMs: time.Since(attemptStart).Milliseconds(),
	}
	if resp != nil {
		attempt.StatusCode = resp.StatusCode
	}
	if doErr != nil {
		attempt.Error = doErr.Error()
	}
	rec.Attempts = append(rec.Attempts, attempt)

	hk := health.Key{ProviderID: p.ID, KeyID: p.KeyID, Model: req.Model}
	sig := health.Signal{Outcome: outcome}
	if resp != nil {
		sig.StatusCode = resp.StatusCode
		// Read before the body is closed; a 429's Retry-After is the difference
		// between a precise cooldown and the generic ladder.
		if d, ok := health.ParseRetryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
			sig.RetryAfter, sig.HasRetryAfter = d, true
		}
	}
	e.recordHealth(hk, sig)
```

In the `ParseResponse` failure branch, record the reclassification too — a 2xx
that cannot be read is a provider fault and must reach the breaker:

```go
	out, err := e.ad.ParseResponse(resp)
	if err != nil {
		e2 := errorFor(adapter.OutcomeRetryableProvider, err)
		rec.ErrorCode = string(e2.Type)
		rec.Attempts[0].Outcome = string(adapter.OutcomeRetryableProvider)
		rec.Attempts[0].Error = err.Error()
		e.recordHealth(hk, health.Signal{
			Outcome: adapter.OutcomeRetryableProvider, StatusCode: resp.StatusCode,
		})
		_ = d.WriteError(w, e2)
		return
	}
```

Add `github.com/darkraise/darkrouter/internal/health` to the imports.

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./... -race`
Expected: PASS across every package.

- [x] **Step 6: Commit**

```bash
git add internal/exec/
git commit -m "feat(exec): record attempt outcomes against health"
```

---

### Task 16: The usage rollup worker

**Files:**
- Create: `internal/store/rollup.go`
- Test: `internal/store/rollup_test.go`

**Interfaces:**
- Consumes: the `requests` table from Task 3 and records written by Task 10.
- Produces: `func (*DB) Rollup(ctx context.Context, now time.Time) error`, `func store.RunRollup(ctx context.Context, d *DB, interval time.Duration) error`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

Hourly, recomputing today and idempotently finalizing yesterday in UTC.
Finalization is recomputation rather than an incremental add, so a request that
starts before midnight and finishes after it is counted once, in the day it
began, no matter how many times the worker runs.

- [x] **Step 1: Write the failing test**

Create `internal/store/rollup_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func insertRequest(t *testing.T, db *DB, id string, ts time.Time, provider, model string, in, out int64, cost *int64) {
	t.Helper()
	_, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO requests (id, ts, dialect, surface, requested_model,
		    final_provider_id, final_model, status, tokens_in, tokens_out, cost_micros)
		 VALUES (?,?,'openai','chat',?,?,?,'success',?,?,?)`,
		id, ts.UnixMilli(), model, provider, model, in, out, cost)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRollupAggregatesByDayProviderAndModel(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)

	insertRequest(t, db, "a", now.Add(-2*time.Hour), "groq", "m", 10, 20, nil)
	insertRequest(t, db, "b", now.Add(-time.Hour), "groq", "m", 5, 7, nil)
	insertRequest(t, db, "c", now.Add(-time.Hour), "groq", "other", 1, 2, nil)

	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}

	var requests, in, out int64
	err := db.Read.QueryRowContext(ctx,
		`SELECT requests, tokens_in, tokens_out FROM usage_daily
		  WHERE day = '2026-08-22' AND provider_id = 'groq' AND model = 'm'`).
		Scan(&requests, &in, &out)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || in != 15 || out != 27 {
		t.Errorf("rollup = %d requests, %d in, %d out", requests, in, out)
	}
}

// Finalization is idempotent recomputation: running it repeatedly must not
// multiply the totals.
func TestRollupIsIdempotent(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	insertRequest(t, db, "a", now.Add(-time.Hour), "groq", "m", 10, 20, nil)

	for i := 0; i < 3; i++ {
		if err := db.Rollup(ctx, now); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	var requests, in int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT requests, tokens_in FROM usage_daily WHERE day='2026-08-22'`).
		Scan(&requests, &in); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || in != 10 {
		t.Errorf("after three runs: %d requests, %d tokens — recomputation is not idempotent", requests, in)
	}
}

// A request that starts before midnight lands in the day it began.
func TestRollupKeysOnRequestStart(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC)
	// 23:59 UTC on the 21st.
	insertRequest(t, db, "spanning", time.Date(2026, 8, 21, 23, 59, 0, 0, time.UTC),
		"groq", "m", 10, 20, nil)

	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	var day string
	if err := db.Read.QueryRowContext(ctx, `SELECT day FROM usage_daily`).Scan(&day); err != nil {
		t.Fatal(err)
	}
	if day != "2026-08-21" {
		t.Errorf("day = %q, want 2026-08-21", day)
	}
}

func TestRollupLeavesCostNullWhenNoRequestIsPriced(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	insertRequest(t, db, "a", now.Add(-time.Hour), "groq", "m", 10, 20, nil)

	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	var cost *int64
	if err := db.Read.QueryRowContext(ctx, `SELECT cost_micros FROM usage_daily`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	// NULL, not 0. Zero would report the day's spend as genuinely nothing.
	if cost != nil {
		t.Errorf("cost_micros = %d, want NULL", *cost)
	}
}

func TestRollupSumsCostWhenPricingExists(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	c1, c2 := int64(1500), int64(2500)
	insertRequest(t, db, "a", now.Add(-time.Hour), "groq", "m", 10, 20, &c1)
	insertRequest(t, db, "b", now.Add(-time.Hour), "groq", "m", 10, 20, &c2)

	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	var cost *int64
	if err := db.Read.QueryRowContext(ctx, `SELECT cost_micros FROM usage_daily`).Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost == nil || *cost != 4000 {
		t.Errorf("cost_micros = %v, want 4000", cost)
	}
}

func TestRollupIgnoresRequestsThatNeverReachedAProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	insertRequest(t, db, "a", now.Add(-time.Hour), "", "", 0, 0, nil)

	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM usage_daily`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("usage_daily has %d rows; a request with no provider has nothing to attribute", n)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Rollup -v`
Expected: FAIL — `db.Rollup undefined`.

- [x] **Step 3: Write the rollup**

Create `internal/store/rollup.go`:

```go
package store

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// Rollup recomputes usage_daily for yesterday and today in UTC.
//
// Two days rather than one: a request logged just after midnight belongs to
// yesterday, and the batching writer may not have flushed it before the day
// turned. Recomputing yesterday on every run is what finalizes it.
func (d *DB) Rollup(ctx context.Context, now time.Time) error {
	utc := now.UTC()
	startOfToday := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	from := startOfToday.AddDate(0, 0, -1)
	to := startOfToday.AddDate(0, 0, 1)

	// The whole window is recomputed and upserted, so running this repeatedly
	// converges rather than accumulating.
	_, err := d.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, requests, tokens_in, tokens_out, cost_micros)
		 SELECT strftime('%Y-%m-%d', ts / 1000, 'unixepoch') AS day,
		        final_provider_id,
		        final_model,
		        count(*),
		        coalesce(sum(tokens_in), 0),
		        coalesce(sum(tokens_out), 0),
		        -- NULL rather than 0 when nothing in the group is priced:
		        -- zero would report the day's spend as genuinely nothing.
		        CASE WHEN count(cost_micros) = 0 THEN NULL ELSE sum(cost_micros) END
		   FROM requests
		  WHERE ts >= ? AND ts < ?
		    AND final_provider_id <> ''
		  GROUP BY day, final_provider_id, final_model
		 ON CONFLICT(day, provider_id, model) DO UPDATE SET
		        requests    = excluded.requests,
		        tokens_in   = excluded.tokens_in,
		        tokens_out  = excluded.tokens_out,
		        cost_micros = excluded.cost_micros`,
		from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	return nil
}

// RunRollup runs the rollup on an interval until ctx is cancelled.
//
// The interval is jittered so that a restart of several services does not line
// every worker up on the same instant.
func RunRollup(ctx context.Context, d *DB, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(interval)):
			if err := d.Rollup(ctx, time.Now()); err != nil {
				// Logged, not fatal: a missed rollup is recomputed on the next
				// run, because finalization is idempotent.
				log.Printf("rollup: %v", err)
			}
		}
	}
}

// jitter spreads a worker's wakeups over the last quarter of its interval.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d - time.Duration(rand.Int63n(int64(d/4)+1))
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): recompute daily usage rollups"
```

---

### Task 17: The retention worker

**Files:**
- Create: `internal/store/retention.go`
- Test: `internal/store/retention_test.go`

**Interfaces:**
- Consumes: the `requests`, `request_attempts`, and `request_bodies` tables from Task 3.
- Produces: `func (*DB) Prune(ctx context.Context, now time.Time, logRetention, captureRetention time.Duration, batch int) (int, error)`, `func store.RunRetention(ctx context.Context, d *DB, cfgStore *config.Store, interval time.Duration) error`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Retention deletes in bounded batches with a pause between them, and the reason
is worth stating precisely: it is not avoiding locking out "the writer" — it
**is** the writer. The pause yields the single write handle so log batches are
not starved behind a long prune.

Deletes do not shrink the file. `auto_vacuum=incremental` was set in the DSN at
Task 2, and a periodic `incremental_vacuum` step is what keeps growth bounded.

Attempts are deleted before their parent requests, while the parent rows are
still present to identify them. Reversing that order orphans every attempt row,
and `request_attempts` deliberately carries no foreign key to cascade from.

Body capture itself is not wired into the executor in this phase — `capture`
appears in the config and the table exists, but nothing writes to it until a
later phase. The prune is implemented and tested now so retention does not have
to be revisited when it does.

- [x] **Step 1: Write the failing test**

Create `internal/store/retention_test.go`:

```go
package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func seedOldRequests(t *testing.T, db *DB, n int, ts time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("old-%06d", i)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO requests (id, ts, dialect, surface, requested_model, status)
			 VALUES (?,?,'openai','chat','m','success')`, id, ts.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO request_attempts (request_id, seq, provider_id, model, outcome)
			 VALUES (?,0,'groq','m','success')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPruneRemovesExpiredRequestsAndAttempts(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now()

	seedOldRequests(t, db, 40, now.Add(-800*time.Hour))
	insertRequest(t, db, "fresh", now.Add(-time.Hour), "groq", "m", 1, 1, nil)

	deleted, err := db.Prune(ctx, now, 720*time.Hour, 72*time.Hour, 7)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 40 {
		t.Errorf("deleted = %d, want 40", deleted)
	}
	if got := countRows(t, db, "requests"); got != 1 {
		t.Errorf("requests = %d, want 1 (the fresh one)", got)
	}
	if got := countRows(t, db, "request_attempts"); got != 0 {
		t.Errorf("request_attempts = %d, want 0 — attempts must go with their requests", got)
	}
}

func TestPruneRemovesExpiredBodies(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now()

	for i, exp := range []time.Time{now.Add(-time.Hour), now.Add(time.Hour)} {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO request_bodies (request_id, request_json, expires_at) VALUES (?, '{}', ?)`,
			fmt.Sprintf("b%d", i), exp.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Prune(ctx, now, 720*time.Hour, 72*time.Hour, 10); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, db, "request_bodies"); got != 1 {
		t.Errorf("request_bodies = %d, want 1 — only the expired one goes", got)
	}
}

func TestPruneIsANoOpWhenNothingHasExpired(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now()
	insertRequest(t, db, "fresh", now.Add(-time.Hour), "groq", "m", 1, 1, nil)

	deleted, err := db.Prune(ctx, now, 720*time.Hour, 72*time.Hour, 10)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
	if got := countRows(t, db, "requests"); got != 1 {
		t.Errorf("requests = %d, want 1", got)
	}
}

// The point of batching: a large backlog must not starve the log writer, which
// shares the single write handle.
func TestPruneDoesNotStarveTheLogWriter(t *testing.T) {
	db := migrated(t)
	seedOldRequests(t, db, 600, time.Now().Add(-800*time.Hour))

	w := NewLogWriter(db, LogOptions{Buffer: 256, BatchSize: 8, FlushEvery: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	writerDone := make(chan error, 1)
	go func() { writerDone <- w.Run(ctx) }()

	pruneDone := make(chan error, 1)
	go func() {
		_, err := db.Prune(context.Background(), time.Now(), 720*time.Hour, 72*time.Hour, 20)
		pruneDone <- err
	}()

	// Stamped now, not with rec's fixed 2023 timestamp: these records must be
	// inside the retention window or the prune would delete them legitimately
	// and the test would blame starvation for correct behaviour.
	// (Correction applied during execution.)
	const newRecords = 120
	for i := 0; i < newRecords; i++ {
		w.Log(recAt(fmt.Sprintf("new-%04d", i), time.Now()))
		time.Sleep(time.Millisecond)
	}

	select {
	case err := <-pruneDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("prune did not finish")
	}

	cancel()
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}

	if w.Dropped() != 0 {
		t.Errorf("the log writer dropped %d records while retention ran", w.Dropped())
	}
	var n int
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT count(*) FROM requests WHERE id LIKE 'new-%'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != newRecords {
		t.Errorf("%d of %d new records survived; retention starved the writer", n, newRecords)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run Prune -v`
Expected: FAIL — `db.Prune undefined`.

- [x] **Step 3: Write the retention worker**

Create `internal/store/retention.go`:

```go
package store

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

const (
	defaultPruneBatch = 500
	// prunePause yields the single write handle between batches so queued log
	// batches are not stuck behind a long prune.
	prunePause = 25 * time.Millisecond
)

// Prune deletes expired log rows in bounded batches and runs one incremental
// vacuum step. It returns the number of request rows deleted.
func (d *DB) Prune(ctx context.Context, now time.Time,
	logRetention, captureRetention time.Duration, batch int) (int, error) {

	if batch <= 0 {
		batch = defaultPruneBatch
	}
	total := 0

	logCutoff := now.Add(-logRetention).UnixMilli()
	for {
		n, err := d.pruneRequestBatch(ctx, logCutoff, batch)
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			break
		}
		if err := pause(ctx); err != nil {
			return total, err
		}
	}

	// Bodies expire on their own stored deadline rather than on log.retention,
	// because capture.retention is deliberately much shorter.
	for {
		n, err := d.pruneBodyBatch(ctx, now.UnixMilli(), batch)
		if err != nil {
			return total, err
		}
		if n == 0 {
			break
		}
		if err := pause(ctx); err != nil {
			return total, err
		}
	}

	// Deletes do not shrink the file. auto_vacuum=incremental was set in the
	// DSN; this is the step that actually returns pages.
	if _, err := d.Write.ExecContext(ctx, `PRAGMA incremental_vacuum(1000)`); err != nil {
		return total, fmt.Errorf("incremental vacuum: %w", err)
	}
	return total, nil
}

func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(prunePause):
		return nil
	}
}

func (d *DB) pruneRequestBatch(ctx context.Context, cutoff int64, batch int) (int, error) {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Attempts go first, while their parent rows are still present to identify
	// them. request_attempts carries no foreign key, so nothing cascades.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM request_attempts
		  WHERE request_id IN (SELECT id FROM requests WHERE ts < ? ORDER BY ts LIMIT ?)`,
		cutoff, batch); err != nil {
		return 0, fmt.Errorf("prune attempts: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM requests
		  WHERE id IN (SELECT id FROM requests WHERE ts < ? ORDER BY ts LIMIT ?)`,
		cutoff, batch)
	if err != nil {
		return 0, fmt.Errorf("prune requests: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit prune: %w", err)
	}
	return int(n), nil
}

func (d *DB) pruneBodyBatch(ctx context.Context, nowMillis int64, batch int) (int, error) {
	// SQLite's DELETE ... LIMIT needs a compile-time option that is not enabled
	// here, so the bound goes in a subquery.
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM request_bodies
		  WHERE request_id IN (
		        SELECT request_id FROM request_bodies WHERE expires_at < ? LIMIT ?)`,
		nowMillis, batch)
	if err != nil {
		return 0, fmt.Errorf("prune bodies: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// RunRetention prunes on an interval until ctx is cancelled. Retention windows
// are read live from the config store, because log.retention hot-reloads.
func RunRetention(ctx context.Context, d *DB, cfgStore *config.Store, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(interval)):
			cfg := cfgStore.Current()
			n, err := d.Prune(ctx, time.Now(), cfg.Log.Retention, cfg.Capture.Retention, 0)
			if err != nil {
				log.Printf("retention: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("retention: pruned %d request records", n)
			}
		}
	}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -race -v`
Expected: PASS. `TestPruneDoesNotStarveTheLogWriter` is slow by design; it must
still finish well inside its 30-second guard.

- [x] **Step 5: Commit**

```bash
git add internal/store/
git commit -m "feat(store): prune expired log rows in batches"
```

---

### Task 18: Wire the store, workers, and shutdown ordering into the server

**Files:**
- Modify: `internal/server/server.go`
- Modify: `cmd/darkrouter/main.go`
- Test: `internal/server/run_test.go`

**Interfaces:**
- Consumes: everything from Tasks 2 through 17.
- Produces: `func server.New(cfgStore *config.Store, db *store.DB, key *crypto.Key, startupWarnings []string) (*Server, error)`, and the `-db` flag on the server subcommand.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Shutdown drains in the order master design §16 fixes: stop accepting proxy
connections, let in-flight requests finish within `server.shutdown_grace`, drain
the log channel, flush health state, close the database.

That ordering is the whole point of giving the workers their own context. If the
workers shared the request lifecycle context, cancelling it would stop the log
writer while requests were still producing records, and the drain would run
against a channel that was still being filled.

- [x] **Step 1: Write the failing test**

Append to `internal/server/run_test.go`:

```go
func TestHealthzReportsDroppedRecordsAndWarnings(t *testing.T) {
	dir := t.TempDir()
	cfgStore := testConfigStore(t, dir)

	db, err := store.Open(filepath.Join(dir, "darkrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}

	s, err := New(cfgStore, db, key, []string{"a startup warning"})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rr, httptest.NewRequest("GET", "/healthz", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["log_records_dropped"]; !ok {
		t.Error("healthz must report the log drop counter; without it a shortfall in spend is invisible")
	}
	warnings, _ := body["warnings"].([]any)
	found := false
	for _, w := range warnings {
		if s, _ := w.(string); s == "a startup warning" {
			found = true
		}
	}
	if !found {
		t.Errorf("startup warnings must reach healthz, got %v", warnings)
	}
}

func TestMetricsReportsCounters(t *testing.T) {
	dir := t.TempDir()
	cfgStore := testConfigStore(t, dir)
	db, err := store.Open(filepath.Join(dir, "darkrouter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, _ := store.OpenKeyring(ctx, db, "master")
	s, err := New(cfgStore, db, key, nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(rr, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rr.Body.String(), "darkrouter_log_records_dropped_total") {
		t.Errorf("metrics = %s", rr.Body.String())
	}
}

// A done criterion: a cooldown survives a restart.
func TestCooldownSurvivesAGracefulRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.db")
	ctx := context.Background()
	k := health.Key{ProviderID: "groq", KeyID: "k1", Model: "m"}

	// First process: cool the triple, then flush on shutdown.
	db1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db1.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	b1 := health.New(3, 15*time.Minute)
	p1 := health.NewPersister(b1, db1, time.Hour)
	for i := 0; i < 3; i++ {
		b1.Record(k, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 503})
	}
	if b1.Available(k) {
		t.Fatal("the triple should be cooling before the restart")
	}
	runCtx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p1.Run(runCtx) }()
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := db1.Close(); err != nil {
		t.Fatal(err)
	}

	// Second process: rehydrate and confirm the cooldown and counter survived.
	db2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	b2 := health.New(3, 15*time.Minute)
	p2 := health.NewPersister(b2, db2, time.Hour)
	if err := p2.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if b2.Available(k) {
		t.Fatal("the cooldown did not survive the restart")
	}
	snap := b2.Snapshot()
	if len(snap) != 1 || snap[0].ConsecutiveFailures != 3 {
		t.Errorf("failure counter did not survive: %+v", snap)
	}
}
```

Add a helper beside the existing ones in `run_test.go`:

```go
func testConfigStore(t *testing.T, dir string) *config.Store {
	t.Helper()
	path := filepath.Join(dir, "darkrouter.yaml")
	body := "server:\n  proxy_listen: :0\n  admin_listen: :0\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgStore, err := config.NewStore(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return cfgStore
}
```

Its imports are `encoding/json`, `os`, `path/filepath`, `strings`, `time`, plus
`internal/adapter`, `internal/config`, `internal/health`, and `internal/store`.

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -v`
Expected: FAIL — `too many arguments to New`.

- [x] **Step 3: Rebuild the Server struct and constructor**

In `internal/server/server.go`, replace the struct and `New`:

```go
type Server struct {
	store   *config.Store
	db      *store.DB
	src     *provider.SQLSource
	ex      *exec.Executor
	logw    *store.LogWriter
	breaker *health.Breaker
	persist *health.Persister

	started  time.Time
	warnings []string
}

// New wires the gateway. It loads the provider set eagerly so a bad credential
// fails startup rather than every request.
func New(cfgStore *config.Store, db *store.DB, key *crypto.Key, startupWarnings []string) (*Server, error) {
	cfg := cfgStore.Current()

	src := provider.NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		return nil, fmt.Errorf("load providers: %w", err)
	}

	logw := store.NewLogWriter(db, store.LogOptions{})
	breaker := health.New(*cfg.Policy.Cooldown.TripAfter, cfg.Policy.Cooldown.Max)

	return &Server{
		store: cfgStore, db: db, src: src, logw: logw, breaker: breaker,
		persist: health.NewPersister(breaker, db, 5*time.Second),
		ex: exec.New(cfgStore, src, openaicompat.New(), exec.Deps{
			Log: logw, Health: breaker,
		}),
		started:  time.Now(),
		warnings: startupWarnings,
	}, nil
}
```

Add `internal/crypto`, `internal/health`, and `internal/store` to the imports.

- [x] **Step 4: Report the counters on healthz and metrics**

Replace the `/healthz` and `/metrics` handlers in `AdminHandler`:

```go
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.Current()
		// Read once: two calls could straddle a reload and report a valid config
		// with an error attached, or an invalid one with none.
		cfgErr := s.store.LastError()

		// Startup warnings first: they explain state the config file cannot,
		// such as a providers block that is no longer the source of truth.
		warnings := append(append([]string{}, s.warnings...), cfg.Warnings...)

		body := map[string]any{
			"config_valid": cfgErr == nil,
			"warnings":     warnings,
			"uptime":       time.Since(s.started).Round(time.Second).String(),
			"version":      Version,
			// A non-zero count means usage_daily is a lower bound. It counts
			// records, not tokens or dollars.
			"log_records_dropped": s.logw.Dropped(),
			"log_records_written": s.logw.Written(),
		}
		if cfgErr != nil {
			body["config_error"] = cfgErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintf(w,
			"# HELP darkrouter_log_records_dropped_total Request records discarded because the log channel was full.\n"+
				"# TYPE darkrouter_log_records_dropped_total counter\n"+
				"darkrouter_log_records_dropped_total %d\n"+
				"# HELP darkrouter_log_records_written_total Request records persisted.\n"+
				"# TYPE darkrouter_log_records_written_total counter\n"+
				"darkrouter_log_records_written_total %d\n",
			s.logw.Dropped(), s.logw.Written())
	})
```

- [x] **Step 5: Start the workers and fix the shutdown order**

In `Run`, add the worker context and goroutines before the listeners are bound,
and stop them after the proxy has drained. Insert immediately after the existing
`ctx, cancel := context.WithCancel(ctx)` / `defer cancel()` pair:

```go
	// Workers get a context independent of the request lifecycle. Cancelling
	// them with the handlers would stop the log writer while requests were
	// still producing records, and the drain would race the producers.
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	startWorker := func(name string, fn func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := fn(workerCtx); err != nil {
				log.Printf("%s: %v", name, err)
			}
		}()
	}

	if err := s.persist.Restore(workerCtx); err != nil {
		// Not fatal: an unreadable health table costs a restart's worth of
		// accuracy, and refusing to serve over it would be worse.
		s.store.RecordError(fmt.Errorf("health rehydration: %w", err))
	}

	startWorker("log writer", s.logw.Run)
	startWorker("health persister", s.persist.Run)
	startWorker("rollup", func(c context.Context) error {
		return store.RunRollup(c, s.db, time.Hour)
	})
	startWorker("retention", func(c context.Context) error {
		return store.RunRetention(c, s.db, s.store, time.Hour)
	})
	startWorker("config watcher", func(c context.Context) error {
		// A watcher that cannot start leaves hot reload silently dead, so the
		// failure has to reach /healthz rather than being discarded.
		s.store.RecordError(s.store.Watch(c))
		return nil
	})
```

Delete the existing anonymous goroutine that called `s.store.Watch(ctx)`; the
watcher is now one of the workers.

At the end of `Run`, after `_ = admin.Shutdown(drain)` and `_ = admin.Close()`
and before the final `return`, stop the workers and wait:

```go
	// In-flight requests have finished, so nothing more will be produced.
	// Stopping the workers now drains the log channel and flushes health, in
	// the order master design §16 fixes.
	stopWorkers()
	workers.Wait()
	return shutdownErr
```

Add `log` and `sync` to the imports.

- [x] **Step 6: Rebuild main to open the database first**

In `cmd/darkrouter/main.go`, replace `runServer`:

```go
func runServer(args []string) error {
	fs := flag.NewFlagSet("darkrouter", flag.ExitOnError)
	path := fs.String("config", "darkrouter.yaml", "path to the configuration file")
	dbPath := fs.String("db", "", "path to the database file (default: darkrouter.db beside the config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(filepath.Dir(*path), "darkrouter.db")
	}

	cfgStore, err := config.NewStore(*path, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	// Closed last, after Run has drained the log channel and flushed health.
	defer db.Close()

	if err := db.Migrate(context.Background()); err != nil {
		return err
	}
	key, err := store.OpenKeyring(context.Background(), db, os.Getenv("DARKROUTER_MASTER_KEY"))
	if err != nil {
		return err
	}

	cfg := cfgStore.Current()
	var warnings []string

	res, err := store.ImportFromConfig(context.Background(), db, key, cfg)
	if err != nil {
		return err
	}
	if res.Imported {
		log.Printf("imported %d providers from %s into the database", res.Providers, *path)
	}
	stale, err := store.StaleBlockWarning(context.Background(), db, cfg)
	if err != nil {
		return err
	}
	if stale != "" {
		warnings = append(warnings, stale)
	}

	srv, err := server.New(cfgStore, db, key, warnings)
	if err != nil {
		return err
	}

	log.Printf("darkrouter %s listening: proxy %s admin %s",
		server.Version, cfg.Server.ProxyListen, cfg.Server.AdminListen)
	for _, w := range append(warnings, cfg.Warnings...) {
		log.Printf("config warning: %s", w)
	}

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	log.Print("darkrouter stopped")
	return nil
}
```

Add `path/filepath` and `github.com/darkraise/darkrouter/internal/store` to the
imports.

- [x] **Step 7: Run the whole suite under the race detector**

Run: `go test ./... -race -count=1`
Expected: PASS across every package.

Run: `go vet ./...`
Expected: no output.

- [x] **Step 8: Verify it starts, serves, and rotates**

```bash
go build ./cmd/darkrouter
mkdir -p /tmp/dr2 && cp darkrouter.example.yaml /tmp/dr2/darkrouter.yaml
cd /tmp/dr2
DARKROUTER_MASTER_KEY=test-master GROQ_KEY=placeholder \
  darkrouter -config ./darkrouter.yaml &
sleep 2
curl -s localhost:8081/healthz    # config_valid true, log_records_dropped 0
curl -s localhost:8081/metrics    # darkrouter_log_records_dropped_total 0
```

Expected: the first start logs `imported 1 providers`, a second start does not,
and the second start's `/healthz` carries the stale-block warning naming the
import date. Stop the process, then:

```bash
printf 'new-master\n' | DARKROUTER_MASTER_KEY=test-master darkrouter rotate-key -db ./darkrouter.db
DARKROUTER_MASTER_KEY=test-master darkrouter -config ./darkrouter.yaml
```

Expected: the last command fails with the message naming `DARKROUTER_MASTER_KEY`,
and starting with `DARKROUTER_MASTER_KEY=new-master` succeeds.

- [x] **Step 9: Commit**

```bash
git add internal/server/ cmd/darkrouter/
git commit -m "feat(server): wire persistence, health, and workers"
```

---

## Done criteria

Check each against spec §11 before calling the phase complete.

- [x] Provider connections and credentials survive a restart; credentials are unreadable in the raw database file and a swapped ciphertext fails to decrypt. *(Tasks 6, 8)*
- [x] A wrong `DARKROUTER_MASTER_KEY` fails startup with a clear message; `darkrouter rotate-key` re-encrypts everything atomically. *(Tasks 5, 7)*
- [x] Every request produces one `requests` row and one `request_attempts` row per attempt, **except under log-channel saturation**, where the drop counter reports the shortfall and spend is a documented lower bound. *(Tasks 10, 11, 18)*
- [x] A provider returning 429 is recorded as cooling and the cooldown survives a restart; three consecutive 5xx are required before cooling, and one is not. *(Tasks 12, 13, 14, 18)*
- [x] A client disconnect leaves every provider healthy. *(Task 15)*
- [x] Log writing under sustained load does not increase request latency. *(Task 10's non-blocking `Log`, Task 17's starvation test)*
- [x] `go test ./... -race` passes and `go vet ./...` is clean. *(Task 18)*

## Carried into Phase 3

Recorded so Phase 3 does not rediscover them:

- **Health is recorded but not routed on.** Phase 2 has one candidate, so `Breaker.Available` is exercised only by tests. A cooling sole candidate is still attempted, because there is nowhere to fail over to and refusing to serve would make the gateway less useful than Phase 1 was.
- **One credential per provider.** `SQLSource` selects the first enabled credential. `KeyID` is recorded correctly so the health triples are right, but choosing among credentials is Phase 3's attempt loop.
- **`policy.timeout.idle` is still unenforced**, and `connect`/`first_byte` are still restart-only. Both need Phase 3's commit machinery.
- **Body capture is not wired in.** `capture.*` is parsed and `request_bodies` is created and pruned, but nothing writes to it yet.
- **A fully dead provider costs `trip_after x models x credentials` failures to cool completely**, because health is triple-keyed. Spec §7.3 accepts this at homelab scale rather than solving it, and puts the remedy in the admin overview — surfacing "many triples cooling on one provider" as a provider-level signal. That screen is Phase 7, so nothing in Phase 2 or 3 makes the cause visible.
