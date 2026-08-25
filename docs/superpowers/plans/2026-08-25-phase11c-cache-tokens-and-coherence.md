# Cache-Token Semantics and Cost Coherence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Give `ir.Usage.InputTokens` one meaning repo-wide, stop cached tokens being charged twice, and make the two cost surfaces of the console agree.

**Architecture:** Adapters normalize incoming usage so `InputTokens` always excludes cache reads — the convention Anthropic already uses. Edges re-add them when rendering back to dialects that report an inclusive prompt count, so no client-visible wire shape changes. The served attempt inherits the request's cost rather than being re-priced without its cache component. The rollup stops freezing a day before it has had a chance to be finalized.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` §8

**Prior plans:** `2026-08-25-phase11a-cost-attempts-usage.md` and `2026-08-25-phase11a-fix-attempt-attribution.md`. This closes the final whole-branch review of both.

## Global Constraints

- TDD: a failing test precedes the implementation.
- Race-clean: `go test -race` passes.
- **No client-visible wire change.** This branch must not alter a single byte any dialect returns. `internal/golden` holds recorded responses: if a golden test fails, that is a REGRESSION to fix, never a file to re-record.
- Comments explain WHY, never WHAT. No comment may reference the current task, fix, or plan.
- Commit subjects: `<type>(<scope>): <subject>`, imperative, **50 characters or fewer**, no trailing period. Verify with `printf '%s' "subject" | wc -c`.
- English only.
- `export PATH=/usr/local/go/bin:$PATH` before any Go command.
- Stage explicit paths. Never `git add -A`.

## The decision this plan implements

`ir.Usage.InputTokens` means **input tokens excluding cache reads**, on every provider. Anthropic and Bedrock already report it that way. OpenAI-compatible providers and Gemini report an inclusive prompt count, so their adapters subtract. Cost is then `in·r_in + out·r_out + cacheRead·r_cache` with no overlap, which is what `Pricing.CostMicros` already computes.

---

### Task 1: One meaning for InputTokens

**Files:**
- Modify: `internal/ir/ir.go` (or wherever `Usage` is defined) — add `PromptTokens()`
- Modify: `internal/adapter/openaicompat/parse.go` — `toIR`
- Modify: `internal/adapter/gemini/parse.go` — `toIR`
- Modify: `internal/edge/openai/write.go`, `aux.go`, `stream.go`, `responses.go`
- Modify: `internal/edge/gemini/write.go`
- Test: the adapter and edge packages' own test files

**Interfaces:**
- Produces: `func (u Usage) PromptTokens() int` returning `InputTokens + CacheReadTokens` — the inclusive count dialects that want one should render.

Do NOT touch `internal/adapter/anthropic` or `internal/adapter/bedrock`: they already exclude cache reads, which is now the house convention.

- [ ] **Step 1: Write the failing tests**

In `internal/adapter/openaicompat`:

```go
func TestCachedTokensAreRemovedFromTheInputCount(t *testing.T) {
	// OpenAI reports prompt_tokens INCLUDING the cached subset. Leaving it
	// inclusive makes the cached tokens billable twice: once at the input
	// rate and again at the cache-read rate.
	var u wireUsage
	if err := json.Unmarshal([]byte(`{
		"prompt_tokens": 10000,
		"completion_tokens": 500,
		"prompt_tokens_details": {"cached_tokens": 8000}
	}`), &u); err != nil {
		t.Fatal(err)
	}
	got := u.toIR()
	if got.InputTokens != 2000 {
		t.Fatalf("InputTokens = %d, want 2000 (10000 less the 8000 cached)", got.InputTokens)
	}
	if got.CacheReadTokens != 8000 {
		t.Fatalf("CacheReadTokens = %d, want 8000", got.CacheReadTokens)
	}
}

func TestAnInclusiveCountIsNeverDrivenNegative(t *testing.T) {
	// A provider reporting cached greater than prompt is malformed, but a
	// negative token count would reach pricing and produce a negative cost.
	var u wireUsage
	if err := json.Unmarshal([]byte(`{
		"prompt_tokens": 100,
		"prompt_tokens_details": {"cached_tokens": 500}
	}`), &u); err != nil {
		t.Fatal(err)
	}
	if got := u.toIR().InputTokens; got != 0 {
		t.Fatalf("InputTokens = %d, want 0", got)
	}
}
```

