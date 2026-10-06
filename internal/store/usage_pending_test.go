package store

import (
	"context"
	"maps"
	"testing"
	"time"
)

// usageTotals sums UsageBy's rows per key, so a test can compare two reads of
// the same window without caring how either split it into days.
func usageTotals(t *testing.T, db *DB, now time.Time, dim UsageDimension) map[string][2]int64 {
	t.Helper()
	rows, err := db.UsageBy(context.Background(), now, 30, dim)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]int64{}
	for _, r := range rows {
		v := out[r.Key]
		out[r.Key] = [2]int64{v[0] + r.Requests, v[1] + r.Attempts}
	}
	return out
}

// Usage read between rollups counts what the log already holds. usage_daily
// alone left every new gateway, and every new day, reading "no traffic" for up
// to an hour beside a live strip counting requests per minute.
func TestUsageByCountsRequestsTheRollupHasNotReachedYet(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)

	insertRequest(t, db, "a", now.Add(-3*time.Hour), "groq", "m", 1, 1, nil)
	if err := db.Rollup(ctx, now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Logged after that rollup: only the requests table has these.
	insertRequest(t, db, "b", now.Add(-time.Hour), "groq", "m", 1, 1, nil)
	db.WriteBatchForTest(t, []*RequestRecord{{
		ID: "c", TS: now.Add(-time.Minute), Dialect: "openai", Surface: "llm",
		RequestedModel: "m", FinalProviderID: "nebius", FinalModel: "m", Status: "success",
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "groq", Model: "m", Outcome: "retryable_provider"},
			{Seq: 2, ProviderID: "nebius", Model: "m", Outcome: "success"},
		},
	}})

	before := usageTotals(t, db, now, UsageByProvider)
	if before["groq"] != [2]int64{2, 1} || before["nebius"] != [2]int64{1, 1} {
		t.Fatalf("before the next rollup = %v, want groq {2 1} and nebius {1 1}", before)
	}

	// And it is the rollup's own answer: running it changes nothing a reader
	// sees, so nothing is counted twice once the day is in usage_daily.
	if err := db.Rollup(ctx, now); err != nil {
		t.Fatal(err)
	}
	if after := usageTotals(t, db, now, UsageByProvider); !maps.Equal(after, before) {
		t.Fatalf("after the rollup = %v, before it = %v", after, before)
	}
}

// Yesterday as the last rollup left it is not read again: only the days that
// rollup could not have finished are taken from the log.
func TestUsageByReadsOnlyTheDaysTheLastRollupLeftOpen(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	if err := db.Rollup(ctx, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	pending, err := pendingFrom(ctx, db.Read, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC); !pending.Equal(want) {
		t.Fatalf("pending from %v, want the last run's day %v", pending, want)
	}
}

// Setting the pending days' usage_daily rows aside follows the rollup's own
// clear: a day the requests table no longer backs keeps the row it has.
func TestUsageByKeepsAPendingDayTheLogNoLongerHolds(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 22, 14, 0, 0, 0, time.UTC)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests)
		 VALUES ('2026-08-22', 'groq', 'm', '', 5)`); err != nil {
		t.Fatal(err)
	}
	if got := usageTotals(t, db, now, UsageByDayOnly); got[""] != [2]int64{5, 0} {
		t.Fatalf("usage = %v, want the stored 5 requests", got)
	}
}
