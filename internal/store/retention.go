package store

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

const (
	defaultPruneBatch = 500
	// prunePause yields the single write handle between batches so queued log
	// batches are not stuck behind a long prune.
	prunePause = 25 * time.Millisecond
)

// Prune deletes expired log rows in bounded batches and runs one incremental
// vacuum step. It returns the number of request rows deleted.
func (d *DB) Prune(ctx context.Context, now time.Time,
	logRetention, captureRetention time.Duration, batch int) (int, error) {

	if batch <= 0 {
		batch = defaultPruneBatch
	}
	total := 0

	logCutoff := now.Add(-logRetention).UnixMilli()
	for {
		n, err := d.pruneRequestBatch(ctx, logCutoff, batch)
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			break
		}
		if err := pause(ctx); err != nil {
			return total, err
		}
	}

	// Bodies expire on their own stored deadline rather than on log.retention,
	// because capture.retention is deliberately much shorter.
	for {
		n, err := d.pruneBodyBatch(ctx, now.UnixMilli(), batch)
		if err != nil {
			return total, err
		}
		if n == 0 {
			break
		}
		if err := pause(ctx); err != nil {
			return total, err
		}
	}

	// Deletes do not shrink the file. auto_vacuum=incremental was set in the
	// DSN; this is the step that actually returns pages.
	if _, err := d.Write.ExecContext(ctx, `PRAGMA incremental_vacuum(1000)`); err != nil {
		return total, fmt.Errorf("incremental vacuum: %w", err)
	}
	return total, nil
}

func pause(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(prunePause):
		return nil
	}
}

func (d *DB) pruneRequestBatch(ctx context.Context, cutoff int64, batch int) (int, error) {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin prune: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Attempts go first, while their parent rows are still present to identify
	// them. request_attempts carries no foreign key, so nothing cascades.
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM request_attempts
		  WHERE request_id IN (SELECT id FROM requests WHERE ts < ? ORDER BY ts LIMIT ?)`,
		cutoff, batch); err != nil {
		return 0, fmt.Errorf("prune attempts: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM requests
		  WHERE id IN (SELECT id FROM requests WHERE ts < ? ORDER BY ts LIMIT ?)`,
		cutoff, batch)
	if err != nil {
		return 0, fmt.Errorf("prune requests: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit prune: %w", err)
	}
	return int(n), nil
}

func (d *DB) pruneBodyBatch(ctx context.Context, nowMillis int64, batch int) (int, error) {
	// SQLite's DELETE ... LIMIT needs a compile-time option that is not enabled
	// here, so the bound goes in a subquery.
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM request_bodies
		  WHERE request_id IN (
		        SELECT request_id FROM request_bodies WHERE expires_at < ? LIMIT ?)`,
		nowMillis, batch)
	if err != nil {
		return 0, fmt.Errorf("prune bodies: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// RunRetention prunes on an interval until ctx is cancelled. Retention windows
// are read live from the config store, because log.retention hot-reloads.
func RunRetention(ctx context.Context, d *DB, cfgStore *config.Store, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(interval)):
			cfg := cfgStore.Current()
			n, err := d.Prune(ctx, time.Now(), cfg.Log.Retention, cfg.Capture.Retention, 0)
			if err != nil {
				log.Printf("retention: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("retention: pruned %d request records", n)
			}
		}
	}
}
