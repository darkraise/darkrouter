package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func insertRequest(t *testing.T, db *DB, id string, ts time.Time, provider, model string, in, out int64, cost *int64) {
	t.Helper()
	_, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO requests (id, ts, dialect, surface, requested_model,
		    final_provider_id, final_model, status, tokens_in, tokens_out, cost_micros)
		 VALUES (?,?,'openai','llm',?,?,?,'success',?,?,?)`,
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

	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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
		if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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
	insertRequest(t, db, "spanning", time.Date(2026, 8, 21, 23, 59, 0, 0, time.UTC),
		"groq", "m", 10, 20, nil)

	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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

	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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

	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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

// A request with no attempt rows and no final provider truly has nothing to
// attribute. One whose every attempt failed is a different shape: no provider
// served it, but its attempts still burned tokens, and those tokens must
// reach usage_daily under the provider that burned them.
func TestRollupAttributesAttemptTokensEvenWhenNothingServed(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	insertRequest(t, db, "empty", now.Add(-time.Hour), "", "", 0, 0, nil)

	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{{
		ID: "all-failed", TS: now.Add(-time.Hour), RequestedModel: "m",
		Attempts: []AttemptRecord{
			{Seq: 0, ProviderID: "groq", Model: "m", Outcome: "retryable_provider", TokensIn: 50},
		},
	}}); err != nil {
		t.Fatal(err)
	}

	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM usage_daily`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("usage_daily has %d rows, want 1: the attempt-less request contributes nothing, "+
			"but the all-failed request's burned tokens must still land", n)
	}

	var requests, attempts, tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT requests, attempts, tokens_in FROM usage_daily WHERE provider_id='groq' AND model='m'`,
	).Scan(&requests, &attempts, &tokensIn); err != nil {
		t.Fatal(err)
	}
	if requests != 0 || attempts != 1 || tokensIn != 50 {
		t.Fatalf("groq: requests=%d attempts=%d tokens_in=%d, want 0/1/50",
			requests, attempts, tokensIn)
	}
}

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
	if err := db.Rollup(ctx, ts, 720*time.Hour); err != nil {
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
	if err := db.Rollup(ctx, ts, 720*time.Hour); err != nil {
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

// A group that mixes an attempt-bearing request with an attempt-less one
// must add both contributions, not let the attempt-bearing row's non-NULL
// sum silently coalesce away the attempt-less row's own tokens.
func TestRollupMixesAttemptBearingAndAttemptLessRequests(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	ts := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)

	withAttempt := &RequestRecord{
		ID: "r-with-attempt", TS: ts, RequestedModel: "m",
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 100,
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "success", TokensIn: 100},
		},
	}
	withoutAttempt := &RequestRecord{
		ID: "r-without-attempt", TS: ts, RequestedModel: "m",
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 50,
	}
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{withAttempt, withoutAttempt}); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollup(ctx, ts, 720*time.Hour); err != nil {
		t.Fatal(err)
	}

	var total int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT coalesce(sum(tokens_in), 0) FROM usage_daily`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	// 100 from the attempt-bearing request plus 50 from the attempt-less one.
	if total != 150 {
		t.Fatalf("want 150 tokens from the mixed group, got %d", total)
	}
}

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
				{Seq: 0, ProviderID: "groq", Model: "m1", Outcome: "retryable_provider", TokensIn: 100},
				{Seq: 1, ProviderID: "together", Model: "m2", Outcome: "success", TokensIn: 1000, TokensOut: 500, CostMicros: &c4500},
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
	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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

	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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

func TestRollupSkipsADayPruningHasAlreadyTouched(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Now().UTC()
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")

	// Yesterday, finalized when it was complete.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests, tokens_in)
		 VALUES (?,'groq','m','fast',100,10000)`, yesterday); err != nil {
		t.Fatal(err)
	}

	// A remnant of yesterday that pruning has not reached yet.
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{{
		ID: "leftover", TS: now.AddDate(0, 0, -1), ResolvedAlias: "fast",
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 7,
		Attempts: []AttemptRecord{{
			Seq: 0, ProviderID: "groq", Model: "m",
			Outcome: "success", TokensIn: 7,
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	// A 24h retention means yesterday's midnight is already outside it.
	if err := db.Rollup(ctx, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}

	var requests, tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT requests, tokens_in FROM usage_daily WHERE day=?`, yesterday,
	).Scan(&requests, &tokensIn); err != nil {
		t.Fatal(err)
	}
	// Recomputing from the remnant alone would have written 1 and 7.
	if requests != 100 || tokensIn != 10000 {
		t.Fatalf("a partly-pruned day was recomputed from its remnant: "+
			"requests=%d tokens_in=%d, want 100/10000", requests, tokensIn)
	}
}

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

