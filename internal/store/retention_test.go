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
			 VALUES (?,?,'openai','llm','m','success')`, id, ts.UnixMilli()); err != nil {
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