Write the mirror of the first test in `internal/adapter/gemini` using `promptTokenCount` and `cachedContentTokenCount`.

In `internal/edge/openai`, pin that the wire shape is unchanged:

```go
func TestPromptTokensStayInclusiveOnTheWire(t *testing.T) {
	// The IR excludes cache reads; the OpenAI dialect reports them inside
	// prompt_tokens. A client reconciling against its own provider bill must
	// see the number the provider would have sent.
	u := ir.Usage{InputTokens: 2000, OutputTokens: 500, CacheReadTokens: 8000}
	if got := u.PromptTokens(); got != 10000 {
		t.Fatalf("PromptTokens() = %d, want 10000", got)
	}
}
```

- [ ] **Step 2: Run them, watch them fail**

Run: `go test ./internal/adapter/openaicompat/ ./internal/adapter/gemini/ ./internal/edge/openai/ -run 'Cached|Inclusive|Negative' -v`

- [ ] **Step 3: Implement**

Add to `ir.Usage`:

```go
// PromptTokens is the inclusive prompt count that OpenAI-compatible and
// Gemini dialects report. InputTokens excludes cache reads so that pricing
// can charge each at its own rate without overlap; a dialect that reports
// one combined number adds them back here.
func (u Usage) PromptTokens() int { return u.InputTokens + u.CacheReadTokens }
```

In `openaicompat`'s `toIR`, and the same shape in `gemini`'s:

```go
	// prompt_tokens includes the cached subset. Subtracting it here is what
	// lets every provider's InputTokens mean the same thing downstream.
	in := u.PromptTokens - u.PromptDetails.CachedTokens
	if in < 0 {
		in = 0
	}
```

Then replace every render site that writes an inclusive prompt count:
- `internal/edge/openai/write.go` — `prompt_tokens` and `total_tokens`
- `internal/edge/openai/aux.go` — all four sites, including `input_tokens` at line ~463
- `internal/edge/openai/stream.go` — `prompt_tokens` and `total_tokens`
- `internal/edge/openai/responses.go` — `input_tokens` and its total
- `internal/edge/gemini/write.go` — `promptTokenCount` and `totalTokenCount`

Each becomes `u.PromptTokens()`, and every `total_tokens` that read `u.InputTokens + u.OutputTokens` becomes `u.PromptTokens() + u.OutputTokens`.

Leave `internal/edge/anthropic` alone: it reports `input_tokens` and `cache_read_input_tokens` separately, which is already correct.

- [ ] **Step 4: Prove nothing on the wire moved**

Run: `go test -race ./internal/golden/ ./internal/edge/... ./internal/adapter/... -v`

Every golden test must pass UNCHANGED. If one fails, a render site was missed — fix the site. Do not re-record a golden file. State in your report how many render sites you changed and confirm the golden suite passed without modification.

- [ ] **Step 5: Commit**

Subject: `fix(ir): exclude cache reads from InputTokens`

---

### Task 2: Price the served attempt like the request

**Files:**
- Modify: `internal/exec/exec.go` — `priceRecord`
- Test: `internal/exec/exec_test.go`

The request is priced with its cache-read tokens; the served attempt is re-priced with cache-read hard-coded to zero, and the attempt is what reaches `usage_daily`. So the spend tile and the usage chart disagree by the whole cache-read component — on cache-heavy traffic, by most of the day's spend.

The served attempt ran the same model at the same rates on the same tokens. It should carry the same cost, not a second, poorer estimate.

- [ ] **Step 1: Write the failing test**

