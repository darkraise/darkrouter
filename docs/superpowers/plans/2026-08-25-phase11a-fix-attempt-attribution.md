# Phase 11a Fix: Attempt Attribution and the Rollup Seam

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Make `usage_daily` record what actually happened — per-attempt tokens attributed to the provider that burned them — and close the seam that let ten green reviews ship a rollup that zeroes live traffic.

**Architecture:** The executor starts recording usage on the attempt that served. Pricing stops turning "no tokens recorded" into a confident zero. The rollup is rebuilt around attempts rather than the request's final provider, gains an `attempts` column so `requests` keeps meaning requests, and clears its window before reinserting so a key change cannot double-count. One end-to-end test covers exec → log → rollup, which is the seam nothing tested.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` §8

**Prior plan:** `docs/superpowers/plans/2026-08-25-phase11a-cost-attempts-usage.md` — this fixes findings from its final review.

## Global Constraints

- TDD: a failing test precedes the implementation.
- Race-clean: `go test -race` passes.
- Migrations are append-only. `0006` is committed; the next free number is `0007`. Tables are `STRICT`. A column added to an existing table needs a `DEFAULT`.
- Comments explain WHY, never WHAT. No comment may reference the current task, fix, or plan.
- Commit subjects: `<type>(<scope>): <subject>`, imperative, **50 characters or fewer**, no trailing period. Verify with `printf '%s' "subject" | wc -c` before committing.
- English only.
- `export PATH=/usr/local/go/bin:$PATH` before any Go command.

## The decisions this plan implements

Two were settled by the user; the rest follow from them.

1. **Attribution is per attempt.** Tokens and cost burned by an attempt belong to the provider and model that attempt used, not to whichever provider eventually served.
2. **Requests where every attempt failed are included.** Their burned tokens reach `usage_daily`, attributed per attempt.
3. **`requests` keeps meaning requests.** Counted only on the attempt that served, so summing across providers still equals the true request count and the existing day-only view does not regress. A new `attempts` column carries the per-provider attempt count, so a provider that only ever failed reads as `requests=0, attempts=N` rather than as a contradiction.
4. **An attempt with no tokens recorded is unpriced, not free.** `CostMicros` stays NULL. A non-nil zero is what let the rollup report `priced: true` against zero spend.

---

### Task 1: Record usage on the attempt that served

**Files:**
- Modify: `internal/exec/exec.go` — `priceRecord`
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Consumes: `store.AttemptRecord.TokensIn/TokensOut/CostMicros`, `catalog.Pricing.CostMicros`.
- Produces: after `priceRecord` runs, the attempt matching the request's final provider and model carries the request's token counts; attempts with no tokens carry a nil `CostMicros`.

`recordAttempt` runs while an attempt is in flight, before its usage is known, so it cannot fill these in. `priceRecord` runs at log time, when `applyUsage` has already put the served attempt's usage on the request record. That is where the served attempt gets its counts.

- [ ] **Step 1: Write the failing tests**

Add to `internal/exec/exec_test.go`:

```go
func TestServedAttemptCarriesTheRequestUsage(t *testing.T) {
	e := newPricedExecutor(t) // a catalog where ("groq","m") has known pricing
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		TokensIn: 1000, TokensOut: 500,
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "openai", Model: "x", Outcome: "error"},
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success"},
		},
	}
	e.priceRecord(rec)

	if rec.Attempts[1].TokensIn != 1000 || rec.Attempts[1].TokensOut != 500 {
		t.Fatalf("served attempt must carry the request's usage, got %d/%d",
			rec.Attempts[1].TokensIn, rec.Attempts[1].TokensOut)
	}
	if rec.Attempts[0].TokensIn != 0 {
		t.Fatalf("a failed attempt must not inherit the served attempt's usage")
	}
}

func TestAnAttemptWithNoTokensIsUnpricedNotFree(t *testing.T) {
	// A confident zero is indistinguishable from a real zero downstream, and
	// the rollup treats a non-NULL cost as authoritative.
	e := newPricedExecutor(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "groq", Model: "m", Outcome: "error"},
		},
	}
	e.priceRecord(rec)

	if rec.Attempts[0].CostMicros != nil {
		t.Fatalf("an attempt that recorded no tokens must stay unpriced, got %d",
			*rec.Attempts[0].CostMicros)
	}
}
```