func TestRollupNeverDropsTokensLostToPrunedFailedAttempts(t *testing.T) {
	// Comparing request COUNTS as the safety proxy misses this: is_served
	// only credits the attempt that succeeded, so 8 fully-failed requests
	// contribute nothing to "requests" either before or after they are
	// pruned. The token loss is real and only tokens_in ever shows it.
	db := migrated(t)
	ctx := context.Background()
	day := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	w := NewLogWriter(db, LogOptions{})

	var failedIDs []string
	var recs []*RequestRecord
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("served-%d", i)
		recs = append(recs, &RequestRecord{
			ID: id, TS: day.Add(time.Duration(i) * time.Hour), ResolvedAlias: "fast",
			FinalProviderID: "groq", FinalModel: "m", TokensIn: 100,
			Attempts: []AttemptRecord{{
				Seq: 0, ProviderID: "groq", Model: "m",
				Outcome: "success", TokensIn: 100,
			}},
		})
	}
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("failed-%d", i)
		failedIDs = append(failedIDs, id)
		recs = append(recs, &RequestRecord{
			ID: id, TS: day.Add(time.Duration(i) * time.Hour), ResolvedAlias: "fast",
			Attempts: []AttemptRecord{{
				Seq: 0, ProviderID: "groq", Model: "m",
				Outcome: "retryable_provider", TokensIn: 1,
			}},
		})
	}
	if _, err := w.writeBatch(ctx, recs); err != nil {
		t.Fatal(err)
	}

	if err := db.Rollup(ctx, now, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	var requests, tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT requests, tokens_in FROM usage_daily WHERE day='2026-08-24'`,
	).Scan(&requests, &tokensIn); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || tokensIn != 208 {
		t.Fatalf("finalized requests=%d tokens_in=%d, want 2/208", requests, tokensIn)
	}

	// Pruning removes the 8 failed requests wholesale: attempts first, then
	// the request rows, mirroring retention.go's own deletion order.
	for _, id := range failedIDs {
		if _, err := db.Write.ExecContext(ctx,
			`DELETE FROM request_attempts WHERE request_id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Write.ExecContext(ctx,
			`DELETE FROM requests WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.Rollup(ctx, now.Add(time.Hour), 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.Read.QueryRowContext(ctx,
		`SELECT requests, tokens_in FROM usage_daily WHERE day='2026-08-24'`,
	).Scan(&requests, &tokensIn); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || tokensIn != 208 {
		t.Fatalf("day shrank after pruning removed only unserved rows: "+
			"requests=%d tokens_in=%d, want 2/208 (unchanged)", requests, tokensIn)
	}
}

func TestRollupStillRecomputesUnderTheDefaultRetention(t *testing.T) {
	// The guard must not change behaviour for anyone running a sane retention.
	db := migrated(t)
	ctx := context.Background()
	now := time.Now().UTC()
	today := now.Format("2006-01-02")

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests, tokens_in)
		 VALUES (?,'groq','m','',9,900)`, today); err != nil {
		t.Fatal(err)
	}
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{{
		ID: "live", TS: now, ResolvedAlias: "fast",
		FinalProviderID: "groq", FinalModel: "m", TokensIn: 10,
		Attempts: []AttemptRecord{{
			Seq: 0, ProviderID: "groq", Model: "m",
			Outcome: "success", TokensIn: 10,
		}},
	}}); err != nil {
		t.Fatal(err)
	}

	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
		t.Fatal(err)
	}

	var rows, requests int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*), coalesce(sum(requests),0) FROM usage_daily WHERE day=?`, today,
	).Scan(&rows, &requests); err != nil {
		t.Fatal(err)
	}
	if rows != 1 || requests != 1 {
		t.Fatalf("stale row survived under a sane retention: rows=%d requests=%d",
			rows, requests)
	}
}

func TestTodayIsNeverPrunedAtTheRetentionFloor(t *testing.T) {
	// Today's earliest possible request sits at today's midnight, and
	// pruning removes anything older than now-retention. So today cannot be
	// pruned while it is still being rolled up exactly when retention is at
	// least 24h -- the floor config now enforces. Below the floor this same
	// shape (requests spread across the morning, finalized once, then
	// partly pruned by afternoon) is what silently shrinks a day's total.
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})

	startOfToday := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)
	today := startOfToday.Format("2006-01-02")

	var recs []*RequestRecord
	for i, hour := range []int{2, 3, 4, 5, 6, 7} {
		recs = append(recs, &RequestRecord{
			ID: fmt.Sprintf("r%d", i), TS: startOfToday.Add(time.Duration(hour) * time.Hour),
			ResolvedAlias: "fast", FinalProviderID: "groq", FinalModel: "m", TokensIn: 100,
			Attempts: []AttemptRecord{{
				Seq: 0, ProviderID: "groq", Model: "m",
				Outcome: "success", TokensIn: 100,
			}},
		})
	}
	if _, err := w.writeBatch(ctx, recs); err != nil {
		t.Fatal(err)
	}

	// The 08:00 rollup finalizes today at 600 tokens.
	if err := db.Rollup(ctx, startOfToday.Add(8*time.Hour), 24*time.Hour); err != nil {
		t.Fatal(err)
	}

	// 14:00: pruning at the floor retention, then another rollup.
	if _, err := db.Prune(ctx, startOfToday.Add(14*time.Hour), 24*time.Hour, 24*time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollup(ctx, startOfToday.Add(14*time.Hour), 24*time.Hour); err != nil {
		t.Fatal(err)
	}

	var tokensIn int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT coalesce(sum(tokens_in),0) FROM usage_daily WHERE day=?`, today,
	).Scan(&tokensIn); err != nil {
		t.Fatal(err)
	}
	if tokensIn != 600 {
		t.Fatalf("today shrank at the retention floor: tokens_in=%d, want 600", tokensIn)
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
		Attempts: []AttemptRecord{{Seq: 0, ProviderID: "groq", Model: "m1", Outcome: "success", TokensIn: 10}},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Rollup(ctx, now, 720*time.Hour); err != nil {
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
