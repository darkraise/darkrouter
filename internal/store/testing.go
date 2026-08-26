package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// MigratedForTest opens a migrated database in a temp directory.
//
// It lives in a non-test file because internal/admin's tests need it and Go
// does not export helpers from _test.go files across package boundaries. The
// alternative — every package reimplementing Open plus Migrate — is how two
// packages end up testing against differently-shaped databases.
//
// It mirrors this package's own openTest plus migrated helpers exactly; if
// those grow a step, this has to grow it too.
func MigratedForTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

// WriteBatchForTest persists request records synchronously.
//
// It exists because internal/admin's tests need a populated request log and the
// normal path is an asynchronous channel drained by a worker — a test that
// enqueued and slept would be timing-dependent for no reason. It is the same
// code path the worker uses, so what it writes is what production writes.
func (d *DB) WriteBatchForTest(t *testing.T, rows []*RequestRecord) {
	t.Helper()
	w := NewLogWriter(d, LogOptions{})
	if _, err := w.writeBatch(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
}

// SeedFailoverTraceForTest writes the two-attempt trace the admin handler tests
// read. It lives beside the other test helpers so both packages assert against
// one fixture shape rather than two that can drift.
func (d *DB) SeedFailoverTraceForTest(t *testing.T, id string) {
	t.Helper()
	cost := int64(1234)
	ttft := int64(56)
	attempt1Cost := int64(200)
	attempt2Cost := int64(1234)
	d.WriteBatchForTest(t, []*RequestRecord{{
		ID: id, TS: time.UnixMilli(1700000000000),
		Dialect: "openai", Surface: "llm", RequestedModel: "fast",
		ResolvedAlias: "fast", FinalProviderID: "b", FinalModel: "m2",
		Status: "success", TokensIn: 10, TokensOut: 20, CacheReadTokens: 4,
		CostMicros: &cost, TTFTMs: &ttft,
		Candidates:  []string{"a/m1", "b/m2", "c/m3"},
		Skips:       []string{"c/m3:cooling", "d/m4:no_credential"},
		Warnings:    []string{"top_k -> openai: not expressible"},
		SurfaceMeta: map[string]any{"input_count": 3},
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "a", KeyID: "k1", Model: "m1",
				Outcome: "retryable_provider", StatusCode: 500, LatencyMs: 120,
				Error: "upstream 500", TokensIn: 15, TokensOut: 5, CostMicros: &attempt1Cost},
			{Seq: 2, ProviderID: "b", KeyID: "k2", Model: "m2",
				Outcome: "success", StatusCode: 200, LatencyMs: 340,
				TokensIn: 10, TokensOut: 20, CostMicros: &attempt2Cost},
		},
	}})
}