Add a third test pinning the retry case, because it is the one a provider match gets wrong:

```go
func TestARetriedProviderAttributesUsageToTheAttemptThatServed(t *testing.T) {
	// The pre-commit 400 retry re-attempts the same provider and model, so
	// two attempt rows carry identical provider and model and only the
	// second one served.
	e := newPricedExecutor(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		TokensIn: 400, TokensOut: 200,
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "groq", Model: "m", Outcome: "fatal"},
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success"},
		},
	}
	e.priceRecord(rec)

	if rec.Attempts[0].TokensIn != 0 {
		t.Fatalf("the rejected attempt must stay at zero, got %d", rec.Attempts[0].TokensIn)
	}
	if rec.Attempts[1].TokensIn != 400 || rec.Attempts[1].TokensOut != 200 {
		t.Fatalf("the serving attempt must carry the usage, got %d/%d",
			rec.Attempts[1].TokensIn, rec.Attempts[1].TokensOut)
	}
}
```

Use whatever the package's existing helper for a priced executor is; if there is none, build the catalog the way the existing pricing tests in this package already do, and name the helper `newPricedExecutor`.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/exec/ -run 'TestServedAttempt|TestAnAttemptWithNoTokens' -v`
Expected: FAIL — the served attempt has 0/0, and the zero-token attempt gets a non-nil 0.

- [ ] **Step 3: Implement**

In `priceRecord`, before the pricing loop, copy the request's usage onto the served attempt. Then skip pricing any attempt that recorded no tokens.

```go
	// recordAttempt runs while the attempt is still in flight, before its
	// usage is known. By log time applyUsage has put the served attempt's
	// usage on the request, so this is the first point it can be attributed.
	for i := range rec.Attempts {
		a := &rec.Attempts[i]
		// Identified by outcome, not by matching the request's final provider:
		// the pre-commit 400 retry re-attempts the same provider and model, so
		// a provider match would find the rejected attempt first.
		if a.Outcome == string(adapter.OutcomeSuccess) &&
			a.TokensIn == 0 && a.TokensOut == 0 {
			a.TokensIn, a.TokensOut = rec.TokensIn, rec.TokensOut
			break
		}
	}
```

and in the pricing loop, extend the skip condition:

```go
		if a.CostMicros != nil || a.ProviderID == "" || a.Model == "" {
			continue
		}
		// No tokens recorded is not the same as nothing spent: a NULL cost
		// keeps the rollup from reporting a priced day of zero.
		if a.TokensIn == 0 && a.TokensOut == 0 {
			continue
		}
```

- [ ] **Step 4: Run them and watch them pass**

Run: `go test -race ./internal/exec/ -v`

- [ ] **Step 5: Commit**

Subject: `feat(exec): attribute usage to the served attempt`

---

### Task 2: Add the attempts column

**Files:**
- Create: `internal/store/migrations/0007_usage_daily_attempts.sql`
- Modify: `internal/store/adminstore.go` — `UsageDay`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Produces: `usage_daily.attempts INTEGER NOT NULL DEFAULT 0`; `UsageDay.Attempts int64`.

This is additive — `ALTER TABLE ADD COLUMN` with a default, the `0005_attempt_path.sql` pattern. Do NOT rebuild the table.

- [ ] **Step 1: Write the failing test**

```go
func TestMigration0007AddsAttemptsAndKeepsRows(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()

	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('usage_daily') WHERE name='attempts'`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("usage_daily is missing the attempts column")
	}

	// A row written before the column existed must read as zero, not NULL.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-25','groq','m','',3)`); err != nil {
		t.Fatal(err)
	}
	var attempts int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT attempts FROM usage_daily`).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("attempts must default to 0, got %d", attempts)
	}
}
```

