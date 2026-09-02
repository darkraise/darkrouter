package store

import (
	"context"
	"testing"
	"time"
)

func TestPruneDropsOldUsageDays(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	for _, day := range []string{"2025-07-01", "2025-07-30", "2026-09-01"} {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO usage_daily (day, provider_id, model, requests) VALUES (?, 'p', 'm', 1)`, day); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Prune(ctx, now, 720*time.Hour, 72*time.Hour, 400*24*time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Read.QueryContext(ctx, `SELECT day FROM usage_daily ORDER BY day`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kept []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, d)
	}
	// 400 days before 2026-09-02 is 2025-07-29: the day before it goes, the
	// day after it stays.
	if len(kept) != 2 || kept[0] != "2025-07-30" || kept[1] != "2026-09-01" {
		t.Errorf("kept %v, want [2025-07-30 2026-09-01]", kept)
	}
}