```go
func TestTheServedAttemptCostsWhatTheRequestCosts(t *testing.T) {
	// usage_daily sums attempt cost while today_spend sums request cost.
	// If they are computed differently the console contradicts itself.
	e := newPricedExecutor(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		TokensIn: 1000, TokensOut: 500, CacheReadTokens: 100000,
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "groq", Model: "m", Outcome: "success"},
		},
	}
	e.priceRecord(rec)

	if rec.CostMicros == nil {
		t.Fatal("the request was not priced")
	}
	if rec.Attempts[0].CostMicros == nil {
		t.Fatal("the served attempt was not priced")
	}
	if *rec.Attempts[0].CostMicros != *rec.CostMicros {
		t.Fatalf("served attempt cost %d, request cost %d: the usage chart and "+
			"the spend tile would disagree",
			*rec.Attempts[0].CostMicros, *rec.CostMicros)
	}
}
```

- [ ] **Step 2: Run it, watch it fail**

Run: `go test ./internal/exec/ -run TestTheServedAttemptCosts -v`
Expected: FAIL — the attempt's cost omits the cache-read component.

- [ ] **Step 3: Implement**

In the usage-copy loop, when the served attempt is found and the request already carries a cost, give the attempt that same cost:

```go
		if a.Outcome == string(adapter.OutcomeSuccess) &&
			a.TokensIn == 0 && a.TokensOut == 0 {
			a.TokensIn, a.TokensOut = rec.TokensIn, rec.TokensOut
			// The same model at the same rates on the same tokens. Re-pricing
			// it separately drops the cache-read component the attempt row has
			// no column for, and the two cost surfaces stop agreeing.
			if rec.CostMicros != nil {
				c := *rec.CostMicros
				a.CostMicros = &c
			}
			break
		}
```

Copy the value rather than sharing the pointer: the two records are written independently and an aliased pointer invites a later mutation of one to change the other.

The pricing loop's existing `a.CostMicros != nil` guard then leaves it alone. Failed attempts are unaffected and keep their own per-model pricing.

- [ ] **Step 4: Run the package**

Run: `go test -race ./internal/exec/`

- [ ] **Step 5: Commit**

Subject: `fix(exec): price served attempt as the request`

---

### Task 3: Finalize a day before freezing it

**Files:**
- Modify: `internal/store/rollup.go` — `Rollup`
- Test: `internal/store/rollup_test.go`

A day is frozen once its midnight leaves the retention window, but the day's data is only complete at the FOLLOWING midnight. At `retention = 24h` those coincide, so yesterday becomes off-limits at the very moment it becomes finalizable and the finalizing run never happens. Requests logged after the last pre-midnight tick never reach `usage_daily` even though nothing was pruned.

Demonstrated: requests at 22:00 and 23:40 with rollups at 23:00, 00:30 and 01:30 leave `usage_daily` at one request forever against two in `requests`.

A day is safe to recompute while pruning cannot have removed any of its requests. Pruning removes `ts < now - retention`, and the LAST instant of day D is `D + 24h`. So D is fully intact while `D + 24h > now - retention` — a day later than the current rule allows.

- [ ] **Step 1: Write the failing test**