- [ ] **Step 2: Run it, watch it fail**

Run: `go test ./internal/store/ -run TestMigration0007 -v`

- [ ] **Step 3: Write the migration**

`internal/store/migrations/0007_usage_daily_attempts.sql`:

```sql
-- requests counts only the attempt that served, so summing the column across
-- providers still equals the real request count. Without a separate attempts
-- column a provider that only ever failed would read as zero requests against
-- non-zero tokens, which looks like corruption rather than like failover.
ALTER TABLE usage_daily ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
```

Add `Attempts int64` to `UsageDay` in `internal/store/adminstore.go` and carry it through `UsageBy`'s SELECT and row scan. Both `usage_daily` readers must select the new column.

- [ ] **Step 4: Run it, watch it pass**

Run: `go test -race ./internal/store/ -run 'TestMigration' -v`

- [ ] **Step 5: Commit**

Subject: `feat(store): count attempts alongside requests`

---

### Task 3: Rebuild the rollup around attempts

**Files:**
- Modify: `internal/store/rollup.go`
- Test: `internal/store/rollup_test.go`

**Interfaces:**
- Consumes: `usage_daily.attempts` (Task 2), attempt usage columns (`0006`).
- Produces: `usage_daily` rows keyed on the ATTEMPT's provider and model.

This SQL has been verified against a fixture covering all four shapes. Use it as given.

- [ ] **Step 1: Write the failing test**

```go
func TestRollupAttributesUsageToTheAttemptsOwnProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	now := time.Now().UTC()
	c4500, c900 := int64(4500), int64(900)

	recs := []*RequestRecord{
		{ // failed over: groq burned 100 tokens, together served
			ID: "A", TS: now, ResolvedAlias: "fast",
			FinalProviderID: "together", FinalModel: "m2",
			TokensIn: 1000, TokensOut: 500, CostMicros: &c4500,
			Attempts: []AttemptRecord{
				{Seq: 0, ProviderID: "groq", Model: "m1", TokensIn: 100},
				{Seq: 1, ProviderID: "together", Model: "m2", TokensIn: 1000, TokensOut: 500, CostMicros: &c4500},
			},
		},
		{ // every attempt failed: nothing served, but tokens were burned
			ID: "C", TS: now, ResolvedAlias: "fast",
			Attempts: []AttemptRecord{
				{Seq: 0, ProviderID: "groq", Model: "m1", TokensIn: 50},
			},
		},
		{ // no attempt rows at all
			ID: "D", TS: now, ResolvedAlias: "slow",
			FinalProviderID: "openai", FinalModel: "m3",
			TokensIn: 300, TokensOut: 150, CostMicros: &c900,
		},
	}
	if _, err := w.writeBatch(ctx, recs); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}

	var requests, attempts, tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT requests, attempts, tokens_in FROM usage_daily
		  WHERE provider_id='groq' AND model='m1'`,
	).Scan(&requests, &attempts, &tokensIn); err != nil {
		t.Fatal(err)
	}
	// groq served nothing, tried twice, and burned 150 tokens doing it.
	if requests != 0 || attempts != 2 || tokensIn != 150 {
		t.Fatalf("groq: requests=%d attempts=%d tokens_in=%d, want 0/2/150",
			requests, attempts, tokensIn)
	}

	var total int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT sum(requests) FROM usage_daily`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	// Two requests were served; summing across providers must not inflate.
	if total != 2 {
		t.Fatalf("sum(requests)=%d, want 2", total)
	}

	var legacyAttempts, legacyTokens int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT attempts, tokens_in FROM usage_daily WHERE provider_id='openai'`,
	).Scan(&legacyAttempts, &legacyTokens); err != nil {
		t.Fatal(err)
	}
	if legacyAttempts != 0 || legacyTokens != 300 {
		t.Fatalf("attempt-less request: attempts=%d tokens_in=%d, want 0/300",
			legacyAttempts, legacyTokens)
	}
}

