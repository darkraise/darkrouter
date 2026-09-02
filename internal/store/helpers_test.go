package store

import (
	"context"
	"testing"
)

// WriteBatchForTest persists request records synchronously. storetest carries
// the same helper for other packages; this package cannot import it without a
// cycle.
func (d *DB) WriteBatchForTest(t *testing.T, rows []*RequestRecord) {
	t.Helper()
	w := NewLogWriter(d, LogOptions{})
	if _, err := w.WriteBatch(context.Background(), rows); err != nil {
		t.Fatal(err)
	}
}

func (d *DB) SeedFailoverTraceForTest(t *testing.T, id string) {
	t.Helper()
	d.WriteBatchForTest(t, FailoverTraceFixture(id))
}
