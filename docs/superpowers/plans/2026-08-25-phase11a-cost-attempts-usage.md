# Cost, Attempts and the Usage Dimension — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make spend real — compute cost at commit time from catalog pricing, count the tokens failed attempts burn, and give `usage_daily` the alias dimension the operator console's routing graph reads.

**Architecture:** Three changes that all land in the same table. Cost is computed once, in `(*Executor).log` — the single funnel every request record passes through — from the catalog price of the model that actually served. `request_attempts` gains usage columns so pre-commit failures stop being invisible, and the rollup adds them to the day. `usage_daily`'s primary key widens to carry `alias`, which is one migration over a table the rollup rewrite is already opening.

**Tech Stack:** Go 1.26.1, SQLite via `internal/store` (numbered SQL migrations, `STRICT` tables), `net/http` admin handlers, table-driven tests with `-race`.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` — §8.2 (the usage and overview extensions), §8.3 (both backend capabilities), §8.4 step 1.

## Global Constraints

Copy these values verbatim. Every task's requirements implicitly include this section.

**Build and test.** Go 1.26.1 at `/usr/local/go`; `PATH` needs `/usr/local/go/bin`. `-race` requires cgo, so gcc must be present and `CGO_ENABLED` stays 1. Every task ends race-clean: `go test -race ./...`. `go vet ./...` must be clean before any commit.

**Migrations are append-only.** Add `internal/store/migrations/000N_<name>.sql`; never edit a shipped migration. Migrations run in filename order at open. The next free number is `0006`. Tables are `STRICT`. A column added to an existing table needs a `DEFAULT` because rows already exist — this is what `0005_attempt_path.sql` does and is the pattern to follow.

**`internal/store` cannot import `internal/catalog`.** `catalog` imports `provider` and `provider` imports `store`, so a store→catalog edge closes a cycle and the package will not build. Anything needing `catalog.Pricing` lives in `catalog` or in a package that already depends on it, such as `internal/exec`.

**Unpriced is nil, never zero.** `CostMicros` is `*int64` throughout. A model with no catalog price stays `nil`; zero would report genuinely-free. The rollup already encodes this as `CASE WHEN count(cost_micros) = 0 THEN NULL ELSE sum(cost_micros) END` and that behaviour must survive every change here. Per-call image pricing has no catalog field and stays unpriced — §8.3 states this explicitly.

**Pricing units.** `catalog.Pricing` is micro-dollars per **million** tokens: `InputMicrosPerMTok`, `OutputMicrosPerMTok`, `CacheReadMicrosPerMTok`, and `Known bool` which separates a free model from an unpriced one — both are zero.

**There is no `db.LogRequest`.** A request row is written by building a writer and flushing a
batch: `w := NewLogWriter(db, LogOptions{})` then
`w.writeBatch(ctx, []*RequestRecord{rec})`. `writeBatch` is synchronous and returns
`(int, error)`, which is what a test wants — `LogWriter.Run` is the asynchronous path and needs a
goroutine and a drain. `internal/store/log_test.go` uses both; follow `writeBatch` for a test that
just needs a row on disk.

**Test helpers already exist; do not invent them.** `internal/store` tests get a fully-migrated
database from `migrated(t) *DB` (89 existing uses). `internal/admin` tests use
`testServerFull(t) (server, db)`, `login(t, s) (cookie, token)` and
`do(t, s, cookie, token, method, path, body)`; admin endpoints reject an unauthenticated request
with 401.

**Commit style.** `<type>(<scope>): <subject>`, type = feat|fix|docs|style|refactor|test|chore|perf. Subject ≤50 chars, imperative, no period. English only, everywhere.

**Language.** allowlist/blocklist, primary/replica, placeholder/example, main branch.

**`RequestRecord.TS` is a `time.Time`,** not milliseconds — the column is
integer milliseconds but the Go struct is not. Every test in this plan that
builds a record passes a `time.Time`. `FailoverRow.TS` is `int64` milliseconds
because it is scanned straight out of SQL for a JSON response.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/store/migrations/0006_usage_alias_and_attempt_usage.sql` | **Create.** Widens `usage_daily`'s key with `alias`; adds `tokens_in`/`tokens_out`/`cost_micros` to `request_attempts`. |
| `internal/catalog/pricing.go` | **Create.** `func (p Pricing) CostMicros(in, out, cacheRead int64) *int64` — the arithmetic lives beside the prices it reads. It CANNOT live in `internal/store`: store→catalog→provider→store is an import cycle. |
| `internal/catalog/pricing_test.go` | **Create.** Unit tests for the arithmetic, the unpriced case and rounding. |
| `internal/exec/exec.go:702` | **Modify.** `(*Executor).log` computes cost before handing the record to the Logger. |
| `internal/store/log.go` | **Modify.** `AttemptRecord` gains the three usage fields; the attempt insert writes them. |
| `internal/store/rollup.go` | **Modify.** Group by alias, and add attempt usage to the day. |
| `internal/store/adminstore.go:508` | **Modify.** `UsageByDay` gains a group-by dimension; add `UsageBy`. |
| `internal/admin/usage.go:99` | **Modify.** `handleUsage` accepts `group_by=provider|model|alias`. |
| `internal/admin/usage.go` | **Modify.** `handleOverview` (line 24) gains percentiles, sparkline series and recent failovers; `handleUsage` (line 99) gains `group_by`. Both handlers live in this one file — there is no `overview.go`. |

Tasks 1–3 are the storage and arithmetic; 4–5 the commit path; 6–7 the rollup; 8–9 the endpoints. Each ends with a passing race-clean suite and a commit.

---

## Task 1: The cost arithmetic

Cost is arithmetic over three token counts and three prices. It goes in its own function before anything calls it, because the rounding rule is the part that will be got wrong and a unit test is the cheapest place to pin it.

**Files:**
- Create: `internal/catalog/pricing.go`
- Test: `internal/catalog/pricing_test.go`

**Interfaces:**
- Consumes: `catalog.Pricing` (fields in Global Constraints).
- Produces: `func (p catalog.Pricing) CostMicros(in, out, cacheRead int64) *int64` — a METHOD on Pricing, in package `catalog`. Nil when `!p.Known`, otherwise the total in micro-dollars, rounded half-up.

- [ ] **Step 1: Write the failing test**

Create `internal/catalog/pricing_test.go`:

```go
package catalog

import "testing"

func TestCostMicrosIsNilWhenPriceIsUnknown(t *testing.T) {
	// Unpriced and free are both zero prices. Only Known separates them, and
	// a nil result is what makes the UI render an em-dash instead of $0.00.
	if got := (Pricing{Known: false}).CostMicros(1_000_000, 1_000_000, 0); got != nil {
		t.Fatalf("unpriced model: want nil, got %d", *got)
	}
}

func TestCostMicrosIsZeroForAKnownFreeModel(t *testing.T) {
	got := (Pricing{Known: true}).CostMicros(1_000_000, 1_000_000, 0)
	if got == nil {
		t.Fatal("known free model: want 0, got nil")
	}
	if *got != 0 {
		t.Fatalf("known free model: want 0, got %d", *got)
	}
}

func TestCostMicrosSumsTheThreeRates(t *testing.T) {
	p := Pricing{
		InputMicrosPerMTok:     3_000_000,
		OutputMicrosPerMTok:    15_000_000,
		CacheReadMicrosPerMTok: 300_000,
		Known:                  true,
	}
	// 2M in, 1M out, 4M cache-read = 6_000_000 + 15_000_000 + 1_200_000
	got := p.CostMicros(2_000_000, 1_000_000, 4_000_000)
	if got == nil || *got != 22_200_000 {
		t.Fatalf("want 22200000, got %v", got)
	}
}

func TestCostMicrosRoundsHalfUp(t *testing.T) {
	// 1 token at 1 micro/MTok is 0.000001 micros. Truncation would report
	// every small request as free; half-up keeps a sub-micro request at 0
	// but a half-micro one at 1.
	p := Pricing{InputMicrosPerMTok: 1_000_000, Known: true}
	if got := p.CostMicros(1, 0, 0); got == nil || *got != 1 {
		t.Fatalf("1 token at 1 micro/token: want 1, got %v", got)
	}
	half := Pricing{InputMicrosPerMTok: 1, Known: true}
	if got := half.CostMicros(500_000, 0, 0); got == nil || *got != 1 {
		t.Fatalf("half a micro: want 1 (half-up), got %v", got)
	}
	if got := half.CostMicros(499_999, 0, 0); got == nil || *got != 0 {
		t.Fatalf("just under half a micro: want 0, got %v", got)
	}
}

func TestCostMicrosIgnoresNegativeCounts(t *testing.T) {
	// A provider that reports a negative usage count is malformed, not a
	// refund. Clamping keeps one bad response from making a day's spend
	// smaller than it was.
	p := Pricing{InputMicrosPerMTok: 1_000_000, Known: true}
	if got := p.CostMicros(-5, 0, 0); got == nil || *got != 0 {
		t.Fatalf("negative tokens: want 0, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/catalog/ -run TestCostMicros -v`
Expected: FAIL — `undefined: CostMicros`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/catalog/pricing.go`:

```go
package catalog

// CostMicros is the micro-dollar cost of one request at this price, or nil
// when the model has no price.
//
// A method on Pricing, in package catalog, because the arithmetic needs the
// prices and internal/store cannot import internal/catalog: catalog imports
// provider and provider imports store, so store->catalog closes a cycle.
//
// Unpriced and free are both zero rates and only Pricing.Known separates
// them. Returning nil for unpriced is what lets the trace and the spend tile
// render an em-dash: reporting 0 would state that a request cost nothing,
// which is a different and usually false claim.
func (p Pricing) CostMicros(in, out, cacheRead int64) *int64 {
	if !p.Known {
		return nil
	}
	total := rateMicros(p.InputMicrosPerMTok, in) +
		rateMicros(p.OutputMicrosPerMTok, out) +
		rateMicros(p.CacheReadMicrosPerMTok, cacheRead)
	return &total
}

// rateMicros applies one per-million-token rate to one token count, rounding
// half-up. Truncating would report every request under a million tokens as
// costing nothing, which is most of them.
func rateMicros(perMTok, tokens int64) int64 {
	if tokens <= 0 || perMTok <= 0 {
		return 0
	}
	const perM = 1_000_000
	return (perMTok*tokens + perM/2) / perM
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/catalog/ -run TestCostMicros -v`
Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/catalog/
git add internal/catalog/pricing.go internal/catalog/pricing_test.go
git commit -m "feat(catalog): add cost arithmetic to Pricing"
```

---

## Task 2: The migration

One migration, two changes, because both are over tables the rollup rewrite in Task 6 touches and shipping them separately would mean migrating the same table twice.

`usage_daily`'s primary key cannot be altered in place in SQLite, so the table is rebuilt. `request_attempts` only gains columns, which `ALTER TABLE ... ADD COLUMN` does.

**Files:**
- Create: `internal/store/migrations/0006_usage_alias_and_attempt_usage.sql`
- Test: `internal/store/migrate_test.go` (modify — add the case below)

**Interfaces:**
- Consumes: the schema at `0005`.
- Produces: `usage_daily(day, provider_id, model, alias, requests, tokens_in, tokens_out, cost_micros)` keyed on `(day, provider_id, model, alias)`; `request_attempts` with `tokens_in`, `tokens_out`, `cost_micros`.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/migrate_test.go`:

```go
func TestMigration0006AddsAliasAndAttemptUsage(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	// usage_daily carries alias and keys on it
	var pk string
	err := db.Read.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type='table' AND name='usage_daily'`).Scan(&pk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pk, "alias") {
		t.Fatalf("usage_daily has no alias column:\n%s", pk)
	}
	if !strings.Contains(pk, "PRIMARY KEY (day, provider_id, model, alias)") {
		t.Fatalf("usage_daily key was not widened:\n%s", pk)
	}

	// two rows differing only by alias must coexist
	for _, alias := range []string{"", "fast-coder"} {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
			 VALUES ('2026-08-25','groq','gpt-oss-120b',?,1)`, alias); err != nil {
			t.Fatalf("insert alias=%q: %v", alias, err)
		}
	}

	// request_attempts carries usage
	for _, col := range []string{"tokens_in", "tokens_out", "cost_micros"} {
		var n int
		if err := db.Read.QueryRowContext(ctx,
			`SELECT count(*) FROM pragma_table_info('request_attempts') WHERE name=?`,
			col).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("request_attempts is missing %s", col)
		}
	}
}
```

Add `"strings"` to that file's imports if it is not already there.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestMigration0006 -v`
Expected: FAIL — `usage_daily has no alias column`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/migrations/0006_usage_alias_and_attempt_usage.sql`:

```sql
-- Alias is the routing unit, and usage_daily could not report it: the table
-- keys on (day, provider_id, model) and never carried the alias a request
-- resolved through. The operator console's routing-flow graph reads its whole
-- left-hand column from this dimension.
--
-- SQLite cannot widen a primary key in place, so the table is rebuilt. The
-- copy sets alias = '' for every existing row, which is what a request with no
-- alias already means: a direct model call, not a missing value.
CREATE TABLE usage_daily_new (
  day         TEXT    NOT NULL,
  provider_id TEXT    NOT NULL,
  model       TEXT    NOT NULL,
  alias       TEXT    NOT NULL DEFAULT '',
  requests    INTEGER NOT NULL DEFAULT 0,
  tokens_in   INTEGER NOT NULL DEFAULT 0,
  tokens_out  INTEGER NOT NULL DEFAULT 0,
  cost_micros INTEGER,
  PRIMARY KEY (day, provider_id, model, alias)
) STRICT;