func TestRollupClearsItsWindowBeforeReinserting(t *testing.T) {
	// 0006 copied every pre-existing row in with alias ''. Recomputing the
	// same day grouped by a wider key inserts new rows without matching the
	// old one, so without a clear the day's totals double permanently.
	db := migrated(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := now.Format("2006-01-02")

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests, tokens_in)
		 VALUES (?,'groq','m1','',9,999)`, day); err != nil {
		t.Fatal(err)
	}

	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{{
		ID: "A", TS: now, ResolvedAlias: "fast",
		FinalProviderID: "groq", FinalModel: "m1", TokensIn: 10,
		Attempts: []AttemptRecord{{Seq: 0, ProviderID: "groq", Model: "m1", TokensIn: 10}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}

	var rows, requests int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*), coalesce(sum(requests),0) FROM usage_daily WHERE day=?`, day,
	).Scan(&rows, &requests); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || requests != 1 {
		t.Fatalf("stale alias='' row survived: rows=%d requests=%d, want 1/1",
			rows, requests)
	}
}
```

- [ ] **Step 2: Run them, watch them fail**

Run: `go test ./internal/store/ -run TestRollup -v`

- [ ] **Step 3: Replace the rollup body**

Delete the window's days first, then insert. Because the window is cleared, the `ON CONFLICT` clause is no longer needed and must be removed rather than left dead.

```go
	// The window's rows are cleared rather than upserted. 0006 widened the key
	// with alias, so a recomputed group no longer matches the row a narrower
	// key wrote: upserting alone would leave the old row behind and double the
	// day permanently.
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM usage_daily
		  WHERE day >= strftime('%Y-%m-%d', ? / 1000, 'unixepoch')
		    AND day <  strftime('%Y-%m-%d', ? / 1000, 'unixepoch')`,
		from.UnixMilli(), to.UnixMilli()); err != nil {
		return fmt.Errorf("rollup clear: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests, attempts, tokens_in, tokens_out, cost_micros)
		 SELECT day, provider_id, model, alias,
		        sum(is_served), sum(is_attempt), sum(t_in), sum(t_out),
		        CASE WHEN count(c) = 0 THEN NULL ELSE sum(c) END
		   FROM (
		     -- Attributed to the attempt's OWN provider: a failover's discarded
		     -- tokens were burned where they were tried, not where the retry
		     -- happened to succeed.
		     SELECT strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch') AS day,
		            a.provider_id AS provider_id, a.model AS model,
		            r.resolved_alias AS alias,
		            -- Only the serving attempt counts as a request, so summing
		            -- this column across providers still equals the real
		            -- request count. Keyed on the outcome rather than on
		            -- matching the request's final provider: the pre-commit 400
		            -- retry re-attempts the SAME provider and model, so a
		            -- provider match identifies two rows where one served.
		            CASE WHEN a.outcome = 'success' THEN 1 ELSE 0 END AS is_served,
		            1 AS is_attempt,
		            coalesce(a.tokens_in, 0) AS t_in,
		            coalesce(a.tokens_out, 0) AS t_out,
		            a.cost_micros AS c
		       FROM requests r
		       JOIN request_attempts a ON a.request_id = r.id
		      WHERE r.ts >= ? AND r.ts < ?
		     UNION ALL
		     -- A request that predates attempt rows still has its own counts.
		     SELECT strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch'),
		            r.final_provider_id, r.final_model, r.resolved_alias,
		            1, 0,
		            coalesce(r.tokens_in, 0), coalesce(r.tokens_out, 0),
		            r.cost_micros
		       FROM requests r
		      WHERE r.ts >= ? AND r.ts < ?
		        AND r.final_provider_id <> ''
		        AND NOT EXISTS (
		              SELECT 1 FROM request_attempts a WHERE a.request_id = r.id)
		   )
		  GROUP BY day, provider_id, model, alias`,
		from.UnixMilli(), to.UnixMilli(), from.UnixMilli(), to.UnixMilli()); err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	return tx.Commit()