```go
func TestADayIsFinalizedBeforeItIsFrozen(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	// 00:30 UTC: yesterday is complete and, at a 24h retention, none of it
	// has been pruned yet. It must still be recomputable.
	now := time.Date(2026, 8, 25, 0, 30, 0, 0, time.UTC)
	w := NewLogWriter(db, LogOptions{})

	for i, ts := range []time.Time{
		time.Date(2026, 8, 24, 22, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 24, 23, 40, 0, 0, time.UTC),
	} {
		if _, err := w.writeBatch(ctx, []*RequestRecord{{
			ID: fmt.Sprintf("r%d", i), TS: ts, ResolvedAlias: "fast",
			FinalProviderID: "groq", FinalModel: "m", TokensIn: 5,
			Attempts: []AttemptRecord{{
				Seq: 0, ProviderID: "groq", Model: "m",
				Outcome: "success", TokensIn: 5,
			}},
		}}); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Rollup(ctx, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}

	var requests, tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT requests, tokens_in FROM usage_daily WHERE day='2026-08-24'`,
	).Scan(&requests, &tokensIn); err != nil {
		t.Fatalf("yesterday was frozen before it was ever finalized: %v", err)
	}
	if requests != 2 || tokensIn != 10 {
		t.Fatalf("requests=%d tokens_in=%d, want 2/10", requests, tokensIn)
	}
}
```

The existing `TestRollupSkipsADayPruningHasAlreadyTouched` must still pass: it uses a `now` late enough in the day that yesterday's earliest hours really are outside a 24h retention.

- [ ] **Step 2: Run them, watch the new one fail**

Run: `go test ./internal/store/ -run TestRollup -v`

- [ ] **Step 3: Move the boundary by one day**

A day is intact while its LAST instant is still inside retention, not its first:

```go
	// Pruning removes requests older than the retention window, so a day is
	// safe to recompute while its final instant is still inside it. Keying on
	// the day's first instant instead freezes a day at the same moment it
	// becomes complete, and the run that would have finalized it never comes.
	cutoff := utc.Add(-logRetention)
	cutoffDay := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(),
		0, 0, 0, 0, time.UTC)
	if safeFrom := cutoffDay; safeFrom.After(from) {
		from = safeFrom
	}
```

- [ ] **Step 4: Say when a day is dropped**

The guard currently logs only when the window is empty. The case that loses data is the silent narrowing, so report that too:

```go
	if from.After(originalFrom) {
		log.Printf("rollup: log.retention %s excludes %s; "+
			"days older than the retention window are not recomputed",
			logRetention, originalFrom.Format("2006-01-02"))
	}
```

Capture `originalFrom` before the guard adjusts `from`.

- [ ] **Step 5: Run the package**

Run: `go test -race ./internal/store/ ./internal/server/`

- [ ] **Step 6: Commit**

Subject: `fix(store): finalize a day before freezing it`

---

### Task 4: Never stamp one provider's tokens onto another

**Files:**
- Modify: `internal/exec/exec.go` — `priceRecord`
- Test: `internal/exec/exec_test.go`

`applyUsage` writes onto the shared request record, and nothing resets it between attempts. If a failing attempt reports usage before dying — an Anthropic stream that sends `message_start` usage then errors, or a forwarded stream that emits a usage chunk before failing pre-commit — those tokens stay on the record across the failover. If the attempt that then serves reports no usage of its own, the copy loop stamps the FAILED provider's tokens onto the SERVING provider and prices them at the serving model's rate.

That is the mis-attribution this whole rework existed to remove, surviving on a narrow path.

- [ ] **Step 1: Write the failing test**

```go
func TestAFailedProvidersTokensAreNotStampedOnTheNextOne(t *testing.T) {
	// A stream can report usage and then fail pre-commit. Those tokens stay
	// on the shared record; they belong to the provider that burned them,
	// never to whoever serves the retry.
	e := newPricedExecutor(t)
	rec := &store.RequestRecord{
		FinalProviderID: "groq", FinalModel: "m",
		TokensIn: 5000, TokensOut: 0, // carried over from the failed attempt
		Attempts: []store.AttemptRecord{
			{Seq: 0, ProviderID: "other", Model: "x",
				Outcome: "retryable_provider", TokensIn: 5000},
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success"},
		},
	}
	e.priceRecord(rec)

	if got := rec.Attempts[1].TokensIn; got == 5000 {
		t.Fatalf("the serving attempt was billed the failed provider's 5000 tokens")
	}
}
```

- [ ] **Step 2: Run it, watch it fail**

Run: `go test ./internal/exec/ -run TestAFailedProvidersTokens -v`

- [ ] **Step 3: Implement**

Only copy the request's usage onto the served attempt when no EARLIER attempt already claimed it. Before the copy loop, sum what the other attempts already carry:

```go
	// Usage recorded by an attempt that later failed stays on the shared
	// record. Handing it to whoever serves next would bill one provider for
	// another's tokens, which is the attribution this exists to prevent.
	var claimed int64
	for i := range rec.Attempts {
		claimed += rec.Attempts[i].TokensIn + rec.Attempts[i].TokensOut
	}
