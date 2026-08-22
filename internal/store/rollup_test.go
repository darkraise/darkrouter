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
