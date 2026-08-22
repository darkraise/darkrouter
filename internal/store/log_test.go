package store

import (
	"context"
	"testing"
	"time"
)

func rec(id string) *RequestRecord {
	return recAt(id, time.Unix(1700000000, 0).UTC())
}

// recAt stamps the record with an explicit time. Retention prunes on ts, so a
// test that writes records while a prune runs must use a current timestamp or
// the prune legitimately deletes them.
func recAt(id string, ts time.Time) *RequestRecord {
	return &RequestRecord{
		ID: id, TS: ts,
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