```

and guard the copy with `claimed == 0`. When an earlier attempt already carries usage, the served attempt keeps zero and stays unpriced — honest, because nothing distinguishable is known about what IT burned.

- [ ] **Step 4: Confirm the ordinary path still works**

`TestServedAttemptCarriesTheRequestUsage`, `TestARetriedProviderAttributesUsageToTheAttemptThatServed` and the seam test must all still pass: in each, no other attempt carries usage, so `claimed` is zero.

Run: `go test -race ./internal/exec/`

- [ ] **Step 5: Commit**

Subject: `fix(exec): do not reassign a failed attempt's use`

---

### Task 5: Finish the surfaces

**Files:**
- Modify: `internal/store/adminstore.go` — `RequestTrace`, remove `UsageByDay`
- Modify: `internal/admin/requests.go` — the attempt view
- Modify: `internal/admin/usage.go` — `today_spend`
- Test: `internal/admin/requests_test.go`, `internal/admin/usage_test.go`

- [ ] **Step 1: Expose per-attempt usage on the trace**

`RequestTrace`'s attempt query selects neither `tokens_in`, `tokens_out` nor `cost_micros`, so the trace drawer — where an operator inspects one specific failover — cannot show the burn that is now recorded in the row underneath it. Add the three columns to the SELECT, the struct, and the handler's attempt view, in the snake_case the rest of the API uses. Add a test that a failover's trace carries per-attempt tokens.

- [ ] **Step 1b: Make the prompt size reconstructible**

Task 1 changed what `tokens_in` means: it now excludes cache reads on every
provider, where for OpenAI-compatible and Gemini sources it previously carried
the full prompt count. `requests.cache_read_tokens` has been stored since
migration `0001` but is exposed by no admin endpoint, so an operator comparing
`tokens_in` against a provider invoice now sees a smaller number with no way to
account for the difference.

Add `cache_read_tokens` alongside `tokens_in` wherever a request's own token
counts are already returned — the request list and the trace detail. Do not add
it to `usage_daily`; that table has no cache column and this plan is not opening
another migration.

Add a test that a request logged with cache reads reports them.

- [ ] **Step 2: Make today_spend agree with the usage chart**

`today_spend` sums `requests.cost_micros`, so a failover's burn appears in `usage_daily` but never in the tile. With per-attempt cost now recorded, that gap becomes visible. Source the tile from the same attempt-level cost the rollup uses, so the two surfaces answer the same question. Add a test that a day containing a failed attempt's cost reports the same total through both.

If this turns out to require a query the `RecentStats` shape cannot express, stop and report rather than reshaping the overview: say what is missing.

- [ ] **Step 3: Remove the dead shim**

`UsageByDay` has no production caller — `handleUsage` and `handleOverview` both call `UsageBy`. Delete it and any test that only pins the delegation. If a test covers behaviour not otherwise covered, move that coverage to `UsageBy` rather than deleting it.

- [ ] **Step 4: Run everything**

Run: `go build ./... && go vet ./... && go test -race ./...`

- [ ] **Step 5: Commit**

Subject: `feat(admin): show per-attempt usage on the trace`

---

## Notes for the executor

**The wire is frozen.** No dialect's output may change by a byte. The IR convention change is internal; `PromptTokens()` is what keeps it internal. `internal/golden` is the tripwire — a golden failure means a render site was missed, never that a file needs re-recording.

**Why no test caught the cache bug.** Nothing in the suite fed cache-read tokens through pricing, so two full review rounds passed over it. Task 1 and Task 2 both add tests that do.

---

### Task 6: Put a floor under log.retention

**Files:**
- Modify: `internal/config/load.go` — validation
- Test: `internal/config/load_test.go`, `internal/store/rollup_test.go`

Task 3's never-shrink guard covers yesterday. Today is deliberately exempt, because migration `0006`'s leftover `alias=''` rows land on whatever day the upgrade happens and MUST be cleared — a clear that is itself a shrink. So the guard cannot protect today without also preserving bogus rows.

