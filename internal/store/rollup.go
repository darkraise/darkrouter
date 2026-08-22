package store

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// Rollup recomputes usage_daily for yesterday and today in UTC.
//
// Two days rather than one: a request logged just after midnight belongs to
// yesterday, and the batching writer may not have flushed it before the day
// turned. Recomputing yesterday on every run is what finalizes it.
func (d *DB) Rollup(ctx context.Context, now time.Time) error {
	utc := now.UTC()
	startOfToday := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	from := startOfToday.AddDate(0, 0, -1)
	to := startOfToday.AddDate(0, 0, 1)

	// The whole window is recomputed and upserted, so running this repeatedly
	// converges rather than accumulating.
	_, err := d.Write.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, requests, tokens_in, tokens_out, cost_micros)
		 SELECT strftime('%Y-%m-%d', ts / 1000, 'unixepoch') AS day,
		        final_provider_id,
		        final_model,
		        count(*),
		        coalesce(sum(tokens_in), 0),
		        coalesce(sum(tokens_out), 0),
		        -- NULL rather than 0 when nothing in the group is priced:
		        -- zero would report the day's spend as genuinely nothing.
		        CASE WHEN count(cost_micros) = 0 THEN NULL ELSE sum(cost_micros) END
		   FROM requests
		  WHERE ts >= ? AND ts < ?
		    AND final_provider_id <> ''
		  GROUP BY day, final_provider_id, final_model
		 ON CONFLICT(day, provider_id, model) DO UPDATE SET
		        requests    = excluded.requests,
		        tokens_in   = excluded.tokens_in,
		        tokens_out  = excluded.tokens_out,
		        cost_micros = excluded.cost_micros`,
		from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	return nil
}

// RunRollup runs the rollup on an interval until ctx is cancelled.
//
// The interval is jittered so that a restart of several services does not line
// every worker up on the same instant.
func RunRollup(ctx context.Context, d *DB, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(interval)):
			if err := d.Rollup(ctx, time.Now()); err != nil {
				// Logged, not fatal: a missed rollup is recomputed on the next
				// run, because finalization is idempotent.
				log.Printf("rollup: %v", err)
			}
		}
	}
}

// jitter spreads a worker's wakeups over the last quarter of its interval.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d - time.Duration(rand.Int63n(int64(d/4)+1))
}