INSERT INTO usage_daily_new (day, provider_id, model, alias, requests, tokens_in, tokens_out, cost_micros)
SELECT day, provider_id, model, '', requests, tokens_in, tokens_out, cost_micros FROM usage_daily;

DROP TABLE usage_daily;
ALTER TABLE usage_daily_new RENAME TO usage_daily;

-- Tokens burned by an attempt that failed before commit never reached
-- usage_daily, so spend understated reality exactly when failover fired --
-- which is when an operator most wants the number. Defaults are 0 because
-- every row written before this migration has no usage to report, and
-- cost_micros stays nullable for the same reason it is nullable on requests:
-- unpriced is not free.
ALTER TABLE request_attempts ADD COLUMN tokens_in   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_attempts ADD COLUMN tokens_out  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_attempts ADD COLUMN cost_micros INTEGER;
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/store/ -v`
Expected: PASS. The whole store suite runs, because a rebuilt table breaks any query that named its columns positionally.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/store/
git add internal/store/migrations/0006_usage_alias_and_attempt_usage.sql internal/store/migrate_test.go
git commit -m "feat(store): give usage_daily an alias dimension"
```

---

## Task 3: Attempt usage reaches the row

The columns exist; nothing writes them. `AttemptRecord` gains the fields and the insert carries them.

**Files:**
- Modify: `internal/store/log.go` — the `AttemptRecord` struct and the attempt insert
- Test: `internal/store/log_test.go`

**Interfaces:**
- Consumes: the `0006` schema from Task 2.
- Produces: `store.AttemptRecord` with `TokensIn int64`, `TokensOut int64`, `CostMicros *int64`.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/log_test.go`:

```go
func TestAttemptUsageIsPersisted(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	cost := int64(4321)

	rec := &RequestRecord{
		ID: "01M0W4NWMRZQCN2VMD9F6K3H7P", TS: time.Now(),
		RequestedModel: "openai/gpt-oss-120b",
		Attempts: []AttemptRecord{{
			Seq: 1, ProviderID: "groq", Model: "openai/gpt-oss-120b",
			Outcome: "retryable_provider", StatusCode: 429,
			TokensIn: 812, TokensOut: 0, CostMicros: &cost,
		}},
	}
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{rec}); err != nil {
		t.Fatal(err)
	}

	var in, out int64
	var got *int64
	err := db.Read.QueryRowContext(ctx,
		`SELECT tokens_in, tokens_out, cost_micros FROM request_attempts
		  WHERE request_id = ? AND seq = 1`, rec.ID).Scan(&in, &out, &got)
	if err != nil {
		t.Fatal(err)
	}
	if in != 812 || out != 0 {
		t.Fatalf("tokens: want 812/0, got %d/%d", in, out)
	}
	if got == nil || *got != 4321 {
		t.Fatalf("cost: want 4321, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestAttemptUsageIsPersisted -v`
Expected: FAIL — `unknown field TokensIn in struct literal`.

- [ ] **Step 3: Write minimal implementation**

In `internal/store/log.go`, add to the `AttemptRecord` struct:

```go
	// Usage burned by this attempt, including one that failed before commit.
	// Without these, a failover's discarded tokens never reach usage_daily and
	// spend understates reality exactly when failover fires.
	TokensIn   int64
	TokensOut  int64
	CostMicros *int64
```

Then extend the attempt insert in the same file to name and bind the three new
columns alongside the existing ones — the statement already lists its columns
explicitly, so add `tokens_in, tokens_out, cost_micros` to the column list, three
`?` to the values list, and `a.TokensIn, a.TokensOut, a.CostMicros` to the
argument list in the same order.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/store/
git add internal/store/log.go internal/store/log_test.go
git commit -m "feat(store): persist per-attempt token usage"
```

---

## Task 4: Cost at commit time

`(*Executor).log` at `internal/exec/exec.go:702` is the single funnel every request record passes through. Computing cost there means one site rather than the eleven that call `applyUsage`, and it matches §8.3: the price of the model that *actually served*.

**Files:**
- Modify: `internal/exec/exec.go:702` — `(*Executor).log`
- Test: `internal/exec/cost_test.go` (create)

**Interfaces:**
- Consumes: `catalog.Pricing.CostMicros` (Task 1); `catalog.Reader.Lookup(providerID, modelID) (catalog.Model, bool)`; `(*Executor).catalogFor(providers []provider.Provider) catalog.Reader` at `exec.go:214`.
- Produces: a `store.RequestRecord` whose `CostMicros` is set whenever the served model has a known price.

- [ ] **Step 1: Write the failing test**

Create `internal/exec/cost_test.go`:

```go
package exec

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/store"
)

// A recording Logger, so the test asserts on what the executor hands over
// rather than on what reaches SQLite.
type capturingLogger struct{ last *store.RequestRecord }

func (c *capturingLogger) Log(r *store.RequestRecord) { c.last = r }

func TestLogPricesTheServedModel(t *testing.T) {
	cap := &capturingLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: fixedCatalog(catalog.Model{
		ProviderID: "groq", ModelID: "openai/gpt-oss-120b",
		Pricing: catalog.Pricing{
			InputMicrosPerMTok: 3_000_000, OutputMicrosPerMTok: 15_000_000, Known: true,
		},
	})}}

	e.log(&store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "openai/gpt-oss-120b",
		TokensIn: 2_000_000, TokensOut: 1_000_000,
	})

	if cap.last.CostMicros == nil {
		t.Fatal("priced model: CostMicros is nil")
	}
	if *cap.last.CostMicros != 21_000_000 {
		t.Fatalf("want 21000000, got %d", *cap.last.CostMicros)
	}
}

func TestLogLeavesCostNilForAnUnpricedModel(t *testing.T) {
	cap := &capturingLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: fixedCatalog(catalog.Model{
		ProviderID: "together", ModelID: "black-forest-labs/FLUX.1-schnell",
		Pricing:    catalog.Pricing{Known: false},
	})}}

	e.log(&store.RequestRecord{
		FinalProviderID: "together", FinalModel: "black-forest-labs/FLUX.1-schnell",
		TokensIn: 10, TokensOut: 10,
	})

	if cap.last.CostMicros != nil {
		t.Fatalf("unpriced model: want nil, got %d", *cap.last.CostMicros)
	}
}

func TestLogLeavesCostNilWhenNothingServed(t *testing.T) {
	// Every attempt failed, so there is no served model to price. A zero here
	// would put a free request in the log for one that never completed.
	cap := &capturingLogger{}
	e := &Executor{deps: Deps{Log: cap}}
	e.log(&store.RequestRecord{FinalProviderID: "", FinalModel: ""})
	if cap.last.CostMicros != nil {
		t.Fatalf("no served model: want nil, got %d", *cap.last.CostMicros)
	}
}

