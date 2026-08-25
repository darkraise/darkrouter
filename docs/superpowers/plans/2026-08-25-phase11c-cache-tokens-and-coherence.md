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