```

Keep `from`/`to` as they are computed today. Every existing rollup test must still pass; where one encodes the old attribution, the TEST is what changes, and say so in the report.

- [ ] **Step 4: Run the package**

Run: `go test -race ./internal/store/ -v`

- [ ] **Step 5: Commit**

Subject: `feat(store): attribute rollup usage per attempt`

---

### Task 4: The seam test

**Files:**
- Test: `internal/exec/rollup_seam_test.go` (create)

Ten task reviews passed while the rollup zeroed live traffic, because every test hand-built attempt records carrying tokens that production never wrote. This test uses only the production path.

- [ ] **Step 1: Write it**

Drive a real request through the executor against a stub provider, let the executor's own logging write it, run `Rollup`, and assert `usage_daily` holds the tokens the provider reported. Build the executor the way `internal/exec`'s existing end-to-end tests do — do NOT construct `AttemptRecord` or `RequestRecord` literals anywhere in this test. That is the whole point: if the test can pass while the executor writes nothing, it is not this test.

Assert: `usage_daily.tokens_in` and `tokens_out` equal what the stub reported, and `cost_micros` is non-NULL when the catalog prices that model.

- [ ] **Step 2: Verify it catches the original bug**

Temporarily revert Task 1's change to `priceRecord`, run this test, and confirm it FAILS with zeroed tokens. Restore the change. Record both outputs in the report — a seam test that cannot fail is worth nothing.

- [ ] **Step 3: Commit**

Subject: `test(exec): cover the exec to rollup seam`

---

### Task 5: API shape and query hygiene

**Files:**
- Modify: `internal/store/adminstore.go` — `UsageDay`, `UsageRow`, `FailoverRow`, `RecentFailovers`
- Modify: `docs/superpowers/plans/2026-08-25-phase11a-cost-attempts-usage.md`
- Test: `internal/admin/usage_test.go`

- [ ] **Step 1: JSON tags**

`handleOverview` serialises `[]store.UsageRow` and `[]store.FailoverRow` directly, and neither struct has JSON tags, so consumers get `{"Day":…,"TokensIn":…,"Key":""}` beside `"requests_per_min"` in the same payload. Add snake_case tags to `UsageDay`, `UsageRow` and `FailoverRow` matching the naming the rest of the API already uses. Tag `UsageRow.Key` as `key,omitempty` — it is meaningless on a day-only row.

Add a test asserting the overview's `series` and `failovers` rows carry snake_case keys, so the shape is pinned before a console is built against it.

- [ ] **Step 1b: Expose attempts on the usage endpoint**

`handleUsage` in `internal/admin/usage.go` builds each row by hand-picking fields
off `UsageDay`, so the `attempts` column added in Task 2 never reaches a
consumer. Add it to the row map alongside `requests`.

This matters because of what the pair means together: `requests` counts only the
attempt that served, so a provider that failed every time reads as
`requests: 0, attempts: 7`. Without `attempts` in the payload that provider looks
like it did nothing, when in fact it burned tokens on seven failures — the exact
case the attribution rework exists to surface.

Add a test asserting a grouped row carries both `requests` and `attempts`.

- [ ] **Step 2: Bound RecentFailovers**

`RecentFailovers` joins and groups the entire request history on every call, and the overview polls every three seconds. Give it the same window its two sibling queries use, so "recent" means recent and the query does not scan months of history:

```go
func (d *DB) RecentFailovers(ctx context.Context, window time.Duration, limit int) ([]FailoverRow, error)
```

Filter on `r.ts >= ?` from the window, keep the existing ordering and limit, and update the caller in `internal/admin/usage.go` to pass the same `overviewWindow` the other two use. Add a test that a failover older than the window is excluded.

- [ ] **Step 3: Correct the stale plan note**

In `docs/superpowers/plans/2026-08-25-phase11a-cost-attempts-usage.md`, the "Notes for the executor" section still states the rollup uses `coalesce(sum(au.a_in), sum(r.tokens_in), 0)` — the form ruled a bug and removed in `bc26200`. Replace that paragraph with one describing the per-attempt attribution this plan installs, and note that the note is historical.

- [ ] **Step 4: Run everything**

Run: `go build ./... && go vet ./... && go test -race ./...`

- [ ] **Step 5: Commit**

Subject: `fix(admin): tag json and bound the failover scan`

---

## Notes for the executor

**What the schema supports and the adapters do not yet.** A failed attempt only carries tokens if the adapter surfaced usage before the failure. Most do not today, so in practice failed attempts will often contribute `0` tokens and a NULL cost. That is honest — the column is there, the attribution is right, and the number improves as adapters learn to report partial usage. Do not "fix" this by inventing an estimate.

**Where the double-count hid.** Attempt usage no longer replaces request usage; the two sources are now mutually exclusive by construction — a request either has attempt rows (attributed per attempt) or does not (its own counts, one row). The `UNION ALL`'s `NOT EXISTS` is what keeps them exclusive, and removing it would double every legacy request.

---

### Task 6: Make the recorded attempt outcome truthful

**Files:**
- Modify: `internal/exec/exec.go` — the attempt-result switch
- Test: `internal/exec/exec_test.go`

**Interfaces:**
- Produces: at most one attempt per request carries `Outcome == "success"`, and it is the attempt that actually served.

`recordAttempt` (`exec.go:512`) stores the HTTP classification optimistically — a 2xx becomes `success` before the body is read or forwarded. Most pre-commit failures rewrite that: `reclassifyStream` (`exec.go:672`), `failedParse` (`embed.go:111`), `chatOp` (`surface.go:142`). Two paths do not: `forwardUnary` (`forward.go:239`) and `speechOp.Respond` (`speech.go:68`) both return `OutcomeRetryableProvider` after an upstream 200, and nothing writes that back onto the attempt row.

The failure: a passthrough unary request gets a 200, the body read fails mid-transfer, the attempt row keeps `success`, the loop fails over, and the next provider serves — leaving TWO attempts marked `success`. `sum(is_served)` then counts one request as two, and `priceRecord`'s loop, which breaks at the first zero-token `success`, attributes the request's tokens and cost to the provider that failed.

Fixing it at the switch site covers both paths and any future one, rather than patching each caller.

- [ ] **Step 1: Write the failing test**

```go
func TestAPreCommitForwardFailureDoesNotStaySuccess(t *testing.T) {
	// The attempt row is recorded from the HTTP status before the body is
	// read. A 200 whose body then fails is a failover, and a row still
	// marked success makes the rollup count one request as two.
	rec := &store.RequestRecord{}
	rec.Attempts = append(rec.Attempts, store.AttemptRecord{
		Seq: 0, ProviderID: "groq", Model: "m",
		Outcome: string(adapter.OutcomeSuccess),
	})

	demoteLastAttempt(rec, adapter.OutcomeRetryableProvider, false)

	if got := rec.Attempts[0].Outcome; got != string(adapter.OutcomeRetryableProvider) {
		t.Fatalf("outcome = %q, want retryable_provider", got)
	}
}