func TestLogDoesNotOverwriteACostAlreadySet(t *testing.T) {
	// A surface that priced itself (per-call rather than per-token) keeps its
	// own number; the catalog rate would be wrong for it.
	cap := &capturingLogger{}
	pre := int64(999)
	e := &Executor{deps: Deps{Log: cap, Catalog: fixedCatalog(catalog.Model{
		ProviderID: "groq", ModelID: "m",
		Pricing:    catalog.Pricing{InputMicrosPerMTok: 1_000_000, Known: true},
	})}}
	e.log(&store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 1_000_000, CostMicros: &pre,
	})
	if cap.last.CostMicros == nil || *cap.last.CostMicros != 999 {
		t.Fatalf("want the pre-set 999, got %v", cap.last.CostMicros)
	}
}
```

`fixedCatalog` is a one-model `CatalogSource` test helper. If `internal/exec`
already has an equivalent, use it and delete this; otherwise add it to the same
file:

```go
type fixedCatalogSource struct{ snap *catalog.Snapshot }

func (f fixedCatalogSource) Snapshot() *catalog.Snapshot { return f.snap }

func fixedCatalog(models ...catalog.Model) CatalogSource {
	return fixedCatalogSource{snap: catalog.NewSnapshot(models)}
}
```

If `catalog.NewSnapshot` does not exist under that name, build the snapshot the
way `internal/catalog`'s own tests do and keep the `fixedCatalog` signature.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run TestLog -v`
Expected: FAIL — `priced model: CostMicros is nil`.

- [ ] **Step 3: Write minimal implementation**

Replace `(*Executor).log` in `internal/exec/exec.go`:

```go
func (e *Executor) log(rec *store.RequestRecord) {
	if e.deps.Log == nil {
		return
	}
	e.priceRecord(rec)
	e.deps.Log.Log(rec)
}

// priceRecord fills in CostMicros from the catalog price of the model that
// actually served.
//
// Here rather than in applyUsage because applyUsage has eleven call sites and
// this has one: cost is a property of the finished request, not of each usage
// event that arrived on the way. A record with nothing served, no catalog, or
// an unpriced model keeps a nil cost -- the em-dash the trace already renders.
func (e *Executor) priceRecord(rec *store.RequestRecord) {
	if rec == nil || rec.CostMicros != nil {
		return
	}
	if rec.FinalProviderID == "" || rec.FinalModel == "" || e.deps.Catalog == nil {
		return
	}
	snap := e.deps.Catalog.Snapshot()
	if snap == nil {
		return
	}
	m, ok := snap.Lookup(rec.FinalProviderID, rec.FinalModel)
	if !ok {
		return
	}
	rec.CostMicros = m.Pricing.CostMicros(
		rec.TokensIn, rec.TokensOut, rec.CacheReadTokens)
}
```

Then update the comment at `applyUsage` (`exec.go:941`) — it currently says
"CostMicros stays nil. Phase 6 supplies pricing" — to say cost is computed once
in `priceRecord` at log time, and why.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/exec/ -v`
Expected: PASS, whole package.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/exec/
git add internal/exec/exec.go internal/exec/cost_test.go
git commit -m "feat(exec): price a request from the served model"
```

---

## Task 5: Cost and usage on each attempt

The served attempt is priced by Task 4 through the request. A *failed* attempt has its own tokens and needs its own price, since the rollup will add them separately.

**Files:**
- Modify: `internal/exec/exec.go` — `priceRecord` extends to attempts
- Test: `internal/exec/cost_test.go`

**Interfaces:**
- Consumes: Task 4's `priceRecord`; Task 3's `AttemptRecord` fields; `catalog.Pricing.CostMicros` (Task 1).
- Produces: every `AttemptRecord` with a known price carries `CostMicros`.

- [ ] **Step 1: Write the failing test**

Add to `internal/exec/cost_test.go`:

```go
func TestLogPricesEachAttempt(t *testing.T) {
	cap := &capturingLogger{}
	e := &Executor{deps: Deps{Log: cap, Catalog: fixedCatalog(
		catalog.Model{ProviderID: "groq", ModelID: "m",
			Pricing: catalog.Pricing{InputMicrosPerMTok: 1_000_000, Known: true}},
		catalog.Model{ProviderID: "cerebras", ModelID: "m",
			Pricing: catalog.Pricing{Known: false}},
	)}}

	e.log(&store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 10,
		Attempts: []store.AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", TokensIn: 1_000_000},
			{Seq: 2, ProviderID: "cerebras", Model: "m", TokensIn: 500},
		},
	})

	a := cap.last.Attempts
	if a[0].CostMicros == nil || *a[0].CostMicros != 1_000_000 {
		t.Fatalf("attempt 1: want 1000000, got %v", a[0].CostMicros)
	}
	if a[1].CostMicros != nil {
		t.Fatalf("attempt 2 is unpriced: want nil, got %d", *a[1].CostMicros)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/exec/ -run TestLogPricesEachAttempt -v`
Expected: FAIL — `attempt 1: want 1000000, got <nil>`.

- [ ] **Step 3: Write minimal implementation**

Extend `priceRecord` in `internal/exec/exec.go`, after the request-level block:

```go
	// Each attempt is priced against the model IT tried, not the one that
	// served: a failover's discarded tokens were burned at the failed
	// provider's rate.
	for i := range rec.Attempts {
		a := &rec.Attempts[i]
		if a.CostMicros != nil || a.ProviderID == "" || a.Model == "" {
			continue
		}
		am, ok := snap.Lookup(a.ProviderID, a.Model)
		if !ok {
			continue
		}
		a.CostMicros = am.Pricing.CostMicros(a.TokensIn, a.TokensOut, 0)
	}
```

The `snap == nil` and `e.deps.Catalog == nil` guards above already cover this
loop; move the request-level `FinalProviderID`/`FinalModel` check so it guards
only the request-level lookup, not the attempt loop.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/exec/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/exec/
git add internal/exec/exec.go internal/exec/cost_test.go
git commit -m "feat(exec): price each attempt at its own rate"
```

---

## Task 6: The rollup carries alias and attempt usage

**Files:**
- Modify: `internal/store/rollup.go`
- Test: `internal/store/rollup_test.go`

**Interfaces:**
- Consumes: the `0006` schema; `requests.resolved_alias`; `request_attempts` usage columns.
- Produces: `usage_daily` rows grouped by `(day, provider_id, model, alias)` whose tokens include failed attempts.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/rollup_test.go`:

```go
func TestRollupGroupsByAlias(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)

	for _, alias := range []string{"fast-coder", "cheap"} {
		rec := &RequestRecord{
			ID: "r-" + alias, TS: ts,
			RequestedModel: "m", ResolvedAlias: alias,
			FinalProviderID: "groq", FinalModel: "m", TokensIn: 100,
		}
		w := NewLogWriter(db, LogOptions{})
		if _, err := w.writeBatch(ctx, []*RequestRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Rollup(ctx, ts); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Read.QueryContext(ctx,
		`SELECT alias, requests FROM usage_daily ORDER BY alias`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]int64{}
	for rows.Next() {
		var a string
		var n int64
		if err := rows.Scan(&a, &n); err != nil {
			t.Fatal(err)
		}
		got[a] = n
	}
	if got["cheap"] != 1 || got["fast-coder"] != 1 {
		t.Fatalf("want one row per alias, got %v", got)
	}
}

func TestRollupCountsTokensFromFailedAttempts(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)

	// 400 tokens burned failing over, 100 on the attempt that served.
	rec := &RequestRecord{
		ID: "r-failover", TS: ts, RequestedModel: "m",
		FinalProviderID: "together", FinalModel: "m", TokensIn: 100,
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "retryable_provider", TokensIn: 400},
			{Seq: 2, ProviderID: "together", Model: "m", Outcome: "success", TokensIn: 100},
		},
	}
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{rec}); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollup(ctx, ts); err != nil {
		t.Fatal(err)
	}

	var total int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT coalesce(sum(tokens_in), 0) FROM usage_daily`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	// 400 from the failed attempt plus 100 from the served one. Counting only
	// the request would report 100 and understate the day by four fifths.
	if total != 500 {
		t.Fatalf("want 500 tokens including the failed attempt, got %d", total)
	}
}

