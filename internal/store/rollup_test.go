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
		t.Fatalf("want 150 tokens from the mixed group, got %d", total)
	}
}