That exemption is safe only while today cannot be pruned. Today's earliest request is at today's midnight, and pruning removes anything older than `now - retention`, so today is untouchable exactly when `retention >= 24h`.

Below that it is not. Demonstrated on this branch: six requests between 02:00 and 07:00, finalized at 600 tokens by the 08:00 rollup; with `retention = 6h` the four oldest are pruned by 14:00, the day is still in the delete's day list because two survive, and the 14:00 rollup rewrites it to **200**. Silent loss of two-thirds of the day.

A floor closes the whole class rather than adding a seventh special case to a guard that has already been rewritten six times. `log.retention` is currently validated only as positive (`internal/config/load.go:162`).

- [ ] **Step 1: Write the failing tests**

```go
func TestRetentionShorterThanADayIsRejected(t *testing.T) {
	// The daily rollup cannot finalize a day that pruning is eating while it
	// is still being written. Rejecting the config is honest; accepting it
	// and silently under-reporting the day is not.
	c := validConfigForTest()
	c.Log.Retention = 6 * time.Hour
	err := validate(c)
	if err == nil {
		t.Fatal("a retention below 24h must be rejected")
	}
	if !strings.Contains(err.Error(), "log.retention") {
		t.Fatalf("the error must name the setting, got %q", err)
	}
}

func TestRetentionOfExactlyADayIsAccepted(t *testing.T) {
	c := validConfigForTest()
	c.Log.Retention = 24 * time.Hour
	if err := validate(c); err != nil {
		t.Fatalf("24h is the floor and must be accepted: %v", err)
	}
}
```

Use whatever helper the package's existing validation tests use to build a valid config and call the validator; if there is none, follow the shape those tests already use rather than inventing a helper.

- [ ] **Step 2: Run them, watch them fail**

Run: `go test ./internal/config/ -run TestRetention -v`

- [ ] **Step 3: Implement**

Replace the positive-only check:

```go
	// A day cannot be rolled up while pruning is still eating it. Today's
	// oldest request sits at today's midnight, so anything below a day means
	// the current day is being pruned as it is written, and its rollup
	// silently under-reports.
	if c.Log.Retention < 24*time.Hour {
		return fmt.Errorf("log.retention must be at least 24h, got %s", c.Log.Retention)
	}
```

Keep the existing default of 720h. Check whether any test fixture, example config, or docs file sets a shorter retention and would now fail to load — fix those, and list every one you changed in your report.

- [ ] **Step 4: Pin the invariant where it matters**

Add to `internal/store/rollup_test.go` a test that today is never pruned at the floor: requests spread across today, a rollup at the floor retention, and an assertion that the day's total does not drop. This is the property the exemption depends on, and it belongs next to the guard rather than only in config.

- [ ] **Step 5: Run everything**

Run: `go build ./... && go vet ./... && go test -race ./...`

- [ ] **Step 6: Commit**

Subject: `fix(config): require at least a day of retention`

---

### Task 7: Delete the guard; raise the floor to 48h

**Files:**
- Modify: `internal/config/load.go` — the retention floor
- Modify: `internal/store/rollup.go` — remove the shrink guard and the `logRetention` parameter
- Modify: `internal/store/rollup.go` — `RunRollup`, and `internal/server/server.go` if the signature change reaches it
- Test: `internal/store/rollup_test.go`, `internal/config/load_test.go`

The rollup's freeze guard has been rewritten six times. Every version was a plausible rule that failed on a case its author had not enumerated, and a review has now demonstrated three more losses in the current one: growth from a late-arriving request masks a pruning loss in the same run; `tokens_out` and `cost_micros` shrink invisibly because only `tokens_in` is watched; and request counts shrink while `tokens_in` stays flat.

They are all the same gap — the guard compares a net sum of one column — and the seventh patch would have the same shape as the first six.

The guard exists only because the rollup's two-day window can overlap what pruning is removing. That overlap is a function of the retention floor, and it is arithmetic:

| retention | earliest still-prunable timestamp inside the window | window safe |
|---|---|---|
| 24h | 24h into yesterday | no — all of yesterday |
| 36h | 12h into yesterday | no |
| 47h | 1h into yesterday | no |
| **48h** | **0h** | **yes** |
| 720h (default) | — | yes |

Verified against the real `Prune`: at 24h, all four of a test day's in-window rows are pruned; at 48h, none are.

So at a 48h floor the window is untouchable, the guard is unnecessary, and every finding above disappears — by deleting code rather than adding another special case.

- [ ] **Step 1: Raise the floor**

In `internal/config/load.go`, change the 24h floor to 48h:

```go
	// The daily rollup recomputes yesterday and today, so anything it can
	// still rewrite must still be in the log. Two days is the exact point
	// where pruning can no longer reach a row the rollup would rebuild.
	if c.Log.Retention < 48*time.Hour {
		return fmt.Errorf("log.retention must be at least 48h, got %s", c.Log.Retention)
	}
```

Update the config tests: the rejected value stays below the floor, and the accepted boundary becomes `48h`.

- [ ] **Step 2: Replace the guard's tests before removing the guard**

`TestADayIsFinalizedBeforeItIsFrozen` and `TestRollupSkipsADayPruningHasAlreadyTouched` both exist to exercise the guard, and `TestRollupNeverShrinksADaysTokens` (or whatever the shrink test is named) does too. Once the guard is gone they no longer describe anything real.

Do NOT simply delete them. Replace them with one test that pins the invariant they were standing in for — that at the floor, nothing the rollup can rewrite is prunable:

```go
func TestTheRollupWindowIsNeverPrunable(t *testing.T) {
	// The rollup rewrites yesterday and today wholesale, which is only safe
	// while pruning cannot reach either. At the retention floor it cannot,
	// and that is what lets the rollup recompute without a shrink guard.
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	// The last instant the window is still current.
	now := time.Date(2026, 8, 25, 23, 59, 0, 0, time.UTC)
	yStart := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 4; i++ {
		ts := yStart.Add(time.Duration(i*6) * time.Hour)
		if _, err := w.writeBatch(ctx, []*RequestRecord{{
			ID: fmt.Sprintf("y%d", i), TS: ts, ResolvedAlias: "fast",
			FinalProviderID: "groq", FinalModel: "m", TokensIn: 100,
			Attempts: []AttemptRecord{{
				Seq: 0, ProviderID: "groq", Model: "m",
				Outcome: "success", TokensIn: 100,
			}},
		}}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.Prune(ctx, now, 48*time.Hour, 72*time.Hour, 0); err != nil {
		t.Fatal(err)
	}

	var n int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM requests`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("pruning reached inside the rollup window: %d of 4 rows left", n)
	}
}
```

Also keep a test that a full recompute of a day still produces the right totals, and keep `TestRollupIsIdempotent` and the stale-`alias=''` clearing test — those describe the rollup itself, not the guard.

- [ ] **Step 3: Remove the guard**

Delete from `Rollup`: the `newTokens`/`oldTokens` queries, the comparison, the `from = startOfToday` reassignment, and the "would drop" log. Delete the `logRetention` parameter and the `defaultLogRetention` constant if nothing else uses it.

Leave a comment where the guard was, explaining why the window can be rewritten wholesale:

```go
	// Yesterday and today are recomputed wholesale. That is safe because
	// log.retention is floored at two days, so pruning can never reach a row
	// inside this window -- a recompute always sees everything the day had.
```

Keep the narrowed DELETE from earlier work. It is no longer load-bearing at the floor, but it costs nothing and keeps a day with no surviving requests from being erased if one ever appears.

- [ ] **Step 4: Update the callers**

`RunRollup` no longer needs the retention, and therefore may no longer need the config store. Simplify it and `internal/server/server.go` to match, and update every `Rollup(...)` call in tests — including the one in `internal/exec/rollup_seam_test.go`.

- [ ] **Step 5: Run everything**

Run: `go build ./... && go vet ./... && go test -race ./...`

- [ ] **Step 6: Commit**

Subject: `refactor(store): drop the rollup shrink guard`