func TestRollupMixesAttemptBearingAndAttemptLessRequests(t *testing.T) {
	// Both requests land in ONE (day, provider, model, alias) group: one has
	// attempt rows, the other has none. This is the case a per-aggregate
	// coalesce gets wrong -- SUM ignores NULL, so the attempt-bearing request
	// alone decides the total and the other request's tokens disappear.
	db := migrated(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	w := NewLogWriter(db, LogOptions{})

	withAttempts := &RequestRecord{
		ID: "r-att", TS: ts, RequestedModel: "m",
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 100,
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success", TokensIn: 100},
		},
	}
	withNone := &RequestRecord{
		ID: "r-none", TS: ts, RequestedModel: "m",
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 50,
	}
	if _, err := w.writeBatch(ctx, []*RequestRecord{withAttempts, withNone}); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollup(ctx, ts); err != nil {
		t.Fatal(err)
	}

	var total int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT coalesce(sum(tokens_in), 0) FROM usage_daily`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	// 100 from the attempt-bearing request plus 50 from the attempt-less one.
	if total != 150 {
		t.Fatalf("want 150, got %d -- the attempt-less request's tokens were dropped", total)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestRollup -v`
Expected: FAIL — the alias test finds one row keyed on `''`; the attempt test reports 100.

- [ ] **Step 3: Write minimal implementation**

Replace the statement in `(*DB).Rollup`. The attempt usage is summed in a
subquery keyed on `request_id` so a request with three attempts is still one
row, not three:

```go
	_, err := d.Write.ExecContext(ctx,
		`WITH attempt_usage AS (
		     SELECT request_id,
		            coalesce(sum(tokens_in), 0)  AS a_in,
		            coalesce(sum(tokens_out), 0) AS a_out,
		            CASE WHEN count(cost_micros) = 0 THEN NULL
		                 ELSE sum(cost_micros) END AS a_cost
		       FROM request_attempts
		      GROUP BY request_id
		 )
		 INSERT INTO usage_daily (day, provider_id, model, alias, requests, tokens_in, tokens_out, cost_micros)
		 SELECT strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch') AS day,
		        r.final_provider_id,
		        r.final_model,
		        r.resolved_alias,
		        count(*),
		        -- Attempt usage REPLACES the request's own counts rather than
		        -- adding to them: the served attempt already carries what the
		        -- request reports, so adding both would double it. A request
		        -- with no attempt rows falls back to its own.
		        -- The inner coalesce runs PER ROW, before aggregation. Written
		        -- as coalesce(sum(au.a_in), sum(r.tokens_in), 0) it would be
		        -- two independent aggregates, and SUM ignores NULL rather than
		        -- propagating it -- so one attempt-bearing request in the group
		        -- makes sum(au.a_in) non-NULL and every attempt-less request's
		        -- tokens are DROPPED, not added. Verified: 100 where 150 is
		        -- right. The cost line below always had the per-row shape.
		        coalesce(sum(coalesce(au.a_in,  r.tokens_in)),  0),
		        coalesce(sum(coalesce(au.a_out, r.tokens_out)), 0),
		        CASE WHEN count(coalesce(au.a_cost, r.cost_micros)) = 0 THEN NULL
		             ELSE sum(coalesce(au.a_cost, r.cost_micros)) END
		   FROM requests r
		   LEFT JOIN attempt_usage au ON au.request_id = r.id
		  WHERE r.ts >= ? AND r.ts < ?
		    AND r.final_provider_id <> ''
		  GROUP BY day, r.final_provider_id, r.final_model, r.resolved_alias
		 ON CONFLICT(day, provider_id, model, alias) DO UPDATE SET
		        requests    = excluded.requests,
		        tokens_in   = excluded.tokens_in,
		        tokens_out  = excluded.tokens_out,
		        cost_micros = excluded.cost_micros`,
		from.UnixMilli(), to.UnixMilli())
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/store/ -v`
Expected: PASS, whole package. The existing rollup tests must still pass — if one now fails because it asserted a row keyed without alias, update its expectation rather than the query.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/store/
git add internal/store/rollup.go internal/store/rollup_test.go
git commit -m "feat(store): roll up by alias and count failed attempts"
```

---

## Task 7: Reading usage by dimension

**Files:**
- Modify: `internal/store/adminstore.go:508`
- Test: `internal/store/adminstore_test.go`

**Interfaces:**
- Consumes: `usage_daily` from Task 6.
**Do not regress `UsageByDay`.** It clamps `days` to 1..365 defaulting to 30, and it returns
**oldest first** — `internal/admin`'s `TestUsageRollsUpByDay` asserts that ordering explicitly,
with the comment "a chart reads left to right". The current implementation gets there by
selecting `DESC LIMIT` and reversing the slice in Go; the replacement selects the newest N days
in a subquery and orders ascending, which is the same result without the reversal.

- Produces: `store.UsageDimension` (`UsageByDayOnly`, `UsageByProvider`, `UsageByModel`, `UsageByAlias`) and `(*DB).UsageBy(ctx context.Context, days int, dim UsageDimension) ([]UsageRow, error)`, where `UsageRow` is `UsageDay` plus a `Key string`. `UsageByDay` keeps its signature and delegates with `UsageByDayOnly`, so existing callers do not change.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/adminstore_test.go`:

```go
func TestUsageByAliasSplitsTheDay(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	for _, r := range []struct {
		alias string
		n     int64
	}{{"fast-coder", 7}, {"cheap", 3}} {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
			 VALUES ('2026-08-25','groq','m',?,?)`, r.alias, r.n); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := db.UsageBy(ctx, 30, UsageByAlias)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		got[r.Key] = r.Requests
	}
	if got["fast-coder"] != 7 || got["cheap"] != 3 {
		t.Fatalf("want fast-coder=7 cheap=3, got %v", got)
	}

	// The day-only rollup still aggregates across aliases.
	flat, err := db.UsageByDay(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 1 || flat[0].Requests != 10 {
		t.Fatalf("want one day totalling 10, got %+v", flat)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestUsageByAlias -v`
Expected: FAIL — `undefined: UsageBy`.

- [ ] **Step 3: Write minimal implementation**

In `internal/store/adminstore.go`, above `UsageByDay`:

```go
// UsageDimension is the column usage rolls up by. The zero value aggregates
// across everything, which is what UsageByDay reported before there was a
// choice.
type UsageDimension int

const (
	UsageByDayOnly UsageDimension = iota
	UsageByProvider
	UsageByModel
	UsageByAlias
)

// column is the SQL identifier for a dimension. It returns "" for the
// day-only case, and the caller must not interpolate anything else -- these
// are fixed identifiers, never user input.
func (d UsageDimension) column() string {
	switch d {
	case UsageByProvider:
		return "provider_id"
	case UsageByModel:
		return "model"
	case UsageByAlias:
		return "alias"
	default:
		return ""
	}
}

// UsageRow is a UsageDay plus the value of the dimension it was grouped by.
// Key is empty for UsageByDayOnly.
type UsageRow struct {
	UsageDay
	Key string
}

// UsageBy rolls usage_daily up over the last `days` days, split by one
// dimension, oldest first because a chart reads left to right.
func (d *DB) UsageBy(ctx context.Context, days int, dim UsageDimension) ([]UsageRow, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	col := dim.column()
	sel, group := "'' AS k", "day"
	if col != "" {
		sel, group = col + " AS k", "day, " + col
	}
	// The LIMIT is on DAYS, not on rows. Grouping by a dimension multiplies
	// the row count by that dimension's cardinality, so a row limit would
	// silently return thirty rows covering four days once eight providers
	// are in play.
	q := `SELECT day, ` + sel + `,
	             sum(requests), sum(tokens_in), sum(tokens_out),
	             CASE WHEN count(cost_micros) = 0 THEN NULL ELSE sum(cost_micros) END
	        FROM usage_daily
	       WHERE day IN (SELECT day FROM usage_daily
	                      GROUP BY day ORDER BY day DESC LIMIT ?)
	       GROUP BY ` + group + `
	       ORDER BY day, k`
	rows, err := d.Read.QueryContext(ctx, q, days)
	if err != nil {
		return nil, fmt.Errorf("usage by: %w", err)
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Day, &r.Key, &r.Requests,
			&r.TokensIn, &r.TokensOut, &r.CostMicros); err != nil {
			return nil, fmt.Errorf("usage by: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
```

Then rewrite `UsageByDay` to delegate:

```go
// UsageByDay rolls usage_daily up across every dimension, oldest first. Its
// signature and its ordering are unchanged; only its implementation moved.
func (d *DB) UsageByDay(ctx context.Context, days int) ([]UsageDay, error) {
	rows, err := d.UsageBy(ctx, days, UsageByDayOnly)
	if err != nil {
		return nil, err
	}
	out := make([]UsageDay, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.UsageDay)
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/store/ ./internal/admin/ -v`
Expected: PASS both packages — `internal/admin` calls `UsageByDay` and must be unaffected.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/store/
git add internal/store/adminstore.go internal/store/adminstore_test.go
git commit -m "feat(store): read usage by provider, model or alias"
```

---

## Task 8: `GET /api/usage?group_by=`

**Files:**
- Modify: `internal/admin/usage.go:99` — `handleUsage`
- Test: `internal/admin/usage_test.go`

**Interfaces:**
- Consumes: `store.UsageBy`, `store.UsageDimension` (Task 7).
- Produces: `GET /api/usage?days=&group_by=provider|model|alias`. Response gains a `group_by` echo and each row a `key`. Omitting `group_by` returns exactly today's shape.

- [ ] **Step 1: Write the failing test**

Add to `internal/admin/usage_test.go`:

```go
func TestUsageGroupByAlias(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if _, err := db.Write.Exec(
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-25','groq','m','fast-coder',7)`); err != nil {
		t.Fatal(err)
	}

	rr := do(t, s, cookie, token, "GET", "/api/usage?group_by=alias", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		GroupBy string `json:"group_by"`
		Days    []struct {
			Key      string `json:"key"`
			Requests int64  `json:"requests"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.GroupBy != "alias" {
		t.Fatalf("group_by echo: want alias, got %q", got.GroupBy)
	}
	if len(got.Days) != 1 || got.Days[0].Key != "fast-coder" || got.Days[0].Requests != 7 {
		t.Fatalf("want one fast-coder row of 7, got %+v", got.Days)
	}
}

func TestUsageRejectsAnUnknownGroupBy(t *testing.T) {
	// A typo must not silently fall back to the day-only rollup: the caller
	// would render a chart with one series and no way to tell it asked wrong.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	rr := do(t, s, cookie, token, "GET", "/api/usage?group_by=providr", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for an unknown dimension, got %d", rr.Code)
	}
}

func TestUsageWithoutGroupByIsUnchanged(t *testing.T) {
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	if _, err := db.Write.Exec(
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-25','groq','m','fast-coder',7),
		        ('2026-08-25','groq','m','cheap',3)`); err != nil {
		t.Fatal(err)
	}
	rr := do(t, s, cookie, token, "GET", "/api/usage", "")
	var got struct {
		Days []struct {
			Requests int64 `json:"requests"`
		} `json:"days"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Days) != 1 || got.Days[0].Requests != 10 {
		t.Fatalf("ungrouped: want one row of 10, got %+v", got.Days)
	}
}
```

These are the helpers `internal/admin`'s existing tests already use:
`testServerFull(t)` returns `(server, db)`, `login(t, s)` returns `(cookie, token)`, and
`do(t, s, cookie, token, method, path, body)` issues an authenticated request. Admin endpoints
require the session cookie and CSRF token — a request without them gets 401, not the status the
test expects.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/admin/ -run TestUsage -v`
Expected: FAIL — `group_by echo: want alias, got ""`.

- [ ] **Step 3: Write minimal implementation**

Replace `handleUsage` in `internal/admin/usage.go`:

```go
// usageDimensions is the closed set of group_by values. An unknown one is a
// 400 rather than a silent fall back to the day-only rollup: a caller that
// misspells the dimension would otherwise render one series and never learn
// it asked for the wrong thing.
var usageDimensions = map[string]store.UsageDimension{
	"":         store.UsageByDayOnly,
	"provider": store.UsageByProvider,
	"model":    store.UsageByModel,
	"alias":    store.UsageByAlias,
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	days := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = n
	}
	groupBy := r.URL.Query().Get("group_by")
	dim, ok := usageDimensions[groupBy]
	if !ok {
		writeError(w, http.StatusBadRequest,
			"group_by must be one of provider, model, alias")
		return
	}

	rows, err := s.deps.DB.UsageBy(r.Context(), days, dim)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	priced := false
	for _, u := range rows {
		if u.CostMicros != nil {
			priced = true
		}
		row := map[string]any{
			"day": u.Day, "requests": u.Requests,
			"tokens_in": u.TokensIn, "tokens_out": u.TokensOut,
			"cost_micros": u.CostMicros,
		}
		if groupBy != "" {
			row["key"] = u.Key
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days": out, "priced": priced, "group_by": groupBy,
	})
}
```

`internal/admin/usage.go` imports only `net/http`, `strconv` and `time` today, so add
`"github.com/darkraise/darkrouter/internal/store"` to it. Task 9 touches the same file and needs
the same import.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/admin/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
go vet ./internal/admin/
git add internal/admin/usage.go internal/admin/usage_test.go
git commit -m "feat(admin): group usage by provider, model or alias"
```

---

## Task 9: The overview extensions

§8.2's three additions to `GET /api/overview`: latency percentiles, a short series for sparklines, and recent failovers. Each degrades honestly — a missing series renders as the bare number the tile renders today — so they are additive and nothing existing changes shape.

**Files:**
- Modify: `internal/admin/usage.go` — `handleOverview` at line 24
- Modify: `internal/store/adminstore.go` — add `LatencyPercentiles` and `RecentFailovers`
- Test: `internal/admin/usage_test.go`, `internal/store/adminstore_test.go`

**Interfaces:**
- Consumes: `requests` (`ts`, `total_ms`, `attempts`).
- Produces: `(*DB).LatencyPercentiles(ctx, window time.Duration) (p50, p95 int64, err error)`; `(*DB).RecentFailovers(ctx, limit int) ([]FailoverRow, error)` where `FailoverRow` is `{ID string; TS int64; Alias string; FinalProviderID string; FinalModel string; Attempts int; TotalMs int64}`. `GET /api/overview` gains `latency{p50_ms,p95_ms}`, `series[]`, `failovers[]`.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/adminstore_test.go`:

```go
func TestLatencyPercentiles(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now()
	// 1..100ms; p50 is the 50th value and p95 the 95th.
	for i := 1; i <= 100; i++ {
		total := int64(i)
		rec := &RequestRecord{
			ID: fmt.Sprintf("r%03d", i), TS: now,
			RequestedModel: "m", FinalProviderID: "groq", FinalModel: "m",
			TotalMs: &total,
		}
		w := NewLogWriter(db, LogOptions{})
		if _, err := w.writeBatch(ctx, []*RequestRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}
	p50, p95, err := db.LatencyPercentiles(ctx, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if p50 != 50 || p95 != 95 {
		t.Fatalf("want p50=50 p95=95, got %d/%d", p50, p95)
	}
}

func TestRecentFailoversReturnsOnlyMultiAttemptRequests(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now()
	mk := func(id string, attempts int) {
		rec := &RequestRecord{
			ID: id, TS: now, RequestedModel: "m",
			FinalProviderID: "groq", FinalModel: "m",
		}
		for i := 1; i <= attempts; i++ {
			rec.Attempts = append(rec.Attempts, AttemptRecord{
				Seq: i, ProviderID: "groq", Model: "m", Outcome: "success",
			})
		}
		w := NewLogWriter(db, LogOptions{})
		if _, err := w.writeBatch(ctx, []*RequestRecord{rec}); err != nil {
			t.Fatal(err)
		}
	}
	mk("one", 1)
	mk("three", 3)

	got, err := db.RecentFailovers(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "three" || got[0].Attempts != 3 {
		t.Fatalf("want only the 3-attempt request, got %+v", got)
	}
}
```

Add to `internal/admin/usage_test.go`:

```go
func TestOverviewCarriesLatencySeriesAndFailovers(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	rr := do(t, s, cookie, token, "GET", "/api/overview", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"latency", "series", "failovers"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("overview is missing %q: %v", k, got)
		}
	}
	// An empty database still answers with the keys present and empty, so the
	// console never has to distinguish "no data" from "old server".
	if got["failovers"] == nil {
		t.Fatal("failovers must be [] rather than null")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ ./internal/admin/ -run 'TestLatency|TestRecentFailovers|TestOverviewCarries' -v`
Expected: FAIL — `undefined: LatencyPercentiles`.

- [ ] **Step 3: Write minimal implementation**

In `internal/store/adminstore.go`:

```go
// LatencyPercentiles returns p50 and p95 of total_ms over the window.
//
// Computed in SQL with a window function rather than by loading the rows: a
// busy window is tens of thousands of requests and the overview polls every
// three seconds.
func (d *DB) LatencyPercentiles(ctx context.Context, window time.Duration) (int64, int64, error) {
	since := time.Now().Add(-window).UnixMilli()
	row := d.Read.QueryRowContext(ctx,
		`WITH ranked AS (
		     SELECT total_ms,
		            row_number() OVER (ORDER BY total_ms) AS rn,
		            count(*)     OVER ()                  AS n
		       FROM requests
		      WHERE ts >= ? AND total_ms IS NOT NULL
		 )
		 -- Nearest-rank: the ceil(n*p)-th value. percent_rank() is
		 -- (rank-1)/(n-1), so "pr >= 0.50" over 100 values returns the 51st,
		 -- not the 50th -- one position high on every sample, on a tile an
		 -- operator reads as p50. Integer division truncates, so
		 -- (n*50 + 99)/100 is ceil(n*50/100).
		 SELECT coalesce((SELECT total_ms FROM ranked WHERE rn = (n * 50 + 99) / 100), 0),
		        coalesce((SELECT total_ms FROM ranked WHERE rn = (n * 95 + 99) / 100), 0)`,
		since)
	var p50, p95 int64
	if err := row.Scan(&p50, &p95); err != nil {
		return 0, 0, err
	}
	return p50, p95, nil
}

// FailoverRow is one request the router had to walk past a candidate for.
type FailoverRow struct {
	ID              string
	TS              int64
	Alias           string
	FinalProviderID string
	FinalModel      string
	Attempts        int
	TotalMs         int64
}

// RecentFailovers returns the newest requests that took more than one attempt.
func (d *DB) RecentFailovers(ctx context.Context, limit int) ([]FailoverRow, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := d.Read.QueryContext(ctx,
		`SELECT r.id, r.ts, r.resolved_alias, r.final_provider_id, r.final_model,
		        count(a.seq) AS attempts, coalesce(r.total_ms, 0)
		   FROM requests r
		   JOIN request_attempts a ON a.request_id = r.id
		  GROUP BY r.id
		 HAVING attempts > 1
		  ORDER BY r.ts DESC
		  LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FailoverRow{}
	for rows.Next() {
		var f FailoverRow
		if err := rows.Scan(&f.ID, &f.TS, &f.Alias, &f.FinalProviderID,
			&f.FinalModel, &f.Attempts, &f.TotalMs); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
```

In `internal/admin/usage.go`, extend the response `handleOverview` already writes with three keys, each degrading to an empty value rather than an error
so one slow query cannot take the whole tile strip down:

```go
	p50, p95, err := s.deps.DB.LatencyPercentiles(r.Context(), overviewWindow)
	if err != nil {
		// A percentile failure must not fail the overview: the tile renders
		// the bare number it renders today.
		p50, p95 = 0, 0
	}
	failovers, err := s.deps.DB.RecentFailovers(r.Context(), 5)
	if err != nil {
		failovers = []store.FailoverRow{}
	}
	series, err := s.deps.DB.UsageBy(r.Context(), 30, store.UsageByDayOnly)
	if err != nil {
		series = nil
	}
```

Then add to the map the handler writes:

```go
		"latency":   map[string]any{"p50_ms": p50, "p95_ms": p95},
		"series":    series,
		"failovers": failovers,
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -race ./internal/store/ ./internal/admin/ -v`
Expected: PASS both packages.

- [ ] **Step 5: Commit**

```bash
go vet ./...
git add internal/store/adminstore.go internal/store/adminstore_test.go internal/admin/usage.go internal/admin/usage_test.go
git commit -m "feat(admin): add percentiles, series and failovers to overview"
```

---

## Task 10: Whole-suite verification and the progress record

**Files:**
- Modify: `docs/PROGRESS.md`

- [ ] **Step 1: Run the whole suite race-clean**

```bash
go build ./... && go vet ./... && go test -race ./...
```

Expected: all packages PASS. A failure in `internal/exec` or `internal/admin` after this plan almost certainly means a test asserted on `CostMicros` being nil for a priced model — that assertion is now wrong and the test is what changes.

- [ ] **Step 2: Verify a migration from a pre-0006 database**

The rebuild of `usage_daily` is the only destructive step in this plan. Task 2's
`TestMigration0006AddsAliasAndAttemptUsage` asserts the resulting *schema*, but it
builds from `migrated(t)` — a fresh database, where the `INSERT INTO
usage_daily_new … SELECT … FROM usage_daily` copies zero rows. The half of the
migration that carries existing data across the drop is therefore untested.

There is no live database on this machine, and a check that silently skips is not
a check. Build the pre-0006 database instead: apply migrations `0001`–`0005` only,
insert known rows, then let `Migrate` apply `0006` and assert nothing was lost.

Add to `internal/store/migrate_test.go`:

```go
func TestMigration0006PreservesExistingUsageRows(t *testing.T) {
	db := openTest(t)
	ctx := context.Background()
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if m.version > 5 {
			continue
		}
		if err := db.applyMigration(ctx, m); err != nil {
			t.Fatalf("apply %04d: %v", m.version, err)
		}
	}

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, requests, tokens_in, tokens_out)
		 VALUES ('2026-08-01','groq','a',5,100,200),
		        ('2026-08-02','openai','b',7,300,400)`); err != nil {
		t.Fatalf("pre-0006 insert: %v", err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate to 0006: %v", err)
	}

	var rows, requests, tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*), sum(requests), sum(tokens_in) FROM usage_daily`,
	).Scan(&rows, &requests, &tokensIn); err != nil {
		t.Fatal(err)
	}
	if rows != 2 || requests != 12 || tokensIn != 400 {
		t.Fatalf("rows lost across the rebuild: rows=%d requests=%d tokens_in=%d, want 2/12/400",
			rows, requests, tokensIn)
	}

	var alias string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT alias FROM usage_daily WHERE provider_id='groq'`).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if alias != "" {
		t.Fatalf("a row that predates aliases must carry the empty alias, got %q", alias)
	}
}
```

- [ ] **Step 2b: Run it**

Run: `go test ./internal/store/ -run TestMigration0006Preserves -v`
Expected: PASS. If it fails on the insert, the pre-0006 `usage_daily` shape
assumed here is wrong — read `0001`–`0005` and correct the column list, not the
assertion. If it fails on the counts, `0006`'s `SELECT` is dropping data and the
migration is what changes.

- [ ] **Step 3: Record the phase**

Add a row to the phase table in `docs/PROGRESS.md`:

```markdown
| 11a — Cost, attempts and the usage dimension | ✅ | ✅ | **Complete.** 10 tasks; race-clean. Cost computed at commit time from catalog pricing, failed attempts counted, `usage_daily` keyed on alias. |
```

- [ ] **Step 4: Commit**

```bash
git add docs/PROGRESS.md
git commit -m "docs: record the cost and usage-dimension phase"
```

---

## Notes for the executor

**The one destructive step.** Task 2 drops and recreates `usage_daily`. Everything else in this plan is additive. If the migration is wrong, it is wrong in a way that loses a month of rolled-up usage. Task 2's own test builds from a fresh database, where the row-copying `SELECT` moves nothing, so it proves only the resulting schema. Task 10 is what proves the data survives: it constructs a pre-0006 database by applying migrations `0001`-`0005` alone, seeds known rows, and asserts the totals across the rebuild.

**Where the double-count hides (historical).** This note originally described Task 6's rollup as using `coalesce(sum(au.a_in), sum(r.tokens_in), 0)`, treating attempt usage as *replacing* the request's own counts. That form was ruled a bug and removed in `bc26200`: it discarded per-attempt attribution, so a failover's tokens were credited to whichever provider happened to serve rather than to the provider that burned them. The rollup instead attributes usage to each attempt's own provider, and sources the two are now mutually exclusive by construction — a request either has attempt rows (attributed per attempt) or predates them and keeps its own single-row counts, kept apart by the `UNION ALL`'s `NOT EXISTS` guard. The test `TestRollupCountsTokensFromFailedAttempts` still pins the total, now reflecting per-attempt attribution rather than the replaced form.

**What is deliberately not here.** Per-call image pricing (§8.3 leaves it unpriced); `GET /api/providers`' discovery health and OAuth detail, which is §8.4 step 2 and belongs with the Providers slice in the third plan; the `GET`/`PUT /api/config` work and the aliases/policy migration (the second plan); and all twelve new endpoints (the third). This plan's boundary is: the numbers the console reads are now real.