func TestACommittedAttemptKeepsItsSuccess(t *testing.T) {
	// Once bytes have reached the client the chain ends and the attempt DID
	// serve, whatever the op reports afterwards.
	rec := &store.RequestRecord{}
	rec.Attempts = append(rec.Attempts, store.AttemptRecord{
		Seq: 0, ProviderID: "groq", Model: "m",
		Outcome: string(adapter.OutcomeSuccess),
	})

	demoteLastAttempt(rec, adapter.OutcomeRetryableProvider, true)

	if got := rec.Attempts[0].Outcome; got != string(adapter.OutcomeSuccess) {
		t.Fatalf("a committed attempt must stay success, got %q", got)
	}
}
```

- [ ] **Step 2: Run them, watch them fail**

Run: `go test ./internal/exec/ -run 'TestAPreCommitForward|TestACommittedAttempt' -v`
Expected: FAIL — `demoteLastAttempt` does not exist.

- [ ] **Step 3: Implement**

Add the helper and call it from the switch site in `attempt`, immediately after the switch and before the `cw.Committed()` check:

```go
// The attempt row is written from the HTTP status before the body is read,
// so a 200 that fails while forwarding is recorded as a success it never
// was. A committed attempt keeps its success: bytes reached the client and
// the chain ends there regardless of what the op reports afterwards.
func demoteLastAttempt(rec *store.RequestRecord, outcome adapter.Outcome, committed bool) {
	if committed || outcome == adapter.OutcomeSuccess {
		return
	}
	if n := len(rec.Attempts); n > 0 {
		rec.Attempts[n-1].Outcome = string(outcome)
	}
}
```

Call it as `demoteLastAttempt(rec, outcome, cw.Committed())`.

Leave the existing rewrites in `reclassifyStream`, `embed.go` and `surface.go` alone — they set an Error string too, and demoting an already-demoted attempt is a no-op.

- [ ] **Step 4: Run the package**

Run: `go test -race ./internal/exec/`

- [ ] **Step 5: Commit**

Subject: `fix(exec): demote an attempt that never served`

---

### Task 7: Stop the rollup wiping days it cannot recompute

**Files:**
- Modify: `internal/store/rollup.go`
- Test: `internal/store/rollup_test.go`

The rollup clears its two-day window before reinserting, which is what keeps a widened key from leaving stale rows behind. But it clears unconditionally and recomputes only from surviving `requests` rows. `Prune` (`internal/store/retention.go`) deletes requests older than `log.retention`, and `log.retention` is validated only as positive (`internal/config/load.go:162`) — `24h` is legal.

The failure: with a 24h retention, yesterday's requests are pruned overnight; the next hourly rollup deletes yesterday's `usage_daily` rows and re-inserts nothing. The daily aggregate, whose entire purpose is to outlive the raw logs, is permanently gone. The old upsert left such rows untouched.

Clear only the days the recompute can actually rebuild.

- [ ] **Step 1: Write the failing test**

```go
func TestRollupKeepsADayWhoseRequestsWerePruned(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// A finalized day: its rollup exists, its raw requests are long gone.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests, tokens_in)
		 VALUES (?,'groq','m','fast',42,4200)`, yesterday); err != nil {
		t.Fatal(err)
	}

	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}

	var requests, tokensIn int64
	err := db.Read.QueryRowContext(ctx,
		`SELECT requests, tokens_in FROM usage_daily WHERE day=?`, yesterday,
	).Scan(&requests, &tokensIn)
	if err != nil {
		t.Fatalf("the day's rollup was destroyed: %v", err)
	}
	if requests != 42 || tokensIn != 4200 {
		t.Fatalf("requests=%d tokens_in=%d, want 42/4200", requests, tokensIn)
	}
}
```

- [ ] **Step 2: Run it, watch it fail**

Run: `go test ./internal/store/ -run TestRollupKeepsADay -v`
Expected: FAIL with "the day's rollup was destroyed: sql: no rows in result set".

- [ ] **Step 3: Narrow the delete**

Restrict it to days the window still has requests for, so a day with nothing left to recompute keeps what it already holds:

```sql
DELETE FROM usage_daily
 WHERE day IN (
       SELECT DISTINCT strftime('%Y-%m-%d', ts / 1000, 'unixepoch')
         FROM requests
        WHERE ts >= ? AND ts < ?)
```

Keep the same `from`/`to` parameters the insert uses, and keep both statements in the one transaction.

- [ ] **Step 4: Run the package**

Run: `go test -race ./internal/store/`

Every existing rollup test must still pass — in particular the one asserting a stale `alias=''` row does not survive a recompute, which is the behaviour this delete exists for and which a day WITH requests must still get.

- [ ] **Step 5: Commit**

Subject: `fix(store): keep rollups for pruned days`
